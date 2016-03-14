// replicaset.go
//
// Copyright (c) 2016, Ayke van Laethem
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
// 1. Redistributions of source code must retain the above copyright
//    notice, this list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright
//    notice, this list of conditions and the following disclaimer in the
//    documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS
// IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED
// TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A
// PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package dtdiff

import (
	"github.com/aykevl/dtsync/tree"
)

// ReplicaSet is a combination of two replicas
type ReplicaSet struct {
	set [2]*Replica
}

func Scan(fs1, fs2 tree.Tree) (*ReplicaSet, error) {
	if fs1 == fs2 {
		return nil, ErrSameRoot
	}

	rs := &ReplicaSet{}

	// Load and scan replica status.
	var scanReplicas [2]chan *Replica
	var scanStart [2]chan struct{}
	var scanErrors [2]chan error
	var scanCancel [2]chan struct{}
	trees := []tree.Tree{fs1, fs2}
	for i := range trees {
		scanReplicas[i] = make(chan *Replica)
		scanStart[i] = make(chan struct{}, 1)
		scanErrors[i] = make(chan error)
		scanCancel[i] = make(chan struct{}, 1)
		go func(i int) {
			switch fs := trees[i].(type) {
			case tree.LocalFileTree:
				file, err := fs.GetFile(STATUS_FILE)
				if err == tree.ErrNotFound {
					// loadReplica doesn't return errors when creating a new
					// replica.
					replica, _ := loadReplica(nil)
					scanReplicas[i] <- replica
					scanErrors[i] <- nil
				} else if err != nil {
					scanReplicas[i] <- nil
					scanErrors[i] <- err
				} else {
					replica, err := loadReplica(file)
					if err != nil {
						scanReplicas[i] <- nil
						scanErrors[i] <- err
					} else {
						scanReplicas[i] <- replica
						<-scanStart[i] // make sure replica.ignore is set
						scanErrors[i] <- replica.scanDir(fs.Root(), replica.Root(), scanCancel[i])
					}
				}
			default:
				panic("tree does not implement tree.LocalFileTree")
			}
		}(i)
	}

	var ignore [2][]string

	// Wait for replicas to be loaded/opened.
	for i := range scanReplicas {
		replica := <-scanReplicas[i]
		rs.set[i] = replica

		// Get files to ignore.
		if replica != nil {
			header := replica.Header()
			ignore[i] = header["Ignore"]
		}
	}

	// Set ignored files on both replicas.
	for _, ignoreList := range ignore {
		for _, replica := range rs.set {
			replica.AddIgnore(ignoreList...)
		}
	}

	// And now start the scan.
	scanStart[0] <- struct{}{}
	scanStart[1] <- struct{}{}

	if rs.set[0] != nil && rs.set[1] != nil {
		// There were no errors while opening the replicas.

		replica1 := rs.set[0]
		replica2 := rs.set[1]

		if replica1.identity == replica2.identity {
			return nil, ErrSameIdentity
		}

		// let them know of each other
		notifyReplica(replica1, replica2)
		notifyReplica(replica2, replica1)
	}

	select {
	case err := <-scanErrors[0]:
		if err != nil {
			scanCancel[1] <- struct{}{}
			<-scanErrors[1]
			return nil, err
		}
		err = <-scanErrors[1]
		if err != nil {
			return nil, err
		}
	case err := <-scanErrors[1]:
		if err != nil {
			scanCancel[0] <- struct{}{}
			<-scanErrors[0]
			return nil, err
		}
		err = <-scanErrors[0]
		if err != nil {
			return nil, err
		}
	}

	return rs, nil
}

// Get returns the replica by index
func (rs *ReplicaSet) Get(index int) *Replica {
	return rs.set[index]
}

// notifyReplica adds the replica to the list of known replicas
func notifyReplica(replica, other *Replica) {
	if _, ok := replica.knowledge[other.identity]; !ok {
		replica.knowledge[other.identity] = 0
	}
}

// MarkSynced sets the generation as including each other. This is done after
// they have been cleanly synchronized.
func (rs *ReplicaSet) MarkSynced() {
	rs.set[0].include(rs.set[1])
	rs.set[1].include(rs.set[0])
}

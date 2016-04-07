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
	var sendOptions [2]chan *tree.ScanOptions // start scan on receive
	var scanErrors [2]chan error
	var scanCancel [2]chan struct{}
	trees := []tree.Tree{fs1, fs2}
	for i := range trees {
		sendOptions[i] = make(chan *tree.ScanOptions, 1)
		scanErrors[i] = make(chan error)
		scanCancel[i] = make(chan struct{}, 1)
	}
	for i := range trees {
		go func(i int) {
			switch fs := trees[i].(type) {
			case tree.LocalFileTree:
				file, err := fs.GetFile(STATUS_FILE)
				if err != nil && !tree.IsNotExist(err) {
					scanErrors[i] <- err
					return
				}

				var replica *Replica
				var myOptions *tree.ScanOptions
				if tree.IsNotExist(err) {
					// loadReplica doesn't return errors when creating a new
					// replica.
					replica, _ = loadReplica(nil)
				} else {
					replica, err = loadReplica(file)
					if err != nil {
						scanErrors[i] <- err
						return
					}
				}
				rs.set[i] = replica

				myOptions = replica.scanOptions()
				replica.addScanOptions(myOptions)

				// Let the other replica exclude using our rules.
				sendOptions[(i+1)%2] <- myOptions

				// Insert options from the other replica.
				otherOptions, ok := <-sendOptions[i]
				if !ok {
					// Something went wrong with the other replica.
					// Cancel now.
					scanErrors[i] <- nil
					return
				}
				replica.addScanOptions(otherOptions)

				// Now we can start.
				scanErrors[i] <- replica.scan(fs, scanCancel[i])

			case tree.RemoteTree:
				reader, err := fs.RemoteScan(sendOptions[i], sendOptions[(i+1)%2])
				if err != nil {
					scanErrors[i] <- err
				}

				replica, err := loadReplica(reader)
				if err != nil {
					scanErrors[i] <- err
				} else {
					rs.set[i] = replica
					scanErrors[i] <- nil
				}
			default:
				panic("tree does not implement tree.LocalFileTree or tree.RemoteTree")
			}
		}(i)
	}

	select {
	case err := <-scanErrors[0]:
		if err != nil {
			close(sendOptions[1])
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
			close(sendOptions[0])
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

// MarkSynced sets the generation as including each other. This is done after
// they have been cleanly synchronized.
func (rs *ReplicaSet) MarkSynced() {
	rs.set[0].mergeKnowledge(rs.set[1])
	rs.set[1].mergeKnowledge(rs.set[0])
}

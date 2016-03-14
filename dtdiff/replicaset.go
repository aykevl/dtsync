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
	"path"

	"github.com/aykevl/dtsync/tree"
)

// ReplicaSet is a combination of two replicas
type ReplicaSet struct {
	set    [2]*Replica
	cancel uint32   // set to non-0 if the scan should cancel
	ignore []string // paths to ignore
}

func Scan(fs1, fs2 tree.Tree) (*ReplicaSet, error) {
	if fs1 == fs2 {
		return nil, ErrSameRoot
	}

	rs := &ReplicaSet{}

	// Load and scan replica status.
	var scanErrors [2]chan error
	var scanReplicas [2]chan *Replica
	trees := []tree.Tree{fs1, fs2}
	for i := range trees {
		scanErrors[i] = make(chan error)
		scanReplicas[i] = make(chan *Replica)
		go func(i int) {
			switch fs := trees[i].(type) {
			case tree.LocalTree:
				file, err := fs.GetFile(STATUS_FILE)
				if err == tree.ErrNotFound {
					// loadReplica doesn't return errors when creating a new
					// replica.
					replica, _ := loadReplica(rs, nil)
					scanReplicas[i] <- replica
					scanErrors[i] <- nil
				} else if err != nil {
					scanReplicas[i] <- nil
					scanErrors[i] <- err
				} else {
					replica, err := loadReplica(rs, file)
					if err != nil {
						scanReplicas[i] <- nil
						scanErrors[i] <- err
					} else {
						scanReplicas[i] <- replica
						scanErrors[i] <- rs.scanDir(fs.Root(), replica.Root())
					}
				}
			default:
				panic("tree does not implement tree.LocalTree")
			}
		}(i)
	}

	for i := range scanReplicas {
		replica := <-scanReplicas[i]
		rs.set[i] = replica

		// Get files to ignore.
		if replica != nil {
			header := replica.Header()
			rs.ignore = append(rs.ignore, header["Ignore"]...)
		}
	}

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
			rs.cancel = 1
			<-scanErrors[1]
			return nil, err
		}
		err = <-scanErrors[1]
		if err != nil {
			return nil, err
		}
	case err := <-scanErrors[1]:
		if err != nil {
			rs.cancel = 1
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

// scanDir scans one side of the tree, updating the status tree to the current
// status.
func (rs *ReplicaSet) scanDir(dir tree.Entry, statusDir *Entry) error {
	fileList, err := dir.List()
	if err != nil {
		return err
	}
	iterator := nextFileStatus(fileList, statusDir.List())

	var file tree.Entry
	var status *Entry
	for {
		if rs.cancel != 0 {
			return ErrCanceled
		}

		file, status = iterator()
		if file != nil && rs.isIgnored(file) {
			file = nil
		}

		if file == nil && status == nil {
			break
		}
		if file == nil {
			// old status entry
			status.Remove()
			continue
		}

		if status == nil {
			// add status
			info, err := file.Info()
			if err != nil {
				return err
			}
			status, err = statusDir.Add(info)
			if err != nil {
				panic(err) // must not happen
			}
		} else {
			// update status (if needed)
			oldHash := status.Hash()
			oldFingerprint := status.Fingerprint()
			newFingerprint := file.Fingerprint()
			var newHash []byte
			var err error
			if oldFingerprint == newFingerprint && oldHash != nil {
				// Assume the hash stayed the same when the fingerprint is the
				// same. But calculate a new hash if there is no hash.
				newHash = oldHash
			} else {
				if file.Type() == tree.TYPE_REGULAR {
					newHash, err = file.Hash()
					if err != nil {
						return err
					}
				}
			}
			status.Update(newFingerprint, newHash)
		}

		if file.Type() == tree.TYPE_DIRECTORY {
			err := rs.scanDir(file, status)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (rs *ReplicaSet) isIgnored(file tree.Entry) bool {
	relpath := path.Join(file.RelativePath()...)
	for _, pattern := range rs.ignore {
		if len(pattern) == 0 {
			continue
		}
		// TODO: use a more advanced pattern matching method.
		if pattern[0] == '/' {
			// Match relative to the root.
			if match, err := path.Match(pattern[1:], relpath); match && err == nil {
				return true
			}
		} else {
			// Match only the name. This does not work when the pattern contains
			// slashes.
			if match, err := path.Match(pattern, file.Name()); match && err == nil {
				return true
			}
		}
	}
	return false
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

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
	set          [2]*Replica
	progress     chan<- ScanProgress
	extraOptions *tree.ScanOptions
}

func Progress(progress chan<- ScanProgress) func(*ReplicaSet) {
	return func(rs *ReplicaSet) {
		rs.progress = progress
	}
}

func ExtraOptions(extraOptions *tree.ScanOptions) func(*ReplicaSet) {
	return func(rs *ReplicaSet) {
		rs.extraOptions = extraOptions
	}
}

func Scan(fs1, fs2 tree.Tree, options ...func(*ReplicaSet)) (*ReplicaSet, error) {
	if fs1 == fs2 {
		return nil, ErrSameRoot
	}

	rs := &ReplicaSet{}
	for _, option := range options {
		option(rs)
	}

	if rs.progress != nil {
		defer close(rs.progress)
	}

	// Load and scan replica status.
	var sendOptions [2]chan *tree.ScanOptions // start scan on receive
	var scanErrors [2]chan error
	var scanCancel [2]chan struct{}
	var scanProgress [2]chan *tree.ScanProgress
	trees := []tree.Tree{fs1, fs2}
	for i := range trees {
		sendOptions[i] = make(chan *tree.ScanOptions, 1)
		scanErrors[i] = make(chan error)
		scanCancel[i] = make(chan struct{}, 1)
		scanProgress[i] = make(chan *tree.ScanProgress)
	}
	for i := range trees {
		i := i
		go func() {
			switch fs := trees[i].(type) {
			case tree.LocalFileTree:
				replica, err := ScanTree(fs, rs.extraOptions, sendOptions[i], sendOptions[(i+1)%2], scanProgress[i], scanCancel[i])
				if err == nil {
					rs.set[i] = replica
				}
				scanErrors[i] <- err

			case tree.RemoteTree:
				reader, err := fs.RemoteScan(rs.extraOptions, sendOptions[i], sendOptions[(i+1)%2], scanProgress[i], scanCancel[i])
				if err != nil {
					scanErrors[i] <- err
					return
				}

				replica, err := LoadReplica(reader)
				reader.Close()
				if err == nil {
					rs.set[i] = replica
				}
				scanErrors[i] <- err
			default:
				panic("tree does not implement tree.LocalFileTree or tree.RemoteTree")
			}
		}()
	}

	var progress ScanProgress

	addProgress := func(index int, newProgress *tree.ScanProgress) {
		if rs.progress == nil {
			return
		}
		progress[index] = newProgress
		rs.progress <- progress
	}

	done1 := false
	done2 := false
	for {
		select {
		case err := <-scanErrors[0]:
			done1 = true
			if err != nil {
				if !done2 {
					close(scanCancel[1])
					<-scanErrors[1]
				}
				return nil, err
			}
		case err := <-scanErrors[1]:
			done2 = true
			if err != nil {
				if !done1 {
					close(scanCancel[0])
					<-scanErrors[0]
				}
				return nil, err
			}
		case newProgress, ok := <-scanProgress[0]:
			if !ok {
				scanProgress[0] = nil
			} else {
				addProgress(0, newProgress)
			}
		case newProgress, ok := <-scanProgress[1]:
			if !ok {
				scanProgress[1] = nil
			} else {
				addProgress(1, newProgress)
			}
		}

		if done1 && done2 {
			return rs, nil
		}
	}
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

// sync.go
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

// Package sync implements the core of the system: synchronizing two directory
// trees. It calculates the differences between two trees and returns them as
// sync jobs.
package sync

import (
	"io"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

// File where current status of the tree is stored.
const STATUS_FILE = ".dtsync"

type Result struct {
	rs    *dtdiff.ReplicaSet
	root1 tree.Entry
	root2 tree.Entry
	jobs  []*Job
}

func Sync(dir1, dir2 tree.Entry) (*Result, error) {
	// Load replica status
	statusFile1, err := getStatus(dir1)
	if err != nil {
		return nil, err
	}
	statusFile2, err := getStatus(dir2)
	if err != nil {
		return nil, err
	}

	rs, err := dtdiff.LoadReplicaSet(statusFile1, statusFile2)
	if err != nil {
		return nil, err
	}

	result := &Result{
		rs:    rs,
		root1: dir1,
		root2: dir2,
	}

	return result, result.sync()
}

func getStatus(dir tree.Entry) (io.ReadCloser, error) {
	file, err := dir.GetFile(STATUS_FILE)
	if err == tree.ErrNotFound {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return file, nil
}

func (r *Result) sync() error {
	return r.syncDirs(r.root1, r.root2, r.rs.Get(0).Root(), r.rs.Get(1).Root())
}

// syncDirs implements the heart of the sync algorithm.
func (r *Result) syncDirs(dir1, dir2 tree.Entry, statusDir1, statusDir2 *dtdiff.Entry) error {
	for row := range iterateEntries(dir1, dir2, statusDir1, statusDir2) {
		if row.err != nil {
			// TODO don't stop, continue but mark the sync as unclean (don't
			// update certain generation numbers)
			return row.err
		}
		file1 := row.file1
		file2 := row.file2
		status1 := row.status1
		status2 := row.status2
		ensureStatus(&file1, &status1, &statusDir1)
		ensureStatus(&file2, &status2, &statusDir2)
		// so now status1 and status2 must both be defined, if the files are
		// defined
		if file1 == nil && file2 == nil {
			if status1 != nil {
				status1.Remove()
			}
			if status2 != nil {
				status2.Remove()
			}
		} else if file1 == nil {
			r.syncSingle(1, file2, status2, status1, statusDir1, dir2, dir1)
		} else if file2 == nil {
			r.syncSingle(0, file1, status1, status2, statusDir2, dir1, dir2)
		} else {
			// All four (file1, file2, status1, status2) are defined.
			// Compare the contents.
			status1.Update(file1)
			status2.Update(file2)
			if status1.Equal(status2) {
			} else if status1.After(status2) {
				r.jobs = append(r.jobs, &Job{
					action:  ACTION_UPDATE,
					status1: status1,
					status2: status2,
					file1:   file1,
					file2:   file2,
				})
			} else if status1.Before(status2) {
				r.jobs = append(r.jobs, &Job{
					action:  ACTION_UPDATE,
					status1: status2,
					status2: status1,
					file1:   file2,
					file2:   file1,
				})
			} else {
				// TODO: must be a conflict
			}
		}
	}
	return nil
}

// ensureStatus adds a status entry if there isn't one.
func ensureStatus(file *tree.Entry, status **dtdiff.Entry, statusDir **dtdiff.Entry) {
	if *file != nil {
		if *status == nil {
			var err error
			*status, err = (*statusDir).Add(*file)
			if err != nil {
				panic("must not happen: " + err.Error())
			}
		} else {
			// TODO update
		}
	}
}

// syncSingle synchoronizes a file on only one side. It is either copied or
// removed.
func (r *Result) syncSingle(direction int, file1 tree.Entry, status1, status2, statusDir2 *dtdiff.Entry, dir1, dir2 tree.Entry) {
	if !statusDir2.HasRevision(status1) {
		r.jobs = append(r.jobs, &Job{
			action:        ACTION_COPY,
			status1:       status1,
			status2:       status2,
			statusParent2: statusDir2,
			file1:         file1,
			parent1:       dir1,
			parent2:       dir2,
			direction:     direction,
		})
	} else {
		r.jobs = append(r.jobs, &Job{
			action:        ACTION_REMOVE,
			status1:       status1,
			status2:       status2,
			statusParent2: statusDir2,
			file1:         file1,
			parent1:       dir1,
			parent2:       dir2,
			direction:     direction,
		})
	}
}

// entryRow is one row as returned by iterateEntries. All entries in here have
// the same name.
type entryRow struct {
	file1   tree.Entry
	file2   tree.Entry
	status1 *dtdiff.Entry
	status2 *dtdiff.Entry
	err     error
}

// iterateEntries returns a channel from which rows can be read of entries (see
// entryRow) that all have the same name, or are nil.
func iterateEntries(dir1, dir2 tree.Entry, statusDir1, statusDir2 *dtdiff.Entry) chan entryRow {
	// I would like to make this function far shorter, but I don't see an easy
	// way...
	c := make(chan entryRow)
	go func() {
		defer close(c)
		listDir1, err := dir1.List()
		if err != nil {
			c <- entryRow{err: err}
			return
		}
		listDir2, err := dir2.List()
		if err != nil {
			c <- entryRow{err: err}
			return
		}
		listStatus1 := statusDir1.List()
		listStatus2 := statusDir2.List()

		iterDir1 := iterateEntrySlice(listDir1)
		iterDir2 := iterateEntrySlice(listDir2)
		iterStatus1 := iterateStatusSlice(listStatus1)
		iterStatus2 := iterateStatusSlice(listStatus2)

		file1 := <-iterDir1
		file2 := <-iterDir2
		status1 := <-iterStatus1
		status2 := <-iterStatus2

		for {
			file1Name := ""
			if file1 != nil {
				file1Name = file1.Name()
			}
			file2Name := ""
			if file2 != nil {
				file2Name = file2.Name()
			}
			status1Name := ""
			if status1 != nil {
				status1Name = status1.Name()
			}
			status2Name := ""
			if status2 != nil {
				status2Name = status2.Name()
			}

			name := leastName([]string{file1Name, file2Name, status1Name, status2Name})
			// All of the names are empty strings, so we must have reached the
			// end.
			if name == "" {
				break
			}

			row := entryRow{}
			if file1 != nil && file1.Name() == name {
				row.file1 = file1
				file1 = <-iterDir1
			}
			if file2 != nil && file2.Name() == name {
				row.file2 = file2
				file2 = <-iterDir2
			}
			if status1 != nil && status1.Name() == name {
				row.status1 = status1
				status1 = <-iterStatus1
			}
			if status2 != nil && status2.Name() == name {
				row.status2 = status2
				status2 = <-iterStatus2
			}
			c <- row
		}
	}()
	return c
}

func iterateEntrySlice(list []tree.Entry) chan tree.Entry {
	c := make(chan tree.Entry)
	go func() {
		for _, entry := range list {
			if entry.Name() == STATUS_FILE {
				continue
			}
			c <- entry
		}
		close(c)
	}()
	return c
}

func iterateStatusSlice(list []*dtdiff.Entry) chan *dtdiff.Entry {
	c := make(chan *dtdiff.Entry)
	go func() {
		for _, entry := range list {
			c <- entry
		}
		close(c)
	}()
	return c
}

// MarkFullySynced sets both replicas as having incorporated all changes made in
// the other replica.
func (r *Result) MarkFullySynced() {
	r.rs.MarkSynced()
}

// SaveStatus saves a status file to the root of both replicas.
func (r *Result) SaveStatus() error {
	err := r.serializeStatus(r.rs.Get(0), r.root1)
	if err != nil {
		return err
	}
	return r.serializeStatus(r.rs.Get(1), r.root2)
}

// serializeStatus saves the status for one replica.
func (r *Result) serializeStatus(replica *dtdiff.Replica, root tree.Entry) error {
	outstatus, err := root.SetFile(STATUS_FILE)
	if err != nil {
		return err
	}
	err = replica.Serialize(outstatus)
	if err != nil {
		return err
	}
	return outstatus.Close()
}

// SyncAll applies all changes detected.
func (r *Result) SyncAll() error {
	for _, job := range r.jobs {
		err := job.Apply()
		if err != nil {
			// TODO: continue after errors, but mark the sync as unclean
			return err
		}
	}
	return nil
}

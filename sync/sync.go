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
	"errors"
	"io"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

var (
	ErrConflict       = errors.New("sync: job: unresolved conflict")
	ErrUnimplemented  = errors.New("sync: job: unimplemented")
	ErrAlreadyApplied = errors.New("sync: job: already applied")
	ErrSameRoot       = errors.New("sync: trying to synchronize the same directory")
)

// File where current status of the tree is stored.
const STATUS_FILE = ".dtsync"

// Result is returned by Scan on success. It contains the scan jobs that can
// then be applied.
type Result struct {
	rs         *dtdiff.ReplicaSet
	root1      tree.Entry
	root2      tree.Entry
	jobs       []*Job
	countTotal int
	countError int
}

// Scan the two filesystem roots for changes, and return results with a list of
// sync jobs.
func Scan(dir1, dir2 tree.Entry) (*Result, error) {
	if dir1 == dir2 {
		return nil, ErrSameRoot
	}

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

	err = result.scan()
	if err != nil {
		return nil, err
	}
	if len(result.jobs) == 0 {
		result.markSynced()
	}
	return result, nil
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

func (r *Result) scan() error {
	return r.scanDirs(r.root1, r.root2, r.rs.Get(0).Root(), r.rs.Get(1).Root())
}

// scanDirs implements the heart of the sync algorithm.
func (r *Result) scanDirs(dir1, dir2 tree.Entry, statusDir1, statusDir2 *dtdiff.Entry) error {
	for row := range iterateEntries(dir1, dir2, statusDir1, statusDir2) {
		if row.err != nil {
			// TODO don't stop, continue but mark the sync as unclean (don't
			// update certain generation numbers)
			return row.err
		}
		// Convenience shortcuts.
		file1 := row.file1
		file2 := row.file2
		status1 := row.status1
		status2 := row.status2

		// Add/update status if necessary.
		err := ensureStatus(&file1, &status1, &statusDir1)
		if err != nil {
			return err
		}
		err = ensureStatus(&file2, &status2, &statusDir2)
		if err != nil {
			return err
		}

		// so now status1 and status2 must both be defined, if the files are
		// defined
		if file1 == nil && file2 == nil {
			// Remove old status entries.
			if status1 != nil {
				status1.Remove()
			}
			if status2 != nil {
				status2.Remove()
			}
			continue
		}
		job := &Job{
			result:        r,
			status1:       status1,
			status2:       status2,
			statusParent1: statusDir1,
			statusParent2: statusDir2,
			file1:         file1,
			file2:         file2,
			parent1:       dir1,
			parent2:       dir2,
		}
		if file1 == nil {
			if status1 != nil {
				status1.Remove()
				status1 = nil
			}
			if statusDir1 != nil {
				if !statusDir1.HasRevision(status2) {
					job.action = ACTION_COPY
					job.direction = -1
				} else {
					job.action = ACTION_REMOVE
					job.direction = 1
				}
				r.jobs = append(r.jobs, job)
			}
			if file2.Type() == tree.TYPE_DIRECTORY {
				r.scanDirs(file1, file2, status1, status2)
			}
		} else if file2 == nil {
			if status2 != nil {
				status2.Remove()
				status2 = nil
			}
			if statusDir2 != nil {
				if !statusDir2.HasRevision(status1) {
					job.action = ACTION_COPY
					job.direction = 1
				} else {
					job.action = ACTION_REMOVE
					job.direction = -1
				}
				r.jobs = append(r.jobs, job)
			}
			if file1.Type() == tree.TYPE_DIRECTORY {
				r.scanDirs(file1, file2, status1, status2)
			}
		} else {
			// All four (file1, file2, status1, status2) are defined.
			// Compare the contents.
			if file1.Type() == tree.TYPE_DIRECTORY && file2.Type() == tree.TYPE_DIRECTORY {
				// Don't compare mtime of directories.
				// Future: maybe check for xattrs?
				r.scanDirs(file1, file2, status1, status2)
			} else if status1.Equal(status2) {
				// Two equal non-directories. We don't have to do more.
			} else if status1.Conflict(status2) {
				r.jobs = append(r.jobs, &Job{
					result:        r,
					action:        ACTION_UPDATE,
					direction:     0,
					status1:       status1,
					status2:       status2,
					statusParent1: statusDir1,
					statusParent2: statusDir2,
					parent1:       dir1,
					parent2:       dir2,
					file1:         file1,
					file2:         file2,
				})
			} else if status1.After(status2) {
				r.jobs = append(r.jobs, &Job{
					result:        r,
					action:        ACTION_UPDATE,
					direction:     1,
					status1:       status1,
					status2:       status2,
					statusParent1: statusDir1,
					statusParent2: statusDir2,
					parent1:       dir1,
					parent2:       dir2,
					file1:         file1,
					file2:         file2,
				})
			} else if status1.Before(status2) {
				r.jobs = append(r.jobs, &Job{
					result:        r,
					action:        ACTION_UPDATE,
					direction:     -1,
					status1:       status1,
					status2:       status2,
					statusParent1: statusDir1,
					statusParent2: statusDir2,
					parent1:       dir1,
					parent2:       dir2,
					file1:         file1,
					file2:         file2,
				})
			} else {
				// TODO we do get here, somehow. Apparently "Equal" doesn't
				// always return true on equality.
			}
		}
	}
	return nil
}

// ensureStatus adds a status entry if there isn't one.
func ensureStatus(file *tree.Entry, status, statusDir **dtdiff.Entry) error {
	if *file != nil {
		if *status == nil {
			var hash []byte
			var err error
			if (*file).Type() == tree.TYPE_REGULAR {
				hash, err = (*file).Hash()
				if err != nil {
					return err
				}
			}
			*status, err = (*statusDir).Add((*file).Name(), (*file).Fingerprint(), hash)
			if err != nil {
				panic("must not happen: " + err.Error())
			}
		} else {
			oldHash := (*status).Hash()
			oldFingerprint := (*status).Fingerprint()
			newFingerprint := (*file).Fingerprint()
			var newHash []byte
			var err error
			if oldFingerprint == newFingerprint && oldHash != nil {
				// Assume the hash stayed the same when the fingerprint is the
				// same. But calculate a new hash if there is no hash.
				newHash = oldHash
			} else {
				if (*file).Type() == tree.TYPE_REGULAR {
					newHash, err = (*file).Hash()
					if err != nil {
						return err
					}
				}
			}
			(*status).Update(newFingerprint, newHash)
		}
	}
	return nil
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

		var err error

		var listDir1 []tree.Entry
		if dir1 != nil {
			listDir1, err = dir1.List()
			if err != nil {
				c <- entryRow{err: err}
				return
			}
		}

		var listDir2 []tree.Entry
		if dir2 != nil {
			listDir2, err = dir2.List()
			if err != nil {
				c <- entryRow{err: err}
				return
			}
		}

		var listStatus1, listStatus2 []*dtdiff.Entry
		if statusDir1 != nil {
			listStatus1 = statusDir1.List()
		}
		if statusDir2 != nil {
			listStatus2 = statusDir2.List()
		}

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

func (r *Result) Jobs() []*Job {
	return r.jobs
}

// markSynced sets both replicas as having incorporated all changes made in the
// other replica.
func (r *Result) markSynced() {
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

// SyncStats is returned by SyncAll() and contains some statistics.
type SyncStats struct {
	CountTotal int
	CountError int
}

// SyncAll applies all changes detected.
func (r *Result) SyncAll() (SyncStats, error) {
	for _, job := range r.jobs {
		if job.applied {
			continue
		}
		err := job.Apply()
		if err != nil {
			// TODO: continue after errors, but mark the sync as unclean
			return SyncStats{
				CountTotal: r.countTotal,
				CountError: r.countError,
			}, err
		}
	}
	return SyncStats{
		CountTotal: r.countTotal,
		CountError: r.countError,
	}, nil
}

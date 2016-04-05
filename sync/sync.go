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

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

var (
	ErrConflict       = errors.New("sync: job: unresolved conflict")
	ErrUnimplemented  = errors.New("sync: job: unimplemented")
	ErrAlreadyApplied = errors.New("sync: job: already applied")
	ErrUnknownScheme  = errors.New("sync: unknown scheme")
)

// Result is returned by Scan on success. It contains the scan jobs that can
// then be applied.
type Result struct {
	rs         *dtdiff.ReplicaSet
	fs1        tree.Tree
	fs2        tree.Tree
	jobs       []*Job
	countTotal int
	countError int
}

// Scan the two filesystem roots for changes, and return results with a list of
// sync jobs.
func Scan(fs1, fs2 tree.Tree) (*Result, error) {
	rs, err := dtdiff.Scan(fs1, fs2)
	if err != nil {
		return nil, err
	}

	r := &Result{
		rs:  rs,
		fs1: fs1,
		fs2: fs2,
	}

	// Reconcile changes
	r.reconcile(r.rs.Get(0).Root(), r.rs.Get(1).Root())
	if len(r.jobs) == 0 {
		r.markSynced()
	}

	return r, nil
}

// reconcile compares two status trees, calculating the difference as sync jobs.
func (r *Result) reconcile(statusDir1, statusDir2 *dtdiff.Entry) {
	iterator := iterateEntries(statusDir1, statusDir2)
	for {
		status1, status2 := iterator()
		if status1 == nil && status2 == nil {
			break
		}

		if status1 != nil && status2 != nil {
			// Both are defined, so compare the contents.

			bothDirs := status1.Type() == tree.TYPE_DIRECTORY && status2.Type() == tree.TYPE_DIRECTORY
			if bothDirs {
				// Don't compare mtime of directories.
				// Future: maybe check for xattrs?
				r.reconcile(status1, status2)
			}

			if status1.Equal(status2) ||
					bothDirs && status1.EqualMode(status2) {
				// Two equal non-directories. We don't have to do more.
				continue
			}

			if !bothDirs && status1.EqualMode(status2) && status1.EqualContents(status2) {
				// Files changed in identical ways.
				continue
			}

			job := &Job{
				result:        r,
				status1:       status1,
				status2:       status2,
				statusParent1: statusDir1,
				statusParent2: statusDir2,
			}

			if status1.EqualContents(status2) || bothDirs && !status1.EqualMode(status2) {
				// only the mode differs
				job.action = ACTION_CHMOD
			} else {
				// the contents changed
				job.action = ACTION_UPDATE
			}

			if status1.Conflict(status2) {
				job.direction = 0
			} else if status1.After(status2) {
				job.direction = 1
			} else if status1.Before(status2) {
				job.direction = -1
			} else {
				panic("equal but not equal? (should be unreachable)")
			}

			r.jobs = append(r.jobs, job)

		} else {
			// One of the files does not exist.

			job := &Job{
				result:        r,
				status1:       status1,
				status2:       status2,
				statusParent1: statusDir1,
				statusParent2: statusDir2,
			}

			if status1 != nil {
				if statusDir2 != nil {
					if !statusDir2.HasRevision(status1) {
						job.action = ACTION_COPY
						job.direction = 1
					} else {
						// TODO: check for conflicts (a new or updated file
						// inside status1).
						job.action = ACTION_REMOVE
						job.direction = -1
					}
					r.jobs = append(r.jobs, job)
				}

			} else if status2 != nil {
				if statusDir1 != nil {
					if !statusDir1.HasRevision(status2) {
						job.action = ACTION_COPY
						job.direction = -1
					} else {
						// TODO: check for conflicts (a new or updated file
						// inside status1).
						job.action = ACTION_REMOVE
						job.direction = 1
					}
					r.jobs = append(r.jobs, job)
				}

			} else {
				panic("unreachable")
			}
		}
	}
}

// iterateEntries returns an iterator iterating over the two status dirs. The
// iterator is a function returning both child entries. Both entries have the
// same name, or one is nil. Both are nil when the end of the status dirs is
// reached.
func iterateEntries(statusDir1, statusDir2 *dtdiff.Entry) func() (*dtdiff.Entry, *dtdiff.Entry) {
	var listStatus1, listStatus2 []*dtdiff.Entry
	if statusDir1 != nil {
		listStatus1 = statusDir1.List()
	}
	if statusDir2 != nil {
		listStatus2 = statusDir2.List()
	}
	iterStatus1 := dtdiff.IterateEntries(listStatus1)
	iterStatus2 := dtdiff.IterateEntries(listStatus2)

	status1 := <-iterStatus1
	status2 := <-iterStatus2
	return func() (retStatus1, retStatus2 *dtdiff.Entry) {
		status1Name := ""
		if status1 != nil {
			status1Name = status1.Name()
		}
		status2Name := ""
		if status2 != nil {
			status2Name = status2.Name()
		}

		name := dtdiff.LeastName(status1Name, status2Name)
		// All of the names are empty strings, so we must have reached the end.
		if name == "" {
			return nil, nil
		}

		if status1Name == name {
			retStatus1 = status1
			status1 = <-iterStatus1
		}
		if status2Name == name {
			retStatus2 = status2
			status2 = <-iterStatus2
		}

		return
	}
}

// Jobs returns a list of jobs. You can apply them via a call to .Apply(), or
// you can call SyncAll() which does the same thing. When all jobs are
// successfully applied, both trees are marked as successfully synchronized.
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
	for i, fs := range []tree.Tree{r.fs1, r.fs2} {
		replica := r.rs.Get(i)
		if !replica.ChangedAny() {
			continue
		}
		err := r.serializeStatus(replica, fs)
		if err != nil {
			return err
		}
	}
	return nil
}

// serializeStatus saves the status for one replica.
func (r *Result) serializeStatus(replica *dtdiff.Replica, fs tree.Tree) error {
	outstatus, err := fs.SetFile(dtdiff.STATUS_FILE)
	if err != nil {
		return err
	}
	err = replica.Serialize(outstatus)
	if err != nil {
		// TODO: cancel?
		outstatus.Close()
		return err
	}
	return outstatus.Close()
}

// Stats is returned by SyncAll() and contains the total number and the number
// of applied jobs (successfull and failed) and the number of jobs that failed.
type Stats struct {
	CountTotal int
	CountError int
}

// SyncAll applies all changes detected. It returns a Stats object with the
// current statistics (including changes applied before, via for example
// Result.Jobs[0].Apply()).
func (r *Result) SyncAll() (Stats, error) {
	for _, job := range r.jobs {
		if job.applied {
			continue
		}
		err := job.Apply()
		if err != nil {
			// TODO: continue after errors, but mark the sync as unclean
			return r.Stats(), err
		}
	}
	return r.Stats(), nil
}

// Stats returns the same statistics as would be returned after SyncAll().
func (r *Result) Stats() Stats {
	return Stats{
		CountTotal: r.countTotal,
		CountError: r.countError,
	}
}

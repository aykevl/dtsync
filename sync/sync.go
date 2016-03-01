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
	"path/filepath"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

var (
	ErrConflict       = errors.New("sync: job: unresolved conflict")
	ErrUnimplemented  = errors.New("sync: job: unimplemented")
	ErrAlreadyApplied = errors.New("sync: job: already applied")
	ErrSameRoot       = errors.New("sync: trying to synchronize the same directory")
	ErrCanceled       = errors.New("sync: canceled") // must always be handled
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
	ignore     []string // paths to ignore
	cancel     uint32   // set to non-0 if the scan should cancel
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

	r := &Result{
		rs:    rs,
		root1: dir1,
		root2: dir2,
	}

	// Get files to ignore.
	for i := 0; i < 2; i++ {
		header := rs.Get(i).Header()
		r.ignore = append(r.ignore, header["Ignore"]...)
	}

	err = r.scan()
	if err != nil {
		return nil, err
	}
	if len(r.jobs) == 0 {
		r.markSynced()
	}
	return r, nil
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
	var scanners [2]chan error
	roots := []tree.Entry{r.root1, r.root2}
	for i := range roots {
		scanners[i] = make(chan error)
		go func(i int) {
			scanners[i] <- r.scanDir(roots[i], r.rs.Get(i).Root())
		}(i)
	}
	select {
	case err := <-scanners[0]:
		if err != nil {
			r.cancel = 1
			<-scanners[1]
			return err
		}
		err = <-scanners[1]
		if err != nil {
			return err
		}
	case err := <-scanners[1]:
		if err != nil {
			r.cancel = 1
			<-scanners[0]
			return err
		}
		err = <-scanners[0]
		if err != nil {
			return err
		}
	}

	// Reconcile changes
	r.reconcile(r.rs.Get(0).Root(), r.rs.Get(1).Root())
	return nil
}

// scanDir scans one side of the tree, updating the status tree to the current
// status.
func (r *Result) scanDir(dir tree.Entry, statusDir *dtdiff.Entry) error {
	fileList, err := dir.List()
	if err != nil {
		return err
	}
	iterator := nextFileStatus(fileList, statusDir.List())

	var file tree.Entry
	var status *dtdiff.Entry
	for {
		if r.cancel != 0 {
			return ErrCanceled
		}

		file, status = iterator()
		if file != nil && r.isIgnored(file) {
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
			var hash []byte
			var err error
			if file.Type() == tree.TYPE_REGULAR {
				hash, err = file.Hash()
				if err != nil {
					return err
				}
			}
			status, err = statusDir.Add(file.Name(), file.Fingerprint(), hash)
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
			err := r.scanDir(file, status)
			if err != nil {
				return err
			}
		}
	}

	return nil
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

			if status1.Type() == tree.TYPE_DIRECTORY && status2.Type() == tree.TYPE_DIRECTORY {
				// Don't compare mtime of directories.
				// Future: maybe check for xattrs?
				r.reconcile(status1, status2)
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
				})
			} else {
				panic("equal but not equal? (should be unreachable)")
			}

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
						job.action = ACTION_REMOVE
						job.direction = -1
					}
					r.jobs = append(r.jobs, job)
				}
				if status1.Type() == tree.TYPE_DIRECTORY {
					r.reconcile(status1, nil)
				}

			} else if status2 != nil {
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
				if status2.Type() == tree.TYPE_DIRECTORY {
					r.reconcile(nil, status2)
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
	iterStatus1 := iterateStatusSlice(listStatus1)
	iterStatus2 := iterateStatusSlice(listStatus2)

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

		name := leastName(status1Name, status2Name)
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

func (r *Result) isIgnored(file tree.Entry) bool {
	relpath := file.RelativePath()
	for _, pattern := range r.ignore {
		if len(pattern) == 0 {
			continue
		}
		// TODO: use a more advanced pattern matching method.
		if pattern[0] == '/' {
			// Match relative to the root.
			if match, err := filepath.Match(pattern[1:], relpath); match && err == nil {
				return true
			}
		} else {
			// Match only the name. This does not work when the pattern contains
			// slashes.
			if match, err := filepath.Match(pattern, file.Name()); match && err == nil {
				return true
			}
		}
	}
	return false
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

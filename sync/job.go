// job.go
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

package sync

import (
	"path"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

// Action is the type for the ACTION_* constants
type Action int

// Some job action constants. The names should be obvious.
const (
	ACTION_COPY   Action = iota
	ACTION_UPDATE Action = iota
	ACTION_CHMOD  Action = iota
	ACTION_REMOVE Action = iota
)

// Size to add per inode that is going to be changed. The cost is roughly the
// number of bytes to copy.
// This is a semi-random number. There is a cost per file that's created/removed
// (even if it's small), this is just a blind guess at that cost.
const COST_PER_INODE = 1024

func (a Action) String() string {
	s := "action?"
	switch a {
	case ACTION_COPY:
		s = "copy"
	case ACTION_UPDATE:
		s = "update"
	case ACTION_CHMOD:
		s = "chmod"
	case ACTION_REMOVE:
		s = "remove"
	}
	return s
}

// Job is one action to apply (copy, update or delete)
type Job struct {
	result        *Result
	applied       bool
	action        Action
	status1       *dtdiff.Entry
	status2       *dtdiff.Entry
	statusParent1 *dtdiff.Entry
	statusParent2 *dtdiff.Entry
	// When newDirection differs from direction, the effective action is changed
	// as well.
	//
	// Values:
	//   1 left-to-right aka forwards (file1 → file2)
	//   0 undecided (conflict, user must choose)
	//  -1 right-to-left aka backwards (file1 ← file2)
	direction     int
	origDirection int
}

// String returns a representation of this job for debugging.
func (j *Job) String() string {
	var direction string
	switch j.direction {
	case 1:
		direction = ",→,"
	case -1:
		direction = ",←,"
	default:
		direction = ",-,"
	}
	return "Job(" + j.Action().String() + direction + j.Path() + ")"
}

// Name returns the filename of the file to be copied, updated, or removed.
func (j *Job) Name() string {
	status1, status2 := j.primary()
	switch j.Direction() {
	case 1:
		return status1.Name()
	case 0:
		return status1.Name() + "," + status2.Name()
	case -1:
		return status2.Name()
	default:
		panic("unknown direction")
	}
}

// Path returns the relative path separated by forward slashes (/). Just like
// Name().
func (j *Job) Path() string {
	status1, status2 := j.primary()
	switch j.Direction() {
	case 1:
		return path.Join(status1.RelativePath()...)
	case 0:
		switch {
		case status1 == nil:
			return path.Join(status2.RelativePath()...)
		case status2 == nil:
			return path.Join(status1.RelativePath()...)
		default:
			return path.Join(status1.RelativePath()...) + "," + path.Join(status2.RelativePath()...)
		}
	case -1:
		return path.Join(status2.RelativePath()...)
	default:
		panic("unknown direction")
	}
}

// RelativePath returns the path relative to it's root.
func (j *Job) RelativePath() string {
	// This must be updated when we implement file or directory moves: then the
	// paths cannot be assumed to be the same.
	var relpath []string
	if j.status1 == nil {
		relpath = j.status2.RelativePath()
	} else {
		relpath = j.status1.RelativePath()
	}
	return path.Join(relpath...)
}

func (j *Job) PathLeft() string {
	if j.status1 == nil {
		return path.Join(path.Join(j.statusParent1.RelativePath()...), j.status2.Name())
	} else {
		return path.Join(j.status1.RelativePath()...)
	}
}

func (j *Job) PathRight() string {
	if j.status2 == nil {
		return path.Join(path.Join(j.statusParent2.RelativePath()...), j.status1.Name())
	} else {
		return path.Join(j.status2.RelativePath()...)
	}
}

// StatusLeft returns an identifying string of what happened on the left side of
// the sync.
func (j *Job) StatusLeft() string {
	return j.status(j.status1, j.statusParent1, j.status2, j.statusParent2)
}

// StatusRight is similar to StatusLeft.
func (j *Job) StatusRight() string {
	return j.status(j.status2, j.statusParent2, j.status1, j.statusParent1)
}

func (j *Job) primary() (*dtdiff.Entry, *dtdiff.Entry) {
	status1, status2 := j.status1, j.status2
	if j.Action() == ACTION_REMOVE {
		status1, status2 = status2, status1
	}
	return status1, status2
}

// Apply this job (copying, updating, or removing).
func (j *Job) Apply(progress chan int64) error {
	defer func() {
		if progress != nil {
			close(progress)
		}
	}()

	if j.applied {
		return ErrAlreadyApplied
	}

	status1 := j.status1
	status2 := j.status2
	statusParent1 := j.statusParent1
	statusParent2 := j.statusParent2
	fs1 := j.result.fs1
	fs2 := j.result.fs2

	switch j.Direction() {
	case 1:
		// don't swap
	case 0:
		return ErrConflict
	case -1:
		// swap: we're going the opposite direction
		status1, status2 = status2, status1
		statusParent1, statusParent2 = statusParent2, statusParent1
		fs1, fs2 = fs2, fs1
	default:
		panic("unknown direction")
	}
	j.applied = true

	// Add error now, remove it at the end when there was no error (all errors
	// return early).
	j.result.lock.Lock()
	j.result.countTotal++
	j.result.countError++
	j.result.lock.Unlock()

	switch j.Action() {
	case ACTION_COPY:
		err := copyFile(fs1, fs2, status1, statusParent2, progress)
		if err != nil {
			return err
		}
	case ACTION_UPDATE:
		info, parentInfo, _, err := tree.Update(fs1, fs2, status1, status2, progress)
		if err != nil {
			if tree.IsChanged(err) {
				status1.UpdateHash(tree.Hash{}, nil)
			}
			return err
		}
		if !info.Hash().Equal(status1.Hash()) {
			// This should have been checked in the Update() function.
			panic("hash change wasn't detected")
		}
		status2.Update(info, statusParent2.Filesystem(), info.Hash(), status1)
		statusParent2.Update(parentInfo, statusParent2.Filesystem(), tree.Hash{}, statusParent1)
	case ACTION_REMOVE:
		parentInfo, err := fs2.Remove(status2)
		if err != nil {
			return err
		}
		status2.Remove()
		statusParent2.Update(parentInfo, statusParent2.Filesystem(), tree.Hash{}, statusParent1)
	case ACTION_CHMOD:
		info, err := fs2.Chmod(status2, status1)
		if err != nil {
			return err
		}
		status2.Update(info, status2.Filesystem(), status2.Hash(), status1)
	default:
		panic("unknown action (must not happen)")
	}

	// There was no error.
	j.result.lock.Lock()
	j.result.countError--
	if j.result.countTotal == len(j.result.jobs) && j.result.countError == 0 {
		j.result.markSynced()
	}
	j.result.lock.Unlock()

	return nil
}

func copyFile(fs1, fs2 tree.Tree, status1, statusParent2 *dtdiff.Entry, progress chan int64) error {
	if status1.Type() == tree.TYPE_DIRECTORY {
		info, err := fs2.CreateDir(status1.Name(), statusParent2, status1)
		if err != nil {
			return err
		}
		status2, err := statusParent2.Add(info, status1)
		if err != nil {
			panic(err)
		}
		if progress != nil {
			progress <- COST_PER_INODE
		}
		list := status1.List()
		for _, child1 := range list {
			err := copyFile(fs1, fs2, child1, status2, progress)
			if err != nil {
				// TODO revert
				return err
			}
		}
	} else {
		info, parentInfo, err := tree.Copy(fs1, fs2, status1, statusParent2, progress)
		if err != nil {
			return err
		}
		statusParent2.Update(parentInfo, statusParent2.Filesystem(), tree.Hash{}, nil)
		_, err = statusParent2.Add(info, status1)
		if err != nil {
			return err
		}
		if progress != nil {
			progress <- COST_PER_INODE
		}
	}
	return nil
}

// Direction returns the sync direction of this file, with 1 for left-to-right,
// 0 for undecided (conflict), and -1 for right-to-left.
func (j *Job) Direction() int {
	return j.direction
}

// OrigDirection returns the original sync direction of this file (before
// SetDirection).
func (j *Job) OrigDirection() int {
	return j.origDirection
}

// SetDirection sets the job direction, which must be -1, 0, or 1. Any other
// value will cause a panic.
func (j *Job) SetDirection(direction int) {
	switch direction {
	case -1, 0, 1:
	default:
		panic("invalid direction")
	}
	j.direction = direction
}

// Action returns the (possibly flipped) action constant.
func (j *Job) Action() Action {
	if j.direction != 0 && j.origDirection != 0 && j.direction != j.origDirection {
		switch j.action {
		case ACTION_COPY:
			return ACTION_REMOVE
		case ACTION_REMOVE:
			return ACTION_COPY
		default:
			return j.action
		}
	}
	return j.action
}

// Applied returns true if this action was already applied.
func (j *Job) Applied() bool {
	return j.applied
}

// status returns the change that was applied to this file (new, modified,
// removed).
func (j *Job) status(status, statusParent, otherStatus, otherStatusParent *dtdiff.Entry) string {
	if status == nil {
		if statusParent.HasRevision(otherStatus) {
			return "removed"
		} else {
			return ""
		}
	} else if otherStatus == nil {
		if otherStatusParent.HasRevision(status) {
			return ""
		} else {
			return "new"
		}
	}
	if status.After(otherStatus) {
		if j.action == ACTION_CHMOD {
			return "chmod"
		} else {
			return "modified"
		}
	}
	return ""
}

func (j *Job) StatusEntryLeft() *dtdiff.Entry {
	return j.status1
}

func (j *Job) StatusEntryRight() *dtdiff.Entry {
	return j.status2
}

func (j *Job) Cost() int64 {
	status1 := j.status1
	status2 := j.status2

	switch j.Direction() {
	case 1:
		// don't swap
	case 0:
		// No cost for a no-op
		return 0
	case -1:
		// swap: we're going the opposite direction
		status1, status2 = status2, status1
	default:
		panic("unknown direction")
	}

	switch j.Action() {
	case ACTION_COPY, ACTION_UPDATE:
		count, bytes := status1.Count()
		return int64(count)*COST_PER_INODE + bytes
	case ACTION_CHMOD:
		return COST_PER_INODE
	case ACTION_REMOVE:
		count, _ := status2.Count()
		return int64(count) * COST_PER_INODE
	default:
		panic("unknown action")
	}
}

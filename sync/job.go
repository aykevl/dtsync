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
	"bytes"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

// Action is the type for the ACTION_* constants
type Action int

// Some job action constants. The names should be obvious.
const (
	ACTION_COPY   Action = iota
	ACTION_UPDATE Action = iota
	ACTION_REMOVE Action = iota
)

func (a Action) String() string {
	s := "action?"
	switch a {
	case ACTION_COPY:
		s = "copy"
	case ACTION_UPDATE:
		s = "update"
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
	file1         tree.Entry
	file2         tree.Entry
	parent1       tree.Entry
	parent2       tree.Entry
	// The direction is a special one.
	// When it is changed, file1, file2 etc are also changed.
	// And if it is a copy or delete, the action is reversed (copy becomes
	// delete, and delete becomes copy).
	//
	// Values:
	//   1 left-to-right aka forwards (file1 → file2)
	//   0 undecided (conflict, user must choose)
	//  -1 right-to-left aka backwards (file1 ← file2)
	direction       int
	hasNewDirection bool
	newDirection    int
}

// String returns a representation of this job for debugging.
func (j *Job) String() string {
	return "Job(" + j.Action().String() + "," + j.Name() + ")"
}

// Name returns the filename of the file to be copied, updated, or removed.
func (j *Job) Name() string {
	var name1, name2 string
	if j.file1 != nil {
		name1 = j.file1.Name()
	}
	if j.file2 != nil {
		name2 = j.file2.Name()
	}
	if j.Action() == ACTION_REMOVE {
		name1, name2 = name2, name1
	}
	switch j.Direction() {
	case 1:
		return name1
	case 0:
		return name1 + "," + name2
	case -1:
		return name2
	default:
		panic("unknown direction")
	}
}

// Apply this job (copying, updating, or removing).
func (j *Job) Apply() error {
	if j.applied {
		return ErrAlreadyApplied
	}

	status1 := j.status1
	status2 := j.status2
	statusParent1 := j.statusParent1
	statusParent2 := j.statusParent2
	file1 := j.file1
	file2 := j.file2
	parent1 := j.parent1
	parent2 := j.parent2

	switch j.Direction() {
	case 1:
		// don't swap
	case 0:
		return ErrConflict
	case -1:
		// swap: we're going the opposite direction
		status1, status2 = status2, status1
		statusParent1, statusParent2 = statusParent2, statusParent1
		file1, file2 = file2, file1
		parent1, parent2 = parent2, parent1
	default:
		panic("unknown direction")
	}
	j.applied = true

	var err error
	switch j.Action() {
	case ACTION_COPY:
		err = copyFile(file1, parent2, status1, statusParent2)
	case ACTION_UPDATE:
		var hash []byte
		hash, err = file1.UpdateOver(file2)
		if err == nil {
			if !bytes.Equal(hash, status1.Hash()) {
				// The first file got updated between the scan and update.
				// TODO Should we report this as an error?
				status1.UpdateHash(hash)
			}
			status2.UpdateFrom(status1)
			if statusParent2 != nil {
				statusParent2.Update(parent2.Fingerprint(), nil)
			}
		}
	case ACTION_REMOVE:
		err = file2.Remove()
		if err == nil {
			status2.Remove()
			statusParent2.Update(parent2.Fingerprint(), nil)
		}
	default:
		panic("unknown action (must not happen)")
	}

	j.result.countTotal++
	if err != nil {
		j.result.countError++
	}
	if j.result.countTotal == len(j.result.jobs) && j.result.countError == 0 {
		j.result.markSynced()
	}
	return err
}

func copyFile(file1, parent2 tree.Entry, status1, statusParent2 *dtdiff.Entry) error {
	if file1.Type() == tree.TYPE_DIRECTORY {
		parent2, ok := parent2.(tree.FileEntry)
		if !ok {
			return tree.ErrNotImplemented
		}
		file2, err := parent2.CreateDir(file1.Name())
		if err != nil {
			// TODO revert
			return err
		}
		status2, err := statusParent2.Add(file2.Name(), file2.Fingerprint(), nil)
		if err != nil {
			return err
		}
		statusParent2.Update(parent2.Fingerprint(), nil)

		list, err := file1.List()
		if err != nil {
			// TODO revert
			return err
		}
		statusList := status1.List()
		if len(list) != len(statusList) {
			// TODO this might occur when something changes between scanning
			// and applying the job.
			panic("list must be equal to statusList")
		}
		for i, child1 := range list {
			childStatus1 := statusList[i]
			if child1.Name() != childStatus1.Name() {
				panic("list must be equal to statusList")
			}
			err := copyFile(child1, file2, childStatus1, status2)
			if err != nil {
				// TODO revert
				return err
			}
		}
		return nil
	} else {
		file2, hash, err := file1.CopyTo(parent2)
		if err == nil {
			_, err = statusParent2.Add(file2.Name(), file2.Fingerprint(), hash)
			statusParent2.Update(parent2.Fingerprint(), nil)
		}
		return err
	}
}

// Direction returns the sync direction of this file, with 1 for left-to-right,
// 0 for undecided (conflict), and -1 for right-to-left.
func (j *Job) Direction() int {
	if j.hasNewDirection {
		return j.newDirection
	}
	return j.direction
}

// SetDirection sets the job direction, which must be -1, 0, or 1. Any other
// value will cause a panic.
func (j *Job) SetDirection(direction int) {
	switch direction {
	case -1, 0, 1:
	default:
		panic("invalid direction")
	}
	j.newDirection = direction
	j.hasNewDirection = true
}

// Action returns the (possibly flipped) action constant.
func (j *Job) Action() Action {
	if j.hasNewDirection && j.direction != 0 && j.newDirection != 0 && j.direction != j.newDirection {
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

// StatusLeft returns an identifying string of what happened on the left side of
// the sync.
func (j *Job) StatusLeft() string {
	return j.status(j.file1, j.parent1, j.file2, j.parent2, j.status1, j.statusParent1, j.status2, j.statusParent2)
}

// StatusRight is similar to StatusLeft.
func (j *Job) StatusRight() string {
	return j.status(j.file2, j.parent2, j.file1, j.parent1, j.status2, j.statusParent2, j.status1, j.statusParent1)
}

// status returns the change that was applied to this file (new, modified,
// removed).
func (j *Job) status(file, parent, otherFile, otherParent tree.Entry, status, statusParent, otherStatus, otherStatusParent *dtdiff.Entry) string {
	if file == nil {
		if statusParent.HasRevision(otherStatus) {
			return "removed"
		} else {
			return ""
		}
	} else if otherFile == nil {
		if otherStatusParent.HasRevision(status) {
			return ""
		} else {
			return "new"
		}
	}
	if status.After(otherStatus) {
		return "modified"
	}
	return ""
}

// RelativePath returns the path relative to it's root.
func (j *Job) RelativePath() string {
	// This must be updated when we implement file or directory moves: then the
	// paths cannot be assumed to be the same.
	if j.file1 == nil {
		return j.status2.RelativePath()
	}
	return j.status1.RelativePath()
}

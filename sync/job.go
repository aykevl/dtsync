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
	direction int
}

// String returns a representation of this job for debugging.
func (j *Job) String() string {
	return "Job(" + j.action.String() + "," + j.Name() + ")"
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
	if j.action == ACTION_REMOVE {
		name1, name2 = name2, name1
	}
	switch j.direction {
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

	switch j.direction {
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
	switch j.action {
	case ACTION_COPY:
		file2, err = file1.CopyTo(parent2)
		if err == nil {
			_, err = statusParent2.AddCopy(status1)
		}
	case ACTION_UPDATE:
		err = file1.Update(file2)
		if err == nil {
			status2.SetRevision(status1)
		}
	case ACTION_REMOVE:
		err = parent2.Remove(file2)
		if err == nil {
			status2.Remove()
			status1.Remove() // TODO status1 may be nil
		}
	default:
		panic("unknown action (must not happen)")
	}
	return err
}

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
	"errors"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

var (
	ErrConflict       = errors.New("sync: job: unresolved conflict")
	ErrUnimplemented  = errors.New("sync: job: unimplemented")
	ErrAlreadyApplied = errors.New("sync: job: already applied")
)

// Action is the type for the ACTION_* constants
type Action int

// Some job action constants. The names should be obvious.
const (
	ACTION_COPY   Action = iota
	ACTION_UPDATE Action = iota
	ACTION_REMOVE Action = iota
)

// Job is one action to apply (copy, update or delete)
type Job struct {
	applied       bool
	action        Action
	status1       *dtdiff.Entry
	status2       *dtdiff.Entry
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
	//   0 left-to-right (file1 → file2)
	//   1 right-to-left (file2 → file1)
	//  -1 conflict (user must choose)
	direction int
}

// String returns a representation of this job for debugging.
func (j *Job) String() string {
	action := "action?"
	switch j.action {
	case ACTION_COPY:
		action = "copy"
	case ACTION_UPDATE:
		action = "update"
	case ACTION_REMOVE:
		action = "remove"
	}

	return "Job(" + action + "," + j.file1.Name() + ")"
}

// Apply this job (copying, updating, or removing).
func (j *Job) Apply() error {
	if j.applied {
		return ErrAlreadyApplied
	}
	j.applied = true

	var err error
	switch j.action {
	case ACTION_COPY:
		j.file2, err = j.file1.CopyTo(j.parent2)
		if err == nil {
			_, err = j.statusParent2.AddCopy(j.status1)
		}
	case ACTION_UPDATE:
		err = j.file1.Update(j.file2)
		if err == nil {
			j.status2.SetRevision(j.status1)
		}
	case ACTION_REMOVE:
		err = j.parent1.Remove(j.file1)
		if err == nil {
			j.status1.Remove()
			j.status2.Remove()
		}
	default:
		panic("unknown action (must not happen)")
	}
	return err
}

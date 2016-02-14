// entry.go
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

package rtdiff

import (
	"time"
)

// An Entry is one object (row) in a Replica. It belongs to one Replica.
type Entry struct {
	name string
	// rev*: replica and it's generation at that moment where this Entry last changed
	revReplica    string
	revGeneration int
	modTime       time.Time
	children      map[string]*Entry
	parent        *Entry
	replica       *Replica
}

// String function, for debugging purposes
func (e *Entry) String() string {
	return "Entry(" + e.name + ")"
}

// Add new entry by recursively finding the parent
func (e *Entry) add(path []string, revReplica string, revGeneration int, modTime time.Time) error {
	if path[0] == "" {
		return ErrInvalidPath
	}
	if len(path) > 1 {
		child, ok := e.children[path[0]]
		if !ok {
			// child does not exist
			// or: the path has a parent that hasn't yet been scanned
			return ErrInvalidPath
		}
		return child.add(path[1:], revReplica, revGeneration, modTime)
	} else {
		_, ok := e.children[path[0]]
		if ok {
			// duplicate path
			return ErrInvalidPath
		}
		newEntry := &Entry{
			name:          path[0],
			revReplica:    revReplica,
			revGeneration: revGeneration,
			modTime:       modTime,
			children:      make(map[string]*Entry),
			parent:        e,
			replica:       e.replica,
		}
		e.children[newEntry.name] = newEntry
		return nil
	}
}

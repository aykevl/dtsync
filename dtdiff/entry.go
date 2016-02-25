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

package dtdiff

import (
	"sort"
)

// An Entry is one object (row) in a Replica. It belongs to one Replica.
type Entry struct {
	name string
	// rev*: replica and it's generation at that moment where this Entry last changed
	revReplica    string
	revGeneration int
	fingerprint   string
	children      map[string]*Entry
	parent        *Entry
	replica       *Replica
}

// String function, for debugging purposes
func (e *Entry) String() string {
	return "dtdiff.Entry(" + e.name + ")"
}

// Name returns the name of this entry
func (e *Entry) Name() string {
	return e.name
}

// Add new entry by recursively finding the parent
func (e *Entry) add(path []string, revReplica string, revGeneration int, fingerprint string) error {
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
		return child.add(path[1:], revReplica, revGeneration, fingerprint)
	} else {
		_, err := e.addChild(path[0], revReplica, revGeneration, fingerprint)
		return err
	}
}

func (e *Entry) addChild(name string, revReplica string, revGeneration int, fingerprint string) (*Entry, error) {
	if _, ok := e.children[name]; ok {
		// duplicate path
		return nil, ErrExists
	}
	newEntry := &Entry{
		name:          name,
		revReplica:    revReplica,
		revGeneration: revGeneration,
		fingerprint:   fingerprint,
		children:      make(map[string]*Entry),
		parent:        e,
		replica:       e.replica,
	}
	e.children[newEntry.name] = newEntry
	return newEntry, nil
}

// Get returns the named child, or nil if it doesn't exist.
func (e *Entry) Get(name string) *Entry {
	return e.children[name]
}

// HasRevision returns true if this file (actually, this replica) includes the
// revision the other entry is at.
func (e *Entry) HasRevision(other *Entry) bool {
	return e.replica.peerGenerations[other.revReplica] >= other.revGeneration
}

// Equal returns true if both entries are of the same revision (replica and
// generation). Not recursive.
func (e *Entry) Equal(e2 *Entry) bool {
	return e.revReplica == e2.revReplica && e.revGeneration == e2.revGeneration
}

// After returns true if this entry was modified after the other.
func (e *Entry) After(e2 *Entry) bool {
	otherGeneration, ok := e2.replica.peerGenerations[e.revReplica]
	if !ok {
		return true
	}
	return e.revGeneration > otherGeneration
}

// Before returns true if this entry is modified before the other.
func (e *Entry) Before(e2 *Entry) bool {
	return e2.After(e)
}

// Conflict returns true if both entries are modified.
func (e *Entry) Conflict(e2 *Entry) bool {
	return e.After(e2) && e2.After(e)
}

// Includes returns true if this entry includes all revisions from the other
// entry (recursively: children are also compared).
//
// FIXME: It does not always work when one file is removed. It does work however
// to check for equality, though.
func (e *Entry) Includes(e2 *Entry) bool {
	if e2.After(e) && !e.isRoot() {
		return false
	}

	for name, child2 := range e2.children {
		_, ok := e.children[name]
		if !ok {
			// if child2 has all revisions, that means it is deleted here, not
			// new there.
			if child2.HasRevision(e) {
				// their child was removed
				return false
			}
		}
	}
	for name, child := range e.children {
		child2, ok := e2.children[name]
		if !ok {
			if !child.HasRevision(e2) {
				// our child is outdated and to-be-removed
				return false
			}
		} else {
			if !child.Includes(child2) {
				return false
			}
		}
	}
	return true
}

func (e *Entry) isRoot() bool {
	return e.parent == nil
}

func (e *Entry) List() []*Entry {
	list := make([]*Entry, 0, len(e.children))
	for _, entry := range e.children {
		list = append(list, entry)
	}
	sortEntries(list)
	return list
}

// Add a new status entry.
func (e *Entry) Add(name, fingerprint string) (*Entry, error) {
	e.replica.markChanged()
	return e.addChild(name, e.replica.identity, e.replica.generation, fingerprint)
}

// AddCopy copies the status entry to here, keeping the revision.
func (e *Entry) AddCopy(other *Entry) (*Entry, error) {
	e.replica.markChanged()
	return e.addChild(other.name, other.revReplica, other.revGeneration, other.fingerprint)
}

// Update updates the revision if the file was changed.
func (e *Entry) Update(fingerprint string) {
	if fingerprint != e.fingerprint {
		e.replica.markChanged()
		e.fingerprint = fingerprint
		e.revReplica = e.replica.identity
		e.revGeneration = e.replica.generation
	}
}

// UpdateFrom sets this entry as updated from the other entry, copying the
// revision and modification time.
func (e *Entry) UpdateFrom(other *Entry) {
	e.replica.markChanged()
	e.revReplica = other.revReplica
	e.revGeneration = other.revGeneration
	e.fingerprint = other.fingerprint
}

// Remove this entry.
func (e *Entry) Remove() {
	e.replica.markChanged()
	// simply make it unreachable
	delete(e.parent.children, e.name)
}

type entrySlice []*Entry

func (es entrySlice) Len() int {
	return len(es)
}

func (es entrySlice) Less(i, j int) bool {
	return es[i].name < es[j].name
}

func (es entrySlice) Swap(i, j int) {
	es[i], es[j] = es[j], es[i]
}

func sortEntries(list []*Entry) {
	sort.Sort(entrySlice(list))
}

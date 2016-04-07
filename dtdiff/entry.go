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
	"bytes"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/dtsync/tree"
)

type revision struct {
	identity   string // random string as identifier for the replica
	generation int    // starts at 1, 0 means 'no generation'
}

// String for debug/test printing.
func (r revision) String() string {
	return r.identity + ":" + strconv.Itoa(r.generation)
}

// An Entry is one object (row) in a Replica. It belongs to one Replica.
type Entry struct {
	name     string
	revision // last content (hash) change
	fileType tree.Type
	modTime  time.Time
	size     int64
	mode     tree.Mode
	hasMode  tree.Mode
	hash     []byte
	children map[string]*Entry
	parent   *Entry
	replica  *Replica
	removed  time.Time
}

// String function, for debugging purposes
func (e *Entry) String() string {
	return "dtdiff.Entry(" + strings.Join(e.RelativePath(), "/") + "," + e.revision.String() + ")"
}

// Name returns the name of this entry
func (e *Entry) Name() string {
	return e.name
}

// RelativePath returns the path relative to the root
func (e *Entry) RelativePath() []string {
	if e.parent == nil {
		return nil
	} else {
		return append(e.parent.RelativePath(), e.name)
	}
}

// Hash returns the hash of the status entry, if one is known.
func (e *Entry) Hash() []byte {
	return e.hash
}

// Type returns the tree.Type filetype.
func (e *Entry) Type() tree.Type {
	return e.fileType
}

// Mode returns the permission bits for this entry.
func (e *Entry) Mode() tree.Mode {
	return e.mode
}

// HasMode returns the permission bits this entry supports.
func (e *Entry) HasMode() tree.Mode {
	return e.hasMode
}

// ModTime returns the last modification time.
func (e *Entry) ModTime() time.Time {
	return e.modTime
}

// Size returns the filesize for regular files, or 0.
func (e *Entry) Size() int64 {
	return e.size
}

// Add new entry by recursively finding the parent
func (e *Entry) addRecursive(path []string, rev revision, fingerprint string, mode tree.Mode, hash []byte) (*Entry, error) {
	if path[0] == "" {
		return nil, errInvalidPath
	}
	if len(path) > 1 {
		child, ok := e.children[path[0]]
		if !ok {
			// child does not exist
			// or: the path has a parent that hasn't yet been scanned
			return nil, errInvalidPath
		}
		return child.addRecursive(path[1:], rev, fingerprint, mode, hash)
	} else {
		fileInfo, err := parseFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
		return e.addChild(path[0], rev, fileInfo, mode, hash)
	}
}

func (e *Entry) addChild(name string, rev revision, fileInfo fingerprintInfo, mode tree.Mode, hash []byte) (*Entry, error) {
	if _, ok := e.children[name]; ok {
		// duplicate path
		return nil, ErrExists
	}
	child := &Entry{
		name:     name,
		revision: rev,
		fileType: fileInfo.fileType,
		modTime:  fileInfo.modTime,
		size:     fileInfo.size,
		mode:     mode,
		hasMode:  e.hasMode,
		hash:     hash,
		children: make(map[string]*Entry),
		parent:   e,
		replica:  e.replica,
	}
	e.children[child.name] = child
	return child, nil
}

// Get returns the named child, or nil if it doesn't exist.
func (e *Entry) Get(name string) *Entry {
	return e.children[name]
}

// HasRevision returns true if this file (actually, this replica) includes the
// revision the other entry is at.
func (e *Entry) HasRevision(other *Entry) bool {
	return e.replica.knowledge[other.identity] >= other.generation
}

// Equal returns true if both entries are of the same revision (replica and
// generation). Not recursive.
func (e *Entry) Equal(e2 *Entry) bool {
	return e.HasRevision(e2) && e2.HasRevision(e)
}

// EqualContents returns true if the contents (fingerprint/hash) of these
// entries is the same.
func (e *Entry) EqualContents(e2 *Entry) bool {
	if tree.MatchFingerprint(e, e2) {
		return true
	}
	if len(e.hash) > 0 && bytes.Equal(e.hash, e2.hash) {
		return true
	}
	return false
}

// EqualMode compares the mode bits, noting the HasMode of both entries.
func (e *Entry) EqualMode(e2 *Entry) bool {
	hasMode := e.hasMode & e2.hasMode
	return e.mode&hasMode == e2.mode&hasMode
}

// After returns true if this entry was modified after the other.
func (e *Entry) After(e2 *Entry) bool {
	return e.generation > e2.replica.knowledge[e.identity]
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
	list := e.rawList()

	// Remove removed children from the list.
	p := 0
	for i, child := range list {
		if i != p {
			list[p] = list[i]
		}
		if child.removed.IsZero() {
			// go to next if not removed
			p++
		}
	}
	return list[:p]
}

func (e *Entry) rawList() []*Entry {
	list := make([]*Entry, 0, len(e.children))
	for _, entry := range e.children {
		list = append(list, entry)
	}

	sortEntries(list)
	return list
}

// Add a new status entry.
func (e *Entry) Add(info tree.FileInfo, source *Entry) (*Entry, error) {
	return e.add(info, source.revision)
}

func (e *Entry) add(info tree.FileInfo, rev revision) (*Entry, error) {
	e.replica.markChanged()
	fileInfo := fingerprintInfo{info.Type(), info.ModTime(), info.Size()}
	if fileInfo.fileType != tree.TYPE_REGULAR {
		// Maybe not necessary, but just to be safe...
		fileInfo.size = 0
	}
	return e.addChild(info.Name(), rev, fileInfo, info.Mode(), info.Hash())
}

// Update updates the revision if the file was changed. The file is not changed
// if the fingerprint but not the hash changed.
func (e *Entry) Update(info tree.FileInfo, hash []byte, source *Entry) {
	if !tree.MatchFingerprint(e, info) {
		e.fileType = info.Type()
		e.modTime = info.ModTime()
		e.size = info.Size()
		e.mode = info.Mode()
		e.hasMode = info.HasMode()
		if e.Type() == tree.TYPE_SYMLINK {
			// Changes in fingerprints of symbolic links must be tracked. For
			// regular files we look at the hash and for directories fingerprint
			// changes do not cause updates at all (but are still tracked).
			if source != nil {
				e.replica.markMetaChanged()
				e.revision = source.revision
			} else {
				e.replica.markChanged()
				e.revision = e.replica.revision
			}
		} else {
			e.replica.markMetaChanged()
		}
	}
	if e.fileType != tree.TYPE_DIRECTORY {
		e.children = nil
	}

	if info.HasMode()&^e.hasMode != 0 {
		// There are new permission bits. Mark as changed so permissions are
		// compared.
		e.replica.markChanged()
		e.revision = e.replica.revision
	}
	if info.HasMode() != e.hasMode {
		// This is a different filesystem or the support for filesystem
		// detection changed.
		e.replica.markMetaChanged()
		e.hasMode = info.HasMode()
	}
	hasMode := e.hasMode // same as info.HasMode()
	if info.Mode()&hasMode != e.mode&hasMode {
		// The permission bits got updated.
		e.mode = info.Mode() & hasMode
		if source != nil {
			e.revision = source.revision
		} else {
			e.replica.markChanged()
			e.revision = e.replica.revision
		}
	}

	e.UpdateHash(hash, source)
}

// UpdateHash sets the new hash from the parameter, marking this file as changed
// if it is different from the existing one.
func (e *Entry) UpdateHash(hash []byte, source *Entry) {
	if !bytes.Equal(e.hash, hash) {
		if source != nil {
			e.replica.markMetaChanged()
			e.revision = source.revision
		} else {
			e.replica.markChanged()
			e.revision = e.replica.revision
		}
		e.hash = hash
	}
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

// IterateEntries returns a channel, reads from the channel will return each
// entry in the slice.
func IterateEntries(list []*Entry) chan *Entry {
	c := make(chan *Entry)
	go func() {
		for _, entry := range list {
			c <- entry
		}
		close(c)
	}()
	return c
}

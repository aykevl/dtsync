// memory.go
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

// Package memory implements the file tree interface (tree.Entry). All data is
// stored in memory. This implementation is mostly intended for testing
// purposes.
package memory

import (
	"bytes"
	"io"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aykevl/dtsync/tree"
)

const (
	DEFAULT_MODE     = 0644
	DEFAULT_DIR_MODE = 0755
	DEFAULT_HAS_MODE = 0777
)

// Call nextUniqueId() to get one.
var uniqueId = uint64(1)

// Entry is one file or directory.
type Entry struct {
	fileType tree.Type
	inode    uint64
	mode     tree.Mode
	hasMode  tree.Mode
	modTime  time.Time
	name     string
	contents []byte
	children map[string]*Entry
	parent   *Entry
}

// NewRoot creates a new in-memory filesystem root.
func NewRoot() *Entry {
	return &Entry{
		fileType: tree.TYPE_DIRECTORY,
		inode:    nextUniqueId(),
		mode:     DEFAULT_DIR_MODE,
		hasMode:  DEFAULT_HAS_MODE,
		modTime:  time.Now(),
	}
}

// String returns a string for debugging purposes (pretty printing).
func (e *Entry) String() string {
	return "memory.Entry(" + e.fileType.Char() + "," + strings.Join(e.RelativePath(), "/") + ")"
}

// Close does nothing: there are no resources allocated that won't be collected
// by the garbage collector.
func (e *Entry) Close() error {
	return nil
}

func (e *Entry) root() *Entry {
	root := e
	for !root.isRoot() {
		root = root.parent
	}
	return root
}

// Tree returns the root Entry.
func (e *Entry) Tree() tree.Tree {
	return e.root()
}

func (e *Entry) Root() tree.Entry {
	return e.root()
}

func (e *Entry) isRoot() bool {
	return e.parent == nil
}

// Type returns the file type (file, directory)
func (e *Entry) Type() tree.Type {
	return e.fileType
}

// Mode returns the mode permission bits.
func (e *Entry) Mode() tree.Mode {
	return e.mode
}

// HasMode returns the supported mode bits, currently 0777.
func (e *Entry) HasMode() tree.Mode {
	return 0777
}

// Name returns the filename (not the path)
func (e *Entry) Name() string {
	return e.name
}

func (e *Entry) pathElements() []string {
	if e.parent == nil {
		parts := make([]string, 0, 1)
		return parts
	}
	return append(e.parent.pathElements(), e.Name())
}

// RelativePath returns the path relative to the root.
func (e *Entry) RelativePath() []string {
	return e.pathElements()
}

func (e *Entry) childRelativePath(name string) []string {
	return append(e.pathElements(), name)
}

// Size returns the filesize for files, or the number of direct children for
// directories.
func (e *Entry) Size() int64 {
	switch e.fileType {
	case tree.TYPE_REGULAR, tree.TYPE_SYMLINK:
		return int64(len(e.contents))
	case tree.TYPE_DIRECTORY:
		return int64(len(e.children))
	case tree.TYPE_NOTFOUND:
		return 0
	default:
		panic("unknown fileType")
	}
}

// ModTime returns the modification or creation time.
func (e *Entry) ModTime() time.Time {
	return e.modTime
}

// Id returns the inode and dummy filesystem information.
func (e *Entry) Id() (uint64, *tree.LocalFilesystem) {
	return e.inode, &tree.LocalFilesystem{
		Type:     "dtsync.memory",
		DeviceId: 1,
	}
}

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() (tree.Hash, error) {
	return e.hash(), nil
}

func (e *Entry) hash() tree.Hash {
	switch e.fileType {
	case tree.TYPE_REGULAR:
		hash := tree.NewHash()
		_, err := hash.Write(e.contents)
		if err != nil {
			panic(err) // hash writer may not return an error
		}
		return tree.Hash{tree.HASH_DEFAULT, hash.Sum(nil)}
	case tree.TYPE_SYMLINK:
		// Don't save the hash, but the actual contents of the symlink (which
		// should be short).
		return tree.Hash{tree.HASH_TARGET, e.contents}
	default:
		return tree.Hash{tree.HASH_NONE, nil}
	}
}

func (e *Entry) makeInfo(hash tree.Hash) tree.FileInfo {
	return tree.NewFileInfo(e.RelativePath(), e.fileType, e.mode, 0777, e.modTime, e.Size(), e.inode, hash)
}

// Info returns a FileInfo object without a hash.
func (e *Entry) Info() tree.FileInfo {
	return e.makeInfo(tree.Hash{})
}

// FullInfo returns a FileInfo object with hash. It won't return an error
// (there are no IO errors in an in-memory filesystem).
func (e *Entry) FullInfo() (tree.FileInfo, error) {
	return e.fullInfo(), nil
}

func (e *Entry) fullInfo() tree.FileInfo {
	return e.makeInfo(e.hash())
}

// ReadInfo returns the FileInfo for the specified file, with a hash, or a 'not
// found' error if the file doesn't exist.
func (e *Entry) ReadInfo(path []string) (tree.FileInfo, error) {
	f := e.get(path)
	if f == nil {
		return nil, tree.ErrNotFound(path)
	}
	return f.fullInfo(), nil
}

// List returns a list of directory entries for directories. It returns an error
// when attempting to list something other than a directory.
func (e *Entry) List(options tree.ListOptions) ([]tree.Entry, error) {
	if e.fileType != tree.TYPE_DIRECTORY {
		return nil, tree.ErrNoDirectory(e.RelativePath())
	}

	ret := make([]tree.Entry, 0, len(e.children))
	for _, entry := range e.children {
		// Quick path, for normal files.
		if entry.fileType != tree.TYPE_SYMLINK || options.Follow == nil || !options.Follow(entry.RelativePath()) {
			ret = append(ret, entry)
			continue
		}

		// Handle symlink.
		entryOrig := entry

		// Keep a set of followed links, to detect circular references.
		followed := make(map[*Entry]struct{})
		for entry != nil && entry.fileType == tree.TYPE_SYMLINK {
			entry = entry.follow()
			if _, ok := followed[entry]; ok {
				// This is a circular reference (this entry exists in the
				// path). A 'not found' error isn't entirely correct, but
				// otherwise we'd have to define yet another filetype for this
				// error. Maybe actually reporting this as an error would be
				// the better solution.
				entry = nil
			}
			followed[entry] = struct{}{}
		}

		if entry == nil {
			entry = &Entry{
				fileType: tree.TYPE_NOTFOUND,
				name:     entryOrig.Name(),
				parent:   entryOrig.parent,
			}
		} else {
			// Clone the Entry if a file was found
			// I hope this works out well.
			// TODO: separate Entry and Node, make a new Entry but keep the Node.
			// The Node is then a filesystem inode (which will be required
			// anyway for hardlink and rename support).
			clone := *entry
			entry = &clone
			entry.name = entryOrig.Name()
			entry.parent = entryOrig.parent
		}

		ret = append(ret, entry)
	}
	tree.SortEntries(ret)
	return ret, nil
}

// get returns the child Entry from the path.
func (e *Entry) get(path []string) *Entry {
	child := e
	for _, part := range path {
		if part == ".." {
			if !child.isRoot() {
				child = child.parent
			}
		} else if part == "." {
			child = child
		} else {
			child = child.children[part]
			if child == nil {
				return nil
			}
		}
	}
	return child
}

// follow returns the Entry this symlink points to.
// Only call this function on symlinks.
func (e *Entry) follow() *Entry {
	if e.fileType != tree.TYPE_SYMLINK {
		panic("this is not a symlink that is followed")
	}
	relpath := path.Clean(string(e.contents))
	var child *Entry
	if path.IsAbs(relpath) {
		child = e.root().get(strings.Split(relpath[1:], "/"))
	} else {
		child = e.parent.get(strings.Split(relpath, "/"))
	}
	return child
}

// CopySource creates a bytes.Buffer reader to read the contents of this file.
func (e *Entry) CopySource(source tree.FileInfo) (io.ReadCloser, error) {
	s := e.get(source.RelativePath())
	if s == nil {
		return nil, tree.ErrNotFound(source.RelativePath())
	}
	if !tree.MatchFingerprint(s.Info(), source) {
		return nil, tree.ErrChanged(s.RelativePath())
	}
	return &fileCloser{
		bytes.NewBuffer(s.contents),
		func(buf *bytes.Buffer) {},
	}, nil
}

// Remove removes the specified entry, recursively.
func (e *Entry) Remove(info tree.FileInfo) (tree.FileInfo, error) {
	child := e.get(info.RelativePath())
	if child == nil {
		return nil, tree.ErrNotFound(info.RelativePath())
	}
	if child.parent.children[child.name] != child {
		// already removed?
		return nil, tree.ErrNotFound(info.RelativePath())
	}
	if child.Type() == tree.TYPE_DIRECTORY {
		if child.Type() != info.Type() {
			return nil, tree.ErrChanged(child.RelativePath())
		}
	} else {
		if !tree.MatchFingerprint(child.Info(), info) {
			return nil, tree.ErrChanged(child.RelativePath())
		}
	}
	delete(child.parent.children, child.name)
	child.parent.modTime = time.Now()
	return child.parent.Info(), nil
}

// GetFile returns a file handle (io.ReadCloser) that can be used to read a
// status file.
func (e *Entry) GetFile(name string) (io.ReadCloser, error) {
	if entry, ok := e.children[name]; ok {
		return newReadCloseBuffer(entry.contents), nil
	} else {
		return nil, tree.ErrNotFound([]string{name})
	}
}

// PutFile returns a file handle (tree.Copier) to a newly created/replaced
// child file that can be used to save the replica state.
func (e *Entry) PutFile(name string) (tree.Copier, error) {
	if child, ok := e.children[name]; ok {
		return newFileCopier(func(buf *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
			child.contents = buf.Bytes()
			return nil, nil, nil
		}), nil
	} else {
		return newFileCopier(func(buf *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
			child := &Entry{
				fileType: tree.TYPE_REGULAR,
				inode:    nextUniqueId(),
				mode:     DEFAULT_MODE & e.hasMode,
				hasMode:  e.hasMode,
				modTime:  time.Now(),
				name:     name,
				contents: buf.Bytes(),
				parent:   e,
			}
			err := e.addChild(child)
			return nil, nil, err
		}), nil
	}
}

// CreateDir creates a directory with the given name.
func (e *Entry) CreateDir(name string, parentInfo, sourceInfo tree.FileInfo) (tree.FileInfo, error) {
	parent := e.get(parentInfo.RelativePath())
	if parent == nil {
		return nil, tree.ErrNotFound([]string{name})
	}
	child := &Entry{
		fileType: tree.TYPE_DIRECTORY,
		inode:    nextUniqueId(),
		mode:     sourceInfo.Mode() & parent.hasMode,
		hasMode:  parent.hasMode,
		modTime:  time.Now(),
		name:     name,
		parent:   parent,
	}
	err := parent.addChild(child)
	if err != nil {
		return nil, err
	}
	return child.fullInfo(), nil
}

// CreateFile is part of tree.FileEntry. It returns the created entry, a
// WriteCloser to write the data to, and possibly an error.
// The file's contents is stored when the returned WriteCloser is closed.
func (e *Entry) CreateFile(name string, parent, source tree.FileInfo) (tree.Copier, error) {
	p := e.get(parent.RelativePath())
	if p == nil {
		return nil, tree.ErrNotFound(parent.RelativePath())
	}

	if _, ok := p.children[name]; ok {
		return nil, tree.ErrFound(p.childRelativePath(name))
	}

	modTime := source.ModTime()
	if modTime.IsZero() {
		modTime = time.Now()
	}

	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		inode:    nextUniqueId(),
		mode:     source.Mode() & p.hasMode,
		hasMode:  p.hasMode,
		modTime:  modTime,
		name:     name,
		parent:   p,
	}

	file := newFileCopier(func(buffer *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
		err := p.addChild(child)
		if err != nil {
			return nil, nil, err
		}
		child.contents = buffer.Bytes()
		return child.Info(), child.parent.Info(), nil
	})

	return file, nil
}

func (e *Entry) addChild(child *Entry) error {
	if e.fileType != tree.TYPE_DIRECTORY {
		return tree.ErrNoDirectory(e.RelativePath())
	}
	if child.parent != e {
		panic("addChild to wrong parent")
	}
	if e.children == nil {
		e.children = make(map[string]*Entry)
	}
	if _, ok := e.children[child.Name()]; ok {
		return tree.ErrFound(e.childRelativePath(child.Name()))
	}
	if !validName(child.name) {
		return tree.ErrInvalidName
	}

	e.children[child.Name()] = child
	e.modTime = time.Now()
	return nil
}

// UpdateFile is part of tree.FileEntry and implements replacing a file.
// When closing the returned WriteCloser, the file is actually replaced.
func (e *Entry) UpdateFile(file, source tree.FileInfo) (tree.Copier, error) {
	child := e.get(file.RelativePath())
	if child == nil {
		return nil, tree.ErrNotFound(file.RelativePath())
	}
	if child.fileType != tree.TYPE_REGULAR {
		return nil, tree.ErrNoRegular(child.RelativePath())
	}
	if !tree.MatchFingerprint(file, child.Info()) {
		return nil, tree.ErrChanged(child.RelativePath())
	}
	return newFileCopier(func(buffer *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
		child.modTime = source.ModTime()
		child.contents = buffer.Bytes()
		child.mode = source.Mode().Calc(source.HasMode(), DEFAULT_MODE) & child.hasMode
		return child.Info(), child.parent.Info(), nil
	}), nil
}

// UpdateRsync opens the existing file to generate a signature for, and creates
// a new file where the new data is written, from the base file and from the
// patch data.
func (e *Entry) UpdateRsync(file, source tree.FileInfo) (tree.RsyncBasis, tree.Copier, error) {
	child := e.get(file.RelativePath())
	if child == nil {
		return nil, nil, tree.ErrNotFound(file.RelativePath())
	}
	if child.fileType != tree.TYPE_REGULAR {
		return nil, nil, tree.ErrNoRegular(child.RelativePath())
	}
	if !tree.MatchFingerprint(file, child.Info()) {
		return nil, nil, tree.ErrChanged(child.RelativePath())
	}
	base := newReadCloseBuffer(child.contents)
	copier := newFileCopier(func(buffer *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
		child.modTime = source.ModTime()
		child.contents = buffer.Bytes()
		child.mode = source.Mode().Calc(source.HasMode(), DEFAULT_MODE) & child.hasMode
		return child.Info(), child.parent.Info(), nil
	})
	return base, copier, nil
}

// CreateSymlink creates an Entry with type link and the contents.
func (e *Entry) CreateSymlink(name string, parentInfo, sourceInfo tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	parent := e.get(parentInfo.RelativePath())
	if parent == nil {
		return nil, nil, tree.ErrNotFound(parentInfo.RelativePath())
	}

	child := &Entry{
		fileType: tree.TYPE_SYMLINK,
		inode:    nextUniqueId(),
		mode:     sourceInfo.Mode() & parent.hasMode, // not relevant on Unix filesystems
		hasMode:  parent.hasMode,
		name:     name,
		contents: []byte(contents),
		parent:   parent,
	}
	if !sourceInfo.ModTime().IsZero() {
		child.modTime = sourceInfo.ModTime()
	}
	err := parent.addChild(child)
	if err != nil {
		return nil, nil, err
	}
	return child.fullInfo(), child.parent.Info(), nil
}

// UpdateSymlink sets the contents of this entry if it is a symlink.
func (e *Entry) UpdateSymlink(file, source tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	child := e.get(file.RelativePath())
	if child == nil {
		return nil, nil, tree.ErrNotFound(file.RelativePath())
	}
	if child.fileType != tree.TYPE_SYMLINK {
		return nil, nil, tree.ErrNoSymlink(child.RelativePath())
	}

	if !tree.MatchFingerprint(child.Info(), file) {
		return nil, nil, tree.ErrChanged(child.RelativePath())
	}

	if source.ModTime().IsZero() {
		child.modTime = time.Now()
	} else {
		child.modTime = source.ModTime()
	}
	child.contents = []byte(contents)
	return child.fullInfo(), child.parent.Info(), nil
}

// ReadSymlink returns the contents of this entry if it is a symlink.
func (e *Entry) ReadSymlink(file tree.FileInfo) (string, error) {
	child := e.get(file.RelativePath())
	if child == nil {
		return "", tree.ErrNotFound(file.RelativePath())
	}
	if child.fileType != tree.TYPE_SYMLINK {
		return "", tree.ErrNoSymlink(child.RelativePath())
	}
	if !tree.MatchFingerprint(child.Info(), file) {
		return "", tree.ErrChanged(child.RelativePath())
	}
	return string(child.contents), nil
}

// Chmod applies the given mode bits, as far as this in-memory tree 'supports'
// them.
func (e *Entry) Chmod(target, source tree.FileInfo) (tree.FileInfo, error) {
	child := e.get(target.RelativePath())
	if child == nil {
		return nil, tree.ErrNotFound(target.RelativePath())
	}
	child.mode = source.Mode().Calc(source.HasMode(), DEFAULT_MODE) & child.hasMode
	return child.Info(), nil
}

// PutFileTest writes the contents to a new or existing file.
// This function only exists for testing purposes.
func (e *Entry) PutFileTest(path []string, contents []byte) (tree.FileInfo, error) {
	parent := e.get(path[:len(path)-1])
	if parent == nil {
		return nil, tree.ErrNotFound(path[:len(path)-1])
	}

	name := path[len(path)-1]
	child := parent.children[name]
	if child != nil {
		if child.fileType != tree.TYPE_REGULAR {
			return nil, tree.ErrNoRegular(child.RelativePath())
		}
		child.modTime = time.Now()
		child.contents = contents
	} else {
		child = &Entry{
			fileType: tree.TYPE_REGULAR,
			inode:    nextUniqueId(),
			mode:     DEFAULT_MODE & parent.hasMode,
			hasMode:  parent.hasMode,
			modTime:  time.Now(),
			name:     path[len(path)-1],
			contents: contents,
			parent:   parent,
		}
		err := parent.addChild(child)
		if err != nil {
			return nil, err
		}
	}
	return child.fullInfo(), nil
}

// Return the next unique number, atomically (may be called from multiple
// goroutines at the same time).
func nextUniqueId() uint64 {
	return atomic.AddUint64(&uniqueId, 1)
}

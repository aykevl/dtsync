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
	"time"

	"github.com/aykevl/dtsync/tree"
)

// Entry is one file or directory.
type Entry struct {
	fileType tree.Type
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
		modTime:  time.Now(),
	}
}

// String returns a string for debugging purposes (pretty printing).
func (e *Entry) String() string {
	return "memory.Entry(" + e.name + ")"
}

// Close does nothing: there are no resources allocated that won't be collected
// by the garbage collector.
func (e *Entry) Close() error {
	return nil
}

func (e *Entry) root() *Entry {
	root := e
	for root.parent != nil {
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

// Size returns the filesize for files, or the number of direct children for
// directories.
func (e *Entry) Size() int64 {
	switch e.fileType {
	case tree.TYPE_REGULAR:
		return int64(len(e.contents))
	case tree.TYPE_DIRECTORY:
		return int64(len(e.children))
	default:
		panic("unknown fileType")
	}
}

// ModTime returns the modification or creation time.
func (e *Entry) ModTime() time.Time {
	return e.modTime
}

// Fingerprint returns a fingerprint calculated from the file's metadata.
func (e *Entry) Fingerprint() string {
	return tree.Fingerprint(e.info())
}

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() ([]byte, error) {
	return e.hash(), nil
}

func (e *Entry) hash() []byte {
	if e.Type() != tree.TYPE_REGULAR {
		return nil
	}
	hash := tree.NewHash()
	_, err := hash.Write(e.contents)
	if err != nil {
		panic(err) // hash writer may not return an error
	}
	return hash.Sum(nil)
}

func (e *Entry) info() tree.FileInfo {
	return tree.NewFileInfo(e.RelativePath(), e.fileType, e.modTime, e.Size(), e.hash())
}

// Info returns a FileInfo object. It won't return an error (there are no IO
// errors in an in-memory filesystem).
func (e *Entry) Info() (tree.FileInfo, error) {
	return e.info(), nil
}

// ReadInfo returns the FileInfo for the specified file, or tree.ErrNotFound if
// the file doesn't exist.
func (e *Entry) ReadInfo(path []string) (tree.FileInfo, error) {
	f := e.get(path)
	if f == nil {
		return nil, tree.ErrNotFound
	}
	return f.info(), nil
}

// List returns a list of directory entries for directories. It returns an error
// when attempting to list something other than a directory.
func (e *Entry) List() ([]tree.Entry, error) {
	if e.fileType != tree.TYPE_DIRECTORY {
		return nil, tree.ErrNoDirectory
	}

	ret := make([]tree.Entry, 0, len(e.children))
	for _, entry := range e.children {
		ret = append(ret, entry)
	}
	tree.SortEntries(ret)
	return ret, nil
}

// get returns the child Entry from the path.
func (e *Entry) get(path []string) *Entry {
	child := e
	for _, part := range path {
		child = child.children[part]
		if child == nil {
			return nil
		}
	}
	return child
}

// CopySource creates a bytes.Buffer reader to read the contents of this file.
func (e *Entry) CopySource(source tree.FileInfo) (io.ReadCloser, error) {
	s := e.get(source.RelativePath())
	if s == nil {
		return nil, tree.ErrNotFound
	}
	if tree.Fingerprint(s.info()) != tree.Fingerprint(source) {
		return nil, tree.ErrChanged
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
		return nil, tree.ErrNotFound
	}
	if child.parent.children[child.name] != child {
		// already removed?
		return nil, tree.ErrNotFound
	}
	if child.Type() == tree.TYPE_DIRECTORY {
		if child.Type() != info.Type() {
			return nil, tree.ErrChanged
		}
	} else {
		if child.Fingerprint() != tree.Fingerprint(info) {
			return nil, tree.ErrChanged
		}
	}
	delete(child.parent.children, child.name)
	child.parent.modTime = time.Now()
	return child.parent.info(), nil
}

// GetFile returns a file handle (io.ReadCloser) that can be used to read a
// status file.
func (e *Entry) GetFile(name string) (io.ReadCloser, error) {
	if entry, ok := e.children[name]; ok {
		return newReadCloseBuffer(entry.contents), nil
	} else {
		return nil, tree.ErrNotFound
	}
}

// SetFile returns a file handle (io.WriteCloser) to a newly created/replaced
// child file that can be used to save the replica state.
func (e *Entry) SetFile(name string) (io.WriteCloser, error) {
	if child, ok := e.children[name]; ok {
		return newFileCloser(func(buf *bytes.Buffer) {
			child.contents = buf.Bytes()
		}), nil
	} else {
		return newFileCloser(func(buf *bytes.Buffer) {
			e.AddRegular([]string{name}, buf.Bytes())
		}), nil
	}
}

// CreateDir creates a directory with the given name.
func (e *Entry) CreateDir(name string, parentInfo tree.FileInfo) (tree.FileInfo, error) {
	parent := e.get(parentInfo.RelativePath())
	if parent == nil {
		return nil, tree.ErrNotFound
	}
	child := &Entry{
		fileType: tree.TYPE_DIRECTORY,
		modTime:  time.Now(),
		name:     name,
		parent:   parent,
	}
	err := parent.addChild(child)
	if err != nil {
		return nil, err
	}
	return child.info(), nil
}

// CreateFile is part of tree.FileEntry. It returns the created entry, a
// WriteCloser to write the data to, and possibly an error.
// The file's contents is stored when the returned WriteCloser is closed.
func (e *Entry) CreateFile(name string, parent, source tree.FileInfo) (tree.Copier, error) {
	p := e.get(parent.RelativePath())
	if p == nil {
		return nil, tree.ErrNotFound
	}

	if _, ok := p.children[name]; ok {
		return nil, tree.ErrFound
	}

	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		modTime:  source.ModTime(),
		name:     name,
		parent:   p,
	}

	file := newFileCopier(func(buffer *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
		err := p.addChild(child)
		if err != nil {
			return nil, nil, err
		}
		child.contents = buffer.Bytes()
		return child.info(), child.parent.info(), nil
	})

	return file, nil
}

func (e *Entry) addChild(child *Entry) error {
	if e.fileType != tree.TYPE_DIRECTORY {
		return tree.ErrNoDirectory
	}
	if child.parent != e {
		panic("addChild to wrong parent")
	}
	if e.children == nil {
		e.children = make(map[string]*Entry)
	}
	if _, ok := e.children[child.Name()]; ok {
		return tree.ErrFound
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
		return nil, tree.ErrNotFound
	}
	if child.fileType != tree.TYPE_REGULAR {
		return nil, tree.ErrNoRegular
	}
	return newFileCopier(func(buffer *bytes.Buffer) (tree.FileInfo, tree.FileInfo, error) {
		child.modTime = source.ModTime()
		child.contents = buffer.Bytes()
		return child.info(), child.parent.info(), nil
	}), nil
}

// AddRegular creates a new regular file.
// This function only exists for testing purposes.
func (e *Entry) AddRegular(path []string, contents []byte) (tree.FileInfo, error) {
	parent := e.get(path[:len(path)-1])
	if parent == nil {
		return nil, tree.ErrNotFound
	}

	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		modTime:  time.Now(),
		name:     path[len(path)-1],
		contents: contents,
		parent:   parent,
	}
	err := parent.addChild(child)
	if err != nil {
		return nil, err
	}
	return child.info(), nil
}

// GetContents returns a reader to read the contents of the file. Must be closed
// after use.
func (e *Entry) GetContents(path []string) (io.ReadCloser, error) {
	if !e.isRoot() {
		panic("not a root")
	}
	child := e.get(path)
	if child == nil {
		return nil, tree.ErrNotFound
	}
	return newReadCloseBuffer(child.contents), nil
}

// SetContents sets the internal contents of the file, for debugging.
func (e *Entry) SetContents(path []string, contents []byte) (tree.FileInfo, error) {
	child := e.get(path)
	if child == nil {
		return nil, tree.ErrNotFound
	}
	child.modTime = time.Now()
	child.contents = contents
	return child.info(), nil
}

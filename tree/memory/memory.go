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
}

// NewRoot creates a new in-memory filesystem root.
func NewRoot() *Entry {
	return &Entry{
		fileType: tree.TYPE_DIRECTORY,
		// do not set the modTime to ease comparing two trees
	}
}

// String returns a string for debugging purposes (pretty printing).
func (e *Entry) String() string {
	return "memory.Entry(" + e.name + ")"
}

// Type returns the file type (file, directory)
func (e *Entry) Type() tree.Type {
	return e.fileType
}

// Name returns the filename (not the path)
func (e *Entry) Name() string {
	return e.name
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

// CopyTo copies this file into the given parent, returning an error if the file
// already exists.
func (e *Entry) CopyTo(otherParent tree.Entry) (tree.Entry, error) {
	file, ok := otherParent.(tree.FileEntry)
	if !ok {
		return nil, tree.ErrNotImplemented
	}
	other, out, err := file.CreateFile(e.name, e.modTime)
	if err != nil {
		return nil, err
	}
	_, err = out.Write(e.contents)
	if err != nil {
		return nil, err
	}
	err = out.Close()
	if err != nil {
		return nil, err
	}
	return other, nil
}

// Update copies data and metadata to the given other file.
func (e *Entry) Update(other tree.Entry) error {
	file, ok := other.(tree.FileEntry)
	if !ok {
		return tree.ErrNotImplemented
	}
	out, err := file.UpdateFile(e.modTime)
	if err != nil {
		return err
	}

	_, err = out.Write(e.contents)
	if err != nil {
		return err
	}

	err = out.Close()
	if err != nil {
		return err
	}

	return nil
}

// Remove removes this file or directory tree, recursively.
func (e *Entry) Remove(child tree.Entry) error {
	if e.fileType != tree.TYPE_DIRECTORY {
		return tree.ErrNoDirectory
	}
	if e.children[child.Name()] != child {
		return tree.ErrNotFound
	}
	delete(e.children, child.Name())
	return child.(*Entry).removeSelf()
}

// removeSelf removes this file, but does not remove the entry from the parent
// map.
// TODO unnecessary?
func (e *Entry) removeSelf() error {
	if e.fileType == tree.TYPE_DIRECTORY {
		list, err := e.List()
		if err != nil {
			return err
		}
		for _, child := range list {
			child.(*Entry).removeSelf()
		}
	}
	return nil
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
		return newFileCopier(func(buf *bytes.Buffer) {
			child.contents = buf.Bytes()
		}), nil
	} else {
		return newFileCopier(func(buf *bytes.Buffer) {
			e.AddRegular(name, buf.Bytes())
		}), nil
	}
}

// CreateDir creates a directory with the given name.
func (e *Entry) CreateDir(name string) (tree.Entry, error) {
	child := &Entry{
		fileType: tree.TYPE_DIRECTORY,
		name:     name,
	}
	err := e.addChild(child)
	if err != nil {
		return nil, err
	}
	return child, nil
}

// CreateFile is part of tree.FileEntry. It returns the created entry, a
// WriteCloser to write the data to, and possibly an error.
// The file's contents is stored when the returned WriteCloser is closed.
func (e *Entry) CreateFile(name string, modTime time.Time) (tree.Entry, io.WriteCloser, error) {
	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		modTime:  modTime,
		name:     name,
	}
	err := e.addChild(child)
	if err != nil {
		return nil, nil, err
	}

	file := newFileCopier(func(buffer *bytes.Buffer) {
		child.contents = buffer.Bytes()
	})

	return child, file, nil
}

func (e *Entry) addChild(child *Entry) error {
	if e.fileType != tree.TYPE_DIRECTORY {
		return tree.ErrNoDirectory
	}
	if e.children == nil {
		e.children = make(map[string]*Entry)
	}
	if _, ok := e.children[child.Name()]; ok {
		return tree.ErrAlreadyExists
	}

	e.children[child.Name()] = child
	return nil
}

// UpdateFile is part of tree.FileEntry and implements replacing a file.
// When closing the returned WriteCloser, the file is actually replaced.
func (e *Entry) UpdateFile(modTime time.Time) (io.WriteCloser, error) {
	if e.fileType != tree.TYPE_REGULAR {
		return nil, tree.ErrNoRegular
	}
	file := newFileCopier(func(buffer *bytes.Buffer) {
		e.modTime = modTime
		e.contents = buffer.Bytes()
	})
	return file, nil
}

// AddRegular creates a new regular file.
// This function only exists for testing purposes.
func (e *Entry) AddRegular(name string, contents []byte) (*Entry, error) {
	if e.fileType != tree.TYPE_DIRECTORY {
		return nil, tree.ErrNoDirectory
	}
	if e.children == nil {
		e.children = make(map[string]*Entry)
	}
	if _, ok := e.children[name]; ok {
		return nil, tree.ErrAlreadyExists
	}
	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		modTime:  time.Now(),
		name:     name,
		contents: contents,
	}
	e.children[name] = child
	return child, nil
}

// SetContents sets the internal contents of the file, for debugging.
func (e *Entry) SetContents(contents []byte) {
	e.modTime = time.Now()
	e.contents = contents
}

// Equal compares this entry with another entry, only returning true when this
// file and possible children (for directories) are exactly equal.
func (e *Entry) Equal(other *Entry) bool {
	if e.name != other.name || !e.modTime.Equal(other.modTime) || e.fileType != other.fileType {
		return false
	}
	switch e.fileType {
	case tree.TYPE_REGULAR:
		return bytes.Equal(e.contents, other.contents)
	case tree.TYPE_DIRECTORY:
		if len(e.children) != len(other.children) {
			return false
		}
		for name, child := range e.children {
			otherchild, ok := other.children[name]
			if !ok || !child.Equal(otherchild) {
				return false
			}
		}
		return true
	default:
		panic("unknown fileType")
	}
}

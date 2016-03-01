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
	"path/filepath"
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
		parts := make([]string, 1)
		parts[0] = e.Name()
		return parts
	}
	return append(e.parent.pathElements(), e.Name())
}

// RelativePath returns the path relative to the root.
func (e *Entry) RelativePath() string {
	return filepath.Join(e.pathElements()...)
}

// Get returns the child that may not be a direct child.
func (e *Entry) Get(path []string) (tree.Entry, error) {
	child := e
	for _, name := range path {
		if name == "" {
			panic("path contains empty string")
		}
		child = child.children[name]
		if child == nil {
			return nil, tree.ErrNotFound
		}
	}
	return child, nil
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
	return tree.Fingerprint(e)
}

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() ([]byte, error) {
	hash := tree.NewHash()
	_, err := hash.Write(e.contents)
	if err != nil {
		panic(err) // hash writer may not return an error
	}
	return hash.Sum(nil), nil
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
func (e *Entry) CopyTo(otherParent tree.Entry) (tree.Entry, []byte, error) {
	file, ok := otherParent.(tree.FileEntry)
	if !ok {
		return nil, nil, tree.ErrNotImplemented
	}

	switch e.fileType {
	case tree.TYPE_REGULAR:
		other, out, err := file.CreateFile(e.name, e.modTime)
		if err != nil {
			return nil, nil, err
		}
		_, err = out.Write(e.contents)
		if err != nil {
			return nil, nil, err
		}
		err = out.Close()
		if err != nil {
			return nil, nil, err
		}
		hash, err := e.Hash()
		if err != nil {
			// we're hasing a buffer, that may not return an error
			panic(err)
		}
		return other, hash, nil

	default:
		return nil, nil, tree.ErrNotImplemented
	}
}

// UpdateOver copies data and metadata to the given other file.
func (e *Entry) UpdateOver(other tree.Entry) ([]byte, error) {
	file, ok := other.(tree.FileEntry)
	if !ok {
		return nil, tree.ErrNotImplemented
	}

	switch e.fileType {
	case tree.TYPE_REGULAR:
		out, err := file.UpdateFile(e.modTime)
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

		hash, err := e.Hash()
		if err != nil {
			// we're hasing a buffer, that may not return an error
			panic(err)
		}

		return hash, nil

	default:
		return nil, tree.ErrNotImplemented
	}
}

// Remove removes this file or directory tree, recursively.
func (e *Entry) Remove() error {
	if e.parent.children[e.name] != e {
		// already removed?
		return tree.ErrNotFound
	}
	delete(e.parent.children, e.name)
	e.parent.modTime = time.Now()
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
		modTime:  time.Now(),
		name:     name,
		parent:   e,
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
		parent:   e,
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
	if !tree.ValidName(child.name) {
		return tree.ErrInvalidName
	}

	e.children[child.Name()] = child
	e.modTime = time.Now()
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
func (e *Entry) AddRegular(name string, contents []byte) (tree.FileEntry, error) {
	child := &Entry{
		fileType: tree.TYPE_REGULAR,
		modTime:  time.Now(),
		name:     name,
		contents: contents,
		parent:   e,
	}
	err := e.addChild(child)
	if err != nil {
		return nil, err
	}
	return child, nil
}

// GetContents returns a reader to read the contents of the file. Must be closed
// after use.
func (e *Entry) GetContents() (io.ReadCloser, error) {
	return newReadCloseBuffer(e.contents), nil
}

// SetContents sets the internal contents of the file, for debugging.
func (e *Entry) SetContents(contents []byte) error {
	e.modTime = time.Now()
	e.contents = contents
	return nil
}

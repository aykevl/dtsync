// file.go
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

// Package file implements the file tree interface (tree.Entry) for local
// filesystems.
package file

// TODO: use the Linux system calls openat(), readdirat() etc.

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/aykevl/dtsync/tree"
)

// A prefix and suffix for files that are being copied.
const (
	TEMPPREFIX = ".usync-"
	TEMPSUFFIX = ".tmp"
)

// Entry is one file or directory in the filesystem. It additionally contains
// it's name, parent, root, and stat() result.
type Entry struct {
	name    string
	root    *Tree
	parents []string
	st      os.FileInfo
}

// String returns a string representation of this file, for debugging.
func (e *Entry) String() string {
	return "file.Entry(" + e.path() + ")"
}

// Name returns the filename.
func (e *Entry) Name() string {
	return e.name
}

// path returns the full path for this entry.
func (e *Entry) path() string {
	parts := make([]string, 1, len(e.parents)+2)
	parts[0] = e.root.path
	parts = append(parts, e.parents...)
	parts = append(parts, e.name)
	return filepath.Join(parts...)
}

// parentPath returns the path of the parent entry
func (e *Entry) parentPath() string {
	if len(e.parents) == 0 && e.name == "" {
		panic("trying to get the parentPath of the root")
	}
	parts := make([]string, 1, len(e.parents)+1)
	parts[0] = e.root.path
	parts = append(parts, e.parents...)
	return filepath.Join(parts...)
}

func (e *Entry) isRoot() bool {
	return len(e.parents) == 0 && e.name == ""
}

func (e *Entry) RelativePath() []string {
	if e.isRoot() {
		return nil
	}
	parts := make([]string, 0, len(e.parents)+1)
	parts = append(parts, e.parents...)
	parts = append(parts, e.name)
	return parts
}

// Type returns the file type (regular, directory, or unknown). More types may
// be added in the future.
func (e *Entry) Type() tree.Type {
	switch e.st.Mode() & os.ModeType {
	case 0:
		return tree.TYPE_REGULAR
	case os.ModeDir:
		return tree.TYPE_DIRECTORY
	default:
		return tree.TYPE_UNKNOWN
	}
}

// ModTime returns the modification time from the (cached) stat() call.
func (e *Entry) ModTime() time.Time {
	return e.st.ModTime()
}

// Size returns the filesize for regular files. For other file types, the result
// is undefined.
func (e *Entry) Size() int64 {
	return e.st.Size()
}

// Fingerprint returns a fingerprint calculated from the file's metadata.
func (e *Entry) Fingerprint() string {
	return tree.Fingerprint(e.makeInfo(nil))
}

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() ([]byte, error) {
	if e.Type() != tree.TYPE_REGULAR {
		return nil, nil
	}
	hash := tree.NewHash()
	file, err := os.Open(e.path())
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(hash, file)
	if err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

// makeInfo returns a tree.FileInfo object with the given hash. As the hash is
// expensive to calculate and can return errors, it is left to the caller to use
// it.
func (e *Entry) makeInfo(hash []byte) tree.FileInfo {
	return tree.NewFileInfo(e.RelativePath(), e.Type(), e.ModTime(), e.Size(), hash)
}

// Info returns a tree.FileInfo, or an error if the hash couldn't be calculated.
func (e *Entry) Info() (tree.FileInfo, error) {
	hash, err := e.Hash()
	if err != nil {
		return nil, err
	}
	return e.makeInfo(hash), nil
}

// Tree returns the tree.Tree interface this Entry belongs to.
func (e *Entry) Tree() tree.Tree {
	return e.root
}

// List returns a directory listing, sorted by name.
func (e *Entry) List() ([]tree.Entry, error) {
	list, err := ioutil.ReadDir(e.path())
	if err != nil {
		return nil, err
	}
	listEntries := make([]tree.Entry, len(list))
	for i, st := range list {
		listEntries[i] = &Entry{
			st:      st,
			name:    st.Name(),
			root:    e.root,
			parents: e.RelativePath(),
		}
	}
	tree.SortEntries(listEntries)
	return listEntries, nil
}

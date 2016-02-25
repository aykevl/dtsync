// tree.go
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

// Package tree specifies a directory tree interface, for use by the
// synchronization algorithm. Many different kinds of backends could be created
// for this interface, like local filesystem, SFTP, a remote dtsync process over
// SSH, maybe even MTP.
package tree

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The type used for TYPE_* constants
type Type int

// Constants used as Type() return values
const (
	TYPE_REGULAR   Type = iota
	TYPE_DIRECTORY Type = iota
	TYPE_UNKNOWN   Type = iota
)

func (t Type) Char() string {
	switch t {
	case TYPE_REGULAR:
		return "f"
	case TYPE_DIRECTORY:
		return "d"
	default:
		return "?"
	}
}

// Error codes that can be used by any filesystem implementation
var (
	ErrNotImplemented = errors.New("tree: not implemented")
	ErrNoDirectory    = errors.New("tree: this is not a directory")
	ErrNoRegular      = errors.New("tree: this is not a regular file")
	ErrAlreadyExists  = errors.New("tree: file already exists")
	ErrNotFound       = errors.New("tree: file not found")
	ErrInvalidName    = errors.New("tree: invalid file name")
)

// Entry is one object tree, e.g. a file or directory. It can also be something
// else, like bookmarks in a browser, or an email message.
type Entry interface {
	// Name returns the name of this file.
	Name() string
	// Return a list of children, in alphabetic order.
	// Returns an error when this is not a directory.
	List() ([]Entry, error)
	// Size returns the filesize for regular files. For others, it's
	// implementation-dependent.
	Size() int64
	// Fingerprint returns a (weak) fingerprint of this file, including it's
	// type and modification time, and possibly including it's size or
	// permission bits.
	Fingerprint() string
	// Type returns the file type (see the Type constants above)
	Type() Type

	// Copy into the other entry (as a child). The returned entry is the new
	// child.
	CopyTo(Entry) (Entry, error)
	// Copy over the other entry, possibly using an optimized algorithm.
	UpdateOver(Entry) error
	// Delete this file or directory tree (recursively)
	Remove() error

	// Get this file. This only exists to read the status file, not to implement
	// copying in the syncer!
	GetFile(name string) (io.ReadCloser, error)
	// SetFile is analogous to GetFile
	SetFile(name string) (io.WriteCloser, error)
}

// FileEntry is a data object, e.g. either a file (blob of data), a directory
// (list of children) or some other type.
type FileEntry interface {
	Entry

	// mkdir: create a directory in this directory with the given name. The
	// modification time is unknown and will likely be the current time.
	CreateDir(name string) (Entry, error)
	// Open a file for writing in this directory at a temporary place. Closing
	// that file will move it to the final destination for atomic operation.
	CreateFile(name string, modTime time.Time) (Entry, io.WriteCloser, error)
	// Update this file. May do about the same as CreateFile.
	UpdateFile(modTime time.Time) (io.WriteCloser, error)
	// GetReader returns a io.ReadCloser with the contents of this file.
	// Useful for Equals()
	GetContents() (io.ReadCloser, error)
	// ModTime returns the last modification time.
	ModTime() time.Time
}

// EntrySlice is a sortable list of Entries, sorting in incrasing order by the
// name (Entry.Name()).
type EntrySlice []Entry

func (es EntrySlice) Len() int {
	return len(es)
}

func (es EntrySlice) Less(i, j int) bool {
	return es[i].Name() < es[j].Name()
}

func (es EntrySlice) Swap(i, j int) {
	es[i], es[j] = es[j], es[i]
}

// SortEntries sorts the given slice by name.
func SortEntries(slice []Entry) {
	es := EntrySlice(slice)
	sort.Sort(es)
}

// ValidName returns true if this name is the empty string or contains invalid
// characters.
func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c == '/' || c == 0 {
			return false
		}
	}
	return true
}

// Equal compares two entries, only returning true when this file and possible
// children (for directories) are exactly equal.
func Equal(file1, file2 FileEntry, includeDirModTime bool) (bool, error) {
	if file1.Name() != file2.Name() || file1.Type() != file2.Type() {
		return false, nil
	}
	if !file1.ModTime().Equal(file2.ModTime()) {
		if file1.Type() == TYPE_DIRECTORY {
			if includeDirModTime {
				return false, nil
			}
		} else {
			return false, nil
		}
	}
	switch file1.Type() {
	case TYPE_REGULAR:
		contents := make([][]byte, 2)
		// TODO compare the contents block-for-block, not by loading the two
		// files in memory.
		for i, file := range []FileEntry{file1, file2} {
			reader, err := file.GetContents()
			if err != nil {
				return false, err
			}
			defer reader.Close()
			contents[i], err = ioutil.ReadAll(reader)
			if err != nil {
				return false, err
			}
		}
		return bytes.Equal(contents[0], contents[1]), nil
	case TYPE_DIRECTORY:
		list1, err := file1.List()
		if err != nil {
			return false, err
		}
		list2, err := file2.List()
		if err != nil {
			return false, err
		}

		if len(list1) != len(list2) {
			return false, nil
		}
		for i := 0; i < len(list1); i++ {
			if equal, err := Equal(list1[i].(FileEntry), list2[i].(FileEntry), includeDirModTime); !equal || err != nil {
				return equal, err
			}
		}
		return true, nil
	default:
		panic("unknown fileType")
	}
}

// Fingerprint returns a fingerprint for this file.
func Fingerprint(e FileEntry) string {
	parts := make([]string, 0, 4)
	modTime := e.ModTime().Format(time.RFC3339Nano)
	parts = append(parts, e.Type().Char(), modTime)
	if e.Type() == TYPE_REGULAR {
		parts = append(parts, strconv.FormatInt(e.Size(), 10))
	}
	return strings.Join(parts, "/")
}

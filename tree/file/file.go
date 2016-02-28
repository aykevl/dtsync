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

// filesystem encapsulates the root path, so every Entry can know the root path.
type filesystem struct {
	path string
}

// Entry is one file or directory in the filesystem. It additionally contains
// it's name, parent, root, and stat() result.
type Entry struct {
	name   string
	root   *filesystem
	parent *Entry
	st     os.FileInfo
}

// NewRoot wraps a root directory in an Entry.
func NewRoot(rootPath string) (*Entry, error) {
	rootPath = filepath.Clean(rootPath)
	st, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	e := &Entry{
		root: &filesystem{
			path: rootPath,
		},
		st: st,
	}
	if e.Type() != tree.TYPE_DIRECTORY {
		return nil, tree.ErrNoDirectory
	}
	return e, nil
}

// NewTestRoot returns a new root in a temporary directory. It should be removed
// after use using root.Remove()
func NewTestRoot() (*Entry, error) {
	rootPath1, err := ioutil.TempDir("", "usync-test-")
	if err != nil {
		return nil, err
	}
	return NewRoot(rootPath1)
}

// String returns a string representation of this file, for debugging.
func (e *Entry) String() string {
	return "file.Entry(" + e.path() + ")"
}

// pathElements returns a list of path elements to be joined by filepath.Join.
func (e *Entry) pathElements() []string {
	if e.parent == nil {
		parts := make([]string, 1, 2)
		parts[0] = e.root.path
		return parts
	} else {
		return append(e.parent.pathElements(), e.name)
	}
}

// path returns the full path for this entry.
func (e *Entry) path() string {
	return filepath.Join(e.pathElements()...)
}

// AddRegular implements tree.TestEntry by adding a single file with the given
// name and contents.
func (e *Entry) AddRegular(name string, contents []byte) (tree.FileEntry, error) {
	if !tree.ValidName(name) {
		return nil, tree.ErrInvalidName
	}

	child := &Entry{
		name:   name,
		parent: e,
		root:   e.root,
	}
	file, err := os.Create(child.path())
	if err != nil {
		return nil, err
	}
	defer file.Close()
	_, err = file.Write(contents)
	if err != nil {
		// "Write must return a non-nil error if it returns n < len(p)."
		return nil, err
	}
	err = file.Sync()
	if err != nil {
		return nil, err
	}
	child.st, err = file.Stat()
	if err != nil {
		return nil, err
	}
	return child, nil
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

// CreateDir adds a single child directory to this directory.
func (e *Entry) CreateDir(name string) (tree.Entry, error) {
	if !tree.ValidName(name) {
		return nil, tree.ErrInvalidName
	}
	child := &Entry{
		name:   name,
		parent: e,
		root:   e.root,
	}
	err := os.Mkdir(child.path(), 0777)
	if err != nil {
		return nil, err
	}
	child.st, err = os.Lstat(child.path())
	if err != nil {
		return nil, err
	}
	return child, nil
}

// GetContents returns an io.ReadCloser (that must be closed) with the contents
// of this entry.
func (e *Entry) GetContents() (io.ReadCloser, error) {
	return os.Open(e.path())
}

// GetFile returns an io.ReadCloser with the named file. The file must be closed
// after use.
func (e *Entry) GetFile(name string) (io.ReadCloser, error) {
	fp, err := os.Open(filepath.Join(e.path(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tree.ErrNotFound
		} else {
			return nil, err
		}
	}
	return fp, nil
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
			st:     st,
			name:   st.Name(),
			parent: e,
			root:   e.root,
		}
	}
	return listEntries, nil
}

// ModTime returns the modification time from the (cached) stat() call.
func (e *Entry) ModTime() time.Time {
	return e.st.ModTime()
}

// Fingerprint returns a fingerprint calculated from the file's metadata.
func (e *Entry) Fingerprint() string {
	return tree.Fingerprint(e)
}

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() ([]byte, error) {
	hash := tree.NewHash()
	file, err := e.GetContents()
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(hash, file)
	if err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

// Name returns the filename.
func (e *Entry) Name() string {
	return e.name
}

func (e *Entry) RelativePath() string {
	return filepath.Join(e.pathElements()[1:]...)
}

// Remove removes this entry, recursively.
func (e *Entry) Remove() error {
	if e.Type() == tree.TYPE_DIRECTORY && e.parent != nil {
		// move to temporary location to provide atomicity in removing a
		// directory tree
		oldPath := e.path()
		tmpName := TEMPPREFIX + e.name + TEMPSUFFIX
		tmpPath := filepath.Join(e.parent.path(), tmpName)
		err := os.Rename(oldPath, tmpPath)
		if err != nil {
			return err
		}
		e.name = tmpName
	}
	return e.removeSelf()
}

func (e *Entry) removeSelf() error {
	if e.Type() == tree.TYPE_DIRECTORY {
		// remove children first
		list, err := e.List()
		if err != nil {
			return err
		}
		for _, child := range list {
			err = child.(*Entry).removeSelf()
			if err != nil {
				return err
			}
		}
	}

	// Actually remove the file or (empty) directory
	err := os.Remove(e.path())
	if err != nil {
		return err
	}

	// Update parent stat result
	if e.parent != nil {
		st, err := os.Lstat(e.parent.path())
		if err != nil {
			return err
		}
		e.parent.st = st
	}
	return nil
}

func (e *Entry) SetFile(name string) (io.WriteCloser, error) {
	_, out, err := e.CreateFile(name, time.Time{})
	return out, err
}

// Size returns the filesize for regular files. For other file types, the result
// is undefined.
func (e *Entry) Size() int64 {
	return e.st.Size()
}

// CreateFile creates the child, implementing tree.FileEntry. This function is useful for CopyTo.
func (e *Entry) CreateFile(name string, modTime time.Time) (tree.Entry, io.WriteCloser, error) {
	child := &Entry{
		name:   name,
		parent: e,
		root:   e.root,
	}

	writer, err := child.replaceFile(modTime)
	return child, writer, err
}

// UpdateFile replaces itself, to implement tree.FileEntry. This function is
// useful for UpdateOver.
func (e *Entry) UpdateFile(modTime time.Time) (io.WriteCloser, error) {
	if e.Type() != tree.TYPE_REGULAR {
		return nil, tree.ErrNoRegular
	}
	return e.replaceFile(modTime)
}

// replaceFile replaces the current file without checking for a type. Used by
// CreateFile and UpdateFile.
func (e *Entry) replaceFile(modTime time.Time) (io.WriteCloser, error) {
	tempPath := filepath.Join(e.parent.path(), TEMPPREFIX+e.name+TEMPSUFFIX)
	fp, err := os.Create(tempPath)
	if err != nil {
		return nil, err
	}
	return &fileWriter{
		fp: fp,
		closeCall: func() error {
			if !modTime.IsZero() {
				err = os.Chtimes(tempPath, modTime, modTime)
				if err != nil {
					return err
				}
			}
			err = os.Rename(tempPath, e.path())
			if err != nil {
				return err
			}
			e.st, err = os.Lstat(e.path())

			// Update parent stat result
			st, err := os.Lstat(e.parent.path())
			if err != nil {
				return err
			}
			e.parent.st = st

			return err
		},
	}, nil
}

// UpdateOver replaces this file with the contents and modtime of the other
// file.
func (e *Entry) UpdateOver(other tree.Entry) ([]byte, error) {
	file, ok := other.(tree.FileEntry)
	if !ok {
		return nil, tree.ErrNotImplemented
	}

	switch e.Type() {
	case tree.TYPE_REGULAR:
		out, err := file.UpdateFile(e.ModTime())
		if err != nil {
			return nil, err
		}

		in, err := os.Open(e.path())

		hasher := tree.NewHash()
		hashReader := io.TeeReader(in, hasher)

		_, err = io.Copy(out, hashReader)
		if err != nil {
			return nil, err
		}

		return hasher.Sum(nil), out.Close()

	default:
		return nil, tree.ErrNotImplemented
	}
}

// SetContents writes contents to this file, for testing.
func (e *Entry) SetContents(contents []byte) error {
	fp, err := os.Create(e.path())
	if err != nil {
		return err
	}
	defer fp.Close()
	_, err = fp.Write(contents)
	if err != nil {
		return err
	}
	now := time.Now()
	err = os.Chtimes(e.path(), now, now)
	if err != nil {
		// could not update
		return err
	}
	e.st, err = fp.Stat()
	if err != nil {
		return err
	}
	return fp.Sync()
}

// CopyTo copies this file into the otherParent. The latter must be a directory.
// Only implemented for regular files, not directories.
func (e *Entry) CopyTo(otherParent tree.Entry) (tree.Entry, []byte, error) {
	file, ok := otherParent.(tree.FileEntry)
	if !ok {
		return nil, nil, tree.ErrNotImplemented
	}

	switch e.Type() {
	case tree.TYPE_REGULAR:
		other, out, err := file.CreateFile(e.name, e.ModTime())
		if err != nil {
			return nil, nil, err
		}
		in, err := os.Open(e.path())
		if err != nil {
			return nil, nil, err
		}
		hasher := tree.NewHash()
		hashReader := io.TeeReader(in, hasher)
		_, err = io.Copy(out, hashReader)
		if err != nil {
			return nil, nil, err
		}
		// out.Close() does an fsync and rename
		err = out.Close()
		if err != nil {
			return nil, nil, err
		}
		return other, hasher.Sum(nil), nil

	default:
		return nil, nil, tree.ErrNotImplemented
	}
}

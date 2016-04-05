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

package file

import (
	"hash"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/aykevl/dtsync/tree"
)

// Tree encapsulates the root path, so every Entry can know the root path.
type Tree struct {
	path string
	root *Entry
}

// NewRoot wraps a root directory in an Entry.
func NewRoot(rootPath string) (*Tree, error) {
	rootPath = filepath.Clean(rootPath)

	// Check that the path exists and is a directory.
	// Use Stat() here as described in symlink(7) (on Linux).
	st, err := os.Stat(rootPath)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, tree.ErrNoDirectory([]string{rootPath})
	}

	r := &Tree{
		path: rootPath,
	}
	r.root = &Entry{
		root: r,
		st:   st,
	}
	return r, nil
}

// NewTestRoot returns a new root in a temporary directory. It should be removed
// after use using root.Remove()
func NewTestRoot() (*Tree, error) {
	rootPath1, err := ioutil.TempDir("", "usync-test-")
	if err != nil {
		return nil, err
	}
	return NewRoot(rootPath1)
}

// String returns a string representation of this root, for debugging.
func (r *Tree) String() string {
	return "file.Tree(" + r.path + ")"
}

// Close does nothing: no files or directories are (currently) kept open.
func (r *Tree) Close() error {
	return nil
}

func (r *Tree) Root() tree.Entry {
	return r.root
}

func (r *Tree) entryFromPath(path []string) *Entry {
	return &Entry{
		root: r,
		path: path,
	}
}

func (r Tree) entryFromPathName(parentPath []string, name string) (*Entry, error) {
	path := make([]string, 0, len(parentPath)+1)
	path = append(path, parentPath...)
	path = append(path, name)
	if !validPath(path) {
		return nil, tree.ErrInvalidName
	}

	return r.entryFromPath(path), nil
}

// CreateDir adds a single child directory.
func (r *Tree) CreateDir(name string, parent, source tree.FileInfo) (tree.FileInfo, error) {
	child, err := r.entryFromPathName(parent.RelativePath(), name)
	if err != nil {
		return nil, err
	}

	err = os.Mkdir(child.fullPath(), os.FileMode(source.Mode().Calc(source.HasMode(), 0777)))
	if err != nil {
		return nil, err
	}

	child.st, err = os.Lstat(child.fullPath())
	if err != nil {
		return nil, err
	}

	return child.makeInfo(nil), nil
}

func (r *Tree) CopySource(source tree.FileInfo) (io.ReadCloser, error) {
	e := r.entryFromPath(source.RelativePath())

	in, err := os.Open(e.fullPath())
	if err != nil {
		return nil, err
	}

	// Make sure the file is still the same.
	st, err := in.Stat()
	if err != nil {
		in.Close()
		return nil, err
	}
	e.st = st

	if !tree.MatchFingerprint(e.Info(), source) {
		in.Close()
		return nil, tree.ErrChanged(e.RelativePath())
	}

	return in, nil
}

// Remove removes this entry, recursively. It returns the FileInfo of the
// parent, or an error.
func (r *Tree) Remove(file tree.FileInfo) (tree.FileInfo, error) {
	e := r.entryFromPath(file.RelativePath())
	st, err := os.Lstat(e.fullPath())
	if err != nil {
		return nil, err
	}
	e.st = st

	if len(file.RelativePath()) == 0 {
		// Don't move this root directory.
	} else if file.Type() == tree.TYPE_DIRECTORY {
		// move to temporary location to provide atomicity in removing a
		// directory tree
		oldPath := e.fullPath()
		tmpPath := e.tempPath()
		err := os.Rename(oldPath, tmpPath)
		if err != nil {
			return nil, err
		}
		e.path[len(e.path)-1] = e.tempName()
	} else {
		if !tree.MatchFingerprint(e.Info(), file) {
			return nil, tree.ErrChanged(e.RelativePath())
		}
	}

	err = e.removeSelf()
	if err != nil {
		return nil, err
	}

	var parentInfo tree.FileInfo
	if len(e.path) != 0 {
		st, err = os.Lstat(e.parentPath())
		if err != nil {
			return nil, err
		}
		parent := Entry{st: st}
		parentInfo = parent.makeInfo(nil)
	}
	return parentInfo, nil
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
	err := os.Remove(e.fullPath())
	if err != nil {
		return err
	}

	return nil
}

// GetFile returns an io.ReadCloser with the named file. The file must be closed
// after use.
func (r *Tree) GetFile(name string) (io.ReadCloser, error) {
	fp, err := os.Open(filepath.Join(r.path, name))
	if err != nil {
		return nil, err
	}
	return fp, nil
}

func (r *Tree) SetFile(name string) (io.WriteCloser, error) {
	e := Entry{
		path: []string{name},
		root: r,
	}
	tempPath := e.tempPath()
	destPath := e.fullPath()
	fp, err := os.Create(tempPath)
	if err != nil {
		return nil, err
	}
	return &fileWriter{
		fp: fp,
		closeCall: func() error {
			return os.Rename(tempPath, destPath)
		},
	}, nil
}

// CreateFile creates the child, implementing tree.FileEntry. This function is useful for Copy.
func (r *Tree) CreateFile(name string, parent, source tree.FileInfo) (tree.Copier, error) {
	child, err := r.entryFromPathName(parent.RelativePath(), name)
	if err != nil {
		return nil, err
	}

	_, err = os.Lstat(child.fullPath())
	if err == nil {
		return nil, tree.ErrFound(child.RelativePath())
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return child.replaceFile(source, tree.NewHash(), false)
}

// UpdateFile replaces itself, to implement tree.FileEntry. This function is
// useful for Update.
func (r *Tree) UpdateFile(file, source tree.FileInfo) (tree.Copier, error) {
	if source.Type() != tree.TYPE_REGULAR {
		return nil, tree.ErrNoRegular(source.RelativePath())
	}

	e := r.entryFromPath(file.RelativePath())

	st, err := os.Lstat(e.fullPath())
	if err != nil {
		return nil, err
	}
	e.st = st
	if !tree.MatchFingerprint(e.Info(), file) {
		return nil, tree.ErrChanged(e.RelativePath())
	}

	return e.replaceFile(source, tree.NewHash(), true)
}

// replaceFile replaces the current file without checking for a type. Used by
// CreateFile and UpdateFile.
func (e *Entry) replaceFile(source tree.FileInfo, hash hash.Hash, update bool) (tree.Copier, error) {
	tempPath := filepath.Join(e.parentPath(), TEMPPREFIX+e.Name()+TEMPSUFFIX)
	fp, err := os.Create(tempPath)
	if err != nil {
		return nil, err
	}
	return &fileHashWriter{
		hash: hash,
		fp:   fp,
		finished: func(hash []byte) (tree.FileInfo, tree.FileInfo, error) {
			defer func() {
				if fp != nil {
					os.Remove(tempPath)
				}
			}()
			if !source.ModTime().IsZero() {
				// TODO: use futimens function
				err = os.Chtimes(tempPath, source.ModTime(), source.ModTime())
				if err != nil {
					return nil, nil, err
				}
			}

			if source.HasMode() != 0 {
				err = fp.Chmod(os.FileMode(source.Mode().Calc(source.HasMode(), 0666)))
				if err != nil {
					return nil, nil, err
				}
			}

			// Test again whether the file indeed exists or not exists as
			// expected.
			_, err := os.Lstat(e.fullPath())
			if update && err != nil {
				return nil, nil, err
			}
			if !update {
				if err == nil {
					return nil, nil, tree.ErrFound(e.RelativePath())
				} else if !os.IsNotExist(err) {
					return nil, nil, err
				}
			}

			err = os.Rename(tempPath, e.fullPath())
			if err != nil {
				return nil, nil, err
			}

			e.st, err = fp.Stat()
			if err != nil {
				return nil, nil, err
			}

			parentSt, err := os.Lstat(e.parentPath())
			if err != nil {
				return nil, nil, err
			}
			parent := Entry{
				st: parentSt,
			}

			fp = nil
			return e.makeInfo(hash), parent.makeInfo(nil), nil
		},
		cancelled: func(hash []byte) error {
			return os.Remove(tempPath)
		},
	}, nil
}

func (r *Tree) CreateSymlink(name string, parentInfo, sourceInfo tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	child, err := r.entryFromPathName(parentInfo.RelativePath(), name)
	if err != nil {
		return nil, nil, err
	}
	fullPath := child.fullPath()

	// There will always be an error (EEXIST) when the target path exists, so
	// we don't have to check whether it exists first.
	err = os.Symlink(contents, fullPath)
	if err != nil {
		return nil, nil, err
	}

	if !sourceInfo.ModTime().IsZero() {
		err := Lchtimes(fullPath, sourceInfo.ModTime(), sourceInfo.ModTime())
		if err != nil {
			return nil, nil, err
		}
	}

	child.st, err = os.Lstat(fullPath)
	if err != nil {
		return nil, nil, err
	}

	parentSt, err := os.Lstat(child.parentPath())
	if err != nil {
		return nil, nil, err
	}
	parent := Entry{
		st: parentSt,
	}
	return child.makeInfo(nil), parent.makeInfo(nil), nil
}

func (r *Tree) UpdateSymlink(file, source tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	e := r.entryFromPath(source.RelativePath())
	fullPath := e.fullPath()
	tempPath := e.tempPath()

	// TODO: this is not necessary with the renameat2 system call and
	// RENAME_NOREPLACE.
	var err error
	e.st, err = os.Lstat(fullPath)
	if err != nil {
		return nil, nil, err
	} else if file.Type() != tree.TYPE_SYMLINK || e.Type() != tree.TYPE_SYMLINK {
		return nil, nil, tree.ErrNoSymlink(e.RelativePath())
	} else if !tree.MatchFingerprint(e.Info(), file) {
		return nil, nil, tree.ErrChanged(e.RelativePath())
	}

	err = os.Symlink(contents, tempPath)
	if err != nil {
		return nil, nil, err
	}

	if !source.ModTime().IsZero() {
		err := Lchtimes(tempPath, source.ModTime(), source.ModTime())
		if err != nil {
			return nil, nil, err
		}
	} else {
		// At least Linux does not always use the current time on very short
		// intervals. This is here to keep the test happy.
		now := time.Now()
		err := Lchtimes(tempPath, now, now)
		if err != nil {
			return nil, nil, err
		}
	}

	err = os.Rename(tempPath, fullPath)
	if err != nil {
		return nil, nil, err
	}

	e.st, err = os.Lstat(fullPath)
	if err != nil {
		return nil, nil, err
	}

	parentSt, err := os.Lstat(e.parentPath())
	if err != nil {
		return nil, nil, err
	}
	parent := Entry{
		st: parentSt,
	}

	return e.makeInfo(nil), parent.makeInfo(nil), nil
}

func (r *Tree) ReadSymlink(file tree.FileInfo) (string, error) {
	e := r.entryFromPath(file.RelativePath())
	fullPath := e.fullPath()

	st, err := os.Lstat(fullPath)
	if err != nil {
		return "", err
	} else if !st.ModTime().Equal(file.ModTime()) {
		return "", tree.ErrChanged(e.RelativePath())
	}

	return os.Readlink(fullPath)
}

// Chmod applies the given mode bits and returns the stat() result. Not all
// bits may be applied (subject to HasMode()).
func (r *Tree) Chmod(target, source tree.FileInfo) (tree.FileInfo, error) {
	e := r.entryFromPath(target.RelativePath())
	fullPath := e.fullPath()

	handle, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	e.st, err = handle.Stat()
	if err != nil {
		return nil, err
	} else if !tree.MatchFingerprint(e.Info(), target) {
		return nil, tree.ErrChanged(e.RelativePath())
	}

	err = handle.Chmod(os.FileMode(source.Mode().Calc(source.HasMode(), 0666)))
	if err != nil {
		return nil, err
	}

	e.st, err = handle.Stat()
	if err != nil {
		return nil, err
	}
	return e.Info(), nil
}

// PutFile implements tree.TestTree by writing a single file with the given
// name and contents.
func (r *Tree) PutFile(path []string, contents []byte) (tree.FileInfo, error) {
	if len(path) == 0 || !validPath(path) {
		return nil, tree.ErrInvalidName
	}

	child := r.entryFromPath(path)
	fullPath := child.fullPath()

	var err error
	child.st, err = os.Lstat(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if child.Type() != tree.TYPE_REGULAR {
			return nil, tree.ErrNoRegular(path)
		}
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	_, err = file.Write(contents)
	if err != nil {
		// "Write must return a non-nil error if it returns n < len(p)."
		return nil, err
	}
	// Sometimes, the OS doesn't save the exact time when overwriting a file.
	now := time.Now()
	err = os.Chtimes(fullPath, now, now)
	if err != nil {
		// could not update
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
	return child.makeInfo(nil), nil
}

// ReadInfo returns the FileInfo for the specified file with a hash.
func (r *Tree) ReadInfo(path []string) (tree.FileInfo, error) {
	file := &Entry{
		root: r,
		path: path,
	}
	st, err := os.Lstat(file.fullPath())
	if err != nil {
		return nil, err
	}
	file.st = st
	return file.FullInfo()
}

// validPath returns true if such a path is allowed as a file path (e.g. no null
// bytes or directory separators in filenames).
func validPath(path []string) bool {
	// Check for special characters in the path.
	for _, part := range path {
		if !validName(part) {
			return false
		}
	}
	return true
}

func validName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i := 0; i < len(name); i++ {
		// TODO: this works on *nix systems, but on Windows, there are some
		// more reserved characters and many, many more invalid filenames.
		// See:
		// http://stackoverflow.com/questions/1976007/what-characters-are-forbidden-in-windows-and-linux-directory-names
		if name[i] == 0 || os.IsPathSeparator(name[i]) {
			return false
		}
	}
	return true
}

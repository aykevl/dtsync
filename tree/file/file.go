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
	"os"
	"path/filepath"
	"sort"
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
	root     *Tree
	path     []string
	st       os.FileInfo
	notFound bool
}

// String returns a string representation of this file, for debugging.
func (e *Entry) String() string {
	return "file.Entry(" + e.fullPath() + ")"
}

// Name returns the filename.
func (e *Entry) Name() string {
	if len(e.path) == 0 {
		return ""
	}
	return e.path[len(e.path)-1]
}

// tempName returns the filename, but with a temporary prefix and suffix.
func (e *Entry) tempName() string {
	return TEMPPREFIX + e.Name() + TEMPSUFFIX
}

// fullPath returns the full path for this entry.
func (e *Entry) fullPath() string {
	parts := make([]string, 1, len(e.path)+1)
	parts[0] = e.root.path
	parts = append(parts, e.path...)
	return filepath.Join(parts...)
}

// realPath returns the full path with symlinks resolved.
func (e *Entry) realPath() string {
	parts := make([]string, 1, len(e.path)+1)
	parts[0] = e.root.realPath
	parts = append(parts, e.path...)
	return filepath.Join(parts...)
}

// tempPath returns a full path to a temporary location for this file (in the
// same parent directory).
func (e *Entry) tempPath() string {
	parts := make([]string, 1, len(e.path)+1)
	parts[0] = e.root.path
	parts = append(parts, e.path[:len(e.path)-1]...)
	parts = append(parts, e.tempName())
	return filepath.Join(parts...)
}

// parentPath returns the path of the parent entry
func (e *Entry) parentPath() string {
	if len(e.path) == 0 {
		panic("trying to get the parentPath of the root")
	}
	parts := make([]string, 1, len(e.path))
	parts[0] = e.root.path
	parts = append(parts, e.path[:len(e.path)-1]...)
	return filepath.Join(parts...)
}

func (e *Entry) RelativePath() []string {
	return e.path
}

func (e *Entry) childPath(name string) []string {
	parts := make([]string, 0, len(e.path)+1)
	parts = append(parts, e.path...)
	parts = append(parts, name)
	return parts
}

// Type returns the file type (regular, directory, or unknown). More types may
// be added in the future.
func (e *Entry) Type() tree.Type {
	if e.notFound {
		return tree.TYPE_NOTFOUND
	}
	switch e.st.Mode() & os.ModeType {
	case 0:
		return tree.TYPE_REGULAR
	case os.ModeDir:
		return tree.TYPE_DIRECTORY
	case os.ModeSymlink:
		return tree.TYPE_SYMLINK
	default:
		return tree.TYPE_UNKNOWN
	}
}

// Mode returns the mode bits for this file, as far as the filesystem supports
// them.
func (e *Entry) Mode() tree.Mode {
	return tree.Mode(e.st.Mode().Perm())
}

// HasMode returns the permission bits this filesystem supports (at least 0777
// for Unix-like filesystems).
func (e *Entry) HasMode() tree.Mode {
	return tree.Mode(e.root.fsInfo.GetReal(e.realPath(), e.st).Filesystem().Permissions)
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

// Hash returns the blake2b hash of this file.
func (e *Entry) Hash() (tree.Hash, error) {
	switch e.Type() {
	case tree.TYPE_REGULAR:
		hash := tree.NewHash()
		file, err := os.Open(e.fullPath())
		if err != nil {
			return tree.Hash{}, err
		}
		_, err = io.Copy(hash, file)
		if err != nil {
			return tree.Hash{}, err
		}
		return tree.Hash{tree.HASH_DEFAULT, hash.Sum(nil)}, nil
	case tree.TYPE_SYMLINK:
		target, err := os.Readlink(e.fullPath())
		if err != nil {
			return tree.Hash{}, err
		}
		return tree.Hash{tree.HASH_TARGET, []byte(target)}, nil
	default:
		return tree.Hash{}, nil
	}
}

// makeInfo returns a tree.FileInfo object with the given hash. As the hash is
// expensive to calculate and can return errors, it is left to the caller to use
// it.
func (e *Entry) makeInfo(hash tree.Hash) tree.FileInfo {
	if e.notFound {
		return tree.NewFileInfo(e.RelativePath(), e.Type(), 0, 0, time.Time{}, 0, tree.Hash{})
	}
	return tree.NewFileInfo(e.RelativePath(), e.Type(), e.Mode(), e.HasMode(), e.ModTime(), e.Size(), hash)
}

// FullInfo returns a tree.FileInfo with hash, or an error if the hash couldn't
// be calculated.
func (e *Entry) FullInfo() (tree.FileInfo, error) {
	hash, err := e.Hash()
	if err != nil {
		return nil, err
	}
	return e.makeInfo(hash), nil
}

// Info returns a tree.FileInfo of the stat() result in this Entry (thus,
// without a hash).
func (e *Entry) Info() tree.FileInfo {
	return e.makeInfo(tree.Hash{})
}

// Tree returns the tree.Tree interface this Entry belongs to.
func (e *Entry) Tree() tree.Tree {
	return e.root
}

// List returns a directory listing, sorted by name.
func (e *Entry) List(options tree.ListOptions) ([]tree.Entry, error) {
	dirfp, err := os.Open(e.fullPath())
	if err != nil {
		return nil, err
	}
	defer dirfp.Close()

	// Read the list of names from the directory.
	nameList, err := dirfp.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(nameList)

	// Fill Entry structs.
	entryList := make([]tree.Entry, len(nameList))
	for i, name := range nameList {
		entry := &Entry{
			root: e.root,
			path: e.childPath(name),
		}
		// TODO: use fstatat with AT_SYMLINK_NOFOLLOW (set or not set)
		if options.Follow != nil && options.Follow(entry.RelativePath()) {
			entry.st, err = os.Stat(entry.fullPath())
			if os.IsNotExist(err) || isLoop(err) {
				err = nil
				entry = &Entry{
					root:     e.root,
					path:     e.childPath(name),
					notFound: true,
				}
			}
		} else {
			entry.st, err = os.Lstat(entry.fullPath())
		}
		if err != nil {
			return nil, err
		}
		entryList[i] = entry
	}

	return entryList, nil
}

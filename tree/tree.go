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
	"hash"
	"io"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codahale/blake2"
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

// Error codes that can be used by any filesystem implementation.
var (
	ErrNotImplemented     = errors.New("tree: not implemented")
	ErrNoDirectory        = errors.New("tree: this is not a directory")
	ErrNoRegular          = errors.New("tree: this is not a regular file")
	ErrAlreadyExists      = errors.New("tree: file already exists")
	ErrNotFound           = errors.New("tree: file not found")
	ErrInvalidName        = errors.New("tree: invalid file name")
	ErrChanged            = errors.New("tree: updated file between scan and sync")
	ErrParsingFingerprint = errors.New("tree: invalid fingerprint")
)

// Tree is an abstraction layer over various types of trees. It tries to be as
// generic as possible, making it possible to synchronize varying types of trees
// (filesystem, sftp, remote filesystem, mtp, etc.). It may even be possible to
// use it for very different trees, e.g. browser bookmark trees.
type Tree interface {
	// mkdir: create a directory in this directory with the given name. The
	// modification time is unknown and will likely be the current time.
	CreateDir(name string, parent FileInfo) (FileInfo, error)
	// Copy copies the object indicated by the source to the target in the other
	// tree. It returns the file info of the copied file (which theoretically
	// may be different from the hash in the provided FileInfo) and it's parent.
	// It may also return an error when the source metadata (e.g. modtime) does
	// not match the actual file.
	// Before the actual copy, the fingerprint of the source is compared with
	// the stat results of the source.
	Copy(source, targetParent FileInfo, other Tree) (info FileInfo, parentInfo FileInfo, err error)
	// Update is very similar to Copy. It replaces target with the data in
	// source, updating the contents and file metadata.
	Update(source, target FileInfo, other Tree) (info FileInfo, parentInfo FileInfo, err error)
	// Remove removes the indicated file, but checks the fingerprint first. It
	// returns ErrChanged if the metadata does not match, or another error if
	// the remove failed.
	Remove(file FileInfo) (parentInfo FileInfo, err error)

	// Get this file. This only exists to read the status file, not to implement
	// copying in the syncer!
	GetFile(name string) (io.ReadCloser, error)
	// SetFile is analogous to GetFile.
	SetFile(name string) (io.WriteCloser, error)
}

// FileTree is a filesystem (like) tree, with normal mkdir and open calls, and
// objects containing data blobs.
type FileTree interface {
	Tree

	// Open a file for writing with the specified name in the parent, with the
	// metadata as specified in 'source'. Calling Finish() should create the
	// file atomically at the target location. For example, by writing to it at
	// a temporary location and moving it to the destination.
	// Implementations should check that the target doesn't yet exist.
	CreateFile(name string, parent, source FileInfo) (Copier, error)

	// Update this file. May do about the same as CreateFile.
	UpdateFile(file, source FileInfo) (Copier, error)

	// GetContents returns a io.ReadCloser with the contents of this file.
	// Useful for Equals()
	GetContents(path []string) (io.ReadCloser, error)
}

type RemoteTree interface {
	Tree

	// RemoteScan issues a scan on the other end, and returns a reader for the
	// newly created .dtsync file.
	//
	// Implementations may either scan the whole tree and return once finished,
	// or start scanning while returning the data as far as they've scanned.
	RemoteScan() (io.Reader, error)
}

type LocalTree interface {
	Tree

	// Root returns the root entry of this tree.
	Root() Entry
}

// Entry is one object tree, e.g. a file or directory. It can also be something
// else, like bookmarks in a browser, or an email message.
type Entry interface {
	// Name returns the name of this file.
	Name() string
	// RelativePath returns the path relative to the root of this tree.
	RelativePath() []string
	// Type returns the file type (see the Type constants above)
	Type() Type
	// ModTime returns the last modification time.
	ModTime() time.Time
	// Size returns the filesize for regular files. For others, it's
	// implementation-dependent.
	Size() int64
	// Fingerprint returns a (weak) fingerprint of this file, including it's
	// type and modification time, and possibly including it's size or
	// permission bits.
	Fingerprint() string
	// Hash calculates the blake2b hash of the file and returns it.
	Hash() ([]byte, error)
	// Info returns a FileInfo from this Entry.
	Info() (FileInfo, error)
	// Tree returns the tree this entry belongs to.
	Tree() Tree
	// Return a list of children, in alphabetic order.
	// Returns an error when this is not a directory.
	List() ([]Entry, error)
}

// FileInfo is like os.FileInfo, but specific for the Tree interface.
// It is implemented by dtdiff.Entry.
type FileInfo interface {
	// Name returns the name of this file.
	Name() string
	// RelativePath returns the path relative to the root of this tree. The last
	// element is always the same as the name.
	RelativePath() []string
	// Size returns the filesize for regular files. For others, it's
	// implementation-dependent.
	Size() int64
	// Type returns the file type (see the Type constants above)
	Type() Type
	// ModTime returns the last modification time.
	ModTime() time.Time
	// Hash returns the blake2b hash of the file, or nil if no hash is known
	// (e.g. for directories).
	Hash() []byte
}

type FileInfoStruct struct {
	path     []string
	fileType Type
	modTime  time.Time
	size     int64
	hash     []byte
}

func NewFileInfo(path []string, fileType Type, modTime time.Time, size int64, hash []byte) FileInfo {
	return &FileInfoStruct{
		path:     path,
		fileType: fileType,
		modTime:  modTime,
		size:     size,
		hash:     hash,
	}
}

func (fi *FileInfoStruct) Name() string {
	if len(fi.path) == 0 {
		return ""
	}
	return fi.path[len(fi.path)-1]
}

func (fi *FileInfoStruct) RelativePath() []string {
	return fi.path
}

func (fi *FileInfoStruct) Hash() []byte {
	return fi.hash
}

func (fi *FileInfoStruct) ModTime() time.Time {
	return fi.modTime
}

func (fi *FileInfoStruct) Size() int64 {
	return fi.size
}

func (fi *FileInfoStruct) Type() Type {
	return fi.fileType
}

// Fingerprint returns a fingerprint for this file.
func Fingerprint(e FileInfo) string {
	parts := make([]string, 0, 4)
	modTime := e.ModTime().UTC().Format(time.RFC3339Nano)
	parts = append(parts, e.Type().Char(), modTime)
	if e.Type() == TYPE_REGULAR {
		parts = append(parts, strconv.FormatInt(e.Size(), 10))
	}
	return strings.Join(parts, "/")
}

type FingerprintInfo struct {
	Type    Type
	ModTime time.Time
	Size    int64
}

// ParseFingerprint parses the given fingerprint.
func ParseFingerprint(fingerprint string) (*FingerprintInfo, error) {
	info := &FingerprintInfo{}

	parts := strings.Split(fingerprint, "/")
	if len(parts) < 2 {
		panic(fingerprint)
		return info, ErrParsingFingerprint
	}

	switch parts[0] {
	case "f":
		info.Type = TYPE_REGULAR
	case "d":
		info.Type = TYPE_DIRECTORY
	default:
		info.Type = TYPE_UNKNOWN
	}

	modTime, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return info, err
	}
	info.ModTime = modTime

	if len(parts) < 3 {
		return info, nil
	}

	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return info, err
	}
	info.Size = size
	return info, nil
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

// Equal compares two entries, only returning true when this file and possible
// children (for directories) are exactly equal.
func Equal(file1, file2 Entry, includeDirModTime bool) (bool, error) {
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
		for i, file := range []Entry{file1, file2} {
			fileTree, ok := file.Tree().(FileTree)
			if !ok {
				return false, ErrNotImplemented
			}
			reader, err := fileTree.GetContents(file.RelativePath())
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
			if equal, err := Equal(list1[i], list2[i], includeDirModTime); !equal || err != nil {
				return equal, err
			}
		}
		return true, nil
	default:
		panic("unknown fileType")
	}
}

// NewHash returns the hashing function used by interfaces implementing Entry.
func NewHash() hash.Hash {
	return blake2.New(&blake2.Config{Size: 32})
}

// Copier is returned by the Copy and Update methods. Calling Finish() closes
// the file, possibly moves it to the destination location, and returns the
// file's and the file parent's FileInfo.
// It also implements io.Writer.
type Copier interface {
	Write([]byte) (n int, err error)
	Finish() (info FileInfo, parentInfo FileInfo, err error)
}

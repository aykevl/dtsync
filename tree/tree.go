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
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The type used for TYPE_* constants
type Type int

// Constants used as Type() return values.
//
// See also: tree/remote/messages.proto
const (
	TYPE_UNKNOWN   Type = 0
	TYPE_REGULAR   Type = 1
	TYPE_DIRECTORY Type = 2
	TYPE_SYMLINK   Type = 3
)

func (t Type) Char() string {
	switch t {
	case TYPE_REGULAR:
		return "f"
	case TYPE_DIRECTORY:
		return "d"
	case TYPE_SYMLINK:
		return "l"
	default:
		return "?"
	}
}

// The mode (mainly Unix-like permissions) used in FileInfo.Mode().
type Mode uint32

// Tree is an abstraction layer over various types of trees. It tries to be as
// generic as possible, making it possible to synchronize varying types of trees
// (filesystem, sftp, remote filesystem, mtp, etc.). It may even be possible to
// use it for very different trees, e.g. browser bookmark trees.
type Tree interface {
	// Close clears all resources allocated for this tree, if any. For example,
	// it closes a network connection.
	Close() error

	// mkdir: create a directory in this directory with the given name. The
	// modification time is unknown and will likely be the current time.
	CreateDir(name string, parent FileInfo) (FileInfo, error) // TODO update mtime
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

	// Create a symbolic link.
	CreateSymlink(name string, parent, source FileInfo, contents string) (FileInfo, FileInfo, error)

	// Update a symbolic link.
	UpdateSymlink(file, source FileInfo, contents string) (FileInfo, FileInfo, error)

	// Read the contents (target path) of a symbolic link.
	ReadSymlink(file FileInfo) (string, error)

	// CopySource returns a io.ReadCloser, but checks first whether the FileInfo
	// matches.
	CopySource(info FileInfo) (io.ReadCloser, error)
}

type RemoteTree interface {
	Tree

	// RemoteScan issues a scan on the other end, and returns a reader for the
	// newly created .dtsync file.
	//
	// Implementations may either scan the whole tree and return once finished,
	// or start scanning while returning the data as far as they've scanned.
	//
	// The sendOptions are sent to the remote scanner (scan will start once
	// received), and recvOptions is a channel from which the options sent by
	// the remote can be read.
	RemoteScan(sendOptions, recvOptions chan ScanOptions) (io.Reader, error)
}

type LocalTree interface {
	// Root returns the root entry of this tree.
	Root() Entry
}

type LocalFileTree interface {
	LocalTree
	FileTree
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
	// Mode returns the mode (permission) bits of this file.
	Mode() Mode
	// HasMode returns the permission bits the filesystem supports.
	HasMode() Mode
	// ModTime returns the last modification time.
	ModTime() time.Time
	// Size returns the filesize for regular files. For others, it's
	// implementation-dependent.
	Size() int64
	// Hash calculates the blake2b hash of the file and returns it.
	Hash() ([]byte, error)
	// Info returns a FileInfo without a hash from this Entry.
	// It must be fast (it shouldn't do any I/O).
	Info() FileInfo
	// FullInfo returns a FileInfo with hash from this Entry.
	// The FileInfo comes from Info(), but it has the hash set for regular
	// files.
	FullInfo() (FileInfo, error)
	// Tree returns the tree this entry belongs to.
	Tree() Tree
	// Return a list of children, in alphabetic order.
	// Returns an error when this is not a directory.
	List() ([]Entry, error)
}

// Copy copies the object indicated by the source to the target in the other
// tree. It returns the file info of the copied file (which theoretically may be
// different from the hash in the provided FileInfo) and it's parent.  It may
// also return an error when the source metadata (e.g. modtime) does not match
// the actual file.
// Before the actual copy, the fingerprint of the source is compared with the
// stat results of the source.
func Copy(this, other Tree, source, targetParent FileInfo) (info FileInfo, parentInfo FileInfo, err error) {
	thisFileTree, ok := this.(FileTree)
	if !ok {
		return nil, nil, ErrNotImplemented("source in Copy not a FileTree")
	}
	otherFileTree, ok := other.(FileTree)
	if !ok {
		return nil, nil, ErrNotImplemented("target in Copy not a FileTree")
	}

	switch source.Type() {
	case TYPE_REGULAR:
		inf, err := thisFileTree.CopySource(source)
		if err != nil {
			return nil, nil, err
		}
		defer inf.Close()

		outf, err := otherFileTree.CreateFile(source.Name(), targetParent, source)
		if err != nil {
			return nil, nil, err
		}
		_, err = io.Copy(outf, inf)
		if err != nil {
			// TODO outf.Revert()?
			return nil, nil, err
		}
		// outf.Finish() usually does an fsync and rename (and closes the file)
		return outf.Finish()

	case TYPE_SYMLINK:
		link, err := thisFileTree.ReadSymlink(source)
		if err != nil {
			return nil, nil, err
		}

		return otherFileTree.CreateSymlink(source.Name(), targetParent, source, link)

	case TYPE_UNKNOWN:
		return nil, nil, ErrNotImplemented("Copy: file type is TYPE_UNKNOWN")

	default:
		return nil, nil, ErrNotImplemented("Copy: unknown file type")
	}
}

// Update replaces this file with the contents and modtime of the other file.
// It is very similar to Copy.
func Update(this, other Tree, source, target FileInfo) (FileInfo, FileInfo, error) {
	thisFileTree, ok := this.(FileTree)
	if !ok {
		return nil, nil, ErrNotImplemented("source in Update not a FileTree")
	}
	otherFileTree, ok := other.(FileTree)
	if !ok {
		return nil, nil, ErrNotImplemented("target in Update not a FileTree")
	}

	switch source.Type() {
	case TYPE_REGULAR:
		inf, err := thisFileTree.CopySource(source)
		if err != nil {
			return nil, nil, err
		}
		defer inf.Close()

		outf, err := otherFileTree.UpdateFile(target, source)
		if err != nil {
			return nil, nil, err
		}

		_, err = io.Copy(outf, inf)
		if err != nil {
			return nil, nil, err
		}

		return outf.Finish()

	case TYPE_SYMLINK:
		link, err := thisFileTree.ReadSymlink(source)
		if err != nil {
			return nil, nil, err
		}

		return otherFileTree.UpdateSymlink(target, source, link)

	case TYPE_UNKNOWN:
		return nil, nil, ErrNotImplemented("Update: file type is TYPE_UNKNOWN")

	default:
		return nil, nil, ErrNotImplemented("Update: unknown file type")
	}
}

// FileInfo is like os.FileInfo, but specific for the Tree interface.
// It is implemented by dtdiff.Entry.
type FileInfo interface {
	// Name returns the name of this file.
	Name() string
	// RelativePath returns the path relative to the root of this tree. The last
	// element is always the same as the name.
	RelativePath() []string
	// Type returns the file type (see the Type constants above)
	Type() Type
	// Mode returns the mode bits (mainly permissions), see os.FileMode.
	Mode() Mode
	// HasMode returns the supported mode bits, 0 for the simplest filesystems
	// (e.g. FAT32).
	HasMode() Mode
	// ModTime returns the last modification time.
	ModTime() time.Time
	// Size returns the filesize for regular files. For others, it's
	// implementation-dependent.
	Size() int64
	// Hash returns the blake2b hash of the file, or nil if no hash is known
	// (e.g. for directories).
	Hash() []byte
}

type FileInfoStruct struct {
	path     []string
	fileType Type
	mode     Mode
	hasMode  Mode
	modTime  time.Time
	size     int64
	hash     []byte
}

func NewFileInfo(path []string, fileType Type, mode Mode, hasMode Mode, modTime time.Time, size int64, hash []byte) FileInfo {
	return &FileInfoStruct{
		path:     path,
		fileType: fileType,
		mode:     mode,
		hasMode:  hasMode,
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

func (fi *FileInfoStruct) Mode() Mode {
	return fi.mode
}

func (fi *FileInfoStruct) HasMode() Mode {
	return fi.hasMode
}

func (fi *FileInfoStruct) String() string {
	return "FileInfoStruct{" + strings.Join(fi.path, "/") + "," + Fingerprint(fi) + "}"
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

// MatchFingerprint returns whether two FileInfos would have the same
// fingerprint without actually creating the fingerprint.
func MatchFingerprint(info1, info2 FileInfo) bool {
	if info1.Type() != info2.Type() {
		return false
	}
	if info1.Type() == TYPE_REGULAR && info1.Size() != info2.Size() {
		return false
	}
	// Equal() only looks at the time instant, not the timezone.
	return info1.ModTime().Equal(info2.ModTime())
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
		return info, ErrParsingFingerprint
	}

	switch parts[0] {
	case "f":
		info.Type = TYPE_REGULAR
	case "d":
		info.Type = TYPE_DIRECTORY
	case "l":
		info.Type = TYPE_SYMLINK
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

// Copier is returned by the Copy and Update methods. Calling Finish() closes
// the file, possibly moves it to the destination location, and returns the
// file's and the file parent's FileInfo. Calling Cancel() tries to undo writing
// the file.
// It also implements io.Writer.
type Copier interface {
	Write([]byte) (n int, err error)
	Finish() (info FileInfo, parentInfo FileInfo, err error)
	Cancel() error
}

// ScanOptions sets some options in a tree.
type ScanOptions interface {
	Ignore() []string
}

// ScanOptionsStruct implements ScanOptions and provides an ignore list to the
// other replica.
type scanOptions []string

func NewScanOptions(ignore []string) ScanOptions {
	return scanOptions(ignore)
}

// Ignore returns all patterns to ignore, to send to the other replica.
func (s scanOptions) Ignore() []string {
	return s
}

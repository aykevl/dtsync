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
	"hash"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/golibrsync/librsync"
	blake2b "github.com/minio/blake2b-simd"
)

// The type used for TYPE_* constants
type Type int

// Constants used as Type() return values. These must be kept stable.
//
// See also: tree/remote/messages.proto
const (
	TYPE_UNKNOWN   Type = 0
	TYPE_REGULAR   Type = 1
	TYPE_DIRECTORY Type = 2
	TYPE_SYMLINK   Type = 3
	TYPE_NOTFOUND  Type = 4
)

func (t Type) Char() string {
	switch t {
	case TYPE_REGULAR:
		return "f"
	case TYPE_DIRECTORY:
		return "d"
	case TYPE_SYMLINK:
		return "l"
	case TYPE_NOTFOUND:
		return "!"
	default:
		return "?"
	}
}

// The mode (mainly Unix-like permissions) used in FileInfo.Mode().
type Mode uint32

// Calculate new mode bits from the source filesystem and the default
// permissions on the target filesystem.
func (m Mode) Calc(sourceHasMode, targetDefault Mode) Mode {
	// Recipe:
	//  - Take the default mode of the target.
	//  - Remove the bits the source supports.
	//    What results is 000 if the source is a Unix filesystem and
	//    targetDefault if the source is e.g. FAT32.
	//  - Add the bits from the source file.
	return (targetDefault &^ sourceHasMode) | m
}

// NewHash returns the hashing function used by interfaces implementing Entry
// and by Copy and Update.
func NewHash() hash.Hash {
	return blake2b.New256()
}

type HashType int

const (
	HASH_NONE    HashType = 0 // no hash (nil)
	HASH_DEFAULT HashType = 1 // the hash that is set with the Hash header
	HASH_TARGET  HashType = 2 // path of the symlink (via readlink())
)

// Hash contains a hash type (hash/blake2b, or symlink target), and the
// contents of the hash (32-byte buffer or variable-width string).
type Hash struct {
	Type HashType
	Data []byte
}

func (h Hash) Equal(h2 Hash) bool {
	return h.Type == h2.Type && bytes.Equal(h.Data, h2.Data)
}

func (h Hash) IsZero() bool {
	return len(h.Data) == 0
}

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
	CreateDir(name string, parent, source FileInfo) (FileInfo, error) // TODO update parent mtime
	// Remove removes the indicated file, but checks the fingerprint first. It
	// returns ErrChanged if the metadata does not match, or another error if
	// the remove failed.
	Remove(file FileInfo) (parentInfo FileInfo, err error)
	// Chmod updates the mode (permission) bits of a file. The newly returned
	// FileInfo contains the new mode.
	Chmod(target, source FileInfo) (FileInfo, error)
}

type RsyncBasis interface {
	io.Reader
	io.ReaderAt
	io.Closer
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
	RemoteScan(extraOptions *ScanOptions, sendOptions, recvOptions chan *ScanOptions, progress chan<- *ScanProgress, cancel chan struct{}) (io.ReadCloser, error)

	// SendStatus is used to overwrite the status file on the remote. Data (in
	// e.g. protobuf format) can be written to the Copier.
	SendStatus() (Copier, error)

	// Use the rsync algorithm (librsync) to send only small changes in files.
	// RsyncSrc sends a signature and receives the binary delta.
	RsyncSrc(file FileInfo, signature io.Reader) (delta io.ReadCloser, err error)
	// RsyncDst receives a signature and sends the binary delta.
	RsyncDst(file, source FileInfo) (signature io.Reader, delta Copier, err error)
}

type LocalTree interface {
	// Root returns the root entry of this tree.
	Root() Entry
}

type LocalFileTree interface {
	LocalTree
	FileTree

	// UpdateRsync returns a source file and a file to write, for use by the
	// rsync algorithm.
	UpdateRsync(info, source FileInfo) (RsyncBasis, Copier, error)

	// Get this file. This only exists to read the status file, not to implement
	// copying in the syncer!
	GetFile(name string) (io.ReadCloser, error)

	// PutFile is analogous to GetFile.
	PutFile(name string) (Copier, error)
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
	// Id returns the inode and the filesystem for this file. If the inode isn't
	// available, return 0 for it. If the id is nonzero, the filesystem must be
	// non-nil as well.
	Id() (inode uint64, fs *LocalFilesystem)
	// Hash calculates the blake2b hash of the file and returns it.
	Hash() (Hash, error)
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
	List(ListOptions) ([]Entry, error)
}

type LocalFilesystem struct {
	Type     string
	DeviceId uint64
}

type Filesystem int64

// Copy copies the object indicated by the source to the target in the other
// tree. It returns the file info of the copied file (which theoretically may be
// different from the hash in the provided FileInfo) and it's parent.  It may
// also return an error when the source metadata (e.g. modtime) does not match
// the actual file.
// Before the actual copy, the fingerprint of the source is compared with the
// stat results of the source.
func Copy(this, other Tree, source, targetParent FileInfo, progress chan int64) (info FileInfo, parentInfo FileInfo, err error) {
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
		defer outf.Cancel()

		return CopyFile(outf, inf, nil, progress)

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

type UpdateStats struct {
	ToSource int64
	ToTarget int64
}

// Update replaces this file with the contents and modtime of the other file.
// It is very similar to Copy.
func Update(this, other Tree, source, target FileInfo, progress chan int64) (FileInfo, FileInfo, UpdateStats, error) {
	stats := UpdateStats{}

	thisFileTree, ok := this.(FileTree)
	if !ok {
		return nil, nil, stats, ErrNotImplemented("source in Update not a FileTree")
	}
	otherFileTree, ok := other.(FileTree)
	if !ok {
		return nil, nil, stats, ErrNotImplemented("target in Update not a FileTree")
	}

	switch source.Type() {
	case TYPE_REGULAR:
		if MatchFingerprint(source, target) {
			// A check that fingerprint updates are not done via the regular
			// Update().
			return nil, nil, stats, ErrNotImplemented("source and target are equal - I don't know what to do")
		}
		_, sourceRemote := this.(RemoteTree)
		_, targetRemote := other.(RemoteTree)
		if sourceRemote && targetRemote {
			// copy between two remote hosts
			// Note: it may be possible (and would certainly be more performant)
			// to copy directly between those hosts, instead of merely acting
			// like a proxy.

			sourceTree := this.(RemoteTree)
			targetTree := other.(RemoteTree)

			signature, deltaWriter, err := targetTree.RsyncDst(target, source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer deltaWriter.Cancel()

			deltaReader, err := sourceTree.RsyncSrc(source, &countReader{signature, &stats.ToSource})
			if err != nil {
				return nil, nil, stats, err
			}
			defer deltaReader.Close()

			stats.ToTarget, err = io.Copy(deltaWriter, &sendProgress{deltaReader, progress})
			if err != nil {
				return nil, nil, stats, err
			}

			info, parentInfo, err := deltaWriter.Finish()
			return info, parentInfo, stats, err

		} else if sourceRemote {
			// copy from remote to local

			sourceTree := this.(RemoteTree)
			targetTree := other.(LocalFileTree)

			// destination file
			basis, copier, err := targetTree.UpdateRsync(target, source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer basis.Close()
			defer copier.Cancel()

			sigJob, err := librsync.NewDefaultSignatureGen(basis)
			if err != nil {
				return nil, nil, stats, err
			}
			defer sigJob.Close()

			delta, err := sourceTree.RsyncSrc(source, &countReader{sigJob, &stats.ToSource})
			if err != nil {
				return nil, nil, stats, err
			}
			defer delta.Close()

			patchJob, err := librsync.NewPatcher(&countReader{delta, &stats.ToTarget}, basis)
			if err != nil {
				return nil, nil, stats, err
			}
			defer patchJob.Close()

			info, parentInfo, err := CopyFile(copier, patchJob, source, progress)
			return info, parentInfo, stats, err

		} else if targetRemote {
			// copy from local to remote

			sourceTree := this.(LocalFileTree)
			targetTree := other.(RemoteTree)

			sigReader, deltaCopier, err := targetTree.RsyncDst(target, source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer deltaCopier.Cancel()

			sourceFile, err := sourceTree.CopySource(source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer sourceFile.Close()

			sig, err := librsync.LoadSignature(&countReader{sigReader, &stats.ToSource})
			if err != nil {
				return nil, nil, stats, err
			}

			deltaJob, err := librsync.NewDeltaGen(sig, &sendProgress{sourceFile, progress})
			if err != nil {
				return nil, nil, stats, err
			}

			stats.ToTarget, err = io.Copy(deltaCopier, deltaJob)
			if err != nil {
				return nil, nil, stats, err
			}

			info, parentInfo, err := deltaCopier.Finish()
			return info, parentInfo, stats, err

		} else {
			// copy from local to local

			inf, err := thisFileTree.CopySource(source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer inf.Close()

			outf, err := otherFileTree.UpdateFile(target, source)
			if err != nil {
				return nil, nil, stats, err
			}
			defer outf.Cancel()

			info, parentInfo, err := CopyFile(outf, &countReader{inf, &stats.ToTarget}, source, progress)
			return info, parentInfo, stats, err
		}

	case TYPE_SYMLINK:
		link, err := thisFileTree.ReadSymlink(source)
		if err != nil {
			return nil, nil, stats, err
		}

		info, parentInfo, err := otherFileTree.UpdateSymlink(target, source, link)
		return info, parentInfo, stats, err

	case TYPE_UNKNOWN:
		return nil, nil, stats, ErrNotImplemented("Update: file type is TYPE_UNKNOWN")

	default:
		return nil, nil, stats, ErrNotImplemented("Update: unknown file type")
	}
}

// CopyFile acts like io.Copy, but it also hashes the data that passes through.
// The returned FileInfo has this hash.
func CopyFile(outf Copier, inf io.Reader, source FileInfo, progress chan int64) (FileInfo, FileInfo, error) {
	hash := NewHash()

	// A lot like io.Copy
	buf := make([]byte, 32*1024)
	for {
		nr, er := inf.Read(buf)
		if nr > 0 {
			nw, ew := outf.Write(buf[:nr])
			if ew != nil {
				return nil, nil, ew
			}
			if nr != nw {
				return nil, nil, io.ErrShortWrite
			}
			hash.Write(buf[:nr])
		}
		if progress != nil {
			progress <- int64(nr)
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return nil, nil, er
		}
	}

	hashResult := Hash{HASH_DEFAULT, hash.Sum(nil)}
	if source != nil && !source.Hash().Equal(hashResult) {
		return nil, nil, ErrChangedHash(source.RelativePath())
	}

	info, parentInfo, err := outf.Finish()
	var fullInfo *FileInfoStruct
	if info != nil {
		fullInfo = cloneFileInfo(info)
		fullInfo.hash = hashResult
	}
	return fullInfo, parentInfo, err
}

type countReader struct {
	r io.Reader
	n *int64
}

func (c *countReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	*c.n += int64(n)
	return n, err
}

type sendProgress struct {
	r io.Reader
	p chan int64
}

func (s *sendProgress) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	if s.p != nil {
		s.p <- int64(n)
	}
	return n, err
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
	// Inode returns the inode number, if available (otherwise 0).
	Inode() uint64
	// Hash returns the blake2b hash of the file, or nil if no hash is known
	// (e.g. for directories).
	Hash() Hash
}

type FileInfoStruct struct {
	path     []string
	fileType Type
	mode     Mode
	hasMode  Mode
	modTime  time.Time
	size     int64
	inode    uint64
	hash     Hash
}

func NewFileInfo(path []string, fileType Type, mode Mode, hasMode Mode, modTime time.Time, size int64, inode uint64, hash Hash) FileInfo {
	return &FileInfoStruct{
		path:     path,
		fileType: fileType,
		mode:     mode,
		hasMode:  hasMode,
		modTime:  modTime,
		size:     size,
		inode:    inode,
		hash:     hash,
	}
}

func cloneFileInfo(orig FileInfo) *FileInfoStruct {
	info := &FileInfoStruct{
		path:     orig.RelativePath(),
		fileType: orig.Type(),
		mode:     orig.Mode(),
		hasMode:  orig.HasMode(),
		modTime:  orig.ModTime(),
		size:     orig.Size(),
		inode:    orig.Inode(),
		hash:     Hash{Type: orig.Hash().Type},
	}
	if !orig.Hash().IsZero() {
		info.hash.Data = make([]byte, len(orig.Hash().Data))
		copy(info.hash.Data, orig.Hash().Data)
	}
	return info
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

func (fi *FileInfoStruct) Hash() Hash {
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

func (fi *FileInfoStruct) Inode() uint64 {
	return fi.inode
}

func (fi *FileInfoStruct) String() string {
	return "FileInfoStruct{" + strings.Join(fi.path, "/") + " " + fi.modTime.String() + " " + strconv.FormatUint(uint64(fi.mode), 8) + "}"
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
	Cancel() error // must not do anything when finished
}

// ScanOptions holds some options to send to the other replica.
type ScanOptions struct {
	Exclude []string
	Include []string
	Follow  []string
	Perms   Mode
	Replica string
}

func (o *ScanOptions) Add(options *ScanOptions) {
	if options == nil {
		return
	}
	o.Exclude = append(o.Exclude, options.Exclude...)
	o.Include = append(o.Include, options.Include...)
	o.Follow = append(o.Follow, options.Follow...)
	o.Perms &= options.Perms
}

// ScanProgress holds the current progress (total estimated number and current
// position) to send from the scanner to the UI.
type ScanProgress struct {
	Total uint64
	Done  uint64
	Path  []string
}

func (p1 *ScanProgress) After(p2 *ScanProgress) bool {
	path1 := strings.Join(p1.Path, "\x00")
	path2 := strings.Join(p2.Path, "\x00")
	return path1 > path2
}

func (p *ScanProgress) Percent() float64 {
	if p == nil {
		return 0
	}
	return float64(p.Done) / float64(p.Total)
}

type ListOptions struct {
	Follow func([]string) bool
}

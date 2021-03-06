// remote.go
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

//go:generate protoc --go_out=. messages.proto

package remote

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
	"github.com/golang/protobuf/proto"
)

// Errors defined by the remote package.
var (
	ErrNoClient       = errors.New("remote: this is not a dtsync client")
	ErrNoServer       = errors.New("remote: this is not a dtsync server")
	ErrInvalidId      = errors.New("remote: invalid request ID")
	ErrInvalidPath    = errors.New("remote: invalid path")
	ErrInvalidError   = errors.New("remote: remote sent an invalid error message")
	ErrNoTests        = errors.New("remote: this is not a test tree")
	ErrConcurrentScan = errors.New("remote: Scan() during scan")
)

type ErrInvalidResponse string

func (e ErrInvalidResponse) Error() string {
	return "remote: invalid response: " + string(e)
}

type RemoteError struct {
	message string
}

func (e RemoteError) Error() string {
	return "remote error: " + e.message
}

type invalidRequest struct {
	message string
}

func (e invalidRequest) Error() string {
	return "invalid request: " + e.message
}

func decodeRemoteError(err *Error) error {
	if err.Type == nil || err.Message == nil {
		return ErrInvalidError
	}

	// TODO: prepend remote address, e.g. host:path

	switch *err.Type {
	case ErrorType_ERR_NOTFOUND:
		return tree.ErrNotFound(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_FOUND:
		return tree.ErrFound(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_CHANGED:
		return tree.ErrChanged(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_NO_DIR:
		return tree.ErrNoDirectory(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_NO_REGULAR:
		return tree.ErrNoRegular(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_NO_SYMLINK:
		return tree.ErrNoSymlink(strings.Split(*err.Message, "/"))
	case ErrorType_ERR_OTHER:
		return RemoteError{*err.Message}
	default:
		// It's an unknown message, but for extensability, we won't err on this
		// unknown error.
		return RemoteError{*err.Message}
	}
}

func encodeRemoteError(err error) *Error {
	switch {
	case tree.IsNotExist(err):
		return encodeRemotePathError(ErrorType_ERR_NOTFOUND, err)
	case tree.IsExist(err):
		return encodeRemotePathError(ErrorType_ERR_FOUND, err)
	case tree.IsChanged(err):
		return encodeRemotePathError(ErrorType_ERR_CHANGED, err)
	case tree.IsNoDirectory(err):
		return encodeRemotePathError(ErrorType_ERR_NO_DIR, err)
	case tree.IsNoRegular(err):
		return encodeRemotePathError(ErrorType_ERR_NO_REGULAR, err)
	case tree.IsNoSymlink(err):
		return encodeRemotePathError(ErrorType_ERR_NO_SYMLINK, err)
	default:
		code := ErrorType_ERR_OTHER
		message := err.Error()
		return &Error{
			Type:    &code,
			Message: &message,
		}
	}
}

func encodeRemotePathError(code ErrorType, err error) *Error {
	path := err.(tree.PathError).Path()
	return &Error{
		Type:    &code,
		Message: &path,
	}
}

// readLength returns the next varint it can read from the reader, without
// sacrificing bytes.
func readLength(r *bufio.Reader) (uint64, error) {
	lengthBuf := make([]byte, 0, 8)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		lengthBuf = append(lengthBuf, b)
		x, n := proto.DecodeVarint(lengthBuf)
		if n != 0 {
			return x, nil
		}
	}
}

// serializeFileInfo takes a tree.FileInfo and puts all fields in *FileInfo.
func serializeFileInfo(info tree.FileInfo) *FileInfo {
	path := info.RelativePath()

	// This is the root, which doesn't have metadata.
	// Serialize a custom object, so we don't panic.
	if _, ok := info.(*dtdiff.Entry); ok && len(path) == 0 {
		fileType := FileType(tree.TYPE_DIRECTORY)
		return &FileInfo{
			Path: path,
			Type: &fileType,
		}
	}

	size := info.Size()
	fileType := FileType(info.Type())
	mode := uint32(info.Mode())
	hasMode := uint32(info.HasMode())
	hashType := uint32(info.Hash().Type)
	fileInfo := &FileInfo{
		Path:     path,
		Type:     &fileType,
		Mode:     &mode,
		HasMode:  &hasMode,
		Size:     &size,
		HashType: &hashType,
		HashData: info.Hash().Data,
	}
	if !info.ModTime().IsZero() {
		modTime := info.ModTime().UnixNano()
		fileInfo.ModTime = &modTime
	}
	if info.Inode() != 0 {
		fileInfo.Inode = proto.Uint64(info.Inode())
	}
	return fileInfo
}

// parseFileInfo does the reverse of serializeFileInfo: convert a *FileInfo to a
// tree.FileInfo.
func parseFileInfo(info *FileInfo) tree.FileInfo {
	if info == nil {
		return nil
	}
	fileType := tree.TYPE_UNKNOWN
	if info.Type != nil {
		fileType = tree.Type(*info.Type)
	}
	mode := tree.Mode(info.GetMode())
	hasMode := tree.Mode(info.GetHasMode())
	modTime := time.Time{}
	if info.ModTime != nil {
		modTime = time.Unix(0, *info.ModTime)
	}
	size := info.GetSize()
	id := info.GetInode()
	return tree.NewFileInfo(info.Path, fileType, mode, hasMode, modTime, size, id, tree.Hash{tree.HashType(info.GetHashType()), info.HashData})
}

// parseScanOptions unpacks a protobuf *ScanOptions into a tree.ScanOptions.
func parseScanOptions(data []byte) (*tree.ScanOptions, error) {
	options := &ScanOptions{}
	err := proto.Unmarshal(data, options)
	if err != nil {
		return nil, err
	}
	var perms tree.Mode
	if options.Perms != nil {
		perms = tree.Mode(*options.Perms)
	}
	return &tree.ScanOptions{
		options.Exclude,
		options.Include,
		options.Follow,
		perms,
		options.GetReplica(),
	}, nil
}

// serializeScanOptions packs a tree.ScanOptions in a protobuf *ScanOptions.
func serializeScanOptions(options *tree.ScanOptions) []byte {
	if options == nil {
		return nil
	}
	perms := uint32(options.Perms)
	data, err := proto.Marshal(&ScanOptions{
		Exclude: options.Exclude,
		Include: options.Include,
		Follow:  options.Follow,
		Perms:   &perms,
		Replica: &options.Replica,
	})
	if err != nil {
		// programming error?
		panic(err)
	}
	return data
}

// receivedData is one unparsed protobuf message received from the other end.
type receivedData struct {
	buf []byte
	err error
}

func runReceiver(reader *bufio.Reader, readChan chan receivedData) {
	for {
		length, err := readLength(reader)
		if err != nil {
			readChan <- receivedData{nil, err}
			break
		}
		buf := make([]byte, length)
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			readChan <- receivedData{nil, err}
			break
		}
		readChan <- receivedData{buf, nil}
	}
}

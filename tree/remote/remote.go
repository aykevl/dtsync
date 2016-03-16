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

// Package remote implements the client and server side of a scan over a networ.
// Any connection can be used, as long as it is a stream of bytes (e.g. TCP,
// SSH).
package remote

import (
	"bufio"
	"errors"
	"io"
	"time"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
	"github.com/golang/protobuf/proto"
)

// Errors defined by the remote package.
var (
	ErrNoClient        = errors.New("remote: this is not a client")
	ErrInvalidId       = errors.New("remote: invalid request ID")
	ErrInvalidRequest  = errors.New("remote: invalid request")
	ErrInvalidPath     = errors.New("remote: invalid path")
	ErrInvalidResponse = errors.New("remote: invalid response")
	ErrNoTests         = errors.New("remote: this is not a test tree")
)

type RemoteError struct {
	message string
}

func (e RemoteError) Error() string {
	return "remote error: " + e.message
}

func remoteError(message string) error {
	switch message {
	case tree.ErrNotFound.Error():
		return tree.ErrNotFound
	default:
		return RemoteError{message}
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

	modTime := info.ModTime().UnixNano()
	size := info.Size()
	fileType := FileType(info.Type())
	return &FileInfo{
		Path:    path,
		Type:    &fileType,
		ModTime: &modTime,
		Size:    &size,
		Hash:    info.Hash(),
	}
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
	modTime := time.Time{}
	if info.ModTime != nil {
		modTime = time.Unix(0, *info.ModTime)
	}
	var size int64
	if info.Size != nil {
		size = *info.Size
	}
	return tree.NewFileInfo(info.Path, fileType, modTime, size, info.Hash)
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
			readChan <- receivedData{nil, err}
			break
		}
		readChan <- receivedData{buf, nil}
	}
}

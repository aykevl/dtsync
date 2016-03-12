// files.go
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

package memory

import (
	"bytes"

	"github.com/aykevl/dtsync/tree"
)

// fileCopier implements tree.FileCopier. It uses bytes.Buffer internally.
type fileCopier struct {
	bytes.Buffer
	callback func(*bytes.Buffer) (tree.FileInfo, tree.FileInfo)
}

// newFileCopier returns a new fileCopier with the given callback.
func newFileCopier(callback func(*bytes.Buffer) (tree.FileInfo, tree.FileInfo)) *fileCopier {
	return &fileCopier{
		callback: callback,
	}
}

// Close calls the callback. bytes.Buffer doesn't have a .Close() method, so we
// don't need to call that one.
func (fc *fileCopier) Finish() (tree.FileInfo, tree.FileInfo, error) {
	info, parentInfo := fc.callback(&fc.Buffer)
	return info, parentInfo, nil
}

// fileCloser implements an io.WriteCloser with a callback that's called when
// the file is closed. It uses bytes.Buffer internally.
type fileCloser struct {
	*bytes.Buffer
	callback func(*bytes.Buffer)
}

// newFileCloser returns a new fileCloser with the given callback.
func newFileCloser(callback func(*bytes.Buffer)) *fileCloser {
	return &fileCloser{
		Buffer:   &bytes.Buffer{},
		callback: callback,
	}
}

// Close calls the callback. bytes.Buffer doesn't have a .Close() method, so we
// don't need to call that one.
func (fc *fileCloser) Close() error {
	fc.callback(fc.Buffer)
	return nil
}

// readCloseBuffer wraps a bytes.Buffer, only exposing Read, and adding a Close
// function (that doesn't do anything)
type readCloseBuffer struct {
	buf *bytes.Buffer
}

func newReadCloseBuffer(data []byte) *readCloseBuffer {
	return &readCloseBuffer{bytes.NewBuffer(data)}
}

// Read is just a pass-through function for Buffer.Read()
func (b *readCloseBuffer) Read(p []byte) (int, error) {
	return b.buf.Read(p)
}

// Close doesn't do anything (returns nil), just to implement io.ReadCloser (we
// don't need to actually close a bytes.Buffer object).
func (b *readCloseBuffer) Close() error {
	return nil
}

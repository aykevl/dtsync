// fileio.go
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
	"os"

	"github.com/aykevl/dtsync/tree"
)

// fileHashWriter wraps an os.File, but takes some special care to provide some
// atomicity. It also calculates a hash of the data written.
type fileHashWriter struct {
	fp        *os.File
	hash      hash.Hash
	closeCall func([]byte) (tree.FileInfo, tree.FileInfo, error)
}

// Write calls Write on the underlying File object, and also adds the data to
// the hash.
func (w *fileHashWriter) Write(b []byte) (int, error) {
	w.hash.Write(b) // must not return an error
	return w.fp.Write(b)
}

// Finish syncs and closes the file, and calls the callback.
func (w *fileHashWriter) Finish() (tree.FileInfo, tree.FileInfo, error) {
	err := w.fp.Sync()
	if err != nil {
		return nil, nil, err
	}
	err = w.fp.Close()
	if err != nil {
		return nil, nil, err
	}
	return w.closeCall(w.hash.Sum(nil))
}

// fileWriter wraps an os.File, without any special things like FileInfo or
// calculating the hash.
type fileWriter struct {
	fp        *os.File
	closeCall func() error
}

// Write is a pass-through function for the underlying os.File object.
func (w *fileWriter) Write(b []byte) (int, error) {
	return w.fp.Write(b)
}

// Close syncs and closes the file, and calls the callback.
func (w *fileWriter) Close() error {
	err := w.fp.Sync()
	if err != nil {
		return err
	}
	err = w.fp.Close()
	if err != nil {
		return err
	}
	return w.closeCall()
}

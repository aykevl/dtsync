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
	"os"

	"github.com/aykevl/dtsync/tree"
)

// fileCopier wraps an os.File, but takes some special care to provide some
// atomicity.
type fileCopier struct {
	fp       *os.File
	finished bool
	onFinish func() (tree.FileInfo, tree.FileInfo, error)
	onCancel func() error
}

// Write calls Write on the underlying File object.
func (w *fileCopier) Write(b []byte) (int, error) {
	return w.fp.Write(b)
}

// Finish syncs and closes the file, and calls the finish callback.
func (w *fileCopier) Finish() (tree.FileInfo, tree.FileInfo, error) {
	err := w.fp.Sync()
	if err != nil {
		return nil, nil, err
	}
	info, parentInfo, err2 := w.onFinish()
	err = w.fp.Close()
	if err != nil {
		return nil, nil, err
	}
	w.finished = true
	return info, parentInfo, err2
}

// Cancel removes the temporary file, and calls the cancel callback.
func (w *fileCopier) Cancel() error {
	if w.finished {
		// already finished
		return nil
	}
	err := w.fp.Close()
	if err != nil {
		return err
	}
	return w.onCancel()
}

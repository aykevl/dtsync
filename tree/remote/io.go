// copier.go
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

package remote

import (
	"io"
	"sync"

	"github.com/aykevl/dtsync/tree"
)

// copier wraps a PipeWriter to implement tree.Copier.
type copier struct {
	w          *io.PipeWriter
	mutex      sync.Mutex
	err        error
	fileInfo   tree.FileInfo
	parentInfo tree.FileInfo
	done       chan struct{}
	finished   bool
}

func (c *copier) Write(p []byte) (int, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.err != nil {
		return 0, c.err
	}
	return c.w.Write(p)
}

func (c *copier) Finish() (tree.FileInfo, tree.FileInfo, error) {
	_ = c.w.Close() // io.PipeWriter does not return errors on close

	<-c.done
	c.finished = true

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.err != nil {
		return nil, nil, c.err
	}
	return c.fileInfo, c.parentInfo, nil
}

func (c *copier) Cancel() error {
	_ = c.w.CloseWithError(tree.ErrCancelled)

	<-c.done
	if c.finished {
		return nil
	}
	c.finished = true

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.err != tree.ErrCancelled {
		return c.err
	}
	return nil
}

func (c *copier) setError(err error) {
	c.mutex.Lock()
	c.err = err
	c.mutex.Unlock()
}

// streamReader wraps a PipeReader that can be set to an error at any time.
type streamReader struct {
	reader *io.PipeReader
	err    error
	mutex  sync.Mutex
}

func (r *streamReader) Read(p []byte) (int, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.Read(p)
}

func (r *streamReader) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.err
}

func (r *streamReader) setError(err error) {
	r.mutex.Lock()
	r.err = err
	r.mutex.Unlock()
}

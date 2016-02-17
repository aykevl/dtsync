// sync.go
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

// Package sync implements the core of the system: synchronizing two directory
// trees. It calculates the differences between two trees and returns them as
// sync jobs.
package sync

import (
	"io"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

// File where current status of the tree is stored.
const STATUS_FILE = ".dtsync"

type Result struct {
	rs    *dtdiff.ReplicaSet
	root1 tree.Entry
	root2 tree.Entry
	jobs  []Job
}

type Job struct {
	direction int
}

func Sync(dir1, dir2 tree.Entry) (*Result, error) {
	// Load replica status
	statusFile1, err := getStatus(dir1)
	if err != nil {
		return nil, err
	}
	statusFile2, err := getStatus(dir2)
	if err != nil {
		return nil, err
	}

	rs, err := dtdiff.LoadReplicaSet(statusFile1, statusFile2)
	if err != nil {
		return nil, err
	}

	result := &Result{
		rs:    rs,
		root1: dir1,
		root2: dir2,
	}

	return result, result.sync()
}

func getStatus(dir tree.Entry) (io.ReadCloser, error) {
	file, err := dir.GetFile(STATUS_FILE)
	if err == tree.ErrNotFound {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return file, nil
}

func (r *Result) sync() error {
	// TODO
	return nil
}

func (r *Result) SaveStatus() error {
	err := r.serializeStatus(r.root1, r.rs.Get(0))
	if err != nil {
		return err
	}
	return r.serializeStatus(r.root2, r.rs.Get(1))
}

func (r *Result) serializeStatus(root tree.Entry, replica *dtdiff.Replica) error {
	outstatus, err := root.SetFile(STATUS_FILE)
	if err != nil {
		return err
	}
	err = replica.Serialize(outstatus)
	if err != nil {
		return err
	}
	return outstatus.Close()
}

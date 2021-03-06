// remote_test.go
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
	"testing"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/file"
	"github.com/aykevl/dtsync/tree/memory"
	"github.com/aykevl/gocount"
	"time"
)

func TestRemote(t *testing.T) {
	numRoutinesStart := gocount.Number()

	var fsList [2]*Client
	for i, _ := range fsList {
		var err error
		fsList[i], err = TestClient()
		if err != nil {
			t.Fatal("could not make test client:", err)
		}
	}

	_ = tree.RemoteTree(fsList[0])

	fs1 := fsList[0]
	fs2 := fsList[1]
	fsCheck := memory.NewRoot()
	fsFile, err := file.NewTestRoot()
	if err != nil {
		t.Fatal("could not open test file tree")
	}
	_ = fsFile

	time.Sleep(time.Millisecond)

	numRoutines := gocount.Number()

	for i, tc := range [][2]tree.TestTree{
		{fs1, fsCheck},
		{fsCheck, fs1},
		{fs1, fs2},
		{fs2, fs1},
		{fs1, fsFile},
		{fsFile, fs1},
		{fsCheck, fsFile},
		{fsFile, fsCheck},
	} {
		tree.TreeTest(t, tc[0], tc[1])
		time.Sleep(time.Millisecond)
		if num := gocount.Number(); num != numRoutines {
			t.Errorf("remote test #%d: number of goroutines changed from %d to %d", i+1, numRoutines, num)
			numRoutines = num
		}
	}

	for i, fs := range fsList {
		err := fs.Close()
		fsList[i] = nil
		if err != nil {
			t.Error("could not close test client:", err)
		}
	}

	if num := gocount.Number(); num != numRoutinesStart {
		t.Errorf("number of goroutines changed from %d to %d after close", numRoutinesStart, num)
	}
}

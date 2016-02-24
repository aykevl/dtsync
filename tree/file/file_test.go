// file_test.go
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
	"io/ioutil"
	"os"
	"testing"

	"github.com/aykevl/dtsync/tree"
)

// TestFilesystem tests the memory-based filesystem memory.Entry.
func TestFilesystem(t *testing.T) {
	rootPath1, err := ioutil.TempDir("", "usync-test-")
	if err != nil {
		t.Fatal("cannot create directory to test:", err)
	}
	defer func() {
		// TODO move to root1.Remove()
		err := os.RemoveAll(rootPath1)
		if err != nil {
			t.Fatal("could not remove root1 after use:", err)
		}
	}()
	root1, err := NewRoot(rootPath1)
	if err != nil {
		t.Fatal("could not open root1:", err)
	}

	rootPath2, err := ioutil.TempDir("", "usync-test-")
	if err != nil {
		t.Fatal("cannot create directory to test:", err)
	}
	defer func() {
		// TODO move to root2.Remove()
		err := os.RemoveAll(rootPath2)
		if err != nil {
			t.Fatal("could not remove root2 after use:", err)
		}
	}()
	root2, err := NewRoot(rootPath2)
	if err != nil {
		t.Fatal("could not open root2:", err)
	}

	t.Log("root1:", rootPath1)
	t.Log("root2:", rootPath2)

	tree.TreeTest(t, root1, root2)
}

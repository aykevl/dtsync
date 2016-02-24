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
	"testing"

	"github.com/aykevl/dtsync/tree"
)

// TestFilesystem tests the memory-based filesystem memory.Entry.
func TestFilesystem(t *testing.T) {
	root1, err := NewTestRoot()
	if err != nil {
		t.Fatal("could not create root1:", err)
	}
	defer func() {
		err := root1.Remove()
		if err != nil {
			t.Fatal("could not remove root1 after use:", err)
		}
	}()

	root2, err := NewTestRoot()
	if err != nil {
		t.Fatal("could not create root2:", err)
	}
	defer func() {
		err := root2.Remove()
		if err != nil {
			t.Fatal("could not remove root2 after use:", err)
		}
	}()

	t.Log("root1:", root1.path())
	t.Log("root2:", root2.path())

	tree.TreeTest(t, root1, root2)
}

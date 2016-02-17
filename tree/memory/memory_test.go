// memory_test.go
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
	"testing"

	"github.com/aykevl/dtsync/tree"
)

// TestFilesystem tests the memory-based filesystem memory.Entry.
//
// TODO: this function should be made more generic so it can test any filesystem
// implementing tree.Entry.
func TestFilesystem(t *testing.T) {
	root1 := NewRoot()

	// Test whether it implements tree.Entry
	var treeItf tree.Entry
	treeItf = root1
	_ = treeItf

	root2 := NewRoot()

	file1, err := root1.AddRegular("file.txt", nil)
	if err != nil {
		t.Error("could not add file:", err)
	}
	checkList1(t, root1, file1)

	file2, err := file1.CopyTo(root2)
	if err != nil {
		t.Errorf("failed to copy file %s to %s: %s", file1, root2, err)
	}
	checkList1(t, root2, file2)

	if !root1.Equal(root2) {
		t.Error("root1 is not equal to root2")
	}

	file1.SetContents([]byte("The quick brown fox jumps over the lazy dog.\n"))
	if root1.Equal(root2) {
		t.Error("root1 is equal to root2 after file1 got updated")
	}

	err = file1.Update(file2)
	if err != nil {
		t.Error("failed to update file:", err)
	}
	if !root1.Equal(root2) {
		t.Error("root1 is not equal to root2 after update")
	}

	// TODO: create directory with and without contents

	for i, root := range []*Entry{root1, root2} {
		if root.Size() != 1 {
			t.Errorf("root %d .Size() is %d while zero was expected after delete", i, root.Size())
		}
		err = root.Remove("file.txt")
		if err != nil {
			t.Errorf("could not remove file from root %d: %s", i, err)
		}
		if root.Size() != 0 {
			t.Errorf("root %d .Size() is non-zero (%d) while zero was expected after delete", i, root.Size())
		}
	}

	if !root1.Equal(root2) {
		t.Error("root1 is not equal to root2 after delete")
	}
}

// checkList1 checks the length of the list of the directory (must be 1) and
// whether the provided file is indeed the one in the list.
func checkList1(t *testing.T, root *Entry, file tree.Entry) {
	l, err := root.List()
	if err != nil {
		t.Error("could not list directory:", err)
	}
	if len(l) != 1 {
		t.Fatalf("len(List()): expected length=1, got %d", len(l))
	}
	if l[0] != file {
		t.Errorf("root1.List()[0] (%s) is not the same as the file added (%s)", l[0], file)
	}
}

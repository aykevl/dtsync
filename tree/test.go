// test.go
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

package tree

import (
	"testing"
)

type TestEntry interface {
	FileEntry

	AddRegular(string, []byte) (FileEntry, error)
	SetContents([]byte) error
}

// TreeTest is not a test in itself, it is called by trees wanting themselves to
// be tested in a generic way.
func TreeTest(t *testing.T, root1, root2 TestEntry) {
	_file1, err := root1.AddRegular("file.txt", nil)
	if err != nil {
		t.Error("could not add file:", err)
	}
	file1 := _file1.(TestEntry)
	checkFile(t, root1, file1, 0, 1)

	_file2, err := file1.CopyTo(root2)
	if err != nil {
		t.Errorf("failed to copy file %s to %s: %s", file1, root2, err)
	}
	file2 := _file2.(TestEntry)
	checkFile(t, root2, file2, 0, 1)

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2")
	}

	quickBrowFox := "The quick brown fox jumps over the lazy dog.\n"
	err = file1.SetContents([]byte(quickBrowFox))
	if err != nil {
		t.Fatalf("could not set contents to file %s: %s", file1, err)
	}
	if testEqual(t, root1, root2) {
		t.Error("root1 is equal to root2 after file1 got updated")
	}

	f, err := root1.GetFile("file.txt")
	if err != nil {
		t.Fatalf("failed to get contents of file.txt: %s", err)
	}
	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("failed to read contents of file %s: %s", file1, err)
	}
	if string(buf[:n]) != quickBrowFox {
		t.Errorf("expected to get %#v but instead got %#v when reading from %s", quickBrowFox, string(buf[:n]), file1)
	}

	err = file1.UpdateOver(file2)
	if err != nil {
		t.Error("failed to update file:", err)
	}
	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after update")
	}

	_dir1, err := root1.CreateDir("dir")
	if err != nil {
		t.Error("could not create directory 1:", err)
	}
	_dir2, err := root2.CreateDir("dir")
	if err != nil {
		t.Error("could not create directory 2:", err)
	}
	dir1 := _dir1.(TestEntry)
	dir2 := _dir2.(TestEntry)
	checkFile(t, root1, dir1, 0, 2)
	checkFile(t, root2, dir2, 0, 2)

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CreateDir")
	}

	child1, err := dir1.AddRegular("file2.txt", []byte("My ship is full of eels."))
	if err != nil {
		t.Fatalf("could not create child in directory %s: %s", dir1, err)
	}
	child2, err := child1.CopyTo(dir2)
	if err != nil {
		t.Errorf("could not copy entry %s to dir %s: %s", child2, dir2, err)
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after adding files to a subdirectory")
	}

	removeTests := []struct {
		entry      TestEntry
		name       string
		sizeBefore int64
	}{
		{root1, "file.txt", 2},
		{root2, "file.txt", 2},
		{root1, "dir", 1},
		{root2, "dir", 1},
	}
	for _, tc := range removeTests {
		if tc.entry.Size() != tc.sizeBefore {
			t.Errorf("entry %s .Size() is %d while %d was expected before delete", tc.entry, tc.entry.Size(), tc.sizeBefore)
		}
		err = tc.entry.Remove(getFile(tc.entry, tc.name))
		if err != nil {
			t.Errorf("could not remove file from entry %s: %s", tc.entry, err)
		}
		if tc.entry.Size() != tc.sizeBefore-1 {
			t.Errorf("entry %s .Size() is %d while %d was expected after delete", tc.entry, tc.entry.Size(), tc.sizeBefore-1)
		}
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after delete")
	}
}

// checkFile tests whether the file exists at a place in the index and checks
// the number of children the directory has.
func checkFile(t *testing.T, dir TestEntry, file TestEntry, index, length int) {
	l, err := dir.List()
	if err != nil {
		t.Error("could not list directory:", err)
		return
	}
	if len(l) != length {
		t.Fatalf("len(List()): expected length=%d, got %d, for directory %s and file %s (list %s)", length, len(l), dir, file, l)
		return
	}
	if l[index] != file {
		t.Errorf("root1.List()[%d] (%s) is not the same as the file added (%s)", index, l[index], file)
	}
}

// getFile returns the entry for the file with that name in the parent.
func getFile(parent Entry, name string) Entry {
	list, err := parent.List()
	if err != nil {
		panic(err) // must not happen
	}
	for _, child := range list {
		if child.Name() == name {
			return child.(TestEntry)
		}
	}
	return nil
}

func testEqual(t *testing.T, file1, file2 TestEntry) bool {
	equal, err := Equal(file1, file2, false)
	if err != nil {
		t.Fatalf("could not compare files %s and %s: %s", file1, file2, err)
	}
	return equal
}

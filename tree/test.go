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
	"bytes"
	"encoding/hex"
)

type TestEntry interface {
	FileEntry

	AddRegular(string, []byte) (FileEntry, error)
	SetContents([]byte) error
}

// Generate a list of hashes (blake2b) to compare with the output of various
// functions.
func generateHashes() map[string][]byte {
	hashList := []struct {
		name string
		hex  string
	}{
		{"", "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8"},
		{"qbf", "3542ad7fd154020a202f8fdf8225ccacae0cb056c1073cf149350806ae58e4d9"},
	}
	hashes := make(map[string][]byte, len(hashList))
	for _, hash := range hashList {
		raw, err := hex.DecodeString(hash.hex)
		if err != nil {
			// must not happen
			panic(err)
		}
		hashes[hash.name] = raw
	}
	return hashes
}

// Tester is a helper interface, abstracting away *testing.T.
// This is useful as we don't have to import package "testing" this way, which
// would pollute the "flag" package with it's flags.
type Tester interface {
	Error(...interface{})
	Errorf(string, ...interface{})
	Fatal(...interface{})
	Fatalf(string, ...interface{})
}

// TreeTest is not a test in itself, it is called by trees wanting themselves to
// be tested in a generic way.
func TreeTest(t Tester, root1, root2 TestEntry) {
	hashes := generateHashes()

	_file1, err := root1.AddRegular("file.txt", nil)
	if err != nil {
		t.Error("could not add file:", err)
	}
	file1 := _file1.(TestEntry)
	checkFile(t, root1, file1, 0, 1, "file.txt")

	_file2, hash2, err := file1.CopyTo(root2)
	if err != nil {
		t.Fatalf("failed to copy file %s to %s: %s", file1, root2, err)
	}
	file2 := _file2.(TestEntry)
	checkFile(t, root2, file2, 0, 1, "file.txt")
	if !bytes.Equal(hash2, hashes[""]) {
		t.Errorf("Hash mismatch for file %s during CopyTo: expected %x, got %x", file2, hashes[""], hash2)
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CopyTo")
	}

	quickBrowFox := "The quick brown fox jumps over the lazy dog.\n"
	err = file1.SetContents([]byte(quickBrowFox))
	if err != nil {
		t.Fatalf("could not set contents to file %s: %s", file1, err)
	}
	if testEqual(t, root1, root2) {
		t.Error("root1 is equal to root2 after file1 got updated")
	}

	hash, err := file1.Hash()
	if err != nil {
		t.Fatal("could not get hash of file1:", err)
	}
	if !bytes.Equal(hash, hashes["qbf"]) {
		t.Errorf("Hash mismatch for file %s during Hash: expected %x, got %x", file1, hashes["qbf"], hash)
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

	hash, err = file1.UpdateOver(file2)
	if err != nil {
		t.Error("failed to update file:", err)
	}
	if !bytes.Equal(hash, hashes["qbf"]) {
		t.Errorf("Hash mismatch during UpdateOver for file %s: expected %x, got %x", file2, hashes["qbf"], hash)
	}
	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after UpdateOver")
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
	checkFile(t, root1, dir1, 0, 2, "dir")
	checkFile(t, root2, dir2, 0, 2, "dir")

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CreateDir")
	}

	child1, err := dir1.AddRegular("file2.txt", []byte("My ship is full of eels."))
	if err != nil {
		t.Fatalf("could not create child in directory %s: %s", dir1, err)
	}
	checkFile(t, dir1, child1.(TestEntry), 0, 1, "dir/file2.txt")
	child2, hash2, err := child1.CopyTo(dir2)
	if err != nil {
		t.Errorf("could not copy entry %s to dir %s: %s", child2, dir2, err)
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after adding files to a subdirectory")
	}

	removeTests := []struct {
		entry      TestEntry
		name       string
		sizeBefore int
	}{
		{root1, "file.txt", 2},
		{root2, "file.txt", 2},
		{root1, "dir", 1},
		{root2, "dir", 1},
	}
	for _, tc := range removeTests {
		list, err := tc.entry.List()
		if err != nil {
			t.Fatalf("could not list directory contents of %s: %s", tc.entry, err)
		}
		if len(list) != tc.sizeBefore {
			t.Errorf("entry %s child count is %d while %d was expected before delete", tc.entry, len(list), tc.sizeBefore)
		}
		err = getFile(tc.entry, tc.name).Remove()
		if err != nil {
			t.Errorf("could not remove file from entry %s: %s", tc.entry, err)
		}
		list, err = tc.entry.List()
		if err != nil {
			t.Fatalf("could not list directory contents of %s: %s", tc.entry, err)
		}
		if len(list) != tc.sizeBefore-1 {
			t.Errorf("entry %s child count is %d while %d was expected after delete", tc.entry, len(list), tc.sizeBefore-1)
		}
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after delete")
	}
}

// checkFile tests whether the file exists at a place in the index and checks
// the number of children the directory has.
func checkFile(t Tester, dir TestEntry, file TestEntry, index, length int, relpath string) {
	l, err := dir.List()
	if err != nil {
		t.Error("could not list directory:", err)
		return
	}
	if len(l) != length {
		t.Fatalf("len(List()): expected length=%d, got %d, for directory %s and file %s (list %s)", length, len(l), dir, file, l)
		return
	}
	if !testEqual(t, l[index].(TestEntry), file) {
		t.Errorf("root1.List()[%d] (%s) is not the same as the file added (%s)", index, l[index], file)
	}
	if file.RelativePath() != relpath {
		t.Errorf("%s: expected RelativePath to give %s, not %s", file, relpath, file.RelativePath())
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

func testEqual(t Tester, file1, file2 TestEntry) bool {
	equal, err := Equal(file1, file2, false)
	if err != nil {
		t.Fatalf("could not compare files %s and %s: %s", file1, file2, err)
	}
	return equal
}

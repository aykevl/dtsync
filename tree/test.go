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
	"io/ioutil"
	"strings"
)

type TestTree interface {
	FileTree

	// AddRegular sets the file at the path to the specified contents. The
	// FileInfo returned does not have to contain the hash.
	AddRegular(path []string, contents []byte) (FileInfo, error)
	SetContents(path []string, contents []byte) error
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
func TreeTest(t Tester, fs1, fs2 TestTree) {
	hashes := generateHashes()

	root1 := fs1.(LocalTree).Root()
	root2 := fs2.(LocalTree).Root()

	info1, err := fs1.AddRegular(pathSplit("file.txt"), nil)
	if err != nil {
		t.Fatal("could not add file:", err)
	}
	checkInfo(t, root1, info1, 0, 1, "file.txt")

	info2, _, err := Copy(fs1, fs2, info1, &FileInfoStruct{})
	if err != nil {
		t.Fatalf("failed to copy file %s to %s: %s", info1, fs2, err)
	}
	file2 := getFile(root2, "file.txt")
	checkFile(t, root2, file2, 0, 1, "file.txt")
	if !bytes.Equal(info2.Hash(), hashes[""]) {
		t.Errorf("Hash mismatch for file %s during Copy: expected %x, got %x", file2, hashes[""], info2.Hash())
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Copy")
	}

	quickBrowFox := "The quick brown fox jumps over the lazy dog.\n"
	err = fs1.SetContents(info1.RelativePath(), []byte(quickBrowFox))
	if err != nil {
		t.Fatalf("could not set contents to file %s: %s", info1, err)
	}
	if testEqual(t, root1, root2) {
		t.Error("root1 is equal to root2 after file1 got updated")
	}
	file1 := getFile(root1, "file.txt")

	hash, err := file1.Hash()
	if err != nil {
		t.Fatal("could not get hash of file1:", err)
	}
	if !bytes.Equal(hash, hashes["qbf"]) {
		t.Errorf("Hash mismatch for file %s during Hash: expected %x, got %x", file1, hashes["qbf"], hash)
	}

	f, err := fs1.GetFile("file.txt")
	if err != nil {
		t.Fatalf("failed to get contents of file.txt: %s", err)
	}
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read contents of file %s: %s", file1, err)
	}
	if string(buf) != quickBrowFox {
		t.Errorf("expected to get %#v but instead got %#v when reading from %s", quickBrowFox, string(buf), file1)
	}

	info, _, err := Update(file1.Tree(), fs2, infoFromEntry(t, file1), infoFromEntry(t, file2))
	if err != nil {
		t.Fatal("failed to update file:", err)
	}
	if !bytes.Equal(info.Hash(), hashes["qbf"]) {
		t.Errorf("Hash mismatch during Update for file %s: expected %x, got %x", file2, hashes["qbf"], info.Hash())
	}
	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Update")
	}
	file2 = getFile(root2, "file.txt")

	infoDir1, err := fs1.CreateDir("dir", &FileInfoStruct{})
	if err != nil {
		t.Error("could not create directory 1:", err)
	}
	dir1 := getFile(root1, "dir")
	if !sameInfo(t, infoDir1, dir1) {
		t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir1)
	}
	infoDir2, err := fs2.CreateDir("dir", &FileInfoStruct{})
	if err != nil {
		t.Error("could not create directory 2:", err)
	}
	dir2 := getFile(root2, "dir")
	if !sameInfo(t, infoDir2, dir2) {
		t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir2)
	}
	checkFile(t, root1, dir1, 0, 2, "dir")
	checkFile(t, root2, dir2, 0, 2, "dir")

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CreateDir")
	}

	childinfo1, err := fs1.AddRegular(pathSplit("dir/file2.txt"), []byte("My ship is full of eels."))
	if err != nil {
		t.Fatalf("could not create child in directory %s: %s", dir1, err)
	}
	checkInfo(t, dir1, childinfo1, 0, 1, "dir/file2.txt")

	_, _, err = Copy(fs1, fs2, childinfo1, infoFromEntry(t, dir2))
	if err != nil {
		t.Errorf("could not copy entry %s to dir %s: %s", childinfo1, dir2, err)
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after adding files to a subdirectory")
	}

	removeTests := []struct {
		root       Tree
		child      Entry
		sizeBefore int
	}{
		{fs1, file1, 2},
		{fs2, file2, 2},
		{fs1, dir1, 1},
		{fs2, dir2, 1},
	}
	for _, tc := range removeTests {
		entry := tc.root.(LocalTree).Root()
		list, err := entry.List()
		if err != nil {
			t.Fatalf("could not list directory contents of %s: %s", tc.root, err)
		}
		if len(list) != tc.sizeBefore {
			t.Fatalf("entry %s child count is %d while %d was expected before delete", entry, len(list), tc.sizeBefore)
		}
		_, err = tc.root.Remove(infoFromEntry(t, tc.child))
		if err != nil {
			t.Errorf("could not remove file %s: %s", tc.child, err)
		}
		list, err = entry.List()
		if err != nil {
			t.Fatalf("could not list directory contents of %s: %s", entry, err)
		}
		if len(list) != tc.sizeBefore-1 {
			t.Fatalf("root %s child count is %d while %d was expected after delete", tc.root, len(list), tc.sizeBefore-1)
		}
	}

	if !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after delete")
	}
}

// pathSplit splits the given string at the slash, to get path elements. An
// empty string also gives an empty slice (nil).
func pathSplit(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// pathJoin does the reverse of pathSplit, joining the array with slashes.
func pathJoin(p []string) string {
	return strings.Join(p, "/")
}

// sameInfo returns true if both FileInfo interfaces are exactly equal for all
// properties.
func sameInfo(t Tester, info FileInfo, entry Entry) bool {
	entryHash, err := entry.Hash()
	if err != nil {
		t.Fatalf("could not hash %s: %s", entry, err)
	}
	if info.Name() != entry.Name() ||
		info.Type() != entry.Type() ||
		info.ModTime() != entry.ModTime() ||
		info.Size() != entry.Size() ||
		!bytes.Equal(info.Hash(), entryHash) {
		return false
	}
	path1 := info.RelativePath()
	path2 := entry.RelativePath()
	if len(path1) != len(path2) {
		return false
	}
	for i, part := range path1 {
		if path2[i] != part {
			return false
		}
	}
	return true
}

// checkFile tests whether the file exists at a place in the index and checks
// the number of children the directory has.
func checkFile(t Tester, dir Entry, file Entry, index, length int, relpath string) {
	l, err := dir.List()
	if err != nil {
		t.Error("could not list directory:", err)
		return
	}
	if len(l) != length {
		t.Fatalf("len(List()): expected length=%d, got %d, for directory %s and file %s (list %s)", length, len(l), dir, file, l)
		return
	}
	if !testEqual(t, l[index], file) {
		t.Errorf("root1.List()[%d] (%s) is not the same as the file added (%s)", index, l[index], file)
	}
	if pathJoin(file.RelativePath()) != relpath {
		t.Errorf("%s: expected RelativePath to give %s, not %s", file, relpath, pathJoin(file.RelativePath()))
	}
}

// checkInfo tests whether the file exists at a place in the index and checks
// the number of children the directory has.
func checkInfo(t Tester, dir Entry, info FileInfo, index, length int, relpath string) {
	l, err := dir.List()
	if err != nil {
		t.Error("could not list directory:", err)
		return
	}
	if len(l) != length {
		t.Fatalf("len(List()): expected length=%d, got %d, for directory %s and file %s (list %s)", length, len(l), dir, info, l)
		return
	}
	file := l[index]
	info2, err := file.Info()
	if err != nil {
		t.Fatalf("could not get info from %s: %s", info, err)
	}
	if Fingerprint(info) != Fingerprint(info2) {
		t.Errorf("root1.List()[%d] (%s) is not the same as the file added (%s)", index, l[index], info)
	}
	if pathJoin(info.RelativePath()) != relpath {
		t.Errorf("%s: expected RelativePath to give %s, not %s", info, relpath, pathJoin(info.RelativePath()))
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
			return child
		}
	}
	return nil
}

func testEqual(t Tester, file1, file2 Entry) bool {
	equal, err := Equal(file1, file2, false)
	if err != nil {
		t.Fatalf("could not compare files %s and %s: %s", file1, file2, err)
	}
	return equal
}

func infoFromEntry(t Tester, e Entry) FileInfo {
	info, err := e.Info()
	if err != nil {
		t.Fatal("Info() returned error:", err)
	}
	return info
}

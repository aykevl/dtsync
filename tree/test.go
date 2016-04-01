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
	"time"
)

type TestTree interface {
	FileTree

	// PutFile sets the file at the path to the specified contents. The
	// FileInfo returned does not have to contain the hash.
	PutFile(path []string, contents []byte) (FileInfo, error)

	// Info returns the FileInfo (with hash) for a particular path.
	ReadInfo(path []string) (FileInfo, error)
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

	var root1, root2 Entry
	if fs1, ok := fs1.(LocalTree); ok {
		root1 = fs1.(LocalTree).Root()
	}
	if fs2, ok := fs2.(LocalTree); ok {
		root2 = fs2.(LocalTree).Root()
	}

	/*** Test regular files ***/

	info1, err := fs1.PutFile(pathSplit("file.txt"), nil)
	if err != nil {
		t.Fatal("could not add file:", err)
	}
	if root1 != nil {
		checkInfo(t, root1, info1, 0, 1, "file.txt")
	}

	info2, _, err := Copy(fs1, fs2, info1, &FileInfoStruct{})
	if err != nil {
		t.Fatalf("failed to copy file %s to %s: %s", info1, fs2, err)
	}
	if root2 != nil {
		file2 := getFile(root2, "file.txt")
		checkFile(t, root2, file2, 0, 1, "file.txt")
	}
	if Fingerprint(info1) != Fingerprint(info2) {
		t.Errorf("files do not match after copy: %s %s", info1, info2)
	}
	if !bytes.Equal(info2.Hash(), hashes[""]) {
		t.Errorf("Hash mismatch for file %s during Copy: expected %x, got %x", info2, hashes[""], info2.Hash())
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Copy")
	}

	if root1 != nil {
		// Try overwriting a file.
		_, err := fs1.CreateFile("file.txt", &FileInfoStruct{}, info2)
		if err == nil {
			t.Error("file.txt was overwritten with CreateFile")
		} else if !IsExist(err) {
			t.Error("failed to try to overwrite file.txt with CreateFile:", err)
		}

		if root2 != nil && !testEqual(t, root1, root2) {
			t.Error("root1 is not equal to root2 after CreateFile (try to overwrite)")
		}

		// Try updating a file that doesn't exist.
		info := &FileInfoStruct{
			path: []string{"file-notexists.txt"},
		}
		_, err = fs1.UpdateFile(info, info2)
		if err == nil {
			t.Error("file-notexists.txt was created with UpdateFile")
		} else if !IsNotExist(err) {
			t.Error("failed to try to create file.txt with UpdateFile:", err)
		}

		if root2 != nil && !testEqual(t, root1, root2) {
			t.Error("root1 is not equal to root2 after UpdateFile (try to create)")
		}
	}

	// Try Copier.Cancel()
	// info2 used, as it needs *any* FileInfo.
	cp, err := fs1.CreateFile("file-create.txt", &FileInfoStruct{}, info2)
	if err != nil {
		t.Fatal("could not create file-create.txt:", err)
	}
	err = cp.Cancel()
	if err != nil {
		t.Error("could not cancel creation of file-create.txt:", err)
	}

	_, err = fs1.ReadInfo([]string{"file-create.txt"})
	if err == nil {
		t.Error("file-create.txt created while it should have been cancelled")
	} else if !IsNotExist(err) {
		t.Error("ReadInfo failed for file-create.txt:", err)
	}

	cp, err = fs1.UpdateFile(info1, NewFileInfo(info2.RelativePath(), TYPE_REGULAR, time.Time{}, 0, nil))
	if err != nil {
		t.Fatal("could not update file.txt:", err)
	}
	err = cp.Cancel()
	if err != nil {
		t.Error("could not cancel updating file.txt:", err)
	}

	info, err := fs1.ReadInfo([]string{"file.txt"})
	if err != nil {
		t.Error("cannot ReadInfo file.txt:", err)
	} else if Fingerprint(info) != Fingerprint(info1) {
		t.Error("file.txt updated while it was cancelled")
	}

	quickBrowFox := "The quick brown fox jumps over the lazy dog.\n"
	info, err = fs1.PutFile(info1.RelativePath(), []byte(quickBrowFox))
	if err != nil {
		t.Fatalf("could not set contents to file %s: %s", info1, err)
	}
	info1 = info
	if root1 != nil && root2 != nil && testEqual(t, root1, root2) {
		t.Error("root1 is equal to root2 after file1 got updated")
	}

	if root1 != nil {
		file1 := getFile(root1, "file.txt")

		hash, err := file1.Hash()
		if err != nil {
			t.Fatal("could not get hash of file1:", err)
		}
		if !bytes.Equal(hash, hashes["qbf"]) {
			t.Errorf("Hash mismatch for file %s during Hash: expected %x, got %x", info1, hashes["qbf"], hash)
		}
	}

	f, err := fs1.GetFile("file.txt")
	if err != nil {
		t.Fatalf("failed to get contents of file.txt: %s", err)
	}
	buf, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read contents of file %s: %s", info1, err)
	}
	if string(buf) != quickBrowFox {
		t.Errorf("expected to get %#v but instead got %#v when reading from %s", quickBrowFox, string(buf), info1)
	}

	info2, _, err = Update(fs1, fs2, info1, info2)
	if err != nil {
		t.Fatal("failed to update file:", err)
	}
	if !bytes.Equal(info2.Hash(), hashes["qbf"]) {
		t.Errorf("Hash mismatch during Update for file %s: expected %x, got %x", info2, hashes["qbf"], info2.Hash())
	}
	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Update")
	}

	/*** Test symlinks ***/

	// create symlink
	linkTarget := "file.txt"
	link1, _, err := fs1.CreateSymlink("link", &FileInfoStruct{}, info1, linkTarget)
	if err != nil {
		t.Fatal("could not create symlink:", err)
	}
	if !link1.ModTime().Equal(info1.ModTime()) {
		t.Error("CreateSymlink did not set ModTime")
	}

	// read symlink
	link, err := fs1.ReadSymlink(link1)
	if err != nil {
		t.Error("could not read symlink after create:", err)
	} else if link != linkTarget {
		t.Errorf("ReadSymlink: expected %#v, got %#v", linkTarget, link)
	}

	// try to update symlink (must fail)
	link2 := NewFileInfo(link1.RelativePath(), TYPE_SYMLINK, time.Time{}, 0, nil)
	_, _, err = Update(fs1, fs2, link1, link2)
	if !IsNotExist(err) {
		t.Error("Update link was not 'not found':", err)
	}

	// try to copy 'modified' symlink
	link1Wrong := NewFileInfo(link1.RelativePath(), TYPE_SYMLINK, time.Time{}, 0, nil)
	_, _, err = Copy(fs1, fs2, link1Wrong, &FileInfoStruct{})
	if !IsChanged(err) {
		t.Errorf("No ErrChanged while copying %s: %s", link2.Name(), err)
	}

	// copy symlink
	link2, _, err = Copy(fs1, fs2, link1, &FileInfoStruct{})
	if err != nil {
		t.Fatal("cannot copy link:", err)
	}

	// try copying symlink again
	_, _, err = Copy(fs1, fs2, link1, &FileInfoStruct{})
	if !IsExist(err) {
		t.Fatal("overwrite with Copy, error:", err)
	}

	// update symlink
	linkTarget = "file-does-not-exist"
	link1Source := &FileInfoStruct{[]string{"link"}, TYPE_SYMLINK, time.Now(), 0, nil}
	link1, _, err = fs1.UpdateSymlink(link1, link1Source, linkTarget)
	if err != nil {
		t.Fatal("could not update symlink (with mtime):", err)
	} else if !link1.ModTime().Equal(link1Source.ModTime()) {
		t.Errorf("UpdateSymlink with time did not set ModTime (expected %s, got %s)", link1Source.ModTime(), link1.ModTime())
	}

	// update symlink without time
	oldModTime := link1.ModTime()
	link1Source.modTime = time.Time{}
	linkTarget = "file-does-not-exist-2"
	link1, _, err = fs1.UpdateSymlink(link1, link1Source, linkTarget)
	if err != nil {
		t.Fatal("could not update symlink (without mtime):", err)
	} else if !link1.ModTime().After(oldModTime) {
		t.Errorf("UpdateSymlink without time did not set ModTime (expected after %s, got %s)", oldModTime, link1.ModTime())
		t.Fatal()
	}

	// try updating with 'changed' source symlink
	_, _, err = Update(fs1, fs2, link1Wrong, link2)
	if !IsChanged(err) {
		t.Error("no ErrChanged in Update (source changed), err:", err)
	}

	// try updating with 'changed' target symlink
	_, _, err = Update(fs1, fs2, link1, link1Wrong) // link1Wrong can be re-used here
	if !IsChanged(err) {
		t.Error("no ErrChanged in Update (source changed), err:", err)
	}

	// read symlink again
	link, err = fs1.ReadSymlink(link1)
	if err != nil {
		t.Error("could not read symlink after update:", err)
	} else if link != linkTarget {
		t.Errorf("ReadSymlink: expected %#v, got %#v", linkTarget, link)
	}

	// update symlink
	link2, _, err = Update(fs1, fs2, link1, link2)
	if err != nil {
		t.Error("cannot update link:", err)
	}

	/*** Test directories ***/

	infoDir1, err := fs1.CreateDir("dir", &FileInfoStruct{})
	if err != nil {
		t.Error("could not create directory 1:", err)
	}
	if root1 != nil {
		dir1 := getFile(root1, "dir")
		if !sameInfo(t, infoDir1, dir1) {
			t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir1)
		}
		checkFile(t, root1, dir1, 0, 3, "dir")
	}

	infoDir2, err := fs2.CreateDir("dir", &FileInfoStruct{})
	if err != nil {
		t.Fatal("could not create directory 2:", err)
	}
	if root2 != nil {
		dir2 := getFile(root2, "dir")
		if !sameInfo(t, infoDir2, dir2) {
			t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir2)
		}
		checkFile(t, root2, dir2, 0, 3, "dir")
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CreateDir")
	}

	childinfo1, err := fs1.PutFile(pathSplit("dir/file2.txt"), []byte("My ship is full of eels."))
	if err != nil {
		t.Fatalf("could not create child in directory %s: %s", infoDir1, err)
	}
	if root1 != nil {
		dir1 := getFile(root1, "dir")
		checkInfo(t, dir1, childinfo1, 0, 1, "dir/file2.txt")
	}

	_, _, err = Copy(fs1, fs2, childinfo1, infoDir2)
	if err != nil {
		t.Errorf("could not copy entry %s to dir %s: %s", childinfo1, infoDir2, err)
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after adding files to a subdirectory")
	}

	/*** Test remove ***/

	removeTests := []struct {
		fs         Tree
		child      FileInfo
		sizeBefore int
	}{
		{fs1, link1, 3},
		{fs2, link2, 3},
		{fs1, info1, 2},
		{fs2, info2, 2},
		{fs1, infoDir1, 1},
		{fs2, infoDir2, 1},
	}
	for i, tc := range removeTests {
		if localFS, ok := tc.fs.(LocalTree); ok {
			root := localFS.Root()
			list, err := root.List()
			if err != nil {
				t.Fatalf("could not list directory contents of %s: %s", root, err)
			}
			if len(list) != tc.sizeBefore {
				t.Fatalf("root %s child count is %d while %d was expected before delete", root, len(list), tc.sizeBefore)
			}
		}
		_, err = tc.fs.Remove(tc.child)
		if err != nil {
			t.Errorf("could not remove file #%d %s: %s", i, tc.child, err)
		}
		if localFS, ok := tc.fs.(LocalTree); ok {
			root := localFS.Root()
			list, err := root.List()
			if err != nil {
				t.Fatalf("could not list directory contents of %s: %s", root, err)
			}
			if len(list) != tc.sizeBefore-1 {
				t.Fatalf("root %s child count is %d while %d was expected after delete", root, len(list), tc.sizeBefore-1)
			}
		}
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
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
	if !MatchFingerprint(info, l[index].Info()) {
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

// Equal compares two entries, only returning true when this file and possible
// children (for directories) are exactly equal.
func Equal(file1, file2 Entry, includeDirModTime bool) (bool, error) {
	if file1.Name() != file2.Name() || file1.Type() != file2.Type() {
		return false, nil
	}
	if !file1.ModTime().Equal(file2.ModTime()) {
		if file1.Type() == TYPE_DIRECTORY {
			if includeDirModTime {
				return false, nil
			}
		} else {
			return false, nil
		}
	}
	switch file1.Type() {
	case TYPE_REGULAR:
		hashes := make([][]byte, 2)
		for i, file := range []Entry{file1, file2} {
			testTree, ok := file.Tree().(TestTree)
			if !ok {
				return false, ErrNotImplemented("Equal: comparing non-TestTree entries")
			}
			info, err := testTree.ReadInfo(file.RelativePath())
			if err != nil {
				return false, err
			}
			hashes[i] = info.Hash()
		}
		return hashes[0] != nil && bytes.Equal(hashes[0], hashes[1]), nil

	case TYPE_DIRECTORY:
		list1, err := file1.List()
		if err != nil {
			return false, err
		}
		list2, err := file2.List()
		if err != nil {
			return false, err
		}

		if len(list1) != len(list2) {
			return false, nil
		}
		for i := 0; i < len(list1); i++ {
			if equal, err := Equal(list1[i], list2[i], includeDirModTime); !equal || err != nil {
				return equal, err
			}
		}
		return true, nil

	case TYPE_SYMLINK:
		// Both are symlinks, so calling Info() is fast.
		var links [2]string
		for i, file := range []Entry{file1, file2} {
			var err error
			links[i], err = file.Tree().(FileTree).ReadSymlink(file.Info())
			if err != nil {
				return false, err
			}
		}
		return links[0] == links[1], nil

	default:
		return false, ErrNotImplemented("Equal: unknown filetype")
	}
}

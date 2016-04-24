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
	"io"
	"io/ioutil"
	"strings"
	"time"
)

type TestTree interface {
	FileTree

	// Get this file. Redefined here to be able to test it (originally defined
	// in LocalFileTree).
	GetFile(name string) (io.ReadCloser, error)

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
		{"big", "25a5f114126caa1a67d30b1e019c96aa0e70fe2041686309df9ed13661631a97"},
		{"big-update", "913f495f2b70630bb6fb550cc4e8a47104c44023e5202f6a27e9efc37b0ca687"},
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
	FailNow()
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

	// chmod before copy
	info1Expected := cloneFileInfo(info1)
	info1Expected.mode = 0600
	info1, err = fs1.Chmod(info1, &FileInfoStruct{mode: 0600, hasMode: 0777})
	if err != nil {
		t.Fatal("could not chmod:", err)
	}
	if !sameInfo(info1, info1Expected) {
		t.Errorf("could not update mode with Chmod:\nexpected: %s\nactual:   %s", info1Expected, info1)
	}

	// try copy with invalid source info
	infoWrong := &FileInfoStruct{info1.RelativePath(), info1.Type(), 0666, 0666, time.Now(), info1.Size(), info1.Hash()}
	reader, err := fs1.CopySource(infoWrong)
	if err == nil {
		buf := make([]byte, 1024)
		_, err = reader.Read(buf)
	}
	if err == nil {
		t.Fatal("file.txt was copied but expected ErrChanged")
	} else if !IsChanged(err) {
		t.Error("error while trying to copy (invalid) file1.txt:", err)
	}

	info2, _, err := Copy(fs1, fs2, info1, &FileInfoStruct{})
	if err != nil {
		t.Fatalf("failed to copy file %s to %s: %s", info1, fs2, err)
	}
	if root2 != nil {
		file2 := getFile(root2, "file.txt")
		checkFile(t, root2, file2, 0, 1, "file.txt")
	}
	if !sameInfo(info1, info2) {
		t.Errorf("files do not match after Copy:\n%s\n%s", info1, info2)
	}
	if !bytes.Equal(info2.Hash().Data, hashes[""]) {
		t.Errorf("Hash mismatch for file %s during Copy: expected %x, got %x", info2, hashes[""], info2.Hash())
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Copy")
	}

	// Try overwriting a file.
	copier, err := fs1.CreateFile("file.txt", &FileInfoStruct{}, info2)
	if err == nil {
		// Remote only returns an error during write or on close.
		_, _, err = copier.Finish()
	}
	if err == nil {
		t.Error("file.txt was overwritten with CreateFile")
	} else if !IsExist(err) {
		t.Error("failed to try to overwrite file.txt with CreateFile:", err)
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after CreateFile (try to overwrite)")
	}

	// Try updating a file that doesn't exist.
	info := FileInfo(&FileInfoStruct{
		path: []string{"file-notexists.txt"},
	})
	copier, err = fs1.UpdateFile(info, info2)
	if err == nil {
		// Remote only returns an error during write or on close.
		_, _, err = copier.Finish()
	}
	if err == nil {
		t.Error("file-notexists.txt was created with UpdateFile")
	} else if !IsNotExist(err) {
		t.Error("failed to try to create file.txt with UpdateFile:", err)
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after UpdateFile (try to create)")
	}

	// Try updating with invalid FileInfo.
	copier, err = fs1.UpdateFile(infoWrong, info1)
	if err == nil {
		_, _, err = copier.Finish()
	}
	if err == nil {
		t.Error("file.txt was updated but expected ErrChanged (invalid dest info)")
	} else if !IsChanged(err) {
		t.Error("error while trying to update file1.txt (invalid dest info):", err)
	}

	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after UpdateFile (invalid source or dest)")
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

	cp, err = fs1.UpdateFile(info1, &FileInfoStruct{info2.RelativePath(), TYPE_REGULAR, 0666, 0666, time.Time{}, 0, Hash{}})
	if err != nil {
		t.Fatal("could not update file.txt:", err)
	}
	err = cp.Cancel()
	if err != nil {
		t.Error("could not cancel updating file.txt:", err)
	}

	info, err = fs1.ReadInfo([]string{"file.txt"})
	if err != nil {
		t.Error("cannot ReadInfo file.txt:", err)
	} else if !MatchFingerprint(info, info1) {
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

	info1, err = fs1.ReadInfo(info1.RelativePath())
	if err != nil {
		t.Fatal("could not get hash of file1:", err)
	}
	if !bytes.Equal(info1.Hash().Data, hashes["qbf"]) {
		t.Errorf("Hash mismatch for file %s: expected %x, got %x", info1, hashes["qbf"], info1.Hash())
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

	info2, _, _, err = Update(fs1, fs2, info1, info2)
	if err != nil {
		t.Fatal("failed to update file:", err)
	}
	if !bytes.Equal(info2.Hash().Data, hashes["qbf"]) {
		t.Errorf("Hash mismatch during Update for file %s: expected %x, got %x", info2, hashes["qbf"], info2.Hash())
	}
	if !sameInfo(info1, info2) {
		t.Errorf("files do not match after Update:\n%s\n%s", info1, info2)
	}
	if root1 != nil && root2 != nil && !testEqual(t, root1, root2) {
		t.Error("root1 is not equal to root2 after Update")
	}

	// chmod before update
	info1Expected = cloneFileInfo(info1)
	info1Expected.mode = 0606
	info1, err = fs1.Chmod(info1, &FileInfoStruct{mode: 0606, hasMode: 0777})
	if err != nil {
		t.Fatal("could not chmod:", err)
	}
	if !sameInfo(info1, info1Expected) {
		t.Errorf("did not fully update file with chmod:\nexpected: %s\nactual:   %s", info1Expected, info1)
	}
	if sameInfo(info1, info2) {
		t.Errorf("Expected info1 and info2 to differ after chmod: %s", info1)
	}

	// chmod in Update
	info2, err = fs1.Chmod(info2, info1)
	if err != nil {
		t.Fatal("could not Update after chmod:", err)
	}
	if !sameInfo(info1, info2) {
		t.Errorf("did not fully chmod:\n%s\n%s", info1, info2)
	}

	/*** Test big files ***/

	// create big buffer (of 1mb)
	bigBuffer := make([]byte, 1000*1000)
	for i := 0; i < 20*1000; i++ {
		copy(bigBuffer[i*50:], []byte("This is a line of text...........................\n")) // 50 chars
	}

	// test creating big file
	copier, err = fs1.CreateFile("big.txt", &FileInfoStruct{}, &FileInfoStruct{mode: 0644})
	if err != nil {
		t.Fatal("could not CreateFile big.txt:", err)
	}
	_, err = io.Copy(copier, bytes.NewReader(bigBuffer))
	if err != nil {
		t.Error("could not copy data to big.txt:", err)
	}
	big1, _, err := copier.Finish()
	if err != nil {
		t.Fatal("could not Finish() copying big.txt:", err)
	}

	// CreateFile() doesn't set the hash.
	big1, _ = fs1.ReadInfo(big1.RelativePath())

	// test copying big file
	big2, _, err := Copy(fs1, fs2, big1, &FileInfoStruct{})
	if err != nil {
		t.Fatal("could not Copy big.txt:", err)
	}
	if !sameFullInfo(big1, big2) {
		t.Errorf("did not update big file in Copy:\n%s\n%s", big1, big2)
	}
	if !bytes.Equal(big1.Hash().Data, hashes["big"]) {
		t.Errorf("Hash mismatch for file %s during Copy:\nexpected %x\ngot      %x", big1, hashes["big"], big1.Hash().Data)
	}

	// test slightly changing big file
	bigBuffer[3045] = 'x' // replace one dot with an x
	big1Update := cloneFileInfo(big1)
	big1Update.modTime = time.Now()
	copier, err = fs1.UpdateFile(big1, big1Update)
	if err != nil {
		t.Fatal("could not UpdateFile big.txt:", err)
	}
	_, err = io.Copy(copier, bytes.NewReader(bigBuffer))
	if err != nil {
		t.Error("could not copy data to big.txt:", err)
	}
	big1, _, err = copier.Finish()
	if err != nil {
		t.Fatal("could not Finish() copying big.txt:", err)
	}
	if sameFullInfo(big1, big2) {
		t.Errorf("did not update fingerprint while updating big.txt:\n%s\n%s", big1, big2)
	}

	// UpdateFile() doesn't set the hash, we'll get it this way.
	big1, _ = fs1.ReadInfo(big1.RelativePath())

	// test updating big file
	big2, _, stats, err := Update(fs1, fs2, big1, big2)
	if err != nil {
		t.Fatal("could not Update big.txt:", err)
	}
	if !sameFullInfo(big1, big2) {
		t.Errorf("did not update big file in Update:\n%s\n%s", big1, big2)
	}
	if !bytes.Equal(big1.Hash().Data, hashes["big-update"]) {
		t.Errorf("Hash mismatch for file %s during Update:\nexpected %x\ngot      %x", big1, hashes["big-update"], big1.Hash().Data)
	}
	if root1 == nil || root2 == nil {
		// using rsync
		if stats.ToSource == 0 || stats.ToTarget >= int64(len(bigBuffer)) {
			t.Errorf("unexpected stats while copying big.txt: %#v", stats)
		}
	} else {
		// using local file copy
		if stats.ToSource != 0 || stats.ToTarget != int64(len(bigBuffer)) {
			t.Errorf("unexpected stats while copying big.txt: %#v", stats)
		}
	}

	/*** Test symlinks ***/

	// create symlink
	linkTarget := "file.txt"
	link1Source := &FileInfoStruct{
		path:     info1.RelativePath(),
		fileType: TYPE_SYMLINK,
		mode:     0777,
		modTime:  time.Now(),
	}
	link1, _, err := fs1.CreateSymlink("link", &FileInfoStruct{}, link1Source, linkTarget)
	if err != nil {
		t.Fatal("could not create symlink:", err)
	}
	if !MatchFingerprint(link1, link1Source) {
		t.Error("CreateSymlink did not set all properties")
	}

	// read symlink
	link, err := fs1.ReadSymlink(link1)
	if err != nil {
		t.Error("could not read symlink after create:", err)
	} else if link != linkTarget {
		t.Errorf("ReadSymlink: expected %#v, got %#v", linkTarget, link)
	}

	// try to update symlink (must fail)
	link2 := NewFileInfo(link1.RelativePath(), TYPE_SYMLINK, 0666, 0666, time.Time{}, 0, Hash{})
	_, _, _, err = Update(fs1, fs2, link1, link2)
	if !IsNotExist(err) {
		t.Error("Update link was not 'not found':", err)
	}

	// try to copy 'modified' symlink
	link1Wrong := &FileInfoStruct{link1.RelativePath(), TYPE_SYMLINK, 0666, 0666, time.Time{}, 0, Hash{}}
	_, _, err = Copy(fs1, fs2, link1Wrong, &FileInfoStruct{})
	if !IsChanged(err) {
		t.Errorf("No ErrChanged while copying %s: %s", link2.Name(), err)
	}

	// copy symlink
	link2, _, err = Copy(fs1, fs2, link1, &FileInfoStruct{})
	if err != nil {
		t.Fatal("cannot copy link:", err)
	}
	if !sameInfo(link1, link2) {
		t.Errorf("symlinks do not match after Copy: %s %s", link1, link2)
	}

	// try copying symlink again
	_, _, err = Copy(fs1, fs2, link1, &FileInfoStruct{})
	if !IsExist(err) {
		t.Fatal("overwrite with Copy, error:", err)
	}

	// update symlink
	linkTarget = "file-does-not-exist"
	link1Source = &FileInfoStruct{[]string{"link"}, TYPE_SYMLINK, 0666, 0666, time.Now(), 0, Hash{}}
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
		t.FailNow()
	}

	// try updating a file with UpdateSymlink
	_, _, err = fs1.UpdateSymlink(info1, link1Source, linkTarget)
	if err == nil {
		t.Error("no ErrNoSymlink when trying to update file1.txt with UpdateSymlink")
	} else if !IsNoSymlink(err) {
		t.Error("error while trying to update file.txt", err)
	}

	// try updating with 'changed' source symlink
	_, _, _, err = Update(fs1, fs2, link1Wrong, link2)
	if !IsChanged(err) {
		t.Error("no ErrChanged in Update (source changed), err:", err)
	}

	// try updating with 'changed' target symlink
	_, _, _, err = Update(fs1, fs2, link1, link1Wrong) // link1Wrong can be re-used here
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
	link2, _, _, err = Update(fs1, fs2, link1, link2)
	if err != nil {
		t.Error("cannot update link:", err)
	}
	if !sameInfo(link1, link2) {
		t.Errorf("symlinks do not match after Update: %s %s", link1, link2)
	}

	// try writing to symlink
	_, err = fs1.PutFile(pathSplit("link"), []byte("impossible"))
	if err == nil {
		t.Error("PutFile overwrites link")
	} else if !IsNoRegular(err) {
		t.Error("PutFile error while overwriting link:", err)
	}

	/*** Test directories ***/

	infoDir1, err := fs1.CreateDir("dir", &FileInfoStruct{}, &FileInfoStruct{mode: 0705, hasMode: 0777})
	if err != nil {
		t.Error("could not create directory 1:", err)
	}
	if infoDir1.Mode() != 0705 {
		t.Errorf("mode was not applied to directory (expected %o, got %o)", 0705, infoDir1.Mode())
	}
	if root1 != nil {
		dir1 := getFile(root1, "dir")
		if !sameInfoEntry(t, infoDir1, dir1) {
			t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir1)
		}
		checkFile(t, root1, dir1, 1, 4, "dir")
	}

	infoDir2, err := fs2.CreateDir("dir", &FileInfoStruct{}, infoDir1)
	if err != nil {
		t.Fatal("could not create directory 2:", err)
	}
	if root2 != nil {
		dir2 := getFile(root2, "dir")
		if !sameInfoEntry(t, infoDir2, dir2) {
			t.Errorf("FileInfo of %s does not match the return value of CreateDir", dir2)
		}
		checkFile(t, root2, dir2, 1, 4, "dir")
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
		{fs1, big1, 4},
		{fs2, big2, 4},
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
			list, err := root.List(ListOptions{})
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
			list, err := root.List(ListOptions{})
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

func sameInfoEntry(t Tester, info FileInfo, entry Entry) bool {
	info2, err := entry.FullInfo()
	if err != nil {
		t.Fatalf("could not get info from %s: %s", entry, err)
	}
	return sameFullInfo(info, info2)
}

// sameFullInfo returns true if both FileInfo interfaces are exactly equal for
// all properties.
func sameFullInfo(info1, info2 FileInfo) bool {
	if !info1.Hash().Equal(info2.Hash()) {
		return false
	}
	return sameInfo(info1, info2)
}

// sameInfo returns true if both FileInfo interfaces are exactly equal for all
// properties except for the hash.
func sameInfo(info1, info2 FileInfo) bool {
	if info1.Name() != info2.Name() ||
		info1.Type() != info2.Type() ||
		info1.Mode() != info2.Mode() ||
		info1.HasMode() != info2.HasMode() ||
		!info1.ModTime().Equal(info2.ModTime()) ||
		info1.Size() != info2.Size() {
		return false
	}
	path1 := info1.RelativePath()
	path2 := info2.RelativePath()
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
	l, err := dir.List(ListOptions{})
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
	l, err := dir.List(ListOptions{})
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
	list, err := parent.List(ListOptions{})
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
	equal, err := Equal(file1, file2)
	if err != nil {
		t.Fatalf("could not compare files %s and %s: %s", file1, file2, err)
	}
	return equal
}

// Equal compares two entries, only returning true when this file and possible
// children (for directories) are exactly equal. Do not compare modification time.
func Equal(file1, file2 Entry) (bool, error) {
	if file1.Name() != file2.Name() || file1.Type() != file2.Type() {
		return false, nil
	}
	switch file1.Type() {
	case TYPE_REGULAR:
		hashes := make([]Hash, 2)
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
		return !hashes[0].IsZero() && hashes[0].Equal(hashes[1]), nil

	case TYPE_DIRECTORY:
		list1, err := file1.List(ListOptions{})
		if err != nil {
			return false, err
		}
		list2, err := file2.List(ListOptions{})
		if err != nil {
			return false, err
		}

		if len(list1) != len(list2) {
			return false, nil
		}
		for i := 0; i < len(list1); i++ {
			if equal, err := Equal(list1[i], list2[i]); !equal || err != nil {
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

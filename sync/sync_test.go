// sync_test.go
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

package sync

import (
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/file"
	"github.com/aykevl/dtsync/tree/memory"
	"github.com/aykevl/dtsync/tree/remote"
)

type testCase struct {
	file      string
	action    Action
	contents  interface{}
	fileCount int
}

func newTestRoot(t *testing.T, scheme string) tree.TestTree {
	switch scheme {
	case "memory":
		return memory.NewRoot()
	case "file":
		root, err := file.NewTestRoot()
		if err != nil {
			t.Fatal("could not create test root:", err)
		}
		return root
	case "remote-memory":
		root, err := remote.TestClient()
		if err != nil {
			t.Fatal("could not create test client:", err)
		}
		return root
	default:
		t.Fatal("unknown scheme:", scheme)
		return nil // keep the compiler happy
	}
}

func removeTestRoots(t *testing.T, filesystems ...tree.TestTree) {
	for _, fs := range filesystems {
		if _, ok := fs.(*memory.Entry); ok {
			// Don't try to remove an in-memory filesystem.
			continue
		}
		if _, ok := fs.(*remote.Client); ok {
			// The remote client is also just a memory filesystem.
			continue
		}
		_, err := fs.Remove(&tree.FileInfoStruct{})
		if err != nil {
			t.Error("could not remove test dir:", err)
		}
	}
}

func TestSync(t *testing.T) {
	schemes := []string{"memory", "file", "remote-memory"}
	for i, scheme1 := range schemes {
		for j := i; j < len(schemes); j++ {
			scheme2 := schemes[j]
			t.Logf("Filesystems: %s + %s", scheme1, scheme2)
			syncRoots(t, scheme1, scheme2)
			if testing.Short() {
				return
			}
		}
	}
}

func syncRoots(t *testing.T, scheme1, scheme2 string) {
	fs1 := newTestRoot(t, scheme1)
	fs2 := newTestRoot(t, scheme2)
	defer removeTestRoots(t, fs1, fs2)

	result, err := Scan(fs1, fs2)
	if err != nil {
		t.Fatal("could not start sync:", err)
	}
	err = result.SaveStatus()
	if err != nil {
		t.Error("could not save replica state:", err)
	}
	for _, testFS := range []tree.TestTree{fs1, fs2} {
		fs, ok := testFS.(tree.LocalTree)
		if !ok {
			continue
		}
		list, err := fs.Root().List(tree.ListOptions{})
		if err != nil {
			t.Errorf("could not get list for %s: %s", fs, err)
		}
		if len(list) != 1 {
			t.Errorf("replica state wasn't saved for %s", fs)
		}
	}

	testCases := []testCase{
		// basic copy/update/remove
		{"file1.txt", ACTION_COPY, "f The quick brown fox...", 2},
		{"file1.txt", ACTION_UPDATE, "f The quick brown fox jumps over the lazy dog.", 2},
		{"file1.txt", ACTION_UPDATE, "f The quick brown fox jumps over the lazy dog.", -1},
		{"file1.txt", ACTION_CHMOD, 0606, 2},
		{"file1.txt", ACTION_REMOVE, nil, 1},
		// insert a file before and after an existing file
		{"file1.txt", ACTION_COPY, "f Still jumping...", 2},
		{"file0.txt", ACTION_COPY, "f Before", 3},
		{"file2.txt", ACTION_COPY, "f After", 4},
		{"file0.txt", ACTION_UPDATE, "f -", 4},
		{"file2.txt", ACTION_UPDATE, "f -", 4},
		{"file1.txt", ACTION_UPDATE, "f -", 4},
		{"file0.txt", ACTION_REMOVE, nil, 3},
		{"file2.txt", ACTION_REMOVE, nil, 2},
		{"file1.txt", ACTION_REMOVE, nil, 1},
		// directory
		{"dir", ACTION_COPY, "d ", 2},
		{"dir/file.txt", ACTION_COPY, "f abc", 2},
		{"dir/file.txt", ACTION_UPDATE, "f def", 2},
		{"dir/file.txt", ACTION_CHMOD, 0602, 2},
		{"dir/file.txt", ACTION_REMOVE, nil, 2},
		{"dir", ACTION_CHMOD, 0705, 2},
		{"dir", ACTION_REMOVE, nil, 1},
		// symbolic links
		{"link1", ACTION_COPY, "l dir", 2},
		{"link2", ACTION_COPY, "l file1.txt", 3},
		{"link3", ACTION_COPY, "l file-404", 4},
		{"link1", ACTION_UPDATE, "l file2.txt", 4},
		{"link1", ACTION_UPDATE, "l file2.txt", -1},
		{"link1", ACTION_REMOVE, nil, 3},
		{"link2", ACTION_REMOVE, nil, 2},
		{"link3", ACTION_REMOVE, nil, 1},
	}

	complexTestCases := []struct {
		jobName      string
		jobAction    Action
		jobFileCount int
		fileAction   func(fs tree.TestTree) error
	}{
		{"dir", ACTION_COPY, 2, func(fs tree.TestTree) error {
			source := tree.NewFileInfo(nil, tree.TYPE_DIRECTORY, 0755, 0777, time.Now(), 0, tree.Hash{})
			_, err := fs.CreateDir("dir", &tree.FileInfoStruct{}, source)
			if err != nil {
				return err
			}
			_, err = fs.PutFile([]string{"dir", "file.txt"}, []byte("abc"))
			return err
		}},
		{"dir", ACTION_REMOVE, 1, func(fs tree.TestTree) error {
			info, err := fs.ReadInfo([]string{"dir"})
			if err != nil {
				t.Fatalf("could not get info for dir: %s", err)
			}
			_, err = fs.Remove(info)
			return err
		}},
	}

	fs1 = newTestRoot(t, scheme1)
	fs2 = newTestRoot(t, scheme2)
	fsCheck := newTestRoot(t, scheme2)
	defer removeTestRoots(t, fs1, fs2, fsCheck)
	fsNames := []struct {
		name string
		fs   tree.TestTree
	}{
		{"fs1", fs1},
		{"fs2", fs2},
		{"fsCheck", fsCheck},
	}
	for _, fs := range fsNames {
		fs.fs.PutFile([]string{dtdiff.STATUS_FILE}, []byte(`Content-Type: text/tab-separated-values; charset=utf-8
Identity: `+fs.name+`
Generation: 1

path	fingerprint	revision
`))
	}

	for _, scanTwice := range []bool{true, false} {
		for _, swapped := range []bool{false, true} {
			for _, fsCheckWith := range []tree.TestTree{fs2, fs1} {
				for _, tc := range testCases {
					t.Log("Action:", tc.action, tc.file)
					applyTestCase(t, fs1, tc)
					runTestCase(t, fs1, fs2, fsCheck, fsCheckWith, swapped, scanTwice, tc)
				}
				for _, ctc := range complexTestCases {
					err := ctc.fileAction(fs1)
					if err != nil {
						t.Fatal("could not apply complex test case:", err)
					}
					tc := testCase{ctc.jobName, ctc.jobAction, nil, ctc.jobFileCount}
					runTestCase(t, fs1, fs2, fsCheck, fsCheckWith, swapped, scanTwice, tc)
				}
			}
		}
	}

	for _, schemes := range [][]string{{scheme1, scheme2}, {scheme2, scheme1}} {
		fs1 = newTestRoot(t, schemes[0])
		fs2 = newTestRoot(t, schemes[1])
		defer removeTestRoots(t, fs1, fs2)
		runPatternTest(t, fs1, fs2)
		if schemes[0] == schemes[1] {
			break
		}
	}

	if t.Failed() {
		// Otherwise an error is printed on the next check.
		t.FailNow()
	}
}

func runPatternTest(t *testing.T, fs1, fs2 tree.TestTree) {
	for _, fs := range []tree.TestTree{fs1, fs2} {
		source := tree.NewFileInfo(nil, tree.TYPE_DIRECTORY, 0755, 0777, time.Now(), 0, tree.Hash{})
		_, err := fs.CreateDir("dir", &tree.FileInfoStruct{}, source)
		assert(err)
	}

	// Set up filesystem.
	files := []testCase{
		{"dir/link1-1", ACTION_COPY, "l ../link1-1", 0},
		{"dir/link1-2", ACTION_COPY, "l ../link1-2", 0},
		{"dir/link1-3", ACTION_COPY, "l nofile", 0},
		{"file", ACTION_COPY, "f abc", 0},
		{"ignore1", ACTION_COPY, "f x", 0},
		{"ignore1-not", ACTION_COPY, "f x", 0},
		{"ignore2", ACTION_COPY, "f x", 0},
		{"ignore2-not", ACTION_COPY, "f x", 0},
		{"link1-1", ACTION_COPY, "l file", 0},      // found, direct
		{"link1-2", ACTION_COPY, "l nofile", 0},    // not found, direct
		{"link1-3", ACTION_COPY, "l link1-1", 0},   // found, indirect
		{"link1-4", ACTION_COPY, "l link1-2", 0},   // not found, indirect
		{"link1-5", ACTION_COPY, "l dir", 0},       // link to directory
		{"link1-6", ACTION_COPY, "l ././file", 0},  // multiple levels of current dir
		{"link2-1", ACTION_COPY, "l file", 0},      // found, direct
		{"link2-2", ACTION_COPY, "l nofile", 0},    // not found, direct
		{"link2-c1", ACTION_COPY, "l link2-c2", 0}, // circle
		{"link2-c2", ACTION_COPY, "l link2-c1", 0}, // circle
	}

	effects := []struct {
		direction int
		name      string
	}{
		{1, "dir/link1-1"},
		{0, "dir/link1-2"},
		{0, "dir/link1-3"},
		{1, "file"},
		{1, "ignore1-not"},
		{1, "ignore2-not"},
		{1, "link1-1"},
		{0, "link1-2"},
		{1, "link1-3"},
		{0, "link1-4"},
		{1, "link1-5"},
		{1, "link1-6"},
		{1, "link2-1"},
		{0, "link2-2"},
		{0, "link2-c1"},
		{0, "link2-c2"},
	}

	for _, tc := range files {
		applyTestCase(t, fs1, tc)
	}

	result, err := Scan(fs1, fs2)
	if err != nil {
		t.Fatal("could not scan in pattern test:", err)
	}

	if len(result.Jobs()) != len(files) {
		t.Errorf("number of jobs is unexpected within pattern test (expected %d, got %d)", len(files), len(result.Jobs()))
	}

	for _, job := range result.Jobs() {
		if job.Direction() != 1 {
			t.Errorf("job direction %s is not left-to-right but %d", job, job.Direction())
		}
	}

	fs1.PutFile([]string{dtdiff.STATUS_FILE}, []byte(`Content-Type: text/tab-separated-values; charset=utf-8
Identity: fs1
Generation: 1
Option-Follow: link1-*
Option-Exclude: ignore1*
Option-Include: ignore1-not*

path	fingerprint	revision
`))
	fs2.PutFile([]string{dtdiff.STATUS_FILE}, []byte(`Content-Type: text/tab-separated-values; charset=utf-8
Identity: fs2
Generation: 1
Option-Follow: link2-*
Option-Exclude: ignore2*
Option-Include: ignore2-not*

path	fingerprint	revision
`))

	result, err = Scan(fs1, fs2)
	if err != nil {
		t.Fatal("could not scan again in pattern test:", err)
	}

	if len(result.Jobs()) != len(effects) {
		t.Errorf("number of jobs is unexpected after pattern tests (expected %d, got %d)", len(effects), len(result.Jobs()))
	}

	for i, effect := range effects {
		if i >= len(result.Jobs()) {
			break
		}
		job := result.Jobs()[i]
		if job.Path() != effect.name {
			t.Errorf("job #%d %s should have name %s", i, job, effect.name)
			continue
		}
		if job.Action() != ACTION_COPY {
			t.Errorf("job #%d %s should be copy", i, job)
		}
		if job.Direction() != effect.direction {
			t.Errorf("job #%d %s direction is not %d but %d", i, job, effect.direction, job.Direction())
		}
	}
}

func applyTestCase(t *testing.T, fs tree.TestTree, tc testCase) {
	parts := strings.Split(tc.file, "/")
	name := parts[len(parts)-1]

	var err error
	switch tc.action {
	case ACTION_COPY: // add
		action := tc.contents.(string)[0]
		contents := tc.contents.(string)[2:]
		switch action {
		case 'd':
			parent := tree.NewFileInfo(parts[:len(parts)-1], tree.TYPE_DIRECTORY, 0755, 0777, time.Time{}, 0, tree.Hash{})
			source := tree.NewFileInfo(nil, tree.TYPE_DIRECTORY, 0755, 0777, time.Now(), 0, tree.Hash{})
			_, err = fs.CreateDir(name, parent, source)
		case 'f':
			_, err = fs.PutFile(parts, []byte(contents))
		case 'l':
			parent := tree.NewFileInfo(parts[:len(parts)-1], tree.TYPE_DIRECTORY, 0755, 0777, time.Time{}, 0, tree.Hash{})
			source := tree.NewFileInfo(parts, tree.TYPE_SYMLINK, 0777, 0777, time.Now(), 0, tree.Hash{})
			_, _, err = fs.CreateSymlink(name, parent, source, contents)
		default:
			panic("unknown copy action: " + tc.contents.(string))
		}
	case ACTION_UPDATE:
		action := tc.contents.(string)[0]
		contents := tc.contents.(string)[2:]
		switch action {
		case 'f':
			_, err = fs.PutFile(parts, []byte(contents))
		case 'l':
			var file tree.FileInfo
			file, err = fs.ReadInfo(strings.Split(tc.file, "/"))
			assert(err)
			source := tree.NewFileInfo(parts, tree.TYPE_SYMLINK, 0644, 0777, time.Now(), 0, tree.Hash{})
			_, _, err = fs.UpdateSymlink(file, source, contents)
		default:
			panic("unknown update action: " + tc.contents.(string))
		}
	case ACTION_CHMOD:
		var file tree.FileInfo
		file, err = fs.ReadInfo(strings.Split(tc.file, "/"))
		source := tree.NewFileInfo(parts, file.Type(), tree.Mode(tc.contents.(int)), 0777, time.Now(), 0, tree.Hash{})
		assert(err)
		_, err = fs.Chmod(file, source)
	case ACTION_REMOVE:
		info, err := fs.ReadInfo(strings.Split(tc.file, "/"))
		assert(err)
		_, err = fs.Remove(info)
		if err != nil {
			t.Fatalf("could not remove file %s: %s: %s", tc.file, info, err)
		}
	default:
		t.Fatalf("unknown action: %d", tc.action)
	}
	if err != nil {
		t.Fatalf("could not %s file %s: %s", tc.action, tc.file, err)
	}
}

func runTestCase(t *testing.T, fs1, fs2, fsCheck, fsCheckWith tree.TestTree, swap, scanTwice bool, tc testCase) {
	failedBefore := t.Failed()
	statusBefore := readStatuses(t, fs1, fs2)

	if swap {
		runTestCaseSync(t, &tc, fs2, fs1, -1, scanTwice, false)
	} else {
		runTestCaseSync(t, &tc, fs1, fs2, 1, scanTwice, false)
	}

	if t.Failed() != failedBefore {
		printStatus(t, "before", statusBefore)
		printStatus(t, "after", readStatuses(t, fs1, fs2))
		t.FailNow()
	}

	statusBefore = readStatuses(t, fsCheck, fsCheckWith)
	runTestCaseSync(t, &tc, fsCheck, fsCheckWith, -1, scanTwice, true)
	if t.Failed() {
		printStatus(t, "before (check)", statusBefore)
		printStatus(t, "after (check)", readStatuses(t, fsCheck, fsCheckWith))
		t.FailNow()
	}
}

func runTestCaseSync(t *testing.T, tc *testCase, fs1, fs2 tree.TestTree, jobDirection int, scanTwice, secondSync bool) {
	result := runTestCaseScan(t, tc, fs1, fs2, jobDirection)
	if t.Failed() || result == nil {
		if result != nil {
			assert(result.SaveStatus())
		}
		return
	}
	replica1a := result.rs.Get(0)
	replica2a := result.rs.Get(1)

	statusBefore := readStatuses(t, fs1, fs2)

	if jobDirection == 1 {
		//t.Logf("sync %s → %s", replica1a, replica2a)
	} else {
		//t.Logf("sync %s ← %s", replica1a, replica2a)
	}

	// False if one (or both) of the trees is connected over a network.
	localSync := true
	for _, fs := range []tree.TestTree{fs1, fs2} {
		if _, ok := fs.(tree.LocalTree); !ok {
			localSync = false
		}
	}

	// Changed() works a bit different over a network connection.
	if localSync {
		if jobDirection == 1 {
			if !replica1a.Changed() {
				t.Errorf("replica 1 %s was not changed while test case was applied to the left side", replica1a)
			}
			if replica2a.Changed() {
				t.Errorf("replica 2 %s was changed while test case was applied to the left side", replica2a)
			}
		} else {
			if replica1a.Changed() {
				t.Errorf("replica 1 %s was changed while test case was applied to the right side", replica1a)
			}
			if replica2a.Changed() == secondSync {
				t.Errorf("replica 2 %s was changed? (=%v) while test case was applied to the right side", replica2a, replica2a.Changed())
			}
		}
	}

	if scanTwice {
		// Save, and scan again, to test whether there were any changes.
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}

		statusBefore := readStatuses(t, fs1, fs2)

		result = runTestCaseScan(t, tc, fs1, fs2, jobDirection)
		if result == nil {
			return
		}
		replica1b := result.rs.Get(0)
		replica2b := result.rs.Get(1)
		if replica1b.Changed() {
			t.Errorf("%s got updated to %s on second scan", replica1a, replica1b)
		}
		if replica2b.Changed() {
			t.Errorf("%s got updated to %s on second scan", replica2a, replica2b)
		}
		if t.Failed() {
			if err := result.SaveStatus(); err != nil {
				t.Errorf("could not save status: %s", err)
			}
			printStatus(t, "before (second scan)", statusBefore)
			printStatus(t, "after (second scan)", readStatuses(t, fs1, fs2))
			t.FailNow()
		}
	}

	if stats, err := result.SyncAll(); err != nil {
		t.Errorf("could not sync all: %s", err)
		return
	} else if stats.CountTotal != 1 {
		t.Errorf("CountTotal expected to be 1, got %d", stats.CountTotal)
	} else if stats.CountError != 0 {
		t.Errorf("CountTotal expected to be 0, got %d", stats.CountError)
	}
	if !fsEqual(fs1, fs2) {
		t.Errorf("directory trees are not equal after: %s %s", tc.action, tc.file)
	} else {
		// both replicas must include all changes from the other
		replica1 := result.rs.Get(0)
		replica2 := result.rs.Get(1)
		if !replica1.Root().Includes(replica2.Root()) {
			t.Errorf("%s does not include all changes from %s", replica1, replica2)
		}
		if !replica2.Root().Includes(replica1.Root()) {
			t.Errorf("%s does not include all changes from %s", replica2, replica1)
		}

		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}
		if t.Failed() {
			printStatus(t, "before (not changed)", statusBefore)
			printStatus(t, "after (not changed)", readStatuses(t, fs1, fs2))
			t.FailNow()
		}

		result, err := Scan(fs1, fs2)
		if err != nil {
			t.Errorf("could not scan the two identical trees %s and %s: %s", fs1, fs2, err)
		} else {
			statusBefore := readStatuses(t, fs1, fs2)
			replica1 := result.rs.Get(0)
			replica2 := result.rs.Get(1)
			fail := false
			if ch1, ch2 := replica1.Changed(), replica2.Changed(); ch1 || ch2 {
				fail = true
				t.Errorf("one of the replicas was changed with identical trees: (%s: %v, %s: %v)", replica1, ch1, replica2, ch2)
				if err := result.SaveStatus(); err != nil {
					t.Errorf("could not save status: %s", err)
				}
			}
			if len(result.jobs) != 0 {
				fail = true
				t.Errorf("scan returned %d job %v when syncing two identical trees", len(result.jobs), result.jobs)
			}
			if fail {
				printStatus(t, "before (changed)", statusBefore)
				printStatus(t, "after (changed)", readStatuses(t, fs1, fs2))
				t.FailNow()
			}
		}
	}

	if localSync && tc.fileCount >= 0 {
		list1, err := fs1.(tree.LocalTree).Root().List(tree.ListOptions{})
		if err != nil {
			t.Errorf("could not get list for %s: %s", fs1, err)
		}
		list2, err := fs2.(tree.LocalTree).Root().List(tree.ListOptions{})
		if err != nil {
			t.Errorf("could not get list for %s: %s", fs2, err)
		}
		if len(list1) != tc.fileCount || len(list2) != tc.fileCount {
			t.Errorf("unexpected number of files after first sync (expected %d): fs1=%d fs2=%d", tc.fileCount, len(list1), len(list2))
		}
	}
}

func runTestCaseScan(t *testing.T, tc *testCase, fs1, fs2 tree.TestTree, jobDirection int) *Result {
	result, err := Scan(fs1, fs2)
	if err != nil {
		t.Errorf("could not sync after: %s %s: %s", tc.action, tc.file, err)
		return nil
	}

	if tc.fileCount < 0 {
		if len(result.jobs) != 0 {
			t.Errorf("list of jobs is expected to be 0, but actually is %d: %v", len(result.jobs), result.jobs)
		}
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}
		return nil
	} else if len(result.jobs) != 1 {
		t.Errorf("list of jobs is expected to be 1, but actually is %d: %v", len(result.jobs), result.jobs)
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}
	} else {
		job := result.jobs[0]
		if job.direction != jobDirection {
			t.Errorf("expected direction for %s to be %d, not %d", job, jobDirection, job.direction)
		}
		parts := strings.Split(tc.file, "/")
		if job.action != tc.action || job.Name() != parts[len(parts)-1] {
			t.Errorf("expected a %s job for file %s, but got %s", tc.action, tc.file, job)
		}
	}

	return result
}

func getEntriesExcept(parent tree.Entry, except string) []tree.Entry {
	list, err := parent.List(tree.ListOptions{})
	assert(err)

	listEntries := make([]tree.Entry, 0, len(list)-1)
	for _, entry := range list {
		if entry.Name() == except {
			continue
		}
		listEntries = append(listEntries, entry)
	}
	return listEntries
}

func fsEqual(fs1, fs2 tree.TestTree) bool {
	// If one isn't local, don't continue the test (assume it's okay).
	localFS1, ok := fs1.(tree.LocalTree)
	if !ok {
		return true
	}
	localFS2, ok := fs2.(tree.LocalTree)
	if !ok {
		return true
	}

	list1 := getEntriesExcept(localFS1.Root(), dtdiff.STATUS_FILE)
	list2 := getEntriesExcept(localFS2.Root(), dtdiff.STATUS_FILE)

	if len(list1) != len(list2) {
		return false
	}

	for i, _ := range list1 {
		equal, err := tree.Equal(list1[i], list2[i])
		if err != nil {
			// IO errors shouldn't happen while testing
			panic(err)
		}
		if !equal {
			return false
		}
	}
	return true
}

// readStatuses returns the contents of the status files in the provided
// directories.
func readStatuses(t *testing.T, roots ...tree.TestTree) [][]byte {
	statusData := make([][]byte, len(roots))
	for i, fs := range roots {
		if fs == nil {
			continue
		}
		statusFile, err := fs.GetFile(dtdiff.STATUS_FILE)
		if err != nil {
			if tree.IsNotExist(err) {
				continue
			}
			t.Fatal("could not get status file:", err)
		}
		status, err := ioutil.ReadAll(statusFile)
		statusData[i] = status
		assert(err)
	}
	return statusData
}

// printStatus dumps status file to the testing console.
func printStatus(t *testing.T, moment string, statuses [][]byte) {
	for i, data := range statuses {
		if data == nil {
			continue
		}
		t.Logf("Status %s, side %d\n%s", moment, i+1, string(data))
	}
}

// Assert panicks when the error is non-nil.
// This is a convenience function for situations that should be impossible to
// happen.
func assert(err error) {
	if err != nil {
		panic("assert: " + err.Error())
	}
}

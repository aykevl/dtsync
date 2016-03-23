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
	contents  []byte
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
		list, err := fs.Root().List()
		if err != nil {
			t.Errorf("could not get list for %s: %s", fs, err)
		}
		if len(list) != 1 {
			t.Errorf("replica state wasn't saved for %s", fs)
		}
	}

	testCases := []testCase{
		// basic copy/update/remove
		{"file1.txt", ACTION_COPY, []byte("The quick brown fox..."), 2},
		{"file1.txt", ACTION_UPDATE, []byte("The quick brown fox jumps over the lazy dog."), 2},
		{"file1.txt", ACTION_REMOVE, nil, 1},
		// insert a file before and after an existing file
		{"file1.txt", ACTION_COPY, []byte("Still jumping..."), 2},
		{"file0.txt", ACTION_COPY, []byte("Before"), 3},
		{"file2.txt", ACTION_COPY, []byte("After"), 4},
		{"file0.txt", ACTION_UPDATE, []byte("-"), 4},
		{"file2.txt", ACTION_UPDATE, []byte("-"), 4},
		{"file1.txt", ACTION_UPDATE, []byte("-"), 4},
		{"file0.txt", ACTION_REMOVE, nil, 3},
		{"file2.txt", ACTION_REMOVE, nil, 2},
		{"file1.txt", ACTION_REMOVE, nil, 1},
		// directory
		{"dir", ACTION_COPY, nil, 2},
		{"dir/file.txt", ACTION_COPY, []byte("abc"), 2},
		{"dir/file.txt", ACTION_UPDATE, []byte("def"), 2},
		{"dir/file.txt", ACTION_REMOVE, nil, 2},
		{"dir", ACTION_REMOVE, nil, 1},
	}

	complexTestCases := []struct {
		jobName      string
		jobAction    Action
		jobFileCount int
		fileAction   func(fs tree.TestTree) error
	}{
		{"dir", ACTION_COPY, 2, func(fs tree.TestTree) error {
			_, err := fs.CreateDir("dir", &tree.FileInfoStruct{})
			if err != nil {
				return err
			}
			_, err = fs.AddRegular([]string{"dir", "file.txt"}, []byte("abc"))
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
		fs.fs.AddRegular([]string{dtdiff.STATUS_FILE}, []byte(`Content-Type: text/tab-separated-values; charset=utf-8
Identity: `+fs.name+`
Generation: 1

path	fingerprint	hash	replica	generation
`))
	}

	for _, scanTwice := range []bool{true, false} {
		for _, swapped := range []bool{false, true} {
			for _, fsCheckWith := range []tree.TestTree{fs2, fs1} {
				for _, tc := range testCases {
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
}

func applyTestCase(t *testing.T, fs tree.TestTree, tc testCase) {
	t.Log("Action:", tc.action, tc.file)

	parts := strings.Split(tc.file, "/")
	name := parts[len(parts)-1]

	var err error
	switch tc.action {
	case ACTION_COPY: // add
		if tc.contents != nil {
			_, err = fs.AddRegular(parts, tc.contents)
		} else {
			_, err = fs.CreateDir(name, tree.NewFileInfo(parts[:len(parts)-1], tree.TYPE_DIRECTORY, time.Time{}, 0, nil))
		}
	case ACTION_UPDATE:
		_, err = fs.SetContents(parts, tc.contents)
		if err != nil {
			t.Fatalf("could not set file contents to file %s: %s", tc.file, err)
		}
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
	if t.Failed() {
		return
	}
	replica1a := result.rs.Get(0)
	replica2a := result.rs.Get(1)

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
				t.Errorf("replica 2 %s was changed? (=%s) while test case was applied to the right side", replica2a, replica2a.Changed())
				panic("abc")
			}
		}
	}

	statusBefore := readStatuses(t, fs1, fs2)

	if scanTwice {
		// Save, and scan again, to test whether there were any changes.
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}

		result = runTestCaseScan(t, tc, fs1, fs2, jobDirection)
		replica1b := result.rs.Get(0)
		replica2b := result.rs.Get(1)
		if replica1b.Changed() {
			t.Errorf("%s got updated to %s on second scan", replica1a, replica1b)
		}
		if replica2b.Changed() {
			t.Errorf("%s got updated to %s on second scan", replica2a, replica2b)
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

	if localSync {
		list1, err := fs1.(tree.LocalTree).Root().List()
		if err != nil {
			t.Errorf("could not get list for %s: %s", fs1, err)
		}
		list2, err := fs2.(tree.LocalTree).Root().List()
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

	if len(result.jobs) != 1 {
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
	list, err := parent.List()
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
		equal, err := tree.Equal(list1[i], list2[i], false)
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

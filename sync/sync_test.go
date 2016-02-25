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

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/file"
	"github.com/aykevl/dtsync/tree/memory"
)

type testCase struct {
	file      string
	action    Action
	contents  []byte
	fileCount int
}

func NewTestRoot(t *testing.T) tree.TestEntry {
	if testing.Short() {
		return memory.NewRoot()
	} else {
		root, err := file.NewTestRoot()
		if err != nil {
			t.Fatal("could not create test root:", err)
		}
		return root
	}
}

func TestSync(t *testing.T) {
	fs1 := NewTestRoot(t)
	fs2 := NewTestRoot(t)

	result, err := Scan(fs1, fs2)
	if err != nil {
		t.Fatal("could not start sync:", err)
	}
	result.MarkFullySynced()
	err = result.SaveStatus()
	if err != nil {
		t.Error("could not save replica state:", err)
	}
	for _, fs := range []tree.TestEntry{fs1, fs2} {
		list, err := fs.List()
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
		fileAction   func(fs tree.TestEntry) error
	}{
		{"dir", ACTION_COPY, 2, func(fs tree.TestEntry) error {
			dir, err := fs.CreateDir("dir")
			if err != nil {
				return err
			}
			_, err = dir.(tree.TestEntry).AddRegular("file.txt", []byte("abc"))
			return err
		}},
		{"dir", ACTION_REMOVE, 1, func(fs tree.TestEntry) error {
			return getFile(fs, "dir").Remove()
		}},
	}

	fs1 = NewTestRoot(t)
	fs2 = NewTestRoot(t)
	fsCheck := NewTestRoot(t)
	fsNames := []struct {
		name string
		fs   tree.TestEntry
	}{
		{"fs1", fs1},
		{"fs2", fs2},
		{"fsCheck", fsCheck},
	}
	for _, fs := range fsNames {
		fs.fs.AddRegular(STATUS_FILE, []byte(`Content-Type: text/tab-separated-values
Identity: `+fs.name+`
Generation: 1

path	fingerprint	replica	generation
`))
	}

	for _, scanTwice := range []bool{true, false} {
		for _, swapped := range []bool{false, true} {
			for _, fsCheckWith := range []tree.TestEntry{fs2, fs1} {
				for _, tc := range testCases {
					applyTestCase(t, fs1, tc)
					runTestCase(t, fs1, fs2, fsCheck, fsCheckWith, swapped, scanTwice, tc)
				}
				for _, ctc := range complexTestCases {
					ctc.fileAction(fs1)
					tc := testCase{ctc.jobName, ctc.jobAction, nil, ctc.jobFileCount}
					runTestCase(t, fs1, fs2, fsCheck, fsCheckWith, swapped, scanTwice, tc)
				}
			}
		}
	}
}

func applyTestCase(t *testing.T, fs tree.TestEntry, tc testCase) {
	parts := strings.Split(tc.file, "/")
	parent := fs
	for i := 0; i < len(parts)-1; i++ {
		parent = getFile(parent, parts[i])
	}
	name := parts[len(parts)-1]

	var err error
	switch tc.action {
	case ACTION_COPY: // add
		if tc.contents != nil {
			_, err = parent.AddRegular(name, tc.contents)
		} else {
			_, err = parent.CreateDir(name)
		}
	case ACTION_UPDATE:
		child := getFile(parent, name)
		if child == nil {
			t.Fatalf("could not find file %s to update", tc.file)
		}
		err = child.SetContents(tc.contents)
		if err != nil {
			t.Fatalf("could not set file contents to file %s: %s", child, err)
		}
	case ACTION_REMOVE:
		child := getFile(parent, name)
		if child == nil {
			t.Fatalf("could not find file %s to remove", tc.file)
		}
		err = child.Remove()
	default:
		t.Fatalf("unknown action: %d", tc.action)
	}
	if err != nil {
		t.Fatalf("could not %s file %s: %s", tc.action, tc.file, err)
	}
}

func runTestCase(t *testing.T, fs1, fs2, fsCheck, fsCheckWith tree.TestEntry, swap, scanTwice bool, tc testCase) {
	t.Logf("Action: %s %s", tc.action, tc.file)
	failedBefore := t.Failed()
	statusBefore := readStatuses(t, fs1, fs2)

	if swap {
		runTestCaseSync(t, &tc, fs2, fs1, -1, scanTwice)
	} else {
		runTestCaseSync(t, &tc, fs1, fs2, 1, scanTwice)
	}

	if t.Failed() != failedBefore {
		printStatus(t, "before", statusBefore)
		printStatus(t, "after", readStatuses(t, fs1, fs2))
		t.FailNow()
	}

	statusBefore = readStatuses(t, fsCheck, fsCheckWith)
	runTestCaseSync(t, &tc, fsCheck, fsCheckWith, -1, scanTwice)
	if t.Failed() {
		printStatus(t, "before (check)", statusBefore)
		printStatus(t, "after (check)", readStatuses(t, fs1, fs2))
		t.FailNow()
	}
}

func runTestCaseSync(t *testing.T, tc *testCase, fs1, fs2 tree.TestEntry, jobDirection int, scanTwice bool) {
	result := runTestCaseScan(t, tc, fs1, fs2, jobDirection)
	if result == nil {
		return
	}

	if scanTwice {
		// Save, and scan again, to test whether there were any changes.
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}
		replica1a := result.rs.Get(0)
		replica2a := result.rs.Get(1)

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

	if err := result.SyncAll(); err != nil {
		t.Errorf("could not sync all: %s", err)
		return
	}
	if !fsEqual(fs1, fs2) {
		t.Errorf("directory trees are not equal after: %s %s", tc.action, tc.file)
	} else {
		result.MarkFullySynced()

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

		result, err := Scan(fs1, fs2)
		if err != nil {
			t.Errorf("could not scan the two identical trees %s and %s: %s", fs1, fs2, err)
		} else {
			statusBefore := readStatuses(t, fs1, fs2)
			replica1 := result.rs.Get(0)
			replica2 := result.rs.Get(1)
			if ch1, ch2 := replica1.Changed(), replica2.Changed(); ch1 || ch2 {
				t.Errorf("one of the replicas was changed with identical trees: (%s: %v, %s: %v)", replica1, ch1, replica2, ch2)
				if err := result.SaveStatus(); err != nil {
					t.Errorf("could not save status: %s", err)
				}
			}
			if len(result.jobs) != 0 {
				t.Errorf("scan returned %d job %v when syncing two identical trees", len(result.jobs), result.jobs)
			}
			if t.Failed() {
				printStatus(t, "before (changed)", statusBefore)
				printStatus(t, "after (changed)", readStatuses(t, fs1, fs2))
				t.FailNow()
			}
		}
	}

	list1, err := fs1.List()
	if err != nil {
		t.Errorf("could not get list for %s: %s", fs1, err)
	}
	list2, err := fs2.List()
	if err != nil {
		t.Errorf("could not get list for %s: %s", fs2, err)
	}
	if len(list1) != tc.fileCount || len(list2) != tc.fileCount {
		t.Errorf("unexpected number of files after first sync (expected %d): fs1=%d fs2=%d", tc.fileCount, len(list1), len(list2))
	}
}

func runTestCaseScan(t *testing.T, tc *testCase, fs1, fs2 tree.TestEntry, jobDirection int) *Result {
	result, err := Scan(fs1, fs2)
	if err != nil {
		t.Errorf("could not sync after: %s %s: %s", tc.action, tc.file, err)
		return nil
	}

	if len(result.jobs) != 1 {
		t.Errorf("list of jobs is expected to be 1, but actually is %d %v", len(result.jobs), result.jobs)
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

func getFile(parent tree.TestEntry, name string) tree.TestEntry {
	list, err := parent.List()
	assert(err)
	for _, child := range list {
		if child.Name() == name {
			return child.(tree.TestEntry)
		}
	}
	return nil
}

func getEntriesExcept(parent tree.TestEntry, except string) []tree.TestEntry {
	list, err := parent.List()
	assert(err)

	listEntries := make([]tree.TestEntry, 0, len(list)-1)
	for _, entry := range list {
		if entry.Name() == except {
			continue
		}
		listEntries = append(listEntries, entry.(tree.TestEntry))
	}
	return listEntries
}

func fsEqual(fs1, fs2 tree.TestEntry) bool {
	list1 := getEntriesExcept(fs1, STATUS_FILE)
	list2 := getEntriesExcept(fs2, STATUS_FILE)

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
func readStatuses(t *testing.T, roots ...tree.TestEntry) [][]byte {
	statusData := make([][]byte, len(roots))
	for i, fs := range roots {
		if fs == nil {
			continue
		}
		statusFile, err := fs.GetFile(STATUS_FILE)
		if err != nil {
			if err == tree.ErrNotFound {
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

func TestLeastName(t *testing.T) {
	testCases := []struct {
		input  []string
		output string
	}{
		{[]string{""}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a"},
		{[]string{"b", "a"}, "a"},
		{[]string{"a", ""}, "a"},
		{[]string{"", "a"}, "a"},
		{[]string{"a", "", "b"}, "a"},
		{[]string{"", "a", "b"}, "a"},
		{[]string{"", "b", "a"}, "a"},
		{[]string{"a", "", "b"}, "a"},
		{[]string{"aba", "abc"}, "aba"},
		{[]string{"a", "aba"}, "a"},
	}
	for _, tc := range testCases {
		name := leastName(tc.input)
		if name != tc.output {
			t.Errorf("expected %#v but got %#v for input %#v", tc.output, name, tc.input)
		}
	}
}

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
	"testing"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/memory"
)

type testCase struct {
	file      string
	action    Action
	contents  []byte
	fileCount int64
}

func TestSync(t *testing.T) {
	fs1 := memory.NewRoot()
	fs2 := memory.NewRoot()

	result, err := Sync(fs1, fs2)
	if err != nil {
		t.Fatal("could not start sync:", err)
	}
	result.MarkFullySynced()
	err = result.SaveStatus()
	if err != nil {
		t.Error("could not save replica state:", err)
	}
	if fs1.Size() != 1 {
		t.Error("replica state wasn't saved for fs1")
	}
	if fs2.Size() != 1 {
		t.Error("replica state wasn't saved for fs2")
	}

	testCases := []testCase{
		{"file1.txt", ACTION_COPY, []byte("The quick brown fox..."), 2},
		{"file1.txt", ACTION_UPDATE, []byte("The quick brown fox jumps over the lazy dog."), 2},
		{"file1.txt", ACTION_REMOVE, nil, 1},
	}

	runTests(t, fs1, fs2, false, testCases)
	runTests(t, fs1, fs2, true, testCases)
	runTests(t, fs2, fs1, false, testCases)
	runTests(t, fs2, fs1, true, testCases)
}

func runTests(t *testing.T, fs1, fs2 *memory.Entry, swap bool, cases []testCase) {
	for _, tc := range cases {
		statusBefore := readStatuses(t, fs1, fs2)

		var err error
		switch tc.action {
		case ACTION_COPY: // add
			_, err = fs1.AddRegular(tc.file, tc.contents)
		case ACTION_UPDATE:
			child := getFile(fs1, tc.file)
			if child == nil {
				t.Fatalf("could not find file %s to update", tc.file)
			}
			child.SetContents(tc.contents)
		case ACTION_REMOVE:
			child := getFile(fs1, tc.file)
			if child == nil {
				t.Fatalf("could not find file %s to remove", tc.file)
			}
			err = fs1.Remove(child)
		default:
			t.Fatalf("unknown action: %d", tc.action)
		}
		if err != nil {
			t.Fatalf("could not %s file %s: %s", tc.action, tc.file, err)
		}

		if swap {
			runTestCase(t, &tc, fs2, fs1)
		} else {
			runTestCase(t, &tc, fs1, fs2)
		}

		if t.Failed() {
			t.Logf("Action: %s %s", tc.action, tc.file)
			t.Logf("Status before, side 1\n%s", string(statusBefore[0]))
			t.Logf("Status before, side 2\n%s", string(statusBefore[1]))
			statusAfter := readStatuses(t, fs1, fs2)
			t.Logf("Status after, side 1\n%s", string(statusAfter[0]))
			t.Logf("Status after, side 2\n%s", string(statusAfter[1]))
		}
		if t.Failed() {
			t.FailNow()
		}
	}
}

func runTestCase(t *testing.T, tc *testCase, fs1, fs2 *memory.Entry) {
	result, err := Sync(fs1, fs2)
	if err != nil {
		t.Errorf("could not sync after: %s %s: %s", tc.action, tc.file, err)
		return
	}

	if len(result.jobs) != 1 {
		t.Errorf("list of jobs is expected to be 1, but actually is %d", len(result.jobs))
	} else {
		if result.jobs[0].action != tc.action || result.jobs[0].file1.Name() != tc.file {
			t.Errorf("expected a %s job for file %s, but got %s", tc.action, tc.file, result.jobs[0])
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
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}
	}

	if fs1.Size() != tc.fileCount || fs2.Size() != tc.fileCount {
		t.Errorf("unexpected number of files after first sync (expected %d): fs1=%d fs2=%d", tc.fileCount, fs1.Size(), fs2.Size())
	}
}

func getFile(parent *memory.Entry, name string) *memory.Entry {
	list, err := parent.List()
	assert(err)
	for _, child := range list {
		if child.Name() == name {
			return child.(*memory.Entry)
		}
	}
	return nil
}

func getEntriesExcept(parent *memory.Entry, except string) []*memory.Entry {
	list, err := parent.List()
	assert(err)

	listEntries := make([]*memory.Entry, 0, len(list)-1)
	for _, entry := range list {
		if entry.Name() == except {
			continue
		}
		listEntries = append(listEntries, entry.(*memory.Entry))
	}
	return listEntries
}

func fsEqual(fs1, fs2 *memory.Entry) bool {
	list1 := getEntriesExcept(fs1, STATUS_FILE)
	list2 := getEntriesExcept(fs2, STATUS_FILE)

	if len(list1) != len(list2) {
		return false
	}

	for i, _ := range list1 {
		file1 := list1[i]
		file2 := list2[i]
		if !file1.Equal(file2) {
			return false
		}
	}
	return true
}

// readStatuses returns the contents of the status files in the provided
// directories.
func readStatuses(t *testing.T, roots ...*memory.Entry) [][]byte {
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

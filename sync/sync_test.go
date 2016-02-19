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

	"github.com/aykevl/dtsync/tree/memory"
)

type testCase struct {
	file     string
	action   Action
	contents []byte
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
		{"file1.txt", ACTION_COPY, []byte("The quick brown fox...")},
		{"file1.txt", ACTION_UPDATE, []byte("The quick brown fox jumps over the lazy dog.")},
		{"file1.txt", ACTION_REMOVE, nil},
	}

	runTests(t, fs1, fs2, false, testCases)
	runTests(t, fs1, fs2, true, testCases)
	runTests(t, fs2, fs1, false, testCases)
	runTests(t, fs2, fs1, true, testCases)
}

func runTests(t *testing.T, fs1, fs2 *memory.Entry, swap bool, cases []testCase) {
	// The number of files currently in the filesystems.
	// It starts at 1, as there are status files.
	fileCount := int64(1)

	for _, tc := range cases {
		statusBefore := readStatuses(t, fs1, fs2)

		var err error
		switch tc.action {
		case ACTION_COPY: // add
			fileCount++
			_, err = fs1.AddRegular(tc.file, tc.contents)
		case ACTION_UPDATE:
			child := getFile(fs1, tc.file)
			if child == nil {
				t.Fatalf("could not find file %s to update", tc.file)
			}
			child.SetContents(tc.contents)
		case ACTION_REMOVE:
			fileCount--
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

		var result *Result
		if swap {
			result, err = Sync(fs2, fs1)
		} else {
			result, err = Sync(fs1, fs2)
		}
		if err != nil {
			t.Fatalf("could not sync after: %s %s: %s", tc.action, tc.file, err)
		}

		if err := result.SyncAll(); err != nil {
			t.Errorf("could not sync all: %s", err)
		}
		result.MarkFullySynced()
		if err := result.SaveStatus(); err != nil {
			t.Errorf("could not save status: %s", err)
		}

		if !fsEqual(fs1, fs2) {
			t.Errorf("directory trees are not equal after: %s %s", tc.action, tc.file)
		}

		if fs1.Size() != fileCount || fs2.Size() != fileCount {
			t.Errorf("unexpected number of files after sync (expected %d): fs1=%d fs2=%d", fileCount, fs1.Size(), fs2.Size())
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

func fsEqual(fs1, fs2 *memory.Entry) bool {
	list1, err := fs1.List()
	assert(err)
	list2, err := fs2.List()
	assert(err)
	if len(list1) != len(list2) {
		return false
	}
	for i := 0; i < len(list1); i++ {
		file1 := list1[i].(*memory.Entry)
		file2 := list2[i].(*memory.Entry)
		if file1.Name() == STATUS_FILE && file2.Name() == STATUS_FILE {
			continue
		}
		if !file1.Equal(file2) {
			return false
		}
	}
	return true
}

// readStatuses returns the contents of the status files in the provided
// directories.
func readStatuses(t *testing.T, fs1, fs2 *memory.Entry) (statusData [2][]byte) {
	for i, fs := range []*memory.Entry{fs1, fs2} {
		statusFile, err := fs.GetFile(STATUS_FILE)
		if err != nil {
			t.Fatal("could not get status file:", err)
		}
		status, err := ioutil.ReadAll(statusFile)
		statusData[i] = status
		assert(err)
	}
	return
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
	testCases := []struct{
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

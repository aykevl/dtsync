// replica_test.go
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

package dtdiff

import (
	"bytes"
	"testing"
)

const status1 = `Content-Type: text/tab-separated-values
Identity: first
Generation: 2
Peers: second
PeerGenerations: 1

path	modtime	replica	generation
conflict.txt	2016-02-14T19:10:10.327687803+01:00	0	2
dir	2016-02-15T19:17:51.35414374+01:00	0	1
dir/file.txt	2016-02-15T19:18:18.290458876+01:00	0	1
file1.txt	2013-01-03T19:04:31.721713001Z	0	1
file2.html	2012-12-18T20:25:21.119862001Z	0	2
file3.jpeg	2012-12-18T20:25:21.099852001Z	1	1
new.txt	2016-02-14T16:30:26.719348761+01:00	0	2
`

const status2 = `Content-Type: text/tab-separated-values
Identity: second
Generation: 5
Peers: first
PeerGenerations: 1

path	modtime	replica	generation
conflict.txt	2016-02-14T19:10:10.327687803+01:00	0	3
dir	2016-02-15T19:17:51.35414374+01:00	0	1
dir/file.txt	2016-02-15T19:18:18.290458876+01:00	0	1
file1.txt	2013-01-03T19:04:31.721713001Z	1	1
file2.html	2012-12-18T20:25:21.119862001Z	1	1
file3.jpeg	2012-12-18T20:25:21.099852001Z	0	2
`

func TestReplica(t *testing.T) {
	file1 := bytes.NewBufferString(status1)
	file2 := bytes.NewBufferString(status2)

	rs, err := LoadReplicaSet(file1, file2)
	if err != nil {
		t.Fatal("failed to load replicas:", err)
	}

	root1 := rs.Get(0).Root()
	root2 := rs.Get(1).Root()

	equalityTests := []struct {
		name     string
		isnew    bool
		hasrev1  bool
		hasrev2  bool
		equal    bool
		after    bool
		before   bool
		conflict bool
	}{
		{"file1.txt", false, true, true, true, false, false, false},
		{"file2.html", false, false, true, false, true, false, false},
		{"file3.jpeg", false, true, false, false, false, true, false},
		{"conflict.txt", false, false, false, false, true, true, true},
		{"new.txt", true, false, false, false, false, false, false}, // everything after 'isnew' doesn't really matter anymore
	}

	for _, tc := range equalityTests {
		file1 := root1.Get(tc.name)
		file2 := root2.Get(tc.name)
		if root2.HasRevision(file1) != tc.hasrev1 {
			t.Error(tc.name+": expected root2.HasRevision(file1):", tc.hasrev1)
		}
		if (file2 == nil) != tc.isnew {
			t.Errorf("%s: expected 'isnew' to be %v", tc.name, tc.isnew)
		}
		if tc.isnew {
			// Other tests can't be done when only one side is present.
			continue
		}
		if root1.HasRevision(file2) != tc.hasrev2 {
			t.Error(tc.name+": expected root1.HasRevision(file2):", tc.hasrev2)
		}
		if file1.Equal(file2) != tc.equal {
			t.Error(tc.name+": expected .Equal:", tc.equal)
		}
		if file1.After(file2) != tc.after {
			t.Error(tc.name+": expected .After:", tc.after)
		}
		if file1.Before(file2) != tc.before {
			t.Error(tc.name+": expected .Before:", tc.before)
		}
		if file1.Conflict(file2) != tc.conflict {
			t.Error(tc.name+": expected .Conflict:", tc.conflict)
		}
	}

	saveTests := []struct {
		replica *Replica
		output  string
	}{
		{rs.Get(0), status1},
		{rs.Get(1), status2},
	}

	for _, tc := range saveTests {
		buf := &bytes.Buffer{}
		tc.replica.Serialize(buf)
		output := buf.Bytes()
		if !bytes.Equal(output, []byte(tc.output)) {
			t.Errorf("replica %s does not have the expected output.\n\n*** Expected:\n%s\n\n*** Actual:\n%s", tc.replica, tc.output, string(output))
		}
	}
}

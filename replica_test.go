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

package rtdiff

import (
	"strings"
	"testing"
)

var status1 = `Content-Type: text/tab-separated-values
Identity: first
Generation: 2
Peers: second
PeerGenerations: 1

path	modtime	replica	generation
file1.txt	2013-01-03T19:04:31.721713001Z	0	1
file2.html	2012-12-18T20:25:21.119862001Z	0	2
file3.jpeg	2012-12-18T20:25:21.099852001Z	1	1
conflict.txt	2016-02-14T19:10:10.327687803+01:00	0	2
`

var status2 = `Content-Type: text/tab-separated-values
Identity: second
Generation: 5
Peers: first
PeerGenerations: 1

path	modtime	replica	generation
file1.txt	2013-01-03T19:04:31.721713001Z	1	1
file2.html	2012-12-18T20:25:21.119862001Z	1	1
file3.jpeg	2012-12-18T20:25:21.099852001Z	0	2
conflict.txt	2016-02-14T19:10:10.327687803+01:00	0	3
new.txt	2016-02-14T16:30:26.719348761+01:00	0	5
`

func TestReplica(t *testing.T) {
	file1 := strings.NewReader(status1)
	file2 := strings.NewReader(status2)

	rs, err := LoadReplicaSet(file1, file2)
	if err != nil {
		t.Fatal("failed to load replicas:", err)
	}

	root1 := rs.Get(0).Root()
	root2 := rs.Get(1).Root()

	equalityTests := []struct {
		name     string
		equal    bool
		after    bool
		before   bool
		conflict bool
	}{
		{"file1.txt", true, false, false, false},
		{"file2.html", false, true, false, false},
		{"file3.jpeg", false, false, true, false},
		{"conflict.txt", false, true, true, true},
	}

	for _, tc := range equalityTests {
		file1 := root1.Get(tc.name)
		file2 := root2.Get(tc.name)
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
}

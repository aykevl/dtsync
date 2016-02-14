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

path	modtime	replica	generation
file1.txt	2013-01-03T19:04:31.721713001Z	0	1
file2.html	2012-12-18T20:25:21.119862001Z	0	2
file3.jpeg	2012-12-18T20:25:21.099852001Z	1	1
`

var status2 = `Content-Type: text/tab-separated-values
Identity: second
Generation: 5
Peers: first

path	modtime	replica	generation
file1.txt	2013-01-03T19:04:31.721713001Z	1	1
file2.html	2012-12-18T20:25:21.119862001Z	1	1
file3.jpeg	2012-12-18T20:25:21.099852001Z	0	2
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

	if !root1.Get("file1.txt").Equal(root2.Get("file1.txt")) {
		t.Error("file1.txt is expected to be equal")
	}

	for _, fn := range []string{"file2.html", "file3.jpeg"} {
		if root1.Get(fn).Equal(root2.Get(fn)) {
			t.Error(fn + " is expected to be unequal")
		}
	}
}

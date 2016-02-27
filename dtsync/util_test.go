// util_test.go
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

package main

import (
	"strings"
	"testing"
)

func TestFieldsN(t *testing.T) {
	testCases := []struct {
		s string // string to test
		n int    // number of fileds
		r string // result (split at comma)
	}{
		{"a b c", 3, "a,b,c"},
		{"a b c d", 3, "a,b,c d"},
		{"a b", 3, "a,b"},
		{" a  b  ", 3, "a,b"},
		{"a b c  d ", 3, "a,b,c  d "},
	}
	for _, tc := range testCases {
		actual := FieldsN(tc.s, tc.n)
		expected := strings.Split(tc.r, ",")
		fail := false
		if len(expected) != len(actual) {
			fail = true
		}
		for i, s := range expected {
			if s != actual[i] {
				fail = true
			}
		}
		if fail {
			t.Errorf("expected %#v but got %#v for %#v (%d)", expected, actual, tc.s, tc.n)
		}
	}
}

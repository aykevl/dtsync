// profile.go
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
	"strconv"
)

// ProfileError is returned when a non-syntax error appears in a config file,
// like an unknown key.
type ProfileError struct {
	name string
	msg  string
}

func (e ProfileError) Error() string {
	return "profile " + e.name + ": " + e.msg
}

// Profile contains two roots and optionally a profile name.
type Profile struct {
	name  string
	root1 string
	root2 string
}

// NewConfigProfile loads and parses the given profile config file and returns a
// Profile including name and roots.
func NewConfigProfile(name string) (*Profile, error) {
	config, err := loadConfig(name)
	if err != nil {
		return nil, err
	}

	// Extract values from config file
	var roots []string
	for key, values := range config {
		switch key {
		case "root":
			if len(values) != 2 {
				return nil, ProfileError{name, "expected exactly 2 roots, got " + strconv.Itoa(len(values))}
			}
			roots = values
		default:
			return nil, ProfileError{name, "unknown key " + key}
		}
	}

	return &Profile{
		name:  name,
		root1: roots[0],
		root2: roots[1],
	}, nil
}

// NewPairProfile returns a new profile with two fixed roots (e.g. provided on
// the command line).
func NewPairProfile(root1 string, root2 string) *Profile {
	return &Profile{
		root1: root1,
		root2: root2,
	}
}

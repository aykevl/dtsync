// errors.go
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

package tree

import (
	"strings"
	"errors"
	"os"
)

// Error codes that can be used by any filesystem implementation.
var (
	ErrNoDirectory        = errors.New("tree: this is not a directory")
	ErrNoRegular          = errors.New("tree: this is not a regular file")
	ErrInvalidName        = errors.New("tree: invalid file name")
	ErrCancelled          = errors.New("tree: write cancelled")
	ErrParsingFingerprint = errors.New("tree: invalid fingerprint")
)

type PathError interface {
	error
	Path() string
}

// ErrNotImplemented is returned when a method is not implemented or a parameter
// is not of a required type.
type ErrNotImplemented string

func (feature ErrNotImplemented) Error() string {
	return "tree: not implemented: " + string(feature)
}

type errNotFound string

// ErrNotFound is returned when a file is not found (e.g. when trying to read
// it).
func ErrNotFound(pathParts []string) error {
	return errNotFound(strings.Join(pathParts, "/"))
}

func (path errNotFound) Error() string {
	return "tree: not found: " + string(path)
}

func (path errNotFound) Path() string {
	return string(path)
}

type errFound string

// ErrFound is returned when a file is found when none was expected (e.g. on
// copy or when creating a directory).
func ErrFound(pathParts []string) error {
	return errFound(strings.Join(pathParts, "/"))
}

func (path errFound) Error() string {
	return "tree: found: " + string(path)
}

func (path errFound) Path() string {
	return string(path)
}

type errChanged string

// ErrChanged is returned when a file's fingerprint changed between scan and sync.
func ErrChanged(pathParts []string) error {
	return errChanged(strings.Join(pathParts, "/"))
}

func (path errChanged) Error() string {
	return "tree: changed: " + string(path)
}

func (path errChanged) Path() string {
	return string(path)
}

// IsNotExist returns true if (and only if) this is a 'not found' error. Very
// similar to os.IsNotExist.
func IsNotExist(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(errNotFound); ok {
		return true
	}
	if os.IsNotExist(err) {
		return true
	}
	return false
}

// IsExist returns true if (and only if) this is a 'found' error (e.g. on
// directory creation). Very similar to os.IsExist.
func IsExist(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(errFound); ok {
		return true
	}
	if os.IsExist(err) {
		return true
	}
	return false
}

// IsChanged returns true if (and only if) this is an error caused by a
// fingerprint that was different from the expected fingerprint.
func IsChanged(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(errChanged)
	return ok
}

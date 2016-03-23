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
	"errors"
	"os"
)

// Error codes that can be used by any filesystem implementation.
var (
	ErrNoDirectory        = errors.New("tree: this is not a directory")
	ErrNoRegular          = errors.New("tree: this is not a regular file")
	ErrNotFound           = errors.New("tree: file not found")
	ErrFound              = errors.New("tree: file already exists")
	ErrInvalidName        = errors.New("tree: invalid file name")
	ErrChanged            = errors.New("tree: updated file between scan and sync")
	ErrCancelled          = errors.New("tree: write cancelled")
	ErrParsingFingerprint = errors.New("tree: invalid fingerprint")
)

// NotImplemented is returned when a method is not implemented or a parameter is
// not of a required type.
type NotImplemented string

func (feature NotImplemented) Error() string {
	return "tree: not implemented: " + string(feature)
}

func IsNotExist(err error) bool {
	if err == nil {
		return false
	}
	if err == ErrNotFound {
		return true
	}
	if os.IsNotExist(err) {
		return true
	}
	return false
}

func IsExist(err error) bool {
	if err == nil {
		return false
	}
	if err == ErrFound {
		return true
	}
	return false
}

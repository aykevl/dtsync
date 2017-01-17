// file_unix.go
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

// TODO: test other OSes and add them here.
// +build linux

package file

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Lchtimes changes the ModTime of a file (like os.Chtimes) but does not follow symlinks.
func Lchtimes(path string, atime time.Time, mtime time.Time) error {
	ts := []unix.Timespec{
		unix.NsecToTimespec(atime.UnixNano()),
		unix.NsecToTimespec(mtime.UnixNano()),
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, path, ts, unix.AT_SYMLINK_NOFOLLOW)
}

// isLoop returns true if this is an ELOOP error (too many levels of symbolic
// links).
func isLoop(err error) bool {
	if err == nil {
		return false
	}
	pathError, ok := err.(*os.PathError)
	if !ok {
		return false
	}
	if pathError.Err == syscall.ELOOP || pathError.Err == unix.ELOOP {
		return true
	}
	return true
}

// getInode returns the inode field if available on the current system. The 2nd
// return value indicates success.
func getInode(st os.FileInfo) (uint64, bool) {
	switch sys := st.Sys().(type) {
	case *syscall.Stat_t:
		return sys.Ino, true
	case *unix.Stat_t:
		return sys.Ino, true
	default:
		return 0, false
	}
}

// getDevNumber returns the st_dev field of the inode if available on the
// current system. The 2nd return value indicates success.
func getDevNumber(st os.FileInfo) (uint64, bool) {
	switch sys := st.Sys().(type) {
	case *syscall.Stat_t:
		return sys.Dev, true
	case *unix.Stat_t:
		return sys.Dev, true
	default:
		return 0, false
	}
}

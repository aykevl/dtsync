// stat_linux.go
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

package file

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func (e *Entry) readStat(dir *os.File, follow bool) (os.FileInfo, error) {
	name := e.path[len(e.path)-1]
	var fd int
	var err error
	if follow {
		fd, err = unix.Openat(int(dir.Fd()), name, unix.O_PATH|unix.O_RDONLY|unix.O_NOATIME, 0)
	} else {
		fd, err = unix.Openat(int(dir.Fd()), name, unix.O_PATH|unix.O_RDONLY|unix.O_NOATIME|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, &os.PathError{"openat", e.fullPath(), err}
	}
	defer unix.Close(fd)

	var sys unix.Stat_t
	err = unix.Fstat(fd, &sys)
	if err != nil {
		return nil, &os.PathError{"fstat", e.fullPath(), err}
	}

	// copied from golang source: src/os/stat_linux.go
	mode := os.FileMode(sys.Mode & 0777)
	switch sys.Mode & unix.S_IFMT {
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	case unix.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFREG:
		// nothing to do
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	}
	if sys.Mode&unix.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if sys.Mode&unix.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if sys.Mode&unix.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}

	return &fileInfo{name, sys, mode, time.Unix(sys.Mtim.Sec, sys.Mtim.Nsec)}, nil
}

// fileInfo implements os.FileInfo.
type fileInfo struct {
	name    string
	sys     unix.Stat_t
	mode    os.FileMode
	modTime time.Time
}

func (i *fileInfo) Name() string {
	return i.name
}

func (i *fileInfo) Size() int64 {
	return i.sys.Size
}

func (i *fileInfo) Mode() os.FileMode {
	return i.mode
}

func (i *fileInfo) ModTime() time.Time {
	return i.modTime
}

func (i *fileInfo) IsDir() bool {
	return i.Mode().IsDir()
}

func (i *fileInfo) Sys() interface{} {
	return &i.sys
}

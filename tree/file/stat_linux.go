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
	"unsafe"

	"golang.org/x/sys/unix"
)

// Magic!
// This value is normally stored in <linux/fs.h>:
//
//     #define FS_IOC_GETVERSION     _IOR('v', 1, long)
const FS_IOC_GETVERSION = 0x80087601

// readGeneration does an IOCTL to read the current i_generation of the inode.
// Not implemented for all filesystems on Linux.
//
// See:
// http://stackoverflow.com/questions/20052912/how-do-i-get-the-generation-number-of-an-inode-in-linux/28006048#28006048
func readGeneration(fd uintptr) (uint64, error) {
	// I *hope* this is the right type, but I'm not sure. It's not like this is
	// a well-defined interface...
	// Looking at the kernel source, the value is copied from i_generation (a
	// member of `struct inode`), which is a 32-bit unsigned integer. I checked
	// ext4 and btrfs. Btrfs stores a 64-bit value on disk, but only exposes a
	// 32-bit value to userspace.
	// At the same time, it's defined in <linux/fs.h> as a long, which is
	// usually a 32-bit signed integer, but can also be larger.
	// Playing it safe here with an unsigned 64-bit integer. I don't expect the
	// generation number to ever go below zero (just like an inode number), and
	// on little-endian hardware it should be safe to provide a bigger number.
	var generation uint64

	// Here the magic happens!
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, FS_IOC_GETVERSION, uintptr(unsafe.Pointer(&generation)))

	var err error
	if errno != 0 {
		err = os.NewSyscallError("ioctl", errno)
	}

	return generation, err
}

func (e *Entry) readStat(dir *os.File, follow bool) (os.FileInfo, error) {
	name := e.path[len(e.path)-1]
	var fd int
	var err error
	if follow {
		fd, err = unix.Openat(int(dir.Fd()), name, unix.O_PATH, 0)
	} else {
		fd, err = unix.Openat(int(dir.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, &os.PathError{"openat", e.fullPath(), err}
	}
	defer unix.Close(fd)

	generation, err := readGeneration(uintptr(fd))
	if err.(*os.SyscallError).Err == unix.EBADF {
		// Wrong FS type? It appears tmpfs hasn't implemented it.
	} else if err != nil {
		return nil, &os.PathError{"readGeneration", e.fullPath(), err}
	} else {
		// valid generation number found
		e.generation = &generation
	}

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

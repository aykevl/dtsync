// util.go
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
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/dtsync/tree"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func makeRandomString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func nextFileStatus(fileList []tree.Entry, statusList []*Entry) func() (tree.Entry, *Entry) {
	fileIterator := iterateEntrySlice(fileList)
	statusIterator := IterateEntries(statusList)

	file := <-fileIterator
	status := <-statusIterator
	return func() (retFile tree.Entry, retStatus *Entry) {
		fileName := ""
		if file != nil {
			fileName = file.Name()
		}
		statusName := ""
		if status != nil {
			statusName = status.Name()
		}

		name := LeastName(fileName, statusName)
		if name == fileName {
			retFile = file
			file = <-fileIterator
			for file != nil && file.Name() == name {
				// In some very broken filesystems, or maybe some weirdly
				// designed systems where a directory may contain multiple
				// entries with the same name (MTP), this may actually happen.
				// I've actually seen it in a corrupted FAT32 filesystem on
				// Linux.
				// TODO: maybe we should handle this event in some other way? Or
				// maybe the actual Tree implementations should handle this?
				file = <-fileIterator
			}
		}
		if name == statusName {
			retStatus = status
			status = <-statusIterator
		}

		return
	}
}

func iterateEntrySlice(list []tree.Entry) chan tree.Entry {
	c := make(chan tree.Entry)
	go func() {
		for _, entry := range list {
			if entry.Name() == STATUS_FILE {
				continue
			}
			c <- entry
		}
		close(c)
	}()
	return c
}

// LeastName returns the alphabetically first name in the list that is not the
// empty string.
func LeastName(names ...string) string {
	name := ""
	for _, s := range names {
		if s != "" && s < name || name == "" {
			name = s
		}
	}
	return name
}

// serializeFingerprint returns a fingerprint string from a FileInfo.
func serializeFingerprint(e tree.FileInfo) string {
	parts := make([]string, 0, 4)
	modTime := e.ModTime().UTC().Format(time.RFC3339Nano)
	parts = append(parts, e.Type().Char(), modTime)
	if e.Type() == tree.TYPE_REGULAR {
		parts = append(parts, strconv.FormatInt(e.Size(), 10))
	}
	return strings.Join(parts, "/")
}

type fingerprintInfo struct {
	fileType tree.Type
	modTime  time.Time
	size     int64
}

// parseFingerprint parses the given fingerprint string.
func parseFingerprint(fingerprint string) (fingerprintInfo, error) {
	info := fingerprintInfo{}

	parts := strings.Split(fingerprint, "/")
	if len(parts) < 2 {
		return info, ErrParsingFingerprint
	}

	switch parts[0] {
	case "f":
		info.fileType = tree.TYPE_REGULAR
	case "d":
		info.fileType = tree.TYPE_DIRECTORY
	case "l":
		info.fileType = tree.TYPE_SYMLINK
	default:
		info.fileType = tree.TYPE_UNKNOWN
	}

	modTime, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return info, err
	}
	info.modTime = modTime

	if len(parts) < 3 {
		return info, nil
	}

	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return info, err
	}
	info.size = size
	return info, nil
}

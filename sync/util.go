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

package sync

import (
	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
)

// leastName returns the alphabetically first name in the list that is not the
// empty string.
func leastName(names ...string) string {
	name := ""
	for _, s := range names {
		if s != "" && s < name || name == "" {
			name = s
		}
	}
	return name
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

func iterateStatusSlice(list []*dtdiff.Entry) chan *dtdiff.Entry {
	c := make(chan *dtdiff.Entry)
	go func() {
		for _, entry := range list {
			c <- entry
		}
		close(c)
	}()
	return c
}

func nextFileStatus(fileList []tree.Entry, statusList []*dtdiff.Entry) func() (tree.Entry, *dtdiff.Entry) {
	fileIterator := iterateEntrySlice(fileList)
	statusIterator := iterateStatusSlice(statusList)

	file := <-fileIterator
	status := <-statusIterator
	return func() (retFile tree.Entry, retStatus *dtdiff.Entry) {
		fileName := ""
		if file != nil {
			fileName = file.Name()
		}
		statusName := ""
		if status != nil {
			statusName = status.Name()
		}

		name := leastName(fileName, statusName)
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

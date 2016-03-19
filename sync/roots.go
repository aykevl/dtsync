// roots.go
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
	"flag"
	"net/url"
	"os"
	"os/exec"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/file"
	"github.com/aykevl/dtsync/tree/memory"
	"github.com/aykevl/dtsync/tree/remote"
)

var remoteCommand = flag.String("server-command", "dtsync", "Which command to execute on the other host")

// NewRoot parses the string (e.g. from the command line) and finds the most
// likely tree to load.
func NewTree(path string) (tree.Tree, error) {
	u, err := url.Parse(path)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "":
		// Not an URL. Treat it as a local directory.
		return file.NewRoot(path)
	case "file":
		// Local file as file:// URL.
		return file.NewRoot(u.Path)
	case "memory":
		// In-memory (test) filesystem.
		return memory.NewRoot(), nil
	case "ssh":
		cmd := exec.Command("ssh", u.Host, *remoteCommand, "-server", u.Path)
		writer, err := cmd.StdinPipe()
		if err != nil {
			return nil, err // unlikely
		}
		reader, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err // unlikely
		}
		cmd.Stderr = os.Stderr
		err = cmd.Start()
		if err != nil {
			return nil, err
		}
		return remote.NewClient(reader, writer)
	default:
		return nil, ErrUnknownScheme
	}
}

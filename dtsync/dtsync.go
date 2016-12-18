// dtsync.go
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
	"flag"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/aykevl/dtsync/sync"
	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/remote"
)

var cpuprofile = flag.String("cpuprofile", "", "write CPU profile to file")
var isServer = flag.Bool("server", false, "Run as server side process")

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [options] <dir1> <dir2>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Options:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not open CPU profile file:", err)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *isServer {
		if flag.NArg() != 1 {
			fmt.Fprintf(os.Stderr, "Provide exactly one directory on the command line (got %d).\n", flag.NArg())
			return
		}
		root, err := sync.NewTree(flag.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not open root %s: %s\n", flag.Arg(0), err)
			return
		}
		if localRoot, ok := root.(tree.LocalFileTree); !ok {
			fmt.Fprintf(os.Stderr, "Cannot use non-local root\n")
			return
		} else {
			err = remote.NewServer(os.Stdin, os.Stdout, localRoot).Run()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Server error: %s\n", err)
			}
		}
		return
	}

	if flag.NArg() != 2 {
		usage()
		return
	}

	root1 := flag.Arg(0)
	root2 := flag.Arg(1)
	runCLI(root1, root2)
}

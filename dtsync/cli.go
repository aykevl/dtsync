// cli.go
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
	"bufio"
	"flag"
	"fmt"
	"os"

	"github.com/aykevl/dtsync/sync"
	"github.com/aykevl/dtsync/tree/file"
)

func main() {
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "Provide exactly two directories on the command line (got %d).\n", flag.NArg())
		return
	}

	fs1, err := file.NewRoot(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not open first root:", err)
	}
	fs2, err := file.NewRoot(flag.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not open second root:", err)
	}

	result, err := sync.Scan(fs1, fs2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not scan roots:", err)
	}

	if len(result.Jobs()) == 0 {
		// Nice! We don't have to do anything.
		err := result.SaveStatus()
		if err != nil {
			fmt.Fprintln(os.Stderr, "No changes, but could not save status:", err)
		} else {
			fmt.Println("No changes.")
		}
		return
	}

	fmt.Println("Scan results:")
	for _, job := range result.Jobs() {
		var direction string
		switch job.Direction() {
		case -1:
			direction = "<--"
		case 0:
			direction = " ? "
		case 1:
			direction = "-->"
		default:
			// We might as wel just panic.
			direction = "!!!"
		}
		fmt.Printf("%-8s  %s  %8s   %s\n", job.StatusLeft(), direction, job.StatusRight(), job.RelativePath())
	}

	scanner := bufio.NewScanner(os.Stdin)
	action := 0
	for action == 0 {
		fmt.Printf("Apply these changes? ")
		if !scanner.Scan() {
			return
		}
		switch scanner.Text() {
		case "y", "Y", "yes", "Yes", "YES":
			// apply changes
			action = 1
		case "n", "N", "no", "No", "NO":
			// exit
			action = -1
		default:
			continue
		}
	}
	if action == -1 {
		err = result.SaveStatus()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not save status:", err)
		}
		return
	}

	stats, err := result.SyncAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not apply all changes:", err)
	}
	fmt.Printf("Applied %d changes (%d errors)\n", stats.CountTotal, stats.CountError)
	err = result.SaveStatus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not save status:", err)
	}
}

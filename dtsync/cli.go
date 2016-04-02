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
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime/pprof"
	"strings"
	"unicode"

	"github.com/aykevl/dtsync/sync"
	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/tree/remote"
)

var cpuprofile = flag.String("cpuprofile", "", "write CPU profile to file")
var editorCommand = flag.String("editor", "/usr/bin/editor", "Editor to use for editing jobs")
var isServer = flag.Bool("server", false, "Run as server side process")

func editJobs(result *sync.Result, root1, root2 string) bool {
	jobs := result.Jobs()

	tmpFile, err := ioutil.TempFile("", "dtsync-edit-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open temporary file:", err)
		return false
	}
	defer tmpFile.Close()

	jobMap := make(map[string]*sync.Job, len(jobs))
	writer := bufio.NewWriter(tmpFile)
	fmt.Fprintf(writer, `# You can change here which changes in which direction are applied.
# Left:  %s
# Right: %s
#  >  apply left-to-right
#  <  apply right-to-left
#  ?  skip
# Lines starting with '#' are comments and are ignored.
# Removing a line has the same effect as marking it '?'.
`, root1, root2)
	for _, job := range jobs {
		statusLeft := job.StatusLeft()
		if statusLeft == "" {
			statusLeft = "-"
		}
		statusRight := job.StatusRight()
		if statusRight == "" {
			statusRight = "-"
		}

		var direction string
		switch job.Direction() {
		case 1:
			direction = ">"
		case 0:
			direction = "?"
		case -1:
			direction = "<"
		default:
			// unreachable
			panic("unknown direction")
		}

		path := job.RelativePath()
		jobMap[path] = job

		// Check for invalid paths, just to be sure (I don't expect these to
		// occur much in practice).
		for i, c := range path {
			if c == '\r' || c == '\n' {
				fmt.Fprintf(os.Stderr, "One of the paths (%#v) contains a newline character, abort.\n", path)
				return false
			}
			if i == 0 && unicode.IsSpace(c) {
				fmt.Fprintf(os.Stderr, "Path %#v contains spaces at the beginning, abort.\n", path)
				return false
			}
		}

		fmt.Fprintf(writer, "%-8s  %s  %8s   %s\n", statusLeft, direction, statusRight, path)
	}
	err = writer.Flush()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to write file to edit:", err)
		return false
	}

	cmd := exec.Command(*editorCommand, tmpFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not start %s %s: %s\n", *editorCommand, tmpFile.Name(), err)
		return false
	}

	readTmpFile, err := os.Open(tmpFile.Name())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not re-open temporary file after editing: %s\n", err)
		return false
	}
	defer readTmpFile.Close()
	reader := bufio.NewReader(readTmpFile)
	parseError := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Could not read from temporary file after editing: %s\n", err)
				return false
			}
			break
		}
		line = line[:len(line)-1]
		if line == "" || line[0] == '#' {
			continue
		}

		// We cannot use strings.Fields as we need exactly 4 fields.
		fields := FieldsN(line, 4)
		if len(fields) != 4 {
			parseError = true
			break
		}

		job := jobMap[fields[3]]
		if job == nil {
			parseError = true
			break
		}

		var direction int
		switch fields[1] {
		case ">":
			direction = 1
		case "?":
			direction = 0
		case "<":
			direction = -1
		default:
			parseError = true
			break
		}

		job.SetDirection(direction)

		// Mark as handled
		delete(jobMap, fields[3])
	}
	if parseError {
		fmt.Fprintf(os.Stderr, "Could not parse temporary file\n")
		return false
	}

	// Disable any jobs not found in the edited file.
	for _, job := range jobMap {
		job.SetDirection(0)
	}

	return true
}

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
	fs1, err := sync.NewTree(root1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open first root %s: %s\n", root1, err)
		return
	}
	fs2, err := sync.NewTree(root2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open second root %s: %s\n", root2, err)
		return
	}

	result, err := sync.Scan(fs1, fs2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not scan roots:", err)
		return
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
	const (
		actionNone = iota
		actionApply
		actionEdit
		actionQuit
	)
	action := actionNone
	for action == actionNone {
		fmt.Printf("Apply these changes? ")
		if !scanner.Scan() {
			return
		}
		input := strings.ToLower(scanner.Text())
		switch input {
		case "y", "yes":
			action = actionApply
		case "e", "edit":
			action = actionEdit
		case "q", "quit", "n", "no":
			action = actionQuit
		default:
			continue
		}
	}

	if action == actionQuit {
		err = result.SaveStatus()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not save status:", err)
		}
		return
	}

	if action == actionEdit {
		if !editJobs(result, root1, root2) {
			return
		}
	}

	for i, job := range result.Jobs() {
		action := job.Action().String()
		if job.Direction() == 0 {
			action = "skip"
		}
		digits := 0
		for i := len(result.Jobs()); i != 0; i /= 10 {
			digits += 1
		}
		fmt.Printf("%*d/%d %6s: %s\n", digits, i+1, len(result.Jobs()), action, job.RelativePath())

		if job.Direction() == 0 {
			continue
		}
		err := job.Apply()
		if err != nil {
			// TODO: print in red: https://github.com/andrew-d/go-termutil
			fmt.Printf("%*s%s\n", digits*2+10, "", err)
		}
	}
	stats := result.Stats()
	fmt.Printf("Applied %d changes (%d errors)\n", stats.CountTotal, stats.CountError)
	err = result.SaveStatus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not save status:", err)
	}
}

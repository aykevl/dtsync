// config.go
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
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrInvalidConfigName = errors.New("config: invalid config name")
var ErrNoUserHome = errors.New("config: no home directory found")

// ParseError indicates a syntax error while parsing *.cfg files, including
// config file name and line number.
type ParseError struct {
	name string
	line int
	msg  string
}

func (e ParseError) Error() string {
	return "config: error in file " + e.name + " on line " + strconv.Itoa(e.line) + ": " + e.msg
}

// e.g. /home/user/.config/dtsync/configname.cfg
const CONFIG_DIRECTORY = ".config/dtsync"
const CONFIG_EXT = ".cfg"

// loadConfig reads a config file and parses it in a map of string slices.
func loadConfig(name string, config map[string][]string) error {
	if !validConfigName(name) {
		return ErrInvalidConfigName
	}

	user, err := user.Current()
	if err != nil {
		return err
	}

	if user.HomeDir == "" {
		return ErrNoUserHome
	}

	path := filepath.Join(user.HomeDir, CONFIG_DIRECTORY, name+CONFIG_EXT)

	fp, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fp.Close()
	reader := bufio.NewReader(fp)
	lineno := 0
	for {
		lineno++ // first line is line 1
		bline, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		line := string(bline)
		line = strings.TrimSpace(line)

		if len(line) == 0 {
			// Empty line
			continue
		}
		if line[0] == '#' {
			// Comment
			continue
		}

		// Read key
		key := ""
		for i, c := range line {
			if c >= 'a' && c <= 'z' {
				continue
			}
			if c == '\t' || c == ' ' {
				// end of key
				if i == 0 {
					// not sure whether this is possible
					return ParseError{name, lineno, "empty key"}
				}
				key = line[:i]
				break
			}
			// Invalid character
			return ParseError{name, lineno, "invalid character in key: " + string(c)}
		}

		value := strings.TrimSpace(line[len(key)+1:])

		if key == "" || value == "" {
			return ParseError{name, lineno, "key without value"}
		}

		config[key] = append(config[key], value)
	}

	return nil
}

// validConfigName checks whether names adhere to [a-zA-Z0-9,_-] and don't start
// with a special character.
func validConfigName(name string) bool {
	if len(name) == 0 || len(name) > 255 {
		// 255 is just to limit the name to sane lengths, there's no reason why
		// it's specifically 255.
		return false
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '-', '_', ',':
			if i == 0 {
				// Don't allow special characters at the beginning.
				return false
			}
		default:
			return false
		}
	}
	return true
}

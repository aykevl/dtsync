// test.go
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

package remote

import (
	"os"

	"github.com/aykevl/dtsync/tree/memory"
)

// TestClient returns a *Client that is already connected to a server where the
// server has a memory filesystem as backend.
func TestClient() (*Client, error) {
	memoryFS := memory.NewRoot()

	// Use os.Pipe, not io.Pipe, as os.Pipe provides buffering (and looks more
	// like a network connection).
	clientReader, serverWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	serverReader, clientWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	server := NewServer(serverReader, serverWriter, memoryFS)
	go func() {
		err := server.Run()
		if err != nil {
			// TODO: use a more graceful error here. Calling t.Fatal() from a
			// goroutine isn't supported.
			panic("error while running server: " + err.Error())
		} else {
			debugLog("S: CLOSED")
		}
	}()

	return NewClient(clientReader, clientWriter)
}

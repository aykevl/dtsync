// replica.go
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

package rtdiff

import (
	"bufio"
	"errors"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/unitsv"
)

var (
	ErrContentType            = errors.New("rtdiff: wrong content type")
	ErrNoIdentity             = errors.New("rtdiff: no Identity header")
	ErrInvalidGeneration      = errors.New("rtdiff: invalid or missing Generation header")
	ErrInvalidPeers           = errors.New("rtdiff: invalid or missing Peers header")
	ErrInvalidPeerGenerations = errors.New("rtdiff: invalid or missing PeerGenerations header")
	ErrInvalidReplicaIndex    = errors.New("rtdiff: invalid or missing replica index in entry row")
	ErrInvalidEntryGeneration = errors.New("rtdiff: invalid generation number in entry row")
	ErrInvalidPath            = errors.New("rtdiff: invalid or missing path in entry row")
)

type Replica struct {
	// generation starts at 1, 0 means 'no generation'
	generation      int
	identity        string
	peerGenerations map[string]int
	replicaSet      *ReplicaSet
	rootEntry       *Entry
}

func loadReplica(replicaSet *ReplicaSet, file io.Reader) (*Replica, error) {
	r := &Replica{
		replicaSet: replicaSet,
		rootEntry: &Entry{
			children: make(map[string]*Entry),
		},
	}
	r.rootEntry.replica = r

	err := r.load(file)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Replica) Set() *ReplicaSet {
	return r.replicaSet
}

// load assumes this replica hasn't yet been loaded
func (r *Replica) load(file io.Reader) error {
	if r.identity != "" {
		panic("replica already loaded")
	}

	msg, err := mail.ReadMessage(file)
	if err != nil {
		return err
	}

	if msg.Header.Get("Content-Type") != "text/tab-separated-values" {
		return ErrContentType
	}

	identity := msg.Header.Get("Identity")
	if identity == "" {
		return ErrNoIdentity
	}
	r.identity = identity

	generationString := msg.Header.Get("Generation")
	if generationString == "" {
		return ErrInvalidGeneration
	}
	generation, err := strconv.Atoi(generationString)
	if err != nil {
		return ErrInvalidGeneration
	}
	r.generation = generation

	// Get peers with generations
	peersString := msg.Header.Get("Peers")
	if peersString == "" {
		return ErrInvalidPeers
	}
	peersList := strings.Split(peersString, ",")
	peerGenerationsString := msg.Header.Get("PeerGenerations")
	if peerGenerationsString == "" {
		return ErrInvalidPeerGenerations
	}
	peerGenerationsList := strings.Split(peerGenerationsString, ",")
	if len(peersList) != len(peerGenerationsList) {
		return ErrInvalidPeerGenerations
	}

	// Create the temporary map of {index: id}
	peers := make(map[int]string, len(peersList)+1)
	peers[0] = identity
	for i, peerString := range peersList {
		peers[i+1] = peerString
	}

	// Make the {peer: generation} map
	r.peerGenerations = make(map[string]int, len(peersList)+1)
	r.peerGenerations[identity] = generation
	for i, peerGenerationString := range peerGenerationsList {
		gen, err := strconv.Atoi(peerGenerationString)
		if err != nil {
			return ErrInvalidPeerGenerations
		}
		r.peerGenerations[peersList[i]] = gen
	}

	const (
		TSV_PATH = iota
		TSV_MODTIME
		TSV_REPLICA
		TSV_GENERATION
	)

	tsvReader, err := unitsv.NewReader(msg.Body.(*bufio.Reader), []string{"path", "modtime", "replica", "generation"})
	if err != nil {
		return err
	}

	// now actually parse this thing
	for {
		fields, err := tsvReader.ReadRow()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		revReplicaIndex, err := strconv.Atoi(fields[TSV_REPLICA])
		if err != nil {
			return ErrInvalidReplicaIndex
		}
		if revReplicaIndex < 0 || revReplicaIndex >= len(peers) {
			return ErrInvalidReplicaIndex
		}
		revReplica := peers[revReplicaIndex]
		revGeneration, err := strconv.Atoi(fields[TSV_GENERATION])
		if err != nil || revGeneration < 1 || revGeneration > r.peerGenerations[peers[revReplicaIndex]] {
			return ErrInvalidEntryGeneration
		}
		// Note: in the future, we might want to use time.RFC3339Nano
		modTime, err := time.Parse(time.RFC3339, fields[TSV_MODTIME])
		if err != nil {
			return err
		}
		path := strings.Split(fields[TSV_PATH], "/")

		// now add this entry
		err = r.rootEntry.add(path, revReplica, revGeneration, modTime)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *Replica) Root() *Entry {
	return r.rootEntry
}

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

package dtdiff

import (
	"bufio"
	"errors"
	"io"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/unitsv"
)

var (
	ErrContentType            = errors.New("dtdiff: wrong content type")
	ErrNoIdentity             = errors.New("dtdiff: no Identity header")
	ErrInvalidGeneration      = errors.New("dtdiff: invalid or missing Generation header")
	ErrInvalidPeers           = errors.New("dtdiff: invalid or missing Peers header")
	ErrInvalidPeerGenerations = errors.New("dtdiff: invalid or missing PeerGenerations header")
	ErrInvalidReplicaIndex    = errors.New("dtdiff: invalid or missing replica index in entry row")
	ErrInvalidEntryGeneration = errors.New("dtdiff: invalid generation number in entry row")
	ErrInvalidPath            = errors.New("dtdiff: invalid or missing path in entry row")
)

type Replica struct {
	// generation starts at 1, 0 means 'no generation'
	generation      int
	isChanged       bool // true if there was a change
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

	if file == nil {
		// This replica is new
		r.generation = 1
		r.identity = makeRandomString(24)
		r.peerGenerations = make(map[string]int, 1)
		return r, nil
	}

	err := r.load(file)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Replica) String() string {
	return "Replica(" + r.identity + "," + strconv.Itoa(r.generation) + ")"
}

// Set returns the ReplicaSet for this replica.
func (r *Replica) Set() *ReplicaSet {
	return r.replicaSet
}

// Root returns the root entry.
func (r *Replica) Root() *Entry {
	return r.rootEntry
}

func (r *Replica) other() *Replica {
	if r.replicaSet.set[0] == r {
		return r.replicaSet.set[1]
	} else if r.replicaSet.set[1] == r {
		return r.replicaSet.set[0]
	} else {
		return nil
	}
}

func (r *Replica) markChanged() {
	if !r.isChanged {
		r.isChanged = true
		r.generation++
		r.peerGenerations[r.identity] = r.generation
	}
}

func (r *Replica) include(other *Replica) {
	for id, gen := range other.peerGenerations {
		if r.peerGenerations[id] < gen {
			r.peerGenerations[id] = gen
		}
	}
}

// load assumes this replica hasn't yet been loaded
func (r *Replica) load(file io.Reader) error {
	if r.identity != "" {
		panic("replica already loaded")
	}

	reader := bufio.NewReader(file)

	header, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return err
	}

	if header.Get("Content-Type") != "text/tab-separated-values" {
		return ErrContentType
	}

	identity := header.Get("Identity")
	if identity == "" {
		return ErrNoIdentity
	}
	r.identity = identity

	generationString := header.Get("Generation")
	if generationString == "" {
		return ErrInvalidGeneration
	}
	generation, err := strconv.Atoi(generationString)
	if err != nil {
		return ErrInvalidGeneration
	}
	r.generation = generation

	// Get peers with generations
	peersString := header.Get("Peers")
	if peersString == "" {
		return ErrInvalidPeers
	}
	peersList := strings.Split(peersString, ",")
	peerGenerationsString := header.Get("PeerGenerations")
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

	tsvReader, err := unitsv.NewReader(reader, []string{"path", "modtime", "replica", "generation"})
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

func (r *Replica) Serialize(out io.Writer) error {
	peerList := make([]string, 0, len(r.peerGenerations)-1)
	peerGenerationList := make([]string, 0, len(r.peerGenerations)-1)
	peerIndex := make(map[string]int, len(r.peerGenerations)-1)
	for id, gen := range r.peerGenerations {
		if id == r.identity {
			// Do not save ourselves as a peer
			continue
		}
		peerList = append(peerList, id)
		peerGenerationList = append(peerGenerationList, strconv.Itoa(gen))
		peerIndex[id] = len(peerList) // peer index, starting with 1 (0 means ourself)
	}

	writer := bufio.NewWriter(out)
	// Don't look at the error return values, errors will be caught in .Flush().
	writeKeyValue(writer, "Content-Type", "text/tab-separated-values")
	writeKeyValue(writer, "Identity", r.identity)
	writeKeyValue(writer, "Generation", strconv.Itoa(r.generation))
	writeKeyValue(writer, "Peers", strings.Join(peerList, ""))
	writeKeyValue(writer, "PeerGenerations", strings.Join(peerGenerationList, ""))
	writer.WriteByte('\n')

	tsvWriter, err := unitsv.NewWriter(writer, []string{"path", "modtime", "replica", "generation"})
	if err != nil {
		return err
	}

	err = r.rootEntry.serializeChildren(tsvWriter, peerIndex, "")
	if err != nil {
		return err
	}

	// unitsv uses a bufio internally. bufio.NewBuffer does not return a new
	// buffer if none is needed, so this call isn't really necessary.
	err = tsvWriter.Flush()
	if err != nil {
		return err
	}

	// only now look at the error
	return writer.Flush()
}

// Write the children of this entry recursively to the TSV file.
// It has this odd way of functioning (writing the children without writing
// itself) because that makes it easier to write the root entry without copying
// (DRY).
func (e *Entry) serializeChildren(tsvWriter *unitsv.Writer, peerIndex map[string]int, path string) error {
	// Put this function in replica.go as it is most closely related to the
	// parsing and serializing code here.

	names := make([]string, 0, len(e.children))
	for name, _ := range e.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child := e.children[name]
		var childpath string
		if path == "" {
			childpath = name
		} else {
			childpath = path + "/" + name
		}
		err := tsvWriter.WriteRow([]string{childpath, child.modTime.Format(time.RFC3339Nano), strconv.Itoa(peerIndex[child.revReplica]), strconv.Itoa(child.revGeneration)})
		err = e.children[name].serializeChildren(tsvWriter, peerIndex, childpath)
		if err != nil {
			return err
		}
	}

	return nil
}

// Write a simple MIME key/value line to the output.
// The line ends in \n, while most text based protocols use \r\n.
func writeKeyValue(out *bufio.Writer, key, value string) error {
	_, err := out.WriteString(key + ": " + value + "\n")
	return err
}

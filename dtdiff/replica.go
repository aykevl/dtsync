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
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/textproto"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/aykevl/unitsv"
)

type Header textproto.MIMEHeader

var (
	ErrContentType            = errors.New("dtdiff: wrong content type")
	ErrNoIdentity             = errors.New("dtdiff: no Identity header")
	ErrInvalidGeneration      = errors.New("dtdiff: invalid or missing Generation header")
	ErrInvalidKnowledgeHeader = errors.New("dtdiff: invalid Knowledge header")
	ErrInvalidReplicaIndex    = errors.New("dtdiff: invalid or missing replica index in entry row")
	ErrInvalidEntryGeneration = errors.New("dtdiff: invalid generation number in entry row")
	ErrInvalidPath            = errors.New("dtdiff: invalid or missing path in entry row")
	ErrExists                 = errors.New("dtdiff: already exists")
	ErrSameIdentity           = errors.New("dtdiff: two replicas with the same ID")
	ErrSameRoot               = errors.New("dtdiff: trying to synchronize the same directory")
	ErrCanceled               = errors.New("dtdiff: canceled") // must always be handled
)

// File where current status of the tree is stored.
const STATUS_FILE = ".dtsync"

type Replica struct {
	// generation starts at 1, 0 means 'no generation'
	generation         int
	isChanged          bool // true if there was a change in generation (any file added/deleted/updated)
	isMetaChanged      bool // true if the metadata of any files changed
	isKnowledgeChanged bool // true if the Knowledge header was updated
	identity           string
	knowledge          map[string]int
	rootEntry          *Entry
	header             textproto.MIMEHeader
	ignore             []string // paths to ignore
}

func loadReplica(file io.Reader) (*Replica, error) {
	r := &Replica{
		rootEntry: &Entry{
			children: make(map[string]*Entry),
		},
	}
	r.rootEntry.replica = r

	if file == nil {
		// This is a blank replica, create initial data
		r.isChanged = true
		r.generation = 1
		r.identity = makeRandomString(24)
		r.knowledge = make(map[string]int, 1)
		r.knowledge[r.identity] = r.generation
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

// Root returns the root entry.
func (r *Replica) Root() *Entry {
	return r.rootEntry
}

func (r *Replica) markChanged() {
	if !r.isChanged {
		r.isChanged = true
		r.generation++
		r.knowledge[r.identity] = r.generation
	}
}

func (r *Replica) markMetaChanged() {
	r.isMetaChanged = true
}

// ChangedAny returns true if this replica got any updated (presumably during
// the last scan).
func (r *Replica) ChangedAny() bool {
	return r.isChanged || r.isMetaChanged || r.isKnowledgeChanged
}

// Changed returns true if this replica got a change in it's own files (data or
// metadata)
func (r *Replica) Changed() bool {
	return r.isChanged || r.isMetaChanged
}

func (r *Replica) include(other *Replica) {
	for id, gen := range other.knowledge {
		if r.knowledge[id] < gen {
			r.isKnowledgeChanged = true
			r.knowledge[id] = gen
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

	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return err
	}
	if mediaType != "text/tab-separated-values" {
		return ErrContentType
	}
	if params["charset"] != "utf-8" {
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

	// Create the temporary map of {index: id}, only used during parsing of the
	// TSV body.
	knowledgeString := header.Get("Knowledge")
	knowledgeParts := strings.Split(knowledgeString, ",")
	peers := make(map[int]string, len(knowledgeParts)+1)
	peers[0] = identity

	// Get knowledge of other replicas.
	// Format: id1:5,id3:8,otherId:20
	r.knowledge = make(map[string]int, len(knowledgeParts)+1)
	r.knowledge[r.identity] = r.generation
	if knowledgeString != "" {
		for i, part := range knowledgeParts {
			partParts := strings.SplitN(part, ":", 2)
			if len(partParts) != 2 {
				return ErrInvalidKnowledgeHeader
			}
			peerId := partParts[0]
			peerGen, err := strconv.Atoi(partParts[1])
			if err != nil {
				return ErrInvalidKnowledgeHeader
			}
			r.knowledge[peerId] = peerGen
			peers[i+1] = peerId
		}
	}

	const (
		TSV_PATH = iota
		TSV_FINGERPRINT
		TSV_HASH
		TSV_REPLICA
		TSV_GENERATION
	)

	tsvReader, err := unitsv.NewReader(reader, []string{"path", "fingerprint", "hash", "replica", "generation"})
	if err != nil {
		return err
	}

	// now actually parse the file list
	for {
		fields, err := tsvReader.ReadRow()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		hashStr := fields[TSV_HASH]
		if len(hashStr) == 43 {
			// In Go 1.5+, we can use base64.RawURLEncoding
			hashStr += "="
		}
		hash, err := base64.URLEncoding.DecodeString(hashStr)
		if err != nil {
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
		if err != nil || revGeneration < 1 {
			return ErrInvalidEntryGeneration
		}
		fingerprint := fields[TSV_FINGERPRINT]
		path := strings.Split(fields[TSV_PATH], "/")

		// now add this entry
		err = r.rootEntry.add(path, revReplica, revGeneration, fingerprint, hash)
		if err != nil {
			return err
		}
	}

	// Remove all headers that are written by Serialize() anyway.
	header.Del("Content-Type")
	header.Del("Identity")
	header.Del("Generation")
	header.Del("Knowledge")
	header.Del("Version")
	r.header = header

	return nil
}

func (r *Replica) Header() Header {
	return Header(r.header)
}

func (r *Replica) Serialize(out io.Writer) error {
	// Get a sorted list of peer identities
	peerIds := make([]string, 0, len(r.knowledge)-1)
	for id, _ := range r.knowledge {
		if id == r.identity {
			// Do not save ourselves as a peer
			continue
		}
		peerIds = append(peerIds, id)
	}
	sort.Strings(peerIds)

	knowledgeList := make([]string, 0, len(peerIds))
	peerIndex := make(map[string]int, len(r.knowledge))
	peerIndex[r.identity] = 0
	for i, id := range peerIds {
		knowledgeList = append(knowledgeList, id+":"+strconv.Itoa(r.knowledge[id]))
		peerIndex[id] = i + 1 // peer index, starting with 1 (0 means ourself)
	}

	writer := bufio.NewWriter(out)
	// Don't look at the error return values, errors will be caught in .Flush().
	writeKeyValue(writer, "Version", version.VERSION)
	writeKeyValue(writer, "Content-Type", "text/tab-separated-values; charset=utf-8")
	writeKeyValue(writer, "Identity", r.identity)
	writeKeyValue(writer, "Generation", strconv.Itoa(r.generation))
	writeKeyValue(writer, "Knowledge", strings.Join(knowledgeList, ","))
	for key, values := range r.header {
		for _, value := range values {
			writeKeyValue(writer, key, value)
		}
	}
	writer.WriteByte('\n')

	tsvWriter, err := unitsv.NewWriter(writer, []string{"path", "fingerprint", "hash", "replica", "generation"})
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
		hash := ""
		if child.hash != nil {
			hash = base64.URLEncoding.EncodeToString(child.hash)
			// In Go 1.5+, we can use base64.RawURLEncoding
			hash = strings.TrimRight(hash, "=")
		}
		err := tsvWriter.WriteRow([]string{childpath, child.fingerprint, hash, strconv.Itoa(peerIndex[child.revReplica]), strconv.Itoa(child.revGeneration)})
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

func (r *Replica) scan(fs tree.LocalFileTree) error {
	return r.scanDir(fs.Root(), r.Root(), nil)
}

// scanDir scans one side of the tree, updating the status tree to the current
// status.
func (r *Replica) scanDir(dir tree.Entry, statusDir *Entry, cancel chan struct{}) error {
	fileList, err := dir.List()
	if err != nil {
		return err
	}
	iterator := nextFileStatus(fileList, statusDir.List())

	var file tree.Entry
	var status *Entry
	for {
		select {
		case <-cancel:
			return ErrCanceled
		default:
		}

		file, status = iterator()
		if file != nil && r.isIgnored(file) {
			// Keep status (don't remove) if it exists, in case the file is
			// un-ignored. It may be desirable to make this configurable.
			continue
		}

		if file == nil && status == nil {
			break
		}
		if file == nil {
			// This is an old status entry: the file has been removed.
			// Now we delete it, but we could improve by, for example, keeping
			// all old entries for up to a month in case the entries appear
			// again (e.g. the filesystem was not mounted).
			status.Remove()
			continue
		}

		if status == nil {
			// add status
			info, err := file.Info()
			if err != nil {
				return err
			}
			status, err = statusDir.Add(info)
			if err != nil {
				panic(err) // must not happen
			}
		} else {
			// update status (if needed)
			oldHash := status.Hash()
			oldFingerprint := status.Fingerprint()
			newFingerprint := file.Fingerprint()
			var newHash []byte
			var err error
			if oldFingerprint == newFingerprint && oldHash != nil {
				// Assume the hash stayed the same when the fingerprint is the
				// same. But calculate a new hash if there is no hash.
				newHash = oldHash
			} else {
				if file.Type() == tree.TYPE_REGULAR {
					newHash, err = file.Hash()
					if err != nil {
						return err
					}
				}
			}
			status.Update(newFingerprint, newHash)
		}

		if file.Type() == tree.TYPE_DIRECTORY {
			err := r.scanDir(file, status, cancel)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *Replica) isIgnored(file tree.Entry) bool {
	relpath := path.Join(file.RelativePath()...)
	for _, pattern := range r.ignore {
		if len(pattern) == 0 {
			continue
		}
		// TODO: use a more advanced pattern matching method.
		if pattern[0] == '/' {
			// Match relative to the root.
			if match, err := path.Match(pattern[1:], relpath); match && err == nil {
				return true
			}
		} else {
			// Match only the name. This does not work when the pattern contains
			// slashes.
			if match, err := path.Match(pattern, file.Name()); match && err == nil {
				return true
			}
		}
	}
	return false
}

func (r *Replica) AddIgnore(ignore ...string) {
	r.ignore = append(r.ignore, ignore...)
}

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
	"sync"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/aykevl/unitsv"
)

var (
	ErrExists             = errors.New("dtdiff: already exists")
	ErrSameRoot           = errors.New("dtdiff: trying to synchronize the same directory")
	ErrParsingFingerprint = errors.New("dtdiff: could not parse fingerprint")

	errCanceled    = errors.New("dtdiff: canceled") // must always be handled
	errInvalidPath = errors.New("dtdiff: invalid or missing path in entry row")
)

// PERMS_DEFAULT has the default permission bits that are compared. It is
// possible to compare less bits with the 'perms' option.
const PERMS_DEFAULT = 0777

// STATUS_RETAIN is the duration how long to retain status entries that have
// disappeared, in case they reappear (e.g. a disk is mounted again).
const STATUS_RETAIN = time.Hour * 24 * 30

// These string constants are used in the header to determine serialization
// format and can be used to identify the filetype (e.g. by the file(1)
// command).
const (
	MAGIC_TEXT  = "dtsync-status-file"
	MAGIC_PROTO = "dtsync-status-file-proto"
)

// Format constants to use for the Replica.Serialize function.
const (
	FORMAT_NONE = iota
	FORMAT_TEXT
	FORMAT_PROTO
)

// HASH_ID is the identifier for the hash function in use. The last part is the
// number of bits for this version (256 bits or 32 bytes).
const HASH_ID = "blake2b-256"

type ParseError struct {
	Message string
	Row     int
	Err     error
}

func parseBinError(message string, err error) error {
	return &ParseError{
		Message: message,
		Err:     err,
	}
}

func (e *ParseError) Error() string {
	s := "dtdiff parser: " + e.Message
	if e.Row > 0 {
		s += " (row " + strconv.Itoa(e.Row) + ")"
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

type ErrSameIdentity struct {
	Identity string
}

func (e *ErrSameIdentity) Error() string {
	return "dtdiff: same identity: " + e.Identity
}

// File where current status of the tree is stored.
const STATUS_FILE = ".dtsync"

type ScanProgress [2]*tree.ScanProgress

func (p ScanProgress) Percent() float64 {
	p1 := p[0].Percent()
	p2 := p[1].Percent()
	return (p1 + p2) / 2.0
}

func (p ScanProgress) Behind() *tree.ScanProgress {
	if p[0] == nil || p[1] == nil {
		return nil
	}
	if p[0].After(p[1]) {
		return p[1]
	} else {
		return p[0]
	}
}

func (p ScanProgress) Ahead() *tree.ScanProgress {
	if p[0] == nil || p[1] == nil {
		return nil
	}
	if p[0].After(p[1]) {
		return p[0]
	} else {
		return p[1]
	}
}

type Replica struct {
	revision
	isChanged           bool // true if there was a change in generation (any file added/deleted/updated)
	isMetaChanged       bool // true if the metadata of any files changed
	isKnowledgeChanged  bool // true if the Knowledge header was updated
	knowledge           map[string]int
	rootEntry           *Entry
	options             textproto.MIMEHeader
	exclude             []string // paths to exclude
	include             []string // paths to not exclude
	follow              []string // paths that should not be treated as symlinks
	perms               tree.Mode
	startScan           time.Time
	scanned             uint64
	total               uint64
	progressSent        time.Time
	deviceIdMap         map[uint64]tree.Filesystem
	filesystemIdCounter tree.Filesystem
	lock                sync.Mutex
}

func ScanTree(fs tree.LocalFileTree, recvOptionsChan, sendOptionsChan chan *tree.ScanOptions, progress chan<- *tree.ScanProgress, cancel chan struct{}) (*Replica, error) {
	var replica *Replica
	file, err := fs.GetFile(STATUS_FILE)
	if tree.IsNotExist(err) {
		replica, err = LoadReplica(nil)
	} else if err != nil {
		return nil, err
	} else {
		defer file.Close()
		replica, err = LoadReplica(file)
	}
	if err != nil {
		return nil, err
	}

	options := replica.scanOptions()
	replica.addScanOptions(options)
	select {
	case sendOptionsChan <- options:
	case <-cancel:
		return nil, tree.ErrCancelled
	}

	var recvOptions *tree.ScanOptions
	select {
	case recvOptions = <-recvOptionsChan:
	case <-cancel:
		return nil, tree.ErrCancelled
	}
	if recvOptions.Replica == options.Replica {
		return nil, &ErrSameIdentity{options.Replica}
	}
	replica.addScanOptions(recvOptions)

	err = replica.scan(fs, cancel, progress)
	if err != nil {
		return nil, err
	}
	return replica, nil
}

func LoadReplica(file io.Reader) (*Replica, error) {
	r := &Replica{
		rootEntry: &Entry{
			fileType: tree.TYPE_DIRECTORY,
		},
		perms:       PERMS_DEFAULT,
		deviceIdMap: make(map[uint64]tree.Filesystem),
	}
	r.rootEntry.replica = r

	if file == nil {
		// This is a blank replica, create initial data.
		r.isChanged = true
		r.revision = revision{
			generation: 1,
			identity:   makeRandomString(24),
		}
		r.knowledge = make(map[string]int, 1)
		r.knowledge[r.identity] = r.generation

	} else {
		err := r.load(file)
		if err != nil {
			return nil, err
		}
	}

	r.rootEntry.revision = revision{r.identity, 1}

	return r, nil
}

func (r *Replica) String() string {
	return "Replica(" + r.identity + "," + strconv.Itoa(r.generation) + ")"
}

// Root returns the root entry.
func (r *Replica) Root() *Entry {
	return r.rootEntry
}

// Perms returns the permission map used for this replica (which permissions are
// used in synchronizing). 0777 by default, but can be changed by the "perms"
// option.
func (r *Replica) Perms() tree.Mode {
	return r.perms
}

func (r *Replica) markChanged() revision {
	if !r.isChanged {
		r.isChanged = true
		r.generation++
		r.knowledge[r.identity] = r.generation
	}
	return r.revision
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

func (r *Replica) mergeKnowledge(other *Replica) {
	for id, gen := range other.knowledge {
		if r.knowledge[id] < gen {
			r.isKnowledgeChanged = true
			r.knowledge[id] = gen
		}
	}
}

func (r *Replica) load(file io.Reader) error {
	if r.identity != "" {
		panic("replica already loaded")
	}

	reader := bufio.NewReader(file)

	// Check header magic string.
	line, err := reader.ReadString('\n')
	if err != nil {
		return parseBinError("could not read magic", err)
	}
	line = strings.TrimRight(line, "\n")

	switch line {
	case "Magic: " + MAGIC_TEXT:
		return r.loadText(reader)
	case MAGIC_PROTO:
		return r.loadProto(reader)
	default:
		return parseBinError("unexpected magic: "+line, nil)
	}
}

// loadText assumes this replica hasn't yet been loaded
func (r *Replica) loadText(reader *bufio.Reader) error {
	header, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return &ParseError{"cannot parse status file", 0, err}
	}

	if header.Get("Version") != "1" {
		return &ParseError{"unknown file version: '" + header.Get("Version") + "'", 0, nil}
	}

	hash := header.Get("Hash")
	if !(hash == "" || hash == HASH_ID) {
		return &ParseError{"unknown hash type: '" + hash + "'", 0, nil}
	}

	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return &ParseError{"cannot parse Content-Type header: " + contentType, 0, err}
	}
	if mediaType != "text/tab-separated-values" {
		return &ParseError{"invalid media type: " + mediaType, 0, nil}
	}
	if params["charset"] != "utf-8" {
		return &ParseError{"invalid charset: " + params["charset"], 0, nil}
	}

	identity := header.Get("Identity")
	if identity == "" {
		return &ParseError{"no Identity header", 0, nil}
	}
	r.identity = identity

	generationString := header.Get("Generation")
	if generationString == "" {
		return &ParseError{"missing generation string", 0, nil}
	}
	generation, err := strconv.Atoi(generationString)
	if err != nil {
		return &ParseError{"invalid generation string", 0, err}
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
				return &ParseError{"invalid Knowledge header: " + knowledgeString, 0, nil}
			}
			peerId := partParts[0]
			peerGen, err := strconv.Atoi(partParts[1])
			if err != nil {
				return &ParseError{"invalid Knowledge header: " + knowledgeString, 0, nil}
			}
			r.knowledge[peerId] = peerGen
			peers[i+1] = peerId
		}
	}

	// Parse root options, if they exist.
	rootOptions := header.Get("Root-Options")
	if rootOptions != "" {
		err := r.rootEntry.parseOptions(rootOptions)
		if err != nil {
			return &ParseError{"cannot parse root options (" + rootOptions + "): ", 0, err}
		}
	}

	const (
		TSV_PATH = iota
		TSV_FINGERPRINT
		TSV_REVISION
		TSV_MODE
		TSV_INODE
		TSV_HASH
		TSV_OPTIONS
	)

	tsvReader, err := unitsv.NewReader(reader, unitsv.Config{
		Required: []string{"path", "fingerprint", "revision"},
		Optional: []string{"mode", "inode", "hash", "options"},
	})
	if err != nil {
		return &ParseError{"could not read TSV header", 0, err}
	}

	// now actually parse the file list
	for row := 1; ; row++ {
		fields, err := tsvReader.ReadRow()
		if err != nil {
			if err == io.EOF {
				break
			}
			return &ParseError{"could not read TSV row", row, err}
		}
		hashStr := fields[TSV_HASH]
		var hash tree.Hash
		if len(hashStr) > 0 {
			if hashStr[0] == '@' {
				// symlink target
				hash = tree.Hash{tree.HASH_TARGET, []byte(hashStr[1:])}
			} else {
				if len(hashStr) == 43 {
					// In Go 1.5+, we can use base64.RawURLEncoding
					hashStr += "="
				}
				data, err := base64.URLEncoding.DecodeString(hashStr)
				if err != nil {
					return &ParseError{"could not decode hash", row, err}
				}
				hash = tree.Hash{tree.HASH_DEFAULT, data}
			}
		}

		revParts := strings.Split(fields[TSV_REVISION], ":")
		if len(revParts) != 2 {
			return &ParseError{"revision does not have exactly two parts", row, nil}
		}

		revReplicaIndex, err := strconv.Atoi(revParts[0])
		if err != nil {
			return &ParseError{"cannot parse replica index", row, err}
		}
		if revReplicaIndex < 0 || revReplicaIndex >= len(peers) {
			return &ParseError{"replica index outside range", row, nil}
		}

		revReplica := peers[revReplicaIndex]
		revGeneration, err := strconv.Atoi(revParts[1])
		if err != nil {
			return &ParseError{"cannot parse generation", row, err}
		} else if revGeneration < 1 {
			return &ParseError{"generation < 1", row, nil}
		}

		modeString := fields[TSV_MODE]
		var mode uint64
		if modeString != "" {
			mode, err = strconv.ParseUint(modeString, 8, 32)
			if err != nil {
				return &ParseError{"cannot parse mode", row, err}
			}
		}

		inodeString := fields[TSV_INODE]
		var inode uint64
		if inodeString != "" {
			inode, err = strconv.ParseUint(inodeString, 10, 64)
			if err != nil {
				return &ParseError{"cannot parse inode", row, err}
			}
		}

		fingerprint := fields[TSV_FINGERPRINT]
		path := strings.Split(fields[TSV_PATH], "/")

		// now add this entry
		child, err := r.rootEntry.addRecursive(path, revision{revReplica, revGeneration}, fingerprint, tree.Mode(mode), inode, hash, fields[TSV_OPTIONS])
		if err != nil {
			return &ParseError{"could not add row", row, err}
		}

		if modeString == "" {
			// We couldn't read a mode, so none of the mode bits are
			// 'supported'.
			child.hasMode = 0
		}

	}

	// Put all headers that start with Option- in the option list.
	r.options = make(textproto.MIMEHeader)
	for key, values := range header {
		if strings.HasPrefix(key, "Option-") {
			r.options[key[len("Option-"):]] = values
		}
	}

	return nil
}

func (e *Entry) parseOptions(s string) error {
	// split the options field in this simple key-value format:
	//    key1=value1,r=3
	options := make(map[string]string)
	for _, field := range strings.Split(s, ",") {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) == 2 {
			options[kv[0]] = kv[1]
		} else {
			options[kv[0]] = ""
		}
	}

	if removed, ok := options["removed"]; ok {
		var err error
		e.removed, err = time.Parse(time.RFC3339, removed)
		if err != nil {
			return err
		}
	}

	if hasModeString, ok := options["hasmode"]; ok {
		hasMode, err := strconv.ParseUint(hasModeString, 8, 32)
		if err != nil {
			return err
		}
		e.hasMode = tree.Mode(hasMode)
	}

	// TODO; duplicate code (see proto.go)
	if fsString, ok := options["fs"]; ok {
		_fs, err := strconv.ParseUint(fsString, 10, 64)
		if err != nil {
			return err
		}
		fs := tree.Filesystem(_fs)
		if fs > e.replica.filesystemIdCounter {
			e.replica.filesystemIdCounter = fs
		}
		e.fs = fs
	}

	return nil
}

func (r *Replica) Serialize(fs tree.Tree) error {
	var out tree.Copier
	var err error

	format := FORMAT_NONE
	switch fs := fs.(type) {
	case tree.LocalFileTree:
		format = FORMAT_TEXT
		out, err = fs.PutFile(STATUS_FILE)
	case tree.RemoteTree:
		format = FORMAT_PROTO
		out, err = fs.SendStatus()
	default:
		panic("unknown tree type")
	}
	if err != nil {
		return err
	}
	defer out.Cancel()

	err = r.SerializeStream(out, format)
	if err != nil {
		return err
	}

	_, _, err = out.Finish()
	return err
}

func (r *Replica) SerializeStream(out io.Writer, format int) error {
	switch format {
	case FORMAT_TEXT:
		return r.serializeText(out)
	case FORMAT_PROTO:
		return r.serializeProto(out)
	default:
		// programmer error
		panic("unknown format to serialize")
	}
}

func (r *Replica) serializeText(out io.Writer) error {
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

	rootOptions := r.rootEntry.serializeOptions()

	writer := bufio.NewWriter(out)
	// Don't look at the error return values, errors will be caught in .Flush().
	writeKeyValue(writer, "Magic", MAGIC_TEXT)
	writeKeyValue(writer, "Version", "1")
	writeKeyValue(writer, "Created-By", version.VERSION)
	writeKeyValue(writer, "Content-Type", "text/tab-separated-values; charset=utf-8")
	writeKeyValue(writer, "Identity", r.identity)
	writeKeyValue(writer, "Generation", strconv.Itoa(r.generation))
	writeKeyValue(writer, "Knowledge", strings.Join(knowledgeList, ","))
	writeKeyValue(writer, "Hash", HASH_ID)
	if rootOptions != "" {
		writeKeyValue(writer, "Root-Options", rootOptions)
	}
	keys := make([]string, 0, len(r.options))
	for key, _ := range r.options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range r.options[key] {
			writeKeyValue(writer, "Option-"+key, value)
		}
	}
	writer.WriteByte('\n')

	tsvWriter, err := unitsv.NewWriter(writer, []string{"path", "fingerprint", "inode", "mode", "hash", "revision", "options"})
	if err != nil {
		return err
	}

	err = r.rootEntry.serializeTextChildren(tsvWriter, peerIndex, "")
	if err != nil {
		return err
	}

	// unitsv uses a bufio internally. bufio.NewBuffer does not return a new
	// buffer if none is needed, so this call isn't really necessary.
	err = tsvWriter.Flush()
	if err != nil {
		return err
	}

	// only now look at I/0 errors
	return writer.Flush()
}

// Write the children of this entry recursively to the TSV file.
// It has this odd way of functioning (writing the children without writing
// itself) because that makes it easier to write the root entry without copying
// (DRY).
func (e *Entry) serializeTextChildren(tsvWriter *unitsv.Writer, peerIndex map[string]int, path string) error {
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
		if !child.hash.IsZero() {
			switch child.hash.Type {
			case tree.HASH_DEFAULT:
				hash = base64.URLEncoding.EncodeToString(child.hash.Data)
				// In Go 1.5+, we can use base64.RawURLEncoding
				hash = strings.TrimRight(hash, "=")
			case tree.HASH_TARGET:
				hash = "@" + string(child.hash.Data)
			}
		}

		inodeString := ""
		if child.inode != 0 {
			inodeString = strconv.FormatUint(child.inode, 10)
		}

		modeString := strconv.FormatUint(uint64(child.mode), 8)
		identity := strconv.Itoa(peerIndex[child.identity])
		generation := strconv.Itoa(child.generation)
		revString := identity + ":" + generation
		options := child.serializeOptions()

		err := tsvWriter.WriteRow([]string{childpath, serializeFingerprint(child), inodeString, modeString, hash, revString, options})
		if err != nil {
			return err
		}
		err = e.children[name].serializeTextChildren(tsvWriter, peerIndex, childpath)
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Entry) serializeOptions() string {
	var options []string
	if !e.removed.IsZero() {
		options = append(options, "removed="+e.removed.UTC().Format(time.RFC3339))
	}
	if e.isRoot() || e.hasMode != e.parent.hasMode {
		options = append(options, "hasmode="+strconv.FormatUint(uint64(e.hasMode), 8))
	}
	if e.fs > 0 && (e.isRoot() || e.fs != e.parent.fs) {
		options = append(options, "fs="+strconv.FormatUint(uint64(e.fs), 10))
	}
	return strings.Join(options, ",")
}

// Write a simple MIME key/value line to the output.
// The line ends in \n, while most text based protocols use \r\n.
func writeKeyValue(out *bufio.Writer, key, value string) error {
	_, err := out.WriteString(key + ": " + value + "\n")
	return err
}

func (r *Replica) scan(fs tree.LocalFileTree, cancel chan struct{}, progress chan<- *tree.ScanProgress) error {
	// Signal we're starting the scan.
	progress <- &tree.ScanProgress{
		Total: r.total,
		Done:  0,
		Path:  nil,
	}

	r.startScan = time.Now()
	r.scanned = 0
	r.progressSent = time.Time{}
	_, rootFilesystem := fs.Root().Id()
	r.Root().updateFilesystem(rootFilesystem, false)
	r.Root().hasMode = fs.Root().Info().HasMode()
	err := r.scanDir(fs.Root(), r.Root(), cancel, progress)
	if r.scanned != r.total {
		panic("scanned != total???")
	}

	// Signal we're done.
	progress <- &tree.ScanProgress{
		Total: r.total,
		Done:  r.scanned,
		Path:  nil,
	}
	close(progress)
	return err
}

// scanDir scans one side of the tree, updating the status tree to the current
// status.
func (r *Replica) scanDir(dir tree.Entry, statusDir *Entry, cancel chan struct{}, progress chan<- *tree.ScanProgress) error {
	fileList, err := dir.List(tree.ListOptions{
		Follow: func(parts []string) bool {
			return r.matchPatterns(parts[len(parts)-1], path.Join(parts...), r.follow)
		},
	})
	if err != nil {
		return err
	}
	iterator := nextFileStatus(fileList, statusDir.rawList())

	var file tree.Entry
	var status *Entry
	for {
		select {
		case <-cancel:
			return errCanceled
		default:
		}

		file, status = iterator()
		if file == nil && status == nil {
			break
		}

		if file != nil && r.isExcluded(file) {
			if status != nil {
				status.Remove()
			}
			continue
		}
		if file == nil {
			status.Remove()
			continue
		}

		now := time.Now()
		if now.Sub(r.progressSent) > 100*time.Millisecond {
			// Send progress without blocking (if it blocks, don't send it).
			select {
			case progress <- &tree.ScanProgress{
				Total: r.total,
				Done:  r.scanned,
				Path:  file.RelativePath(),
			}:
				r.progressSent = now
			default:
			}
		}

		r.scanned++

		if !status.exists() {
			// add status
			info, err := file.FullInfo()
			if err != nil {
				return err
			}
			status, err = statusDir.add(info, r.markChanged())
			if err != nil {
				panic(err) // must not happen
			}
		} else {
			// update status (if needed)
			oldHash := status.Hash()
			var newHash tree.Hash
			info := file.Info()
			if tree.MatchFingerprint(info, status) && !oldHash.IsZero() {
				// Assume the hash stayed the same when the fingerprint is the
				// same. But calculate a new hash if there is no hash.
				newHash = oldHash
			} else {
				newHash, err = file.Hash()
				if err != nil {
					return err
				}
			}
			_, fs := file.Id()
			status.update(info, fs, newHash, nil)
		}

		if file.Type() == tree.TYPE_DIRECTORY {
			err := r.scanDir(file, status, cancel, progress)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *Replica) isExcluded(file tree.Entry) bool {
	relpath := path.Join(file.RelativePath()...)
	return r.matchPatterns(file.Name(), relpath, r.exclude) && !r.matchPatterns(file.Name(), relpath, r.include)
}

func (r *Replica) matchPatterns(name, relpath string, patterns []string) bool {
	for _, pattern := range patterns {
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
			if match, err := path.Match(pattern, name); match && err == nil {
				return true
			}
		}
	}
	return false
}

func (r *Replica) scanOptions() *tree.ScanOptions {
	optionPerms := tree.Mode(PERMS_DEFAULT)
	if perms, err := strconv.ParseUint(r.options.Get("Perms"), 8, 32); err == nil {
		optionPerms = tree.Mode(perms)
	}
	return &tree.ScanOptions{
		r.options["Exclude"],
		r.options["Include"],
		r.options["Follow"],
		optionPerms,
		r.identity,
	}
}

func (r *Replica) addScanOptions(options *tree.ScanOptions) {
	r.exclude = append(r.exclude, options.Exclude...)
	r.include = append(r.include, options.Include...)
	r.follow = append(r.follow, options.Follow...)
	r.perms &= options.Perms
}

// nextFilesystemId returns the next unknown device ID, which is always bigger
// than 0 and the biggerst known device ID.
func (r *Replica) nextFilesystemId() tree.Filesystem {
	r.filesystemIdCounter++
	return r.filesystemIdCounter
}

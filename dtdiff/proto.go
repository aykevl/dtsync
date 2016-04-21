// proto.go
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

//go:generate protoc --go_out=. messages.proto

package dtdiff

import (
	"bufio"
	"io"
	"net/textproto"
	"sort"
	"strconv"
	"time"

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/golang/protobuf/proto"
)

// readLength returns the next varint it can read from the reader, without
// sacrificing bytes.
//
// This function was copied from tree/remote/remote.go.
func readLength(r *bufio.Reader) (uint64, error) {
	lengthBuf := make([]byte, 0, 8)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		lengthBuf = append(lengthBuf, b)
		x, n := proto.DecodeVarint(lengthBuf)
		if n != 0 {
			return x, nil
		}
	}
}

// loadProto assumes this replica hasn't yet been loaded
func (r *Replica) loadProto(reader *bufio.Reader) error {
	buffer := &proto.Buffer{}

	// Load the replica header.
	m := &ProtoReplica{}
	length, err := readLength(reader)
	buf := make([]byte, length)
	_, err = io.ReadFull(reader, buf)
	if err != nil {
		return parseBinError("could not read length of ProtoReplica message", err)
	}
	buffer.SetBuf(buf)
	err = buffer.Unmarshal(m)
	if err != nil {
		return parseBinError("ProtoReplica message: could not parse", err)
	}

	if m.Version == nil {
		return parseBinError("ProtoReplica message: no version set", nil)
	} else if *m.Version != 1 {
		return parseBinError("ProtoReplica message: version "+strconv.Itoa(int(*m.Version))+" unknown", nil)
	}

	if m.Identity == nil || *m.Identity == "" || m.Generation == nil {
		return parseBinError("ProtoReplica message: no identity or generation set", nil)
	}
	r.identity = *m.Identity
	r.generation = int(*m.Generation)
	if uint64(r.generation) != *m.Generation {
		return parseBinError("ProtoReplica message: generation too big", nil)
	}

	if len(m.KnowledgeKeys) != len(m.KnowledgeValues) {
		return parseBinError("ProtoReplica message: knowledge keys length does not equal values length", nil)
	}

	r.knowledge = make(map[string]int, len(m.KnowledgeKeys))
	for i, key := range m.KnowledgeKeys {
		r.knowledge[key] = int(m.KnowledgeValues[i])
		if uint64(r.knowledge[key]) != m.KnowledgeValues[i] {
			return parseBinError("ProtoReplica message: generation in knowledge too big", nil)
		}
	}
	r.knowledge[r.identity] = r.generation

	r.options = make(textproto.MIMEHeader)
	if len(m.OptionKeys) != len(m.OptionValues) {
		return parseBinError("ProtoReplica message: option keys length does not equal values length", nil)
	}
	for i, key := range m.OptionKeys {
		r.options[key] = append(r.options[key], m.OptionValues[i])
	}

	err = r.rootEntry.loadProto(reader, buffer, m.KnowledgeKeys)
	if err != nil {
		return err
	}

	// Check that we're indeed at the end of the stream.
	_, err = reader.ReadByte()
	if err == nil {
		return parseBinError("expected EOF, got no error", nil)
	} else if err != io.EOF {
		return parseBinError("expected EOF, got:", err)
	}
	return nil
}

func (e *Entry) loadProto(reader *bufio.Reader, buffer *proto.Buffer, knowledgeKeys []string) error {
	m := &ProtoEntry{}
	length, err := readLength(reader)
	buf := make([]byte, length)
	_, err = io.ReadFull(reader, buf)
	if err != nil {
		return parseBinError("could not read length of ProtoEntry message", err)
	}
	buffer.SetBuf(buf)
	err = buffer.Unmarshal(m)
	if err != nil {
		return parseBinError("ProtoEntry message: could not parse", err)
	}

	if e.isRoot() {
		if m.HasMode != nil {
			e.hasMode = tree.Mode(m.GetHasMode())
		}
	} else {
		e.name = m.GetName()
		e.fileType = tree.Type(m.GetType())
		e.modTime = time.Unix(0, m.GetModTime())
		if m.Identity == nil {
			return parseBinError("ProtoEntry message: missing identity", nil)
		} else if int(*m.Identity)-1 >= len(knowledgeKeys) {
			return parseBinError("ProtoEntry message: identity outside range", nil)
		}
		if *m.Identity == 0 {
			e.identity = e.replica.identity
		} else {
			e.identity = knowledgeKeys[*m.Identity-1]
		}
		e.generation = int(m.GetGeneration())
		if uint64(e.generation) != m.GetGeneration() {
			return parseBinError("ProtoEntry message: generation overflow", nil)
		}
		e.mode = tree.Mode(m.GetMode())
		if m.HasMode != nil {
			e.hasMode = tree.Mode(m.GetHasMode())
		} else {
			e.hasMode = e.parent.hasMode
		}
		e.hash.Type = tree.HashType(m.GetHashType())
		e.hash.Data = m.HashData
		if m.Removed != nil {
			e.removed = time.Unix(*m.Removed, 0)
		}
	}
	e.size = int64(m.GetSize())

	if e.fileType == tree.TYPE_DIRECTORY && e.size > 0 {
		e.children = make(map[string]*Entry, e.size)
		for i := int64(0); i < e.size; i++ {
			child := &Entry{
				replica: e.replica,
				parent:  e,
			}
			err = child.loadProto(reader, buffer, knowledgeKeys)
			if err != nil {
				return err
			}
			if _, ok := e.children[child.name]; ok {
				return parseBinError("ProtoEntry message: duplicate child", nil)
			}
			e.children[child.name] = child
		}
	}

	return nil
}

// SerializeProto writes a stream of protobuf data to stream out containing
// everything that's inside this Replica.
func (r *Replica) SerializeProto(out io.Writer) error {
	writer := bufio.NewWriter(out)

	writer.WriteString(MAGIC_PROTO + "\n")

	knowledgeKeys := make([]string, 0, len(r.knowledge)-1)
	for key, _ := range r.knowledge {
		if key == r.identity {
			continue
		}
		knowledgeKeys = append(knowledgeKeys, key)
	}
	sort.Strings(knowledgeKeys)
	knowledgeValues := make([]uint64, len(knowledgeKeys))
	knowledgeMap := make(map[string]int, len(r.knowledge))
	knowledgeMap[r.identity] = 0
	for i, key := range knowledgeKeys {
		knowledgeValues[i] = uint64(r.knowledge[key])
		knowledgeMap[key] = i + 1
	}

	optionKeyList := make([]string, 0, len(r.options))
	for key, _ := range r.options {
		optionKeyList = append(optionKeyList, key)
	}
	sort.Strings(optionKeyList)

	var optionKeys []string
	var optionValues []string
	for _, key := range optionKeyList {
		for _, value := range r.options[key] {
			optionKeys = append(optionKeys, key)
			optionValues = append(optionValues, value)
		}
	}

	createdBy := version.VERSION
	hash := HASH_ID
	m := &ProtoReplica{
		Version:         proto.Uint32(1),
		CreatedBy:       &createdBy,
		Identity:        &r.identity,
		Generation:      proto.Uint64(uint64(r.generation)),
		KnowledgeKeys:   knowledgeKeys,
		KnowledgeValues: knowledgeValues,
		OptionKeys:      optionKeys,
		OptionValues:    optionValues,
		Hash:            &hash,
	}

	buffer := &proto.Buffer{}
	err := buffer.EncodeMessage(m)
	if err != nil {
		panic(err)
	}

	_, err = writer.Write(buffer.Bytes())
	if err != nil {
		return err
	}

	err = r.rootEntry.serializeProto(writer, buffer, knowledgeMap)
	if err != nil {
		return err
	}
	return writer.Flush()
}

func (e *Entry) serializeProto(writer *bufio.Writer, buffer *proto.Buffer, knowledgeMap map[string]int) error {
	m := &ProtoEntry{}

	if e.isRoot() {
		if e.hasMode != 0 {
			m.HasMode = proto.Uint32(uint32(e.hasMode))
		}

	} else {
		m.Name = &e.name
		m.Type = proto.Uint32(uint32(e.fileType))
		m.ModTime = proto.Int64(e.modTime.UnixNano())
		m.Identity = proto.Uint32(uint32(knowledgeMap[e.identity]))
		if int(*m.Identity) != knowledgeMap[e.identity] {
			// This is actually theoretically possible on valid input, but only
			// when syncing a much too large set of replicas.
			panic("identity overflow")
		}
		m.Generation = proto.Uint64(uint64(e.generation))

		m.Mode = proto.Uint32(uint32(e.mode))
		if e.hasMode != e.parent.hasMode {
			m.HasMode = proto.Uint32(uint32(e.hasMode))
		}
		m.HashType = proto.Uint32(uint32(e.hash.Type))
		m.HashData = e.hash.Data
		if !e.removed.IsZero() {
			m.Removed = proto.Int64(e.removed.Unix())
		}
	}

	var children []*Entry
	switch e.fileType {
	case tree.TYPE_REGULAR:
		m.Size = proto.Uint64(uint64(e.Size()))
	case tree.TYPE_DIRECTORY:
		children = e.rawList()
		m.Size = proto.Uint64(uint64(len(children)))
	}

	buffer.Reset()
	err := buffer.EncodeMessage(m)
	if err != nil {
		panic(err)
	}

	_, err = writer.Write(buffer.Bytes())
	if err != nil {
		return err
	}

	for _, child := range children {
		err = child.serializeProto(writer, buffer, knowledgeMap)
		if err != nil {
			return err
		}
	}

	return nil
}

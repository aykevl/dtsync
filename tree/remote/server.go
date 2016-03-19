// server.go
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
	"bufio"
	"io"
	"strings"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/golang/protobuf/proto"
)

// replyResponse has all the information to easily send a reply as a protobuf
// message. The fields requestId and err are present to more easily create a
// response, they are simply stored in the *Response if not present.
type replyResponse struct {
	requestId uint64
	msg       *Response
	err       error
}

// Server is an instance of the server side of a dtsync connection. It receives
// commands and processes them, but does not initiate anything.
type Server struct {
	reader      io.ReadCloser
	writer      io.WriteCloser
	fs          tree.LocalFileTree
	replyChan   chan replyResponse
	currentScan chan *ScanOptions
}

// NewServer returns a *Server for the given reader, writer, and filesystem
// tree. It does not start communication: start .Run() in a separate goroutine
// for that.
func NewServer(r io.ReadCloser, w io.WriteCloser, fs tree.LocalFileTree) *Server {
	s := &Server{
		reader:    r,
		writer:    w,
		fs:        fs,
		replyChan: make(chan replyResponse), // may not be buffered for synchronisation
	}
	return s
}

// Run runs in a goroutine, until the server experiences a fatal (connection)
// error. It is used to handle concurrent requests and responses.
func (s *Server) Run() error {
	r := bufio.NewReader(s.reader)
	w := bufio.NewWriter(s.writer)

	w.WriteString("dtsync server: " + version.VERSION + "\n")
	err := w.Flush()
	if err != nil {
		return err
	}

	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "dtsync client: ") {
		return ErrNoClient
	}

	recvStreams := make(map[uint64]chan []byte)

	readChan := make(chan receivedData)
	go runReceiver(r, readChan)

	// TODO: some goroutines might hang after returning an error

	for {
		select {
		case reply := <-s.replyChan:
			err := s.doReply(w, reply)
			if err != nil {
				return err
			}
		case recv := <-readChan:
			if recv.err != nil {
				if recv.err == io.EOF {
					debugLog("S: CLOSING")
					// Closing an already-closed pipe might give errors (but we
					// don't know, so ignore).
					err2 := s.reader.Close()
					if err2 != nil {
						debugLog("S: error on reader close:", err2)
					}
					return s.writer.Close()
				}
				return recv.err
			}
			msg := &Request{}
			err = proto.Unmarshal(recv.buf, msg)
			if err != nil {
				return err
			}
			var err error
			var requestId uint64
			if msg.Command == nil || msg.RequestId == nil {
				// send error to requestId 0
				err = invalidRequest{"command or requestId not set"}
			} else {
				requestId = *msg.RequestId
				debugLog("S: recv command", requestId, Command_name[int32(*msg.Command)])
				err = s.handleRequest(msg, recvStreams)
			}
			if err != nil {
				if err2 := s.doReply(w, replyResponse{requestId, nil, err}); err2 != nil {
					// Error while sending an error reply.
					return err2
				}
			}
		}
	}
}

func (s *Server) handleRequest(msg *Request, recvStreams map[uint64]chan []byte) error {
	switch *msg.Command {
	case Command_DATA:
		stream, ok := recvStreams[*msg.RequestId]
		if !ok {
			return invalidRequest{"unknown stream"}
		}
		if msg.GetStatus() < DataStatus_NORMAL || msg.GetStatus() > DataStatus_CANCEL {
			return invalidRequest{"DataStatus is outside range NORMAL-CANCEL"}
		}
		if msg.Data != nil {
			stream <- msg.Data
		}
		switch msg.GetStatus() {
		case DataStatus_NORMAL:
			// data already sent
		case DataStatus_FINISH:
			close(stream)
		case DataStatus_CANCEL:
			stream <- nil
			close(stream)
		default:
			// Might happen when messages.proto is extended.
			panic("unknown status")
		}
	case Command_SCAN:
		if s.currentScan != nil {
			return invalidRequest{"SCAN while a scan is already running"}
		}
		s.currentScan = make(chan *ScanOptions)
		go s.scan(*msg.RequestId, s.currentScan)
	case Command_SCANOPTS:
		if msg.Data == nil {
			return invalidRequest{"SCANSTATUS expects data field (ScanOptions)"}
		}
		if s.currentScan == nil {
			return invalidRequest{"SCANSTATUS outside running scan or sending SCANSTATUS twice"}
		}
		status := &ScanOptions{}
		err := proto.Unmarshal(msg.Data, status)
		if err != nil {
			return err
		}
		s.currentScan <- status
		s.currentScan = nil
	case Command_MKDIR:
		// Client wants to create a directory.
		if msg.FileInfo1 == nil || msg.Name == nil {
			return invalidRequest{"MKDIR expects fileInfo1 and name fields"}
		}
		go s.mkdir(*msg.RequestId, *msg.Name, msg.FileInfo1)
	case Command_REMOVE:
		// Client wants to create a directory.
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil || msg.FileInfo1.Type == nil {
			return invalidRequest{"REMOVE expects fileInfo1 with path and type"}
		}
		go s.remove(*msg.RequestId, msg.FileInfo1)
	case Command_GETFILE:
		if msg.Name == nil {
			return invalidRequest{"GETFILE expects name field"}
		}
		go s.getFile(*msg.RequestId, *msg.Name)
	case Command_SETFILE:
		if msg.Name == nil {
			return invalidRequest{"SETFILE expects name field"}
		}
		dataChan := make(chan []byte, 1)
		go s.setFile(*msg.RequestId, *msg.Name, dataChan)
		recvStreams[*msg.RequestId] = dataChan
	case Command_CREATE, Command_UPDATE:
		// Client wants to write a file.
		if msg.FileInfo1 == nil || msg.FileInfo2 == nil {
			return invalidRequest{"CREATE/UPDATE expects fileInfo1 and fileInfo2"}
		}
		if *msg.Command == Command_CREATE && msg.Name == nil {
			return invalidRequest{"CREATE expects name to be set"}
		}
		dataChan := make(chan []byte, 1)
		recvStreams[*msg.RequestId] = dataChan
		if *msg.Command == Command_CREATE {
			go s.create(*msg.RequestId, *msg.Name, msg.FileInfo1, msg.FileInfo2, dataChan)
		} else {
			go s.update(*msg.RequestId, msg.FileInfo1, msg.FileInfo2, dataChan)
		}
	case Command_COPYSRC:
		// Client requests a file.
		if msg.FileInfo1 == nil || msg.FileInfo1.Type == nil || msg.FileInfo1.Path == nil ||
			msg.FileInfo1.ModTime == nil || msg.FileInfo1.Size == nil {
			return invalidRequest{"COPYSRC expects fileInfo1 with type, path, modTime and size fields"}
		}
		go s.copySource(*msg.RequestId, msg.FileInfo1)
	case Command_ADDFILE:
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil || msg.Data == nil {
			return invalidRequest{"ADDFILE expects fileInfo1 with path and data"}
		}
		go s.addFile(*msg.RequestId, msg.FileInfo1.Path, msg.Data)
	case Command_SETCONTENTS:
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil || msg.Data == nil {
			return invalidRequest{"SETCONT expects fileInfo1 with path and data"}
		}
		go s.setContents(*msg.RequestId, msg.FileInfo1.Path, msg.Data)
	case Command_INFO:
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil {
			return invalidRequest{"INFO expects fileInfo1 with path"}
		}
		go s.readInfo(*msg.RequestId, msg.FileInfo1.Path)
	default:
		return invalidRequest{"unknown command"}
	}
	return nil
}

// doReply writes the Response to the pipe.
func (s *Server) doReply(w *bufio.Writer, reply replyResponse) error {
	requestId := reply.requestId
	var msg *Response
	if reply.msg != nil {
		msg = reply.msg
	} else {
		msg = &Response{}
	}
	msg.RequestId = &requestId
	if reply.err != nil {
		errString := reply.err.Error()
		msg.Error = &errString
	}
	if msg.Command != nil {
		debugLog("S: send reply  ", *msg.RequestId, Command_name[int32(*msg.Command)])
	} else {
		debugLog("S: send reply  ", *msg.RequestId)
	}

	buf, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	w.Write(proto.EncodeVarint(uint64(len(buf))))
	w.Write(buf)
	return w.Flush()
}

// replyError is a shorthand for sending an error to the client.
func (s *Server) replyError(requestId uint64, err error) {
	s.replyChan <- replyResponse{requestId, nil, err}
}

func (s *Server) setFile(requestId uint64, name string, dataChan chan []byte) {
	defer func() {
		// Drain the dataChan (on errors), so the receiver won't block there.
		for _ = range dataChan {
		}
	}()

	writer, err := s.fs.SetFile(name)
	if err != nil {
		s.replyError(requestId, err)
		return
	}
	for buf := range dataChan {
		_, err := writer.Write(buf)
		if err != nil {
			s.replyError(requestId, err)
			return
		}
	}
	err = writer.Close()
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	// Success!
	s.replyChan <- replyResponse{requestId, nil, nil}
}

func (s *Server) addFile(requestId uint64, path []string, data []byte) {
	if len(path) == 0 {
		s.replyError(requestId, ErrInvalidPath)
		return
	}

	fs, ok := s.fs.(tree.TestTree)
	if !ok {
		s.replyError(requestId, ErrNoTests)
		return
	}

	info, err := fs.AddRegular(path, data)
	s.replyInfo(requestId, info, err)
}

func (s *Server) setContents(requestId uint64, path []string, data []byte) {
	if len(path) == 0 {
		s.replyError(requestId, ErrInvalidPath)
		return
	}

	fs, ok := s.fs.(tree.TestTree)
	if !ok {
		s.replyError(requestId, ErrNoTests)
		return
	}

	info, err := fs.SetContents(path, data)
	s.replyInfo(requestId, info, err)
}

func (s *Server) readInfo(requestId uint64, path []string) {
	if len(path) == 0 {
		s.replyError(requestId, ErrInvalidPath)
		return
	}

	fs, ok := s.fs.(tree.TestTree)
	if !ok {
		s.replyError(requestId, ErrNoTests)
		return
	}

	info, err := fs.ReadInfo(path)
	s.replyInfo(requestId, info, err)
}

// copySource streams the file back to the client, but checks the FileInfo
// first.
func (s *Server) copySource(requestId uint64, fileInfo *FileInfo) {
	info := parseFileInfo(fileInfo)

	switch info.Type() {
	case tree.TYPE_REGULAR:
		reader, err := s.fs.CopySource(info)
		if err != nil {
			s.replyError(requestId, err)
			return
		}
		s.streamSendData(requestId, reader)
	default:
		s.replyError(requestId, tree.ErrNoRegular)
	}
}

func (s *Server) getFile(requestId uint64, name string) {
	reader, err := s.fs.GetFile(name)
	if err != nil {
		s.replyError(requestId, err)
	} else {
		s.streamSendData(requestId, reader)
	}
}

func (s *Server) create(requestId uint64, name string, parentInfo, sourceInfo *FileInfo, dataChan chan []byte) {
	parent := parseFileInfo(parentInfo)
	source := parseFileInfo(sourceInfo)
	cp, err := s.fs.CreateFile(name, parent, source)
	s.handleCopier(requestId, dataChan, cp, err)
}

func (s *Server) update(requestId uint64, fileInfo, sourceInfo *FileInfo, dataChan chan []byte) {
	file := parseFileInfo(fileInfo)
	source := parseFileInfo(sourceInfo)
	cp, err := s.fs.UpdateFile(file, source)
	s.handleCopier(requestId, dataChan, cp, err)
}

func (s *Server) handleCopier(requestId uint64, dataChan chan []byte, cp tree.Copier, err error) {
	if err != nil {
		s.replyError(requestId, err)
	}

	// Drain channel when there are errors.
	defer func() {
		for _ = range dataChan {
		}
	}()

	for block := range dataChan {
		_, err := cp.Write(block)
		if err != nil {
			s.replyError(requestId, err)
			return
			// TODO cp.Revert()?
		}
	}

	newFileInfo, newParentInfo, err := cp.Finish()
	if err != nil {
		s.replyError(requestId, err)
	} else {
		s.replyChan <- replyResponse{
			requestId,
			&Response{
				FileInfo:   serializeFileInfo(newFileInfo),
				ParentInfo: serializeFileInfo(newParentInfo),
			},
			nil,
		}
	}
}

func (s *Server) mkdir(requestId uint64, name string, parentInfo *FileInfo) {
	parent := parseFileInfo(parentInfo)
	info, err := s.fs.CreateDir(name, parent)
	s.replyInfo(requestId, info, err)
}

func (s *Server) remove(requestId uint64, fileInfo *FileInfo) {
	file := parseFileInfo(fileInfo)
	parentInfo, err := s.fs.Remove(file)
	s.replyInfo(requestId, parentInfo, err)
}

func (s *Server) replyInfo(requestId uint64, info tree.FileInfo, err error) {
	if err != nil {
		s.replyError(requestId, err)
	} else {
		s.replyChan <- replyResponse{
			requestId,
			&Response{
				FileInfo: serializeFileInfo(info),
			},
			nil,
		}
	}
}

func (s *Server) scan(requestId uint64, optionsChan chan *ScanOptions) {
	recvOptions := make(chan tree.ScanOptions)
	sendOptions := make(chan tree.ScanOptions)

	go func() {
		options := <-optionsChan
		recvOptions <- tree.NewScanOptions(options.Ignore)
	}()

	go func() {
		options := <-sendOptions
		optionsMsg := &ScanOptions{
			Ignore: options.Ignore(),
		}
		optionsData, err := proto.Marshal(optionsMsg)
		if err != nil {
			panic(err) // programming error?
		}
		command := Command_SCANOPTS
		s.replyChan <- replyResponse{requestId, &Response{
			Command: &command,
			Data:    optionsData,
		}, nil}
	}()

	replica, err := dtdiff.ScanTree(s.fs, recvOptions, sendOptions)
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	// Write to a file and to the client at the same time.
	writer1, err := s.fs.SetFile(dtdiff.STATUS_FILE)
	reader, writer2 := io.Pipe()
	writer := io.MultiWriter(writer1, writer2)

	if err != nil {
		s.replyError(requestId, err)
		// TODO: writer1.Revert()
		writer1.Close()
		writer2.Close()
	} else {
		go func() {
			err = replica.Serialize(writer)
			if err != nil {
				s.replyError(requestId, err)
			}
			writer1.Close()
			writer2.Close()
		}()
		s.streamSendData(requestId, reader)
	}
}

func (s *Server) streamSendData(requestId uint64, reader io.Reader) {
	// TODO: what if the client cancels?
	buf1 := make([]byte, 16*1024)
	var buf2 []byte
	commandDATA := Command_DATA
	// Depend on the non-buffered behavior of channels for synchronisation.
	// Use two buffers to read and send at the same time.
	for {
		for i, buf := range [][]byte{buf1, buf2} {
			if i == 1 && buf == nil {
				// buf2 hasn't yet been initialized
				buf2 = make([]byte, len(buf1))
				buf = buf2
			}
			n, err := reader.Read(buf)
			s.replyChan <- replyResponse{
				requestId,
				&Response{
					Command: &commandDATA,
					Data:    buf[:n],
				},
				nil,
			}
			if err == io.EOF {
				// Send empty message (reply with success).
				s.replyChan <- replyResponse{requestId, nil, nil}
				return
			}
			if err != nil {
				s.replyError(requestId, err)
				return
			}
		}
	}
}

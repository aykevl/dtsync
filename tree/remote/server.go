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
	"sync"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/aykevl/golibrsync/librsync"
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
	reader    io.ReadCloser
	writer    io.WriteCloser
	fs        tree.LocalFileTree
	replyChan chan replyResponse
	jobMutex  sync.Mutex
	job       *serverJob
	running   bool
}

type serverJob struct {
	command  Command
	scanOpts chan []byte
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

func (s *Server) getJob() *serverJob {
	s.jobMutex.Lock()
	defer s.jobMutex.Unlock()
	return s.job
}

func (s *Server) setJob(j *serverJob) bool {
	s.jobMutex.Lock()
	defer s.jobMutex.Unlock()
	ok := s.job == nil
	s.job = j
	return ok
}

// Run runs in a goroutine, until the server experiences a fatal (connection)
// error. It is used to handle concurrent requests and responses.
func (s *Server) Run() error {
	// Lightweight check that the server isn't run twice
	if s.running {
		panic("Server: running twice at the same time")
	}
	s.running = true

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
					// don't know, so exclude).
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
				if *msg.Command == Command_DATA {
					debugLog("S: recv command", requestId, "DATA", DataStatus_name[int32(msg.GetStatus())], len(msg.Data))
				} else {
					debugLog("S: recv command", requestId, Command_name[int32(*msg.Command)])
				}
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
		job := &serverJob{
			command:  Command_SCAN,
			scanOpts: make(chan []byte, 1),
		}
		if !s.setJob(job) {
			return invalidRequest{"SCAN while a job is already running"}
		}
		go s.scan(*msg.RequestId, job)
	case Command_SCANOPTS:
		if msg.Data == nil {
			return invalidRequest{"SCANOPTS expects data field (ScanOptions)"}
		}
		job := s.getJob()
		if job == nil || job.command != Command_SCAN {
			return invalidRequest{"SCANOPTS outside running scan"}
		}
		// When scanOpts is full, it will block, but that only happens when
		// sending SCANOPTS for the second or third time.
		select {
		case job.scanOpts <- msg.Data:
		default:
		}
	case Command_PUTSTATE:
		dataChan := make(chan []byte, 1)
		recvStreams[*msg.RequestId] = dataChan
		// TODO: locking
		// TODO: make sure we're not accidentally overwriting another sync that
		// happened inbetween.
		go s.putState(*msg.RequestId, dataChan)
	case Command_MKDIR:
		// Client wants to create a directory.
		if msg.FileInfo1 == nil || msg.Name == nil || msg.FileInfo2 == nil {
			return invalidRequest{"MKDIR expects fileInfo1, FileInfo2 and name fields"}
		}
		go s.mkdir(*msg.RequestId, *msg.Name, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2))
	case Command_REMOVE:
		// Client wants to create a directory.
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil || msg.FileInfo1.Type == nil {
			return invalidRequest{"REMOVE expects fileInfo1 with path and type"}
		}
		go s.remove(*msg.RequestId, parseFileInfo(msg.FileInfo1))
	case Command_COPY_DST:
		// Client wants to write a file.
		if msg.FileInfo1 == nil || msg.FileInfo2 == nil {
			return invalidRequest{"COPY_SRC expects fileInfo1 and fileInfo2"}
		}
		dataChan := make(chan []byte, 1)
		recvStreams[*msg.RequestId] = dataChan
		if msg.GetName() != "" {
			go s.create(*msg.RequestId, msg.GetName(), parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2), dataChan)
		} else {
			go s.update(*msg.RequestId, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2), dataChan)
		}
	case Command_CREATELINK, Command_UPDATELINK:
		// Client wants to create a file.
		if msg.FileInfo1 == nil || msg.FileInfo2 == nil || msg.Data == nil {
			return invalidRequest{"CREATELINK/UPDATELINK expects fileInfo1, fileInfo2 and data"}
		}
		if *msg.Command == Command_CREATELINK {
			if msg.Name == nil {
				return invalidRequest{"CREATELINK expects name to be set"}
			}
			go s.createSymlink(*msg.RequestId, *msg.Name, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2), string(msg.Data))
		} else {
			go s.updateSymlink(*msg.RequestId, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2), string(msg.Data))
		}
	case Command_READLINK:
		// Client wants to read the link target of a link (readlink).
		if msg.FileInfo1 == nil {
			return invalidRequest{"READLINK expects fileInfo1 to be set"}
		}
		go s.readSymlink(*msg.RequestId, parseFileInfo(msg.FileInfo1))
	case Command_COPY_SRC:
		// Client requests a file.
		if msg.FileInfo1 == nil || msg.FileInfo1.Type == nil || msg.FileInfo1.Path == nil ||
			msg.FileInfo1.ModTime == nil || msg.FileInfo1.Size == nil {
			return invalidRequest{"COPY_SRC expects fileInfo1 with type, path, modTime and size fields"}
		}
		go s.copySource(*msg.RequestId, parseFileInfo(msg.FileInfo1))
	case Command_CHMOD:
		// Client wants to chmod a file.
		if msg.FileInfo1 == nil || msg.FileInfo1.Type == nil || msg.FileInfo1.Path == nil ||
			msg.FileInfo1.ModTime == nil || msg.FileInfo1.Size == nil || msg.FileInfo1.Mode == nil || msg.FileInfo1.HasMode == nil ||
			msg.FileInfo2 == nil || msg.FileInfo2.Mode == nil || msg.FileInfo2.HasMode == nil {
			return invalidRequest{"CHMOD expects fileInfo1 with type, path, modTime, size, mode and hasMode fields, and fileInfo2 with mode and hasMode fields"}
		}
		go s.chmod(*msg.RequestId, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2))
	case Command_RSYNC_SRC:
		if msg.FileInfo1 == nil {
			return invalidRequest{"RSYNC_SRC expects fileInfo1"}
		}
		dataChan := make(chan []byte, 1)
		recvStreams[*msg.RequestId] = dataChan
		go s.rsyncSrc(*msg.RequestId, parseFileInfo(msg.FileInfo1), dataChan)
	case Command_RSYNC_DST:
		if msg.FileInfo1 == nil || msg.FileInfo2 == nil {
			return invalidRequest{"RSYNC_DST expects fileInfo1 and fileInfo2"}
		}
		dataChan := make(chan []byte, 1)
		recvStreams[*msg.RequestId] = dataChan
		go s.rsyncDst(*msg.RequestId, parseFileInfo(msg.FileInfo1), parseFileInfo(msg.FileInfo2), dataChan)
	case Command_PUTFILE_TEST:
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil || msg.Data == nil {
			return invalidRequest{"PUTFILE_TEST expects fileInfo1 with path and data"}
		}
		go s.putFileTest(*msg.RequestId, msg.FileInfo1.Path, msg.Data)
	case Command_INFO:
		if msg.FileInfo1 == nil || msg.FileInfo1.Path == nil {
			return invalidRequest{"INFO expects fileInfo1 with path"}
		}
		go s.readInfo(*msg.RequestId, msg.FileInfo1.Path)
	default:
		return invalidRequest{"unknown command " + Command_name[int32(*msg.Command)]}
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
		msg.Error = encodeRemoteError(reply.err)
	}
	if msg.Command != nil {
		if *msg.Command == Command_DATA {
			debugLog("S: send reply  ", *msg.RequestId, Command_name[int32(*msg.Command)], DataStatus_name[int32(msg.GetStatus())], len(msg.Data))
		} else {
			debugLog("S: send reply  ", *msg.RequestId, Command_name[int32(*msg.Command)])
		}
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

func (s *Server) putFileTest(requestId uint64, path []string, data []byte) {
	if len(path) == 0 {
		s.replyError(requestId, ErrInvalidPath)
		return
	}

	fs, ok := s.fs.(tree.TestTree)
	if !ok {
		s.replyError(requestId, ErrNoTests)
		return
	}

	info, err := fs.PutFileTest(path, data)
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
func (s *Server) copySource(requestId uint64, info tree.FileInfo) {
	switch info.Type() {
	case tree.TYPE_REGULAR:
		reader, err := s.fs.CopySource(info)
		if err != nil {
			s.replyError(requestId, err)
			return
		}
		s.streamSendData(requestId, reader, true)
	default:
		s.replyError(requestId, tree.ErrNoRegular(info.RelativePath()))
	}
}

func (s *Server) rsyncSrc(requestId uint64, info tree.FileInfo, sigChan chan []byte) {
	// Drain channel when there are errors.
	defer func() {
		for _ = range sigChan {
		}
	}()

	sigReader, sigWriter := io.Pipe()
	go func() {
		for block := range sigChan {
			if block == nil {
				sigWriter.CloseWithError(tree.ErrCancelled)
				return
			}
			_, err := sigWriter.Write(block)
			if err != nil {
				// This only happens when the other end is closed (possibly
				// with an error), so it should never happen.
				panic(err)
			}
		}
		sigWriter.Close()
	}()
	sig, err := librsync.LoadSignature(sigReader)
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	switch info.Type() {
	case tree.TYPE_REGULAR:
		reader, err := s.fs.CopySource(info)
		if err != nil {
			s.replyError(requestId, err)
			return
		}

		delta, err := librsync.NewDeltaGen(sig, reader)
		if err != nil {
			s.replyError(requestId, err)
			return
		}

		s.streamSendData(requestId, delta, true)
	default:
		s.replyError(requestId, tree.ErrNoRegular(info.RelativePath()))
	}
}

func (s *Server) rsyncDst(requestId uint64, info, source tree.FileInfo, deltaChan chan []byte) {
	// Drain channel when there are errors.
	defer func() {
		for _ = range deltaChan {
		}
	}()

	basis, copier, err := s.fs.UpdateRsync(info, source)
	if err != nil {
		s.replyError(requestId, err)
		return
	}
	defer basis.Close()
	defer copier.Cancel()

	sigJob, err := librsync.NewDefaultSignatureGen(basis)
	if err != nil {
		s.replyError(requestId, err)
		return
	}
	defer sigJob.Close()

	if !s.streamSendData(requestId, sigJob, false) {
		// an error occured (which was sent back)
		return
	}

	deltaReader, deltaWriter := io.Pipe()

	patchJob, err := librsync.NewPatcher(deltaReader, basis)
	if err != nil {
		s.replyError(requestId, err)
		return
	}
	defer patchJob.Close()

	go func() {
		for block := range deltaChan {
			if block == nil {
				deltaWriter.CloseWithError(tree.ErrCancelled)
				return
			}
			_, err := deltaWriter.Write(block)
			if err != nil {
				// This only happens when the other end is closed (possibly
				// with an error), so it should never happen.
				panic(err)
			}
		}
		deltaWriter.Close()
	}()

	newFileInfo, newParentInfo, err := tree.CopyFile(copier, patchJob, source, nil)
	if err != nil {
		s.replyError(requestId, err)
		return
	}

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

func (s *Server) create(requestId uint64, name string, parent, source tree.FileInfo, dataChan chan []byte) {
	cp, err := s.fs.CreateFile(name, parent, source)
	s.handleCopier(requestId, dataChan, cp, err)
}

func (s *Server) update(requestId uint64, file, source tree.FileInfo, dataChan chan []byte) {
	cp, err := s.fs.UpdateFile(file, source)
	s.handleCopier(requestId, dataChan, cp, err)
}

func (s *Server) handleCopier(requestId uint64, dataChan chan []byte, cp tree.Copier, err error) {
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	// Drain channel when there are errors.
	defer func() {
		for _ = range dataChan {
		}
	}()

	for block := range dataChan {
		if block == nil {
			cp.Cancel()
			s.replyChan <- replyResponse{requestId, nil, nil}
			return
		}
		_, err := cp.Write(block)
		if err != nil {
			s.replyError(requestId, err)
			_ = cp.Cancel() // we can only reply one error
			return
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

func (s *Server) createSymlink(requestId uint64, name string, parent, source tree.FileInfo, contents string) {
	info, parentInfo, err := s.fs.CreateSymlink(name, parent, source, contents)
	s.replyInfo2(requestId, info, parentInfo, err)
}

func (s *Server) updateSymlink(requestId uint64, file, source tree.FileInfo, contents string) {
	info, parentInfo, err := s.fs.UpdateSymlink(file, source, contents)
	s.replyInfo2(requestId, info, parentInfo, err)
}

func (s *Server) readSymlink(requestId uint64, file tree.FileInfo) {
	contents, err := s.fs.ReadSymlink(file)
	if err != nil {
		s.replyError(requestId, err)
	} else {
		s.replyChan <- replyResponse{
			requestId,
			&Response{
				Data: []byte(contents),
			},
			nil,
		}
	}
}

func (s *Server) mkdir(requestId uint64, name string, parent, source tree.FileInfo) {
	info, err := s.fs.CreateDir(name, parent, source)
	s.replyInfo(requestId, info, err)
}

func (s *Server) remove(requestId uint64, file tree.FileInfo) {
	parentInfo, err := s.fs.Remove(file)
	s.replyInfo(requestId, parentInfo, err)
}

func (s *Server) chmod(requestId uint64, target, source tree.FileInfo) {
	info, err := s.fs.Chmod(target, source)
	s.replyInfo(requestId, info, err)
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

func (s *Server) replyInfo2(requestId uint64, file, parent tree.FileInfo, err error) {
	if err != nil {
		s.replyError(requestId, err)
	} else {
		s.replyChan <- replyResponse{
			requestId,
			&Response{
				FileInfo:   serializeFileInfo(file),
				ParentInfo: serializeFileInfo(parent),
			},
			nil,
		}
	}
}

func (s *Server) scan(requestId uint64, job *serverJob) {
	recvOptions := make(chan *tree.ScanOptions)
	sendOptions := make(chan *tree.ScanOptions)
	progressChan := make(chan *tree.ScanProgress)

	cancel := make(chan struct{})

	go func() {
		var optionsBuf []byte
		select {
		case optionsBuf = <-job.scanOpts:
		case <-cancel:
			return
		}
		options, err := parseScanOptions(optionsBuf)
		if err != nil {
			s.replyError(requestId, err)
			return
		}
		recvOptions <- options
	}()

	go func() {
		optionsData := serializeScanOptions(<-sendOptions)
		command := Command_SCANOPTS
		s.replyChan <- replyResponse{requestId, &Response{
			Command: &command,
			Data:    optionsData,
		}, nil}
	}()

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for progress := range progressChan {
			data, err := proto.Marshal(&ScanProgress{
				Total: &progress.Total,
				Done:  &progress.Done,
				Path:  progress.Path,
			})
			if err != nil {
				panic(err) // programming error?
			}
			command := Command_SCANPROG
			s.replyChan <- replyResponse{requestId, &Response{
				Command: &command,
				Data:    data,
			}, nil}
		}
	}()

	replica, err := dtdiff.ScanTree(s.fs, recvOptions, sendOptions, progressChan, cancel)
	<-progressDone
	if err != nil {
		close(cancel)
		s.setJob(nil)
		s.replyError(requestId, err)
		return
	}

	fileDone := make(chan error)
	go func() {
		fileDone <- replica.Serialize(s.fs)
	}()

	sendReader, sendWriter := io.Pipe()
	sendDone := make(chan error)
	go func() {
		err := replica.SerializeStream(sendWriter, dtdiff.FORMAT_PROTO)
		sendWriter.CloseWithError(err)
		sendDone <- err
	}()

	s.streamSendData(requestId, sendReader, false)
	fileErr := <-fileDone
	sendErr := <-sendDone
	s.setJob(nil)
	if fileErr != nil {
		close(cancel)
		s.replyError(requestId, fileErr)
	} else if sendErr != nil {
		close(cancel)
		s.replyError(requestId, sendErr)
	} else {
		s.replyChan <- replyResponse{requestId, nil, nil}
	}
}

func (s *Server) streamSendData(requestId uint64, reader io.Reader, finish bool) bool {
	// TODO: what if the client cancels?
	commandDATA := Command_DATA
	// Depend on the non-buffered behavior of channels for synchronisation.
	// Use two buffers to read and send at the same time.
	bigbuf := make([]byte, 32*1024)
	bufs := [2][]byte{bigbuf[:16*1024], bigbuf[16*1024:]}
	for i := 0; ; i++ {
		buf := bufs[i%2]
		n, err := reader.Read(buf)
		if err == io.EOF {
			status := DataStatus_FINISH
			s.replyChan <- replyResponse{
				requestId,
				&Response{
					Command: &commandDATA,
					Data:    buf[:n],
					Status:  &status,
				},
				nil,
			}
			if finish {
				// Send empty message (reply with success).
				s.replyChan <- replyResponse{requestId, nil, nil}
			}
			return true
		}
		s.replyChan <- replyResponse{
			requestId,
			&Response{
				Command: &commandDATA,
				Data:    buf[:n],
			},
			nil,
		}
		if err != nil {
			s.replyError(requestId, err)
			return false
		}
	}
}

func (s *Server) putState(requestId uint64, dataChan chan []byte) {
	reader, writer := io.Pipe()
	go func() {
		for block := range dataChan {
			if block == nil {
				writer.CloseWithError(tree.ErrCancelled)
				return
			}

			// Ignore errors, we need to drain this channel anyway.
			_, _ = writer.Write(block)
		}
		writer.Close()
	}()

	// Load replica first, to convert to a different format on-disk, and to
	// check for consistency.
	replica, err := dtdiff.LoadReplica(reader)
	if err != nil {
		reader.CloseWithError(err)
		s.replyError(requestId, err)
		return
	}

	copier, err := s.fs.PutFile(dtdiff.STATUS_FILE)
	if err != nil {
		s.replyError(requestId, err)
		return
	}
	defer copier.Cancel()

	err = replica.SerializeStream(copier, dtdiff.FORMAT_TEXT)
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	_, _, err = copier.Finish()
	if err != nil {
		s.replyError(requestId, err)
		return
	}

	s.replyChan <- replyResponse{requestId, nil, nil}
}

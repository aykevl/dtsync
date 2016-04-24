// client.go
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

	"github.com/aykevl/dtsync/tree"
	"github.com/aykevl/dtsync/version"
	"github.com/golang/protobuf/proto"
)

// roundtripRequest is one *Request message received in run(), with some
// additional fields for the response.
type roundtripRequest struct {
	req        *Request
	idChan     chan uint64
	replyChan  chan roundtripResponse
	recvBlocks chan recvBlock
}

// roundtripResponse is one response received in run().
type roundtripResponse struct {
	resp *Response
	err  error
}

// sendBlock is one block of data to send to an open request.
type sendBlock struct {
	stream uint64
	data   []byte
	status DataStatus
}

// recvBlock is one block received from an open request.
type recvBlock struct {
	stream uint64
	data   []byte
	err    error
}

type scanJob struct {
	scanOptions  chan []byte
	scanProgress chan []byte
}

// Client implements the command issuing side of a dtsync connection. Requests
// may be done in parallel, but they must not affect the same file (or parent
// directory).
type Client struct {
	sendRequest chan roundtripRequest
	sendBlocks  chan sendBlock
	w           io.WriteCloser
	closeWait   chan struct{}
	*scanJob
	scanJobMutex sync.Mutex
}

// NewClient writes the connection header and returns a new *Client. It also
// starts a background goroutine to synchronize requests and responses.
func NewClient(r io.ReadCloser, w io.WriteCloser) (*Client, error) {
	c := &Client{
		sendRequest: make(chan roundtripRequest),
		sendBlocks:  make(chan sendBlock),
		w:           w,
		closeWait:   make(chan struct{}),
	}

	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)

	writer.WriteString("dtsync client: " + version.VERSION + "\n")
	err := writer.Flush()
	if err != nil {
		return nil, err
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "dtsync server: ") {
		return nil, ErrNoServer
	}

	go c.run(reader, writer)
	return c, nil
}

// String returns a simple representation for debugging.
func (c *Client) String() string {
	// TODO: can we print some more?
	return "remote.Client{...}"
}

func (c *Client) getScanJob() *scanJob {
	c.scanJobMutex.Lock()
	defer c.scanJobMutex.Unlock()
	return c.scanJob
}

func (c *Client) setScanJob(j *scanJob) bool {
	c.scanJobMutex.Lock()
	defer c.scanJobMutex.Unlock()
	ok := c.scanJob == nil
	c.scanJob = j
	return ok
}

// This is the background goroutine, to synchronize concurrent access. It
// listens to new requests and received responses.
func (c *Client) run(r *bufio.Reader, w *bufio.Writer) {
	readChan := make(chan receivedData)
	go runReceiver(r, readChan)

	inflight := make(map[uint64]chan roundtripResponse)
	recvStreams := make(map[uint64]chan recvBlock)
	var nextId uint64
	var pipeErr error

	for {
		select {
		case req := <-c.sendRequest:
			if pipeErr != nil {
				req.idChan <- 0 // signal error
				// Make sure the next request will get the error message.
				if req.replyChan != nil {
					req.replyChan <- roundtripResponse{nil, pipeErr}
				}
				continue
			}

			nextId++ // start at 1
			id := nextId
			req.req.RequestId = &id
			if req.replyChan != nil {
				inflight[id] = req.replyChan
			}
			req.idChan <- id

			debugLog("C: send command", *req.req.RequestId, Command_name[int32(*req.req.Command)])

			buf, err := proto.Marshal(req.req)
			if err != nil {
				panic(err) // programming error?
			}

			if req.recvBlocks != nil {
				recvStreams[id] = req.recvBlocks
			}

			w.Write(proto.EncodeVarint(uint64(len(buf))))
			w.Write(buf)
			err = w.Flush()
			if err != nil {
				if req.replyChan != nil {
					req.replyChan <- roundtripResponse{nil, err}
				}
				continue
			}

		case block := <-c.sendBlocks:
			command := Command_DATA
			stream := block.stream
			msg := &Request{
				Command:   &command,
				RequestId: &stream,
			}
			debugLog("C: send DATA   ", stream, Command_name[int32(command)], DataStatus_name[int32(block.status)])
			if block.status != DataStatus_NORMAL {
				status := block.status
				msg.Status = &status
			} else {
				msg.Data = block.data
			}

			buf, err := proto.Marshal(msg)
			if err != nil {
				// I *think* this is a programming error.
				panic(err)
			}

			w.Write(proto.EncodeVarint(uint64(len(buf))))
			if block.status == DataStatus_NORMAL {
				// Not reached the end.
				_, err = w.Write(buf)
			} else {
				// Only flush at the end of the stream.
				w.Write(buf)
				err = w.Flush()
			}
			if err != nil {
				pipeErr = err
			}

		case data := <-readChan:
			if data.err != nil {
				if data.err == io.EOF {
					close(c.closeWait) // all reads will continue immediately
					return
				}
				debugLog("C: read err:", data.err)
				// All following request will return this error.
				pipeErr = data.err
				// TODO: close all open requests, with this error.
				continue
			}
			msg := &Response{}
			err := proto.Unmarshal(data.buf, msg)
			if err != nil {
				pipeErr = err
				continue
			}
			if msg.RequestId == nil {
				pipeErr = ErrInvalidResponse("no requestId")
				continue
			}
			if msg.Command != nil {
				switch *msg.Command {
				case Command_DATA:
					debugLog("C: recv        ", *msg.RequestId, "DATA")
					streamChan, ok := recvStreams[*msg.RequestId]
					if !ok {
						pipeErr = ErrInvalidResponse("DATA: unknown stream ID")
						continue
					}
					if msg.Data != nil {
						streamChan <- recvBlock{*msg.RequestId, msg.Data, nil}
					}
					if msg.GetStatus() != DataStatus_NORMAL {
						switch msg.GetStatus() {
						case DataStatus_FINISH:
							// do nothing
						case DataStatus_CANCEL:
							// send error status
							streamChan <- recvBlock{*msg.RequestId, nil, tree.ErrCancelled}
						default:
							// Might happen when messages.proto is extended.
							pipeErr = ErrInvalidResponse("DataStatus is outside range NORMAL-CANCEL")
							continue
						}
						close(streamChan)
						delete(recvStreams, *msg.RequestId)
					}
				case Command_SCANOPTS:
					debugLog("C: recv reply  ", *msg.RequestId, "SCANOPTS")
					job := c.getScanJob()
					if job == nil {
						pipeErr = ErrInvalidResponse("SCANOPTS: no scan running")
						continue
					}
					// Do not send when one is being processed currently
					// already.
					select {
					case job.scanOptions <- msg.Data:
					default:
						pipeErr = ErrInvalidResponse("SCANOPTS: no scan running")
					}
				case Command_SCANPROG:
					debugLog("C: recv reply  ", *msg.RequestId, "SCANPROG")
					job := c.getScanJob()
					if job == nil {
						pipeErr = ErrInvalidResponse("SCANPROG: no scan running")
						continue
					}
					job.scanProgress <- msg.Data
				default:
					pipeErr = ErrInvalidResponse("unknown command " + Command_name[int32(*msg.Command)])
					continue
				}
			} else {
				debugLog("C: recv reply  ", *msg.RequestId)
				replyChan, ok := inflight[*msg.RequestId]
				if !ok {
					debugLog("C: invalid RequestId", *msg.RequestId)
					pipeErr = ErrInvalidId
					continue
				}
				delete(inflight, *msg.RequestId)

				if msg.Error != nil {
					if stream, ok := recvStreams[*msg.RequestId]; ok {
						stream <- recvBlock{*msg.RequestId, nil, decodeRemoteError(msg.Error)}
					}

					replyChan <- roundtripResponse{nil, decodeRemoteError(msg.Error)}
				} else {
					replyChan <- roundtripResponse{msg, nil}
				}

				if streamChan, ok := recvStreams[*msg.RequestId]; ok {
					close(streamChan)
					delete(recvStreams, *msg.RequestId)
				}
			}
		}
	}
}

// Close closes the connection, terminating the background goroutines.
func (c *Client) Close() error {
	debugLog("\nC: Close")

	err := c.w.Close()
	if err != nil {
		return err
	}
	<-c.closeWait
	return nil
}

// RemoteScan runs a remote scan command and returns an io.Reader with the new
// status file.
func (c *Client) RemoteScan(sendOptions, recvOptions chan *tree.ScanOptions, recvProgress chan<- *tree.ScanProgress, cancel chan struct{}) (io.ReadCloser, error) {
	debugLog("\nC: RemoteScan")

	job := &scanJob{
		scanOptions:  make(chan []byte, 1),
		scanProgress: make(chan []byte, 1),
	}
	if !c.setScanJob(job) {
		// a scan job is already set
		return nil, ErrConcurrentScan
	}

	commandScan := Command_SCAN
	request := &Request{
		Command: &commandScan,
	}
	reader, writer := io.Pipe()
	stream := &streamReader{
		reader: reader,
		done:   make(chan struct{}),
	}
	ch, err := c.handleReply(request, nil, writer)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(stream.done)
		respData := <-ch
		c.setScanJob(nil)
		close(job.scanProgress)
		if respData.err != nil {
			stream.setError(respData.err)
		}
	}()

	go func() {
		// Send excluded files from the other replica to the remote replica.
		optionsData := serializeScanOptions(<-sendOptions)
		commandScanOptions := Command_SCANOPTS
		statusRequest := &Request{
			Command: &commandScanOptions,
			Data:    optionsData,
		}
		c.sendRequest <- roundtripRequest{statusRequest, make(chan uint64, 1), nil, nil}
	}()

	go func() {
		options, err := parseScanOptions(<-job.scanOptions)
		if err != nil {
			stream.setError(err)
			recvOptions <- nil
			return
		}
		recvOptions <- options
	}()

	go func() {
		for data := range job.scanProgress {
			progress := &ScanProgress{}
			err := proto.Unmarshal(data, progress)
			if err != nil {
				stream.setError(err)
				return
			}

			// Non-blocking send, in case the receiver has already stopped
			// listening.
			select {
			case recvProgress <- &tree.ScanProgress{
				Total: progress.GetTotal(),
				Done:  progress.GetDone(),
				Path:  progress.Path,
			}:
			default:
			}
		}
		close(recvProgress)
	}()

	return stream, nil
}

// SendStatus creates a Copier that sends status data (encoded as any format,
// but probably the protobuf serialization), and the remote writes it to a
// .dtsync file or similar.
func (c *Client) SendStatus() (tree.Copier, error) {
	command := Command_PUTSTATE
	request := &Request{
		Command: &command,
	}

	return c.sendStream(request)
}

func (c *Client) CreateDir(name string, parent, source tree.FileInfo) (tree.FileInfo, error) {
	debugLog("\nC: CreateDir")
	return c.returnsFileInfo(Command_MKDIR, &name, parent, source)
}

func (c *Client) Remove(file tree.FileInfo) (tree.FileInfo, error) {
	debugLog("\nC: Remove")
	return c.returnsFileInfo(Command_REMOVE, nil, file, nil)
}

// Chmod sends a chmod request to the remote server.
func (c *Client) Chmod(target, source tree.FileInfo) (tree.FileInfo, error) {
	debugLog("\nC: Chmod")
	command := Command_CHMOD
	return c.requestReturnsFileInfo(&Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(target),
		FileInfo2: serializeFileInfo(source),
	})
}

func (c *Client) returnsFileInfo(command Command, name *string, file, source tree.FileInfo) (tree.FileInfo, error) {
	request := &Request{
		Command:   &command,
		Name:      name,
		FileInfo1: serializeFileInfo(file),
	}
	if source != nil {
		request.FileInfo2 = serializeFileInfo(source)
	}
	return c.requestReturnsFileInfo(request)
}

func (c *Client) requestReturnsFileInfo(request *Request) (tree.FileInfo, error) {
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return nil, err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil {
		return nil, ErrInvalidResponse("missing fileInfo with type and modTime")
	}
	return parseFileInfo(resp.FileInfo), nil
}

func (c *Client) CreateFile(name string, parent, source tree.FileInfo) (tree.Copier, error) {
	debugLog("\nC: CreateFile")
	return c.sendFile(name, parent, source)
}

func (c *Client) UpdateFile(file, source tree.FileInfo) (tree.Copier, error) {
	debugLog("\nC: UpdateFile")
	return c.sendFile("", file, source)
}

func (c *Client) sendFile(name string, fileInfo1, fileInfo2 tree.FileInfo) (tree.Copier, error) {
	command := Command_COPY_DST
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(fileInfo1),
		FileInfo2: serializeFileInfo(fileInfo2),
	}
	if name != "" {
		request.Name = &name
	}

	return c.sendStream(request)
}

func (c *Client) sendStream(request *Request) (tree.Copier, error) {
	reader, writer := io.Pipe()
	ch, err := c.handleReply(request, reader, nil)
	if err != nil {
		return nil, err
	}

	cp := &copier{
		w:    writer,
		done: make(chan struct{}),
	}

	go func() {
		defer close(cp.done)
		respData := <-ch
		resp, err := respData.resp, respData.err
		if err != nil {
			cp.setError(err)
			return
		}
		cp.fileInfo = parseFileInfo(resp.FileInfo)
		cp.parentInfo = parseFileInfo(resp.ParentInfo)
	}()

	return cp, nil
}

// CreateSymlink creates a new symbolic link on the remote tree.
func (c *Client) CreateSymlink(name string, parentInfo, sourceInfo tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	debugLog("\nC: CreateSymlink")
	command := Command_CREATELINK
	request := &Request{
		Command:   &command,
		Name:      &name,
		FileInfo1: serializeFileInfo(parentInfo),
		FileInfo2: serializeFileInfo(sourceInfo),
		Data:      []byte(contents),
	}
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, nil, err
	}
	return parseFileInfo(resp.FileInfo), parseFileInfo(resp.ParentInfo), nil
}

// UpdateSymlink updates a symbolic link on the remote tree to the new path (contents).
func (c *Client) UpdateSymlink(file, source tree.FileInfo, contents string) (tree.FileInfo, tree.FileInfo, error) {
	debugLog("\nC: UpdateSymlink")
	command := Command_UPDATELINK
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(file),
		FileInfo2: serializeFileInfo(source),
		Data:      []byte(contents),
	}
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, nil, err
	}
	return parseFileInfo(resp.FileInfo), parseFileInfo(resp.ParentInfo), nil
}

// ReadSymlink reads the symlink from the remote tree.
func (c *Client) ReadSymlink(file tree.FileInfo) (string, error) {
	debugLog("\nC: ReadSymlink")
	command := Command_READLINK
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(file),
	}
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return "", err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return "", err
	}
	if resp.Data == nil {
		return "", ErrInvalidResponse("reading symlink with no data")
	}
	return string(respData.resp.Data), nil
}

func (c *Client) CopySource(info tree.FileInfo) (io.ReadCloser, error) {
	debugLog("\nC: CopySource")
	command := Command_COPY_SRC
	return c.recvFile(&Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(info),
	})
}

// recvFile sends a request and returns a stream to receive a file.
func (c *Client) recvFile(request *Request) (*streamReader, error) {
	reader, writer := io.Pipe()
	stream := &streamReader{
		reader: reader,
		done:   make(chan struct{}),
	}
	ch, err := c.handleReply(request, nil, writer)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(stream.done)
		respData := <-ch
		if respData.err != nil {
			stream.setError(respData.err)
		}
	}()
	return stream, nil
}

// RsyncSrc sends a signature to the server and receives a patch file, to
// update a local file using the rsync algorithm.
func (c *Client) RsyncSrc(file tree.FileInfo, signature io.Reader) (io.ReadCloser, error) {
	debugLog("\nC: RsyncSrc")
	command := Command_RSYNC_SRC
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(file),
	}
	deltaReader, deltaWriter := io.Pipe()
	ch, err := c.handleReply(request, signature, deltaWriter)
	if err != nil {
		return nil, err
	}
	stream := &streamReader{
		reader:    deltaReader,
		done:      make(chan struct{}),
		mustClose: true,
	}
	go func() {
		defer close(stream.done)
		respData := <-ch
		if respData.err != nil {
			deltaWriter.CloseWithError(respData.err)
			stream.setError(respData.err)
		}
	}()
	return stream, nil
}

// RsyncDst requests a signature from the server and sends a patch file, to
// update a remote file using the rsync algorithm.
func (c *Client) RsyncDst(file, source tree.FileInfo) (io.Reader, tree.Copier, error) {
	debugLog("\nC: RsyncDst")
	command := Command_RSYNC_DST
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(file),
		FileInfo2: serializeFileInfo(source),
	}

	deltaReader, deltaWriter := io.Pipe()
	sigReader, sigWriter := io.Pipe()
	ch, err := c.handleReply(request, deltaReader, sigWriter)
	if err != nil {
		return nil, nil, err
	}

	cp := &copier{
		w:    deltaWriter,
		done: make(chan struct{}),
	}

	go func() {
		defer close(cp.done)
		respData := <-ch
		resp, err := respData.resp, respData.err
		if err != nil {
			cp.setError(err)
			return
		}
		cp.fileInfo = parseFileInfo(resp.FileInfo)
		cp.parentInfo = parseFileInfo(resp.ParentInfo)
	}()
	return sigReader, cp, nil
}

func (c *Client) PutFileTest(path []string, contents []byte) (tree.FileInfo, error) {
	debugLog("\nC: PutFileTest")
	if contents == nil {
		contents = []byte{}
	}
	command := Command_PUTFILE_TEST
	request := &Request{
		Command: &command,
		FileInfo1: &FileInfo{
			Path: path,
		},
		Data: contents,
	}
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return nil, err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil || resp.FileInfo.Size == nil {
		return nil, ErrInvalidResponse("PUTFILE: no fileInfo with type, modTime and size")
	}
	return parseFileInfo(resp.FileInfo), nil
}

// ReadInfo returns the FileInfo for the specified file.
func (c *Client) ReadInfo(path []string) (tree.FileInfo, error) {
	debugLog("\nC: ReadInfo")
	command := Command_INFO
	request := &Request{
		Command: &command,
		FileInfo1: &FileInfo{
			Path: path,
		},
	}
	ch, err := c.handleReply(request, nil, nil)
	if err != nil {
		return nil, err
	}
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil || resp.FileInfo.Size == nil {
		return nil, ErrInvalidResponse("ReadInfo: no fileInfo with type, modTime and size")
	}
	return parseFileInfo(resp.FileInfo), nil
}

func (c *Client) handleReply(request *Request, sendStream io.Reader, recvStream *io.PipeWriter) (chan roundtripResponse, error) {
	replyChan := make(chan roundtripResponse, 1)
	idChan := make(chan uint64)
	var recvBlocks chan recvBlock
	if recvStream != nil {
		recvBlocks = make(chan recvBlock)
	}

	c.sendRequest <- roundtripRequest{request, idChan, replyChan, recvBlocks}
	id := <-idChan // also makes sure the request is being sent
	if id == 0 {
		// there is an error
		respData := <-replyChan
		if respData.err == nil {
			panic("error expected")
		}
		return nil, respData.err
	}

	var sendCancel chan struct{}
	var sendFinished, recvFinished chan error

	// Stream from here to remote.
	if sendStream != nil {
		sendCancel = make(chan struct{}, 1)
		sendFinished = make(chan error, 1)
		go func() {
			bigbuf := make([]byte, 32*1024)
			bufs := [2][]byte{bigbuf[:16*1024], bigbuf[16*1024:]}
			for i := 0; ; i++ {
				buf := bufs[i%2]
				n, err := sendStream.Read(buf)
				if err != nil && err != io.EOF {
					c.sendBlocks <- sendBlock{id, nil, DataStatus_CANCEL}
					if err == tree.ErrCancelled {
						sendFinished <- nil
						return
					}
					sendFinished <- err
					return
				}

				select {
				case <-sendCancel:
					c.sendBlocks <- sendBlock{id, nil, DataStatus_CANCEL}
					sendFinished <- nil
					return
				default:
				}

				if err == io.EOF {
					b := buf[:n]
					if n == 0 {
						b = nil
					}
					c.sendBlocks <- sendBlock{id, b, DataStatus_FINISH}
					sendFinished <- nil
					return
				}
				c.sendBlocks <- sendBlock{id, buf[:n], DataStatus_NORMAL}
			}
		}()
	}

	// Stream from remote to here.
	if recvStream != nil {
		recvFinished = make(chan error, 1)
		go func() {
			for block := range recvBlocks {
				if block.err != nil {
					recvStream.CloseWithError(block.err)
					recvFinished <- block.err
					return
				}
				_, err := recvStream.Write(block.data)
				if err != nil {
					recvFinished <- err
					return
				}
			}
			recvFinished <- recvStream.Close()
		}()
	}

	returnChan := make(chan roundtripResponse, 2)
	go func() {
		for {
			select {
			case respData := <-replyChan:
				if sendStream != nil {
					if respData.err != nil {
						sendCancel <- struct{}{}
					}
					err := <-sendFinished
					if err != nil {
						returnChan <- roundtripResponse{nil, err}
						return
					}
				}

				if recvStream != nil {
					err := <-recvFinished
					if err != nil {
						returnChan <- roundtripResponse{nil, err}
						return
					}
				}

				returnChan <- respData
				return
			case err := <-sendFinished:
				if err != nil {
					returnChan <- roundtripResponse{nil, err}
				}
				sendStream = nil
			case err := <-recvFinished:
				if err != nil {
					returnChan <- roundtripResponse{nil, err}
				}
				recvStream = nil
			}
		}
	}()
	return returnChan, nil
}

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

// Client implements the command issuing side of a dtsync connection. Requests
// may be done in parallel, but they must not affect the same file (or parent
// directory).
type Client struct {
	sendRequest chan roundtripRequest
	sendBlocks  chan sendBlock
	scanOptions chan []byte
	w           io.WriteCloser
	closeWait   chan struct{}
}

// NewClient writes the connection header and returns a new *Client. It also
// starts a background goroutine to synchronize requests and responses.
func NewClient(r io.ReadCloser, w io.WriteCloser) (*Client, error) {
	c := &Client{
		sendRequest: make(chan roundtripRequest),
		sendBlocks:  make(chan sendBlock),
		scanOptions: make(chan []byte),
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

// This is the background goroutine, to synchronize concurrent access. It
// listens to new requests and received responses.
func (c *Client) run(r *bufio.Reader, w *bufio.Writer) {
	readChan := make(chan receivedData)
	go runReceiver(r, readChan)

	inflight := make(map[uint64]chan roundtripResponse)
	recvStreams := make(map[uint64]chan recvBlock)
	scanIsRunning := false
	var nextId uint64
	var pipeErr error

	for {
		select {
		case req := <-c.sendRequest:
			if pipeErr != nil {
				// Make sure the next request will get the error message.
				if req.replyChan != nil {
					req.replyChan <- roundtripResponse{nil, pipeErr}
				}
				continue
			}

			nextId++
			id := nextId
			req.req.RequestId = &id
			if req.replyChan != nil {
				inflight[id] = req.replyChan
			}
			req.idChan <- id

			debugLog("C: send command", *req.req.RequestId, Command_name[int32(*req.req.Command)])

			if *req.req.Command == Command_SCAN {
				if scanIsRunning {
					req.replyChan <- roundtripResponse{nil, ErrConcurrentScan}
					continue
				}
				scanIsRunning = true
			}

			buf, err := proto.Marshal(req.req)
			if err != nil {
				panic(err) // programming error?
				continue
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
				pipeErr = ErrInvalidResponse
				continue
			}
			if msg.Command != nil {
				switch *msg.Command {
				case Command_DATA:
					debugLog("C: recv        ", *msg.RequestId, "DATA")
					streamChan, ok := recvStreams[*msg.RequestId]
					if !ok {
						pipeErr = ErrInvalidResponse
						continue
					}
					streamChan <- recvBlock{*msg.RequestId, msg.Data, nil}
				case Command_SCANOPTS:
					debugLog("C: recv reply  ", *msg.RequestId, "SCANOPTS")
					if !scanIsRunning {
						pipeErr = ErrInvalidResponse
						continue
					}
					c.scanOptions <- msg.Data
					scanIsRunning = false // not entirely true
				default:
					pipeErr = ErrInvalidResponse
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
func (c *Client) RemoteScan(sendOptions, recvOptions chan tree.ScanOptions) (io.Reader, error) {
	debugLog("\nC: RemoteScan")
	commandScan := Command_SCAN
	stream, _ := c.recvFile(&Request{
		Command: &commandScan,
	})

	go func() {
		// Send ignored files from the other replica to the remote replica.
		options := <-sendOptions
		optionsData, err := proto.Marshal(&ScanOptions{
			Ignore: options.Ignore(),
		})
		if err != nil {
			panic(err) // programming error?
		}
		commandScanOptions := Command_SCANOPTS
		statusRequest := &Request{
			Command: &commandScanOptions,
			Data:    optionsData,
		}
		c.sendRequest <- roundtripRequest{statusRequest, make(chan uint64, 1), nil, nil}
	}()

	go func() {
		data := <-c.scanOptions
		options := &ScanOptions{}
		err := proto.Unmarshal(data, options)
		if err != nil {
			stream.setError(err)
			recvOptions <- nil
			return
		}
		recvOptions <- tree.NewScanOptions(options.Ignore)
	}()

	return stream, nil
}

func (c *Client) CreateDir(name string, parent tree.FileInfo) (tree.FileInfo, error) {
	debugLog("\nC: CreateDir")
	return c.returnsParent(Command_MKDIR, &name, parent)
}

func (c *Client) Remove(file tree.FileInfo) (tree.FileInfo, error) {
	debugLog("\nC: Remove")
	return c.returnsParent(Command_REMOVE, nil, file)
}

func (c *Client) returnsParent(command Command, name *string, file tree.FileInfo) (tree.FileInfo, error) {
	request := &Request{
		Command:   &command,
		Name:      name,
		FileInfo1: serializeFileInfo(file),
	}
	ch := c.handleReply(request, nil, nil)
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil {
		return nil, ErrInvalidResponse
	}
	return parseFileInfo(resp.FileInfo), nil
}

func (c *Client) GetFile(name string) (io.ReadCloser, error) {
	debugLog("\nC: GetFile")
	command := Command_GETFILE
	return c.recvFile(&Request{
		Command: &command,
		Name:    &name,
	})
}

func (c *Client) SetFile(name string) (io.WriteCloser, error) {
	debugLog("\nC: SetFile")
	command := Command_SETFILE
	request := &Request{
		Command: &command,
		Name:    &name,
	}
	reader, writer := io.Pipe()
	ch := c.handleReply(request, reader, nil)
	finish := make(chan struct{})
	stream := &streamWriter{
		writer: writer,
		finish: finish,
	}
	go func() {
		respData := <-ch
		_, err := respData.resp, respData.err
		if err != nil {
			stream.setError(err)
		} else {
			reader.Close()
		}
		finish <- struct{}{}
	}()
	return stream, nil
}

func (c *Client) CreateFile(name string, parent, source tree.FileInfo) (tree.Copier, error) {
	debugLog("\nC: CreateFile")
	command := Command_CREATE
	request := &Request{
		Command:   &command,
		Name:      &name,
		FileInfo1: serializeFileInfo(parent),
		FileInfo2: serializeFileInfo(source),
	}

	return c.handleFileSend(request)
}

func (c *Client) UpdateFile(file, source tree.FileInfo) (tree.Copier, error) {
	debugLog("\nC: UpdateFile")
	command := Command_UPDATE
	request := &Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(file),
		FileInfo2: serializeFileInfo(source),
	}

	return c.handleFileSend(request)
}

func (c *Client) handleFileSend(request *Request) (tree.Copier, error) {
	reader, writer := io.Pipe()
	ch := c.handleReply(request, reader, nil)

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

func (c *Client) GetContents(path []string) (io.ReadCloser, error) {
	return nil, tree.ErrNotImplemented("GetContents from remote")
}

func (c *Client) CopySource(info tree.FileInfo) (io.ReadCloser, error) {
	debugLog("\nC: CopySource")
	command := Command_COPYSRC
	return c.recvFile(&Request{
		Command:   &command,
		FileInfo1: serializeFileInfo(info),
	})
}

// recvFile sends a request and returns a stream to receive a file. It never
// returns an error.
func (c *Client) recvFile(request *Request) (*streamReader, error) {
	reader, writer := io.Pipe()
	stream := &streamReader{reader: reader}
	ch := c.handleReply(request, nil, writer)
	go func() {
		respData := <-ch
		if respData.err != nil {
			stream.setError(respData.err)
		}
	}()
	return stream, nil
}

func (c *Client) AddRegular(path []string, contents []byte) (tree.FileInfo, error) {
	debugLog("\nC: AddRegular")
	return c.sendFile(Command_ADDFILE, path, contents)
}

func (c *Client) SetContents(path []string, contents []byte) (tree.FileInfo, error) {
	debugLog("\nC: SetContents")
	return c.sendFile(Command_SETCONTENTS, path, contents)
}

func (c *Client) sendFile(command Command, path []string, contents []byte) (tree.FileInfo, error) {
	if contents == nil {
		contents = []byte{}
	}
	request := &Request{
		Command: &command,
		FileInfo1: &FileInfo{
			Path: path,
		},
		Data: contents,
	}
	ch := c.handleReply(request, nil, nil)
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil || resp.FileInfo.Size == nil {
		return nil, ErrInvalidResponse
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
	ch := c.handleReply(request, nil, nil)
	respData := <-ch
	resp, err := respData.resp, respData.err
	if err != nil {
		return nil, err
	}
	if resp.FileInfo == nil || resp.FileInfo.Type == nil || resp.FileInfo.ModTime == nil || resp.FileInfo.Size == nil {
		return nil, ErrInvalidResponse
	}
	return parseFileInfo(resp.FileInfo), nil
}

func (c *Client) handleReply(request *Request, sendStream *io.PipeReader, recvStream *io.PipeWriter) chan roundtripResponse {
	replyChan := make(chan roundtripResponse, 1)
	idChan := make(chan uint64)
	var recvBlocks chan recvBlock
	if recvStream != nil {
		recvBlocks = make(chan recvBlock)
	}

	c.sendRequest <- roundtripRequest{request, idChan, replyChan, recvBlocks}
	id := <-idChan // also makes sure the request is being sent

	var sendCancel chan struct{}
	var sendFinished, recvFinished chan error

	// Stream from here to remote.
	if sendStream != nil {
		sendCancel = make(chan struct{}, 1)
		sendFinished = make(chan error, 1)
		go func() {
			buf := make([]byte, 32*1024)
			for {
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

	returnChan := make(chan roundtripResponse)
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
	return returnChan
}

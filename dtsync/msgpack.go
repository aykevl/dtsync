// msgpack.go
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

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aykevl/dtsync/dtdiff"
	"github.com/aykevl/dtsync/sync"
	"github.com/aykevl/dtsync/tree"
	"github.com/ugorji/go/codec"
)

type mpMessage struct {
	Message string      `codec:"message"`
	Root    int         `codec:"root"`
	Value   interface{} `codec:"value,omitempty"`
	Error   string      `codec:"error,omitempty"`
}

type progressValue struct {
	Done  uint64 `codec:"done"`
	Total uint64 `codec:"total"`
	Path  string `codec:"path"`
}

type jobValue struct {
	Direction     int             `codec:"direction"`
	OrigDirection int             `codec:"origDirection"`
	Sides         [2]jobValueSide `codec:"sides"`
	Path          string          `codec:"path"`
}

type jobValueSide struct {
	Status   string            `codec:"status"`
	Path     string            `codec:"path"`
	Metadata *jobValueMetadata `codec:"metadata"`
}

type jobValueMetadata struct {
	Type     string    `codec:"fileType"`
	Mode     tree.Mode `codec:"mode"`
	UsedMode tree.Mode `codec:"usedMode"`
	ModTime  string    `codec:"mtime"`
	Size     int64     `codec:"size"`
}

type applyProgressValue struct {
	TotalProgress float64 `codec:"totalProgress"`
	Job           int     `codec:"job"`
	State         string  `codec:"state"` // "starting", "progress", "finished", "error"
	Error         string  `codec:"error,omitempty"`
	JobProgress   float64 `codec:"jobProgress"`
}

type applyFinishedValue struct {
	Applied int `codec:"applied"`
	Error   int `codec:"error"`
}

type jobQueueItem struct {
	index int
	job   *sync.Job
}

type mpCommand struct {
	Command   string `codec:"command"`
	Jobs      []int  `codec:"jobs"`
	Direction int    `codec:"direction"`
}

type MessagePack struct {
	writer *bufio.Writer
	enc    *codec.Encoder
	dec    *codec.Decoder
}

func newMessagePack() *MessagePack {
	handle := &codec.MsgpackHandle{RawToString: true}
	reader := bufio.NewReader(os.Stdin)
	mp := &MessagePack{
		writer: bufio.NewWriter(os.Stdout),
		dec:    codec.NewDecoder(reader, handle),
	}
	mp.enc = codec.NewEncoder(mp.writer, handle)
	return mp
}

func (mp *MessagePack) sendMessage(msg mpMessage) {
	err := mp.enc.Encode(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not write message: %s\n", err)
		os.Exit(1)
	}
	err = mp.writer.Flush()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not flush message: %s\n", err)
		os.Exit(1)
	}
}

func (mp *MessagePack) sendValue(message string, root int, value interface{}) {
	mp.sendMessage(mpMessage{
		Message: message,
		Root:    root,
		Value:   value,
	})
}

func (mp *MessagePack) sendError(errorType string, root int, err error) {
	fmt.Fprintf(os.Stderr, "ERROR root%d: %s: %s\n", root, errorType, err)
	mp.sendMessage(mpMessage{
		Message: "error",
		Root:    root,
		Error:   errorType,
		Value:   err.Error(),
	})
}

func createJobValue(result *sync.Result, job *sync.Job) *jobValue {
	jv := &jobValue{
		Direction:     job.Direction(),
		OrigDirection: job.OrigDirection(),
		Sides: [2]jobValueSide{
			{
				Status: job.StatusLeft(),
				Path:   job.PathLeft(),
			},
			{
				Status: job.StatusRight(),
				Path:   job.PathRight(),
			},
		},
		Path: job.RelativePath(),
	}

	for i, status := range [2]*dtdiff.Entry{job.StatusEntryLeft(), job.StatusEntryRight()} {
		if status != nil {
			jv.Sides[i].Metadata = &jobValueMetadata{
				Type:     status.Type().Char(),
				Mode:     status.Mode(),
				UsedMode: status.HasMode() & result.Perms(),
				ModTime:  status.ModTime().Format(time.RFC3339Nano),
				Size:     status.Size(),
			}
		}
	}
	return jv
}

func runMsgpack(root1, root2 string) {
	mp := newMessagePack()
	fs1, err := sync.NewTree(root1)
	if err != nil {
		mp.sendError("could not open root", 0, err)
		return
	}
	fs2, err := sync.NewTree(root2)
	if err != nil {
		mp.sendError("could not open root", 1, err)
		return
	}

	var result *sync.Result

	for {
		var command mpCommand
		err := mp.dec.Decode(&command)
		if err != nil {
			mp.sendError("could not read command", -1, err)
			return
		}

		switch command.Command {
		case "scan":
			progress, optionProgress := sync.Progress()
			progressExit := make(chan struct{})
			go func() {
				for p := range progress {
					var value [2]*progressValue
					for i, side := range p {
						if side != nil {
							value[i] = &progressValue{side.Done, side.Total, strings.Join(side.Path, "/")}
						}
					}
					mp.sendValue("scan-progress", -1, value)
				}
				close(progressExit)
			}()

			result, err = sync.Scan(fs1, fs2, optionProgress)

			// Make sure the progress values have been fully sent before sending the
			// status.
			<-progressExit

			if err != nil {
				mp.sendError("could not scan roots", -1, err)
				return
			}

			err = result.SaveStatus()
			if err != nil {
				mp.sendError("could not save status after scan", -1, err)
				return
			}

			value := struct {
				Jobs []*jobValue `codec:"jobs"`
			}{
				Jobs: make([]*jobValue, 0, len(result.Jobs())),
			}

			for _, job := range result.Jobs() {
				value.Jobs = append(value.Jobs, createJobValue(result, job))
			}

			mp.sendValue("scan-finished", -1, value)

		case "apply":
			jobs := make([]jobQueueItem, 0, len(result.Jobs()))
			var costTotal int64
			var costDone int64
			for i, job := range result.Jobs() {
				if job.Direction() != 0 {
					jobs = append(jobs, jobQueueItem{i, job})
					costTotal += job.Cost()
				}
			}

			for _, item := range jobs {
				mp.sendValue("apply-progress", -1, applyProgressValue{
					TotalProgress: float64(costDone) / float64(costTotal),
					Job:           item.index,
					State:         "starting",
					JobProgress:   0.0,
				})

				// TODO: send better progress of inidividual jobs (e.g. while
				// copying a large file or removing a large directory)

				jobCostTotal := item.job.Cost()
				var jobCostDone int64
				progressChan := make(chan int64)
				progressExit := make(chan struct{})
				go func() {
					for cost := range progressChan {
						jobCostDone += cost
						if jobCostDone > jobCostTotal {
							panic("jobCostDone > jobCostTotal")
						}
						mp.sendValue("apply-progress", -1, applyProgressValue{
							TotalProgress: float64(costDone+jobCostDone) / float64(costTotal),
							Job:           item.index,
							State:         "progress",
							JobProgress:   float64(jobCostDone) / float64(jobCostTotal),
						})
					}
					close(progressExit)
				}()

				err := item.job.Apply(progressChan)
				costDone += jobCostTotal
				progress := applyProgressValue{
					TotalProgress: float64(costDone) / float64(costTotal),
					Job:           item.index,
					State:         "finished",
					JobProgress:   1.0,
				}
				if err != nil {
					progress.State = "error"
					progress.Error = err.Error()
				}
				<-progressExit // wait until the goroutine exited
				mp.sendValue("apply-progress", -1, progress)
			}

			mp.sendValue("apply-progress", -1, applyProgressValue{
				TotalProgress: 1.0,
				Job:           -1,
				State:         "saving-status",
			})

			err := result.SaveStatus()
			if err != nil {
				mp.sendError("could not save status after apply", -1, err)
				return
			}

			stats := result.Stats()
			mp.sendValue("apply-finished", -1, applyFinishedValue{
				Applied: stats.CountTotal,
				Error:   stats.CountError,
			})

		case "job-direction":
			if result == nil {
				mp.sendError("no scan result", -1, errors.New("dtsync: there is no scan result available"))
				return
			}
			if command.Direction < -1 || command.Direction > 1 {
				mp.sendError("job direction invalide", -1, errors.New("dtsync: invalid job direction, must be {-1,0,1}"))
				return
			}
			jobs := make(map[int]*sync.Job, len(command.Jobs))
			for _, job := range command.Jobs {
				if job < 0 || job >= len(result.Jobs()) {
					mp.sendError("job out of range", -1, errors.New("dtsync: job out of range"))
					return
				}
				jobs[job] = result.Jobs()[job]
			}

			for _, job := range jobs {
				job.SetDirection(command.Direction)
			}

			jobMap := make(map[int]*jobValue, len(jobs))
			for i, job := range jobs {
				jobMap[i] = createJobValue(result, job)
			}
			mp.sendValue("job-update", -1, jobMap)
		default:
			mp.sendError("unknown command", -1, errors.New("dtsync: unknown command: "+command.Command))
			return
		}
	}
}

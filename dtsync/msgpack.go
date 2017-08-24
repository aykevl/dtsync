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
	"sync"
	"time"

	"github.com/aykevl/dtsync/dtdiff"
	dtsync "github.com/aykevl/dtsync/sync"
	"github.com/aykevl/dtsync/tree"
	"github.com/ugorji/go/codec"
)

const NUM_PARALLEL_JOBS = 8

type mpMessage struct {
	Message string      `codec:"message"`
	Root    int         `codec:"root"`
	Value   interface{} `codec:"value,omitempty"`
	Error   string      `codec:"error,omitempty"`
}

type profileValue struct {
	Name  string `codec:"name"`
	Root1 string `codec:"root1"`
	Root2 string `codec:"root2"`
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
	job   *dtsync.Job
}

type mpCommand struct {
	Command   string   `codec:"command"`
	Jobs      []int    `codec:"jobs"`
	Direction int      `codec:"direction"`
	Roots     []string `codec:"roots"`
	Profile   string   `codec:"profile"`
}

type MessagePack struct {
	writer  *bufio.Writer
	enc     *codec.Encoder
	encLock sync.Mutex
	dec     *codec.Decoder
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
	mp.encLock.Lock()
	defer mp.encLock.Unlock()

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

func createJobValue(result *dtsync.Result, job *dtsync.Job) *jobValue {
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

func runJob(jobs chan jobQueueItem, mp *MessagePack, state *applyState) {
	defer state.workers.Done()

	for item := range jobs {
		state.lock.Lock()
		progress := float64(state.costDone) / float64(state.costTotal)
		state.lock.Unlock()

		mp.sendValue("apply-progress", -1, applyProgressValue{
			TotalProgress: progress,
			Job:           item.index,
			State:         "starting",
			JobProgress:   0.0,
		})

		jobCostTotal := item.job.Cost()
		progressChan := make(chan int64, 2)
		progressExit := make(chan struct{})
		go func() {
			// Progress indication is inexact. It may not give the exact
			// number of bytes per file, or might even count a bit more
			// in rare circumstances.
			var jobCostDone int64
			for cost := range progressChan {
				prevJobCostDone := jobCostDone
				jobCostDone += cost
				if jobCostDone >= jobCostTotal {
					// Be forgiving in the progress indication.
					jobCostDone = jobCostTotal - 1
				}

				state.lock.Lock()
				state.costDone += (jobCostDone - prevJobCostDone) // add current job progress to global progress
				progress := float64(state.costDone) / float64(state.costTotal)
				state.lock.Unlock()

				mp.sendValue("apply-progress", -1, applyProgressValue{
					TotalProgress: progress,
					Job:           item.index,
					State:         "progress",
					JobProgress:   float64(jobCostDone) / float64(jobCostTotal),
				})
			}

			state.lock.Lock()
			state.costDone -= jobCostDone  // subtract estimated/inexact progress
			state.costDone += jobCostTotal // add real progress
			state.lock.Unlock()
			close(progressExit)
		}()

		err := item.job.Apply(progressChan)
		<-progressExit // wait until the goroutine exited

		state.lock.Lock()
		progress = float64(state.costDone) / float64(state.costTotal)
		state.lock.Unlock()

		progressValue := applyProgressValue{
			TotalProgress: progress,
			Job:           item.index,
			State:         "finished",
			JobProgress:   1.0,
		}
		if err != nil {
			progressValue.State = "error"
			progressValue.Error = err.Error()
		}
		mp.sendValue("apply-progress", -1, progressValue)
	}
}

type applyState struct {
	lock      sync.Mutex
	costTotal int64
	costDone  int64
	workers   sync.WaitGroup
}

func runMsgpack() {
	mp := newMessagePack()

	var result *dtsync.Result

	for {
		var command mpCommand
		err := mp.dec.Decode(&command)
		if err != nil {
			mp.sendError("could not read command", -1, err)
			return
		}

		switch command.Command {
		case "scan":
			var profile *Profile
			if command.Profile != "" {
				profile, err = NewConfigProfile(command.Profile)
				if err != nil {
					mp.sendError("could not open profile", -1, err)
					return
				}
			} else if len(command.Roots) == 2 {
				profile = NewPairProfile(command.Roots[0], command.Roots[1])
			} else {
				mp.sendError("provide a profile or two roots (protocol error)", -1, nil)
				return
			}

			mp.sendValue("scan-profile", -1, profileValue{
				Name:  profile.name,
				Root1: profile.root1,
				Root2: profile.root2,
			})

			fs1, err := dtsync.NewTree(profile.root1)
			if err != nil {
				mp.sendError("could not open root", 0, err)
				return
			}
			fs2, err := dtsync.NewTree(profile.root2)
			if err != nil {
				mp.sendError("could not open root", 1, err)
				return
			}

			progress, optionProgress := dtsync.Progress()
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

			result, err = dtsync.Scan(fs1, fs2, optionProgress, dtsync.ExtraOptions(profile.options))

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
			state := &applyState{}
			jobs := make([]jobQueueItem, 0, len(result.Jobs()))
			for i, job := range result.Jobs() {
				if job.Direction() != 0 {
					jobs = append(jobs, jobQueueItem{i, job})
					state.costTotal += job.Cost()
				}
			}

			jobChan := make(chan jobQueueItem)
			state.workers.Add(NUM_PARALLEL_JOBS)
			for i := 0; i < NUM_PARALLEL_JOBS; i++ {
				go runJob(jobChan, mp, state)
			}

			for _, item := range jobs {
				jobChan <- item
			}
			close(jobChan)

			state.workers.Wait()

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
			jobs := make(map[int]*dtsync.Job, len(command.Jobs))
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

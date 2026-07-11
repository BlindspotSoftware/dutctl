// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/BlindspotSoftware/dutctl/internal/log"
)

// backend implements the module.Session interface.
type backend struct {
	printCh   chan string
	stdinCh   chan []byte
	stdoutCh  chan []byte
	stderrCh  chan []byte
	fileReqCh chan string
	fileCh    chan chan []byte // a single file is represented by a channel of bytes

	// currentFile holds the name of the file currently being transferred.
	// It is used for both, to indicate the file that was requested by the module
	// and the file that is being sent back to the client.
	currentFile string

	// log is the session-scoped logger, frozen in by the broker (see Broker.Start)
	// because the module.Session methods carry no context to derive it from.
	log *slog.Logger
}

// logger returns the session's scoped logger, falling back to the default if
// the broker has not set one (e.g. a session built directly in a test).
func (s *backend) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}

	return slog.Default()
}

func (s *backend) Print(a ...any) {
	s.printCh <- fmt.Sprint(a...)
}

func (s *backend) Printf(format string, a ...any) {
	s.printCh <- fmt.Sprintf(format, a...)
}

func (s *backend) Println(a ...any) {
	s.printCh <- fmt.Sprintln(a...)
}

//nolint:nonamedreturns
func (s *backend) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	// The channels are always initialized by Broker.init before a module runs,
	// so a failure here is a broken invariant (a nil channel), not a runtime
	// condition. Console has no error return, so panic; the module-execution
	// goroutine recovers it into a clean run error (see executeModules).
	stdinReader, err = chanio.NewChanReader(s.stdinCh, log.Scope(s.logger(), scopeSessionUpstream))
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdin reader: %v", err))
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdoutCh)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdout writer: %v", err))
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderrCh)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stderr writer: %v", err))
	}

	return stdinReader, stdoutWriter, stderrWriter
}

func (s *backend) RequestFile(name string) (io.Reader, error) {
	if s.fileReqCh == nil {
		return nil, errors.New("session not initialized: file request channel is nil")
	}

	// Requesting and reading a file is the upstream (client → agent) flow.
	uplog := log.Scope(s.logger(), scopeSessionUpstream)
	uplog.Debug("module requested file", "name", name)

	s.fileReqCh <- name // Send the file request to the client.

	file := <-s.fileCh // This will block until the client sends the file.

	r, err := chanio.NewChanReader(file, uplog)
	if err != nil {
		return nil, fmt.Errorf("request file %q: %w", name, err)
	}

	return r, nil
}

func (s *backend) SendFile(name string, r io.Reader) error {
	if s.currentFile != "" {
		return fmt.Errorf("send file %q: a file request is already in progress", name)
	}

	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	// Sending a file to the client is the downstream (agent → client) flow.
	downlog := log.Scope(s.logger(), scopeSessionDownstream)
	downlog.Debug("module sending file", "name", name, "bytes", len(content))

	s.currentFile = name

	file := make(chan []byte, 1)
	s.fileCh <- file
	file <- content

	close(file) // indicate EOF.

	return nil
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"fmt"
	"io"
	"log"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
)

// session implements the module.Session interface.
type session struct {
	done      <-chan struct{} // closed when broker workers shut down; unblocks pending session calls
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
}

func (s *session) Print(a ...any) {
	select {
	case s.printCh <- fmt.Sprint(a...):
	case <-s.done:
	}
}

func (s *session) Printf(format string, a ...any) {
	select {
	case s.printCh <- fmt.Sprintf(format, a...):
	case <-s.done:
	}
}

func (s *session) Println(a ...any) {
	select {
	case s.printCh <- fmt.Sprintln(a...):
	case <-s.done:
	}
}

//nolint:nonamedreturns
func (s *session) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	stdinReader, err = chanio.NewChanReader(s.stdinCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdinReader: %v", err)
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdoutCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdoutWriter: %v", err)
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderrCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stderrWriter: %v", err)
	}

	return stdinReader, stdoutWriter, stderrWriter
}

func (s *session) RequestFile(name string) (io.Reader, error) {
	if s.fileReqCh == nil {
		log.Fatal("session.RequestFile() called but session.fileReq is nil")
	}

	log.Printf("Module issued file request for: %q", name)

	select {
	case s.fileReqCh <- name:
	case <-s.done:
		return nil, fmt.Errorf("session closed before file request %q could be sent", name)
	}

	var file chan []byte

	select {
	case file = <-s.fileCh:
	case <-s.done:
		return nil, fmt.Errorf("session closed while waiting for file %q", name)
	}

	r, err := chanio.NewChanReader(file)
	if err != nil {
		log.Fatalf("session.RequestFile() failed to create reader: %v", err)
	}

	return r, nil
}

func (s *session) SendFile(name string, r io.Reader) error {
	if s.currentFile != "" {
		log.Fatal("session.SendFile() called during a ongoing file request")
	}

	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	log.Printf("Module issued file transfer of : %q, with %d bytes", name, len(content))

	s.currentFile = name

	file := make(chan []byte, 1)

	select {
	case s.fileCh <- file:
	case <-s.done:
		s.currentFile = ""

		return fmt.Errorf("session closed before file %q could be sent", name)
	}

	file <- content

	close(file) // indicate EOF.

	return nil
}

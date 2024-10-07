// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dutagent

import (
	"io"
	"log"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
)

// session implements the module.Session interface.
type session struct {
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

func (s *session) Print(text string) {
	s.printCh <- text
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

	s.fileReqCh <- name // Send the file request to the client.

	file := <-s.fileCh // This will block until the client sends the file.

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
	s.fileCh <- file
	file <- content
	close(file) // indicate EOF.

	return nil
}

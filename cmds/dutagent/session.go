package main

import (
	"io"
	"log"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
)

// session implements module.Session for remote sessions between the agent
// and the client.
type session struct {
	print   chan string
	stdin   chan []byte
	stdout  chan []byte
	stderr  chan []byte
	fileReq chan string
	file    chan []byte
	done    chan struct{}
	err     error
}

func (s *session) Print(text string) {
	s.print <- text
}

//nolint:nonamedreturns
func (s *session) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	stdinReader, err = chanio.NewChanReader(s.stdin)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdinReader: %v", err)
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdout)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdoutWriter: %v", err)
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderr)
	if err != nil {
		log.Fatalf("session.Console() failed to create stderrWriter: %v", err)
	}

	return stdinReader, stdoutWriter, stderrWriter
}

func (s *session) RequestFile(name string) (io.Reader, error) {
	if s.fileReq == nil {
		log.Fatal("session.RequestFile() called but session.fileReq is nil")
	}

	r, err := chanio.NewChanReader(s.file)
	if err != nil {
		log.Fatalf("session.RequestFile() failed to create reader: %v", err)
	}

	s.fileReq <- name

	return r, nil
}

func (s *session) SendFile(_ string, _ io.Reader) error {
	log.Println("SendFile not implemented")

	return nil
}

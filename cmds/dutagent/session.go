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
	return chanio.NewChanReader(s.stdin), chanio.NewChanWriter(s.stdout), chanio.NewChanWriter(s.stderr)
}

func (s *session) RequestFile(name string) (io.Reader, error) {
	s.fileReq <- name

	return chanio.NewChanReader(s.file), nil
}

func (s *session) SendFile(_ string, _ io.Reader) error {
	log.Println("SendFile not implemented")

	return nil
}

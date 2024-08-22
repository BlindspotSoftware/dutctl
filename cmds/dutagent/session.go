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
	file    chan chan []byte // a single file is represented by a channel of bytes
	done    chan struct{}
	err     error

	// currentFile holds the name of the file currently being transferred.
	// It is used for both, to indicate the file that was requested by the module
	// and the file that is being sent back to the client.
	currentFile string
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

	log.Printf("Module issued file request for: %q", name)

	s.fileReq <- name // Send the file request to the client.

	file := <-s.file // This will block until the client sends the file.

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
	s.file <- file
	file <- content
	close(file) // indicate EOF.

	return nil
}

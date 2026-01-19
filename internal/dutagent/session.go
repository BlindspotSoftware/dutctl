// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
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

	// Chunked file transfer state
	receivingFilePath string   // Path to temp file receiving chunks
	receivingFileHandle *os.File // File handle for receiving chunks
}

func (s *session) Print(a ...any) {
	s.printCh <- fmt.Sprint(a...)
}

func (s *session) Printf(format string, a ...any) {
	s.printCh <- fmt.Sprintf(format, a...)
}

func (s *session) Println(a ...any) {
	s.printCh <- fmt.Sprintln(a...)
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

// receiveFileChunk handles receiving a file chunk from the client.
// It writes chunks directly to a temporary file to avoid channel blocking issues.
func (s *session) receiveFileChunk(chunk *pb.FileChunk) error {
	data := chunk.GetData()
	offset := chunk.GetOffset()
	isLast := chunk.GetIsLast()

	// First chunk - create temp file
	if offset == 0 {
		tmpFile, err := os.CreateTemp("", "dutagent-filetransfer-*")
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}

		s.receivingFilePath = tmpFile.Name()
		s.receivingFileHandle = tmpFile

		log.Printf("Created temp file for chunked transfer: %s", s.receivingFilePath)
	}

	// Write chunk data at the specified offset
	_, err := s.receivingFileHandle.WriteAt(data, int64(offset))
	if err != nil {
		s.receivingFileHandle.Close()
		os.Remove(s.receivingFilePath)
		s.receivingFileHandle = nil
		s.receivingFilePath = ""

		return fmt.Errorf("failed to write chunk at offset %d: %w", offset, err)
	}

	// Last chunk - close file and pass it to module
	if isLast {
		err := s.receivingFileHandle.Close()
		if err != nil {
			os.Remove(s.receivingFilePath)
			s.receivingFileHandle = nil
			s.receivingFilePath = ""

			return fmt.Errorf("failed to close temp file: %w", err)
		}

		// Open file for reading and create channel to pass to module
		file, err := os.Open(s.receivingFilePath)
		if err != nil {
			os.Remove(s.receivingFilePath)
			s.receivingFileHandle = nil
			s.receivingFilePath = ""

			return fmt.Errorf("failed to open temp file for reading: %w", err)
		}

		log.Printf("File transfer complete, passing to module: %s", s.receivingFilePath)

		// Create a channel that will stream the file contents
		fileChan := make(chan []byte, 10)
		s.fileCh <- fileChan

		// Stream file contents in a goroutine and clean up when done
		go func() {
			defer func() {
				file.Close()
				os.Remove(s.receivingFilePath)
				close(fileChan)
				log.Printf("Cleaned up temp file: %s", s.receivingFilePath)
			}()

			buf := make([]byte, 32*1024) // 32KB buffer for streaming
			for {
				n, err := file.Read(buf)
				if n > 0 {
					// Make a copy since we're reusing the buffer
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					fileChan <- chunk
				}

				if err == io.EOF {
					break
				}

				if err != nil {
					log.Printf("Error reading from temp file: %v", err)

					break
				}
			}
		}()

		s.receivingFileHandle = nil
		s.receivingFilePath = ""
	}

	return nil
}

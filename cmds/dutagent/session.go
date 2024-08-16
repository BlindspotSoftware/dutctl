package main

import (
	"io"
	"log"
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
	return &chanReader{s.stdin, []byte{}}, &chanWriter{s.stdout}, &chanWriter{s.stderr}
}

func (s *session) RequestFile(name string) (io.Reader, error) {
	s.fileReq <- name

	return &chanReader{s.file, []byte{}}, nil
}

func (s *session) SendFile(_ string, _ io.Reader) error {
	log.Println("SendFile not implemented")

	return nil
}

type chanReader struct {
	ch  <-chan []byte
	buf []byte // Buffer to store excess bytes
}

func (r *chanReader) Read(bytes []byte) (int, error) {
	log.Printf("Channel Reader: Read called with %d bytes\n", len(bytes))
	// If there's enough data in the buffer, use it and return early.
	if len(r.buf) >= len(bytes) {
		n := copy(bytes, r.buf)
		r.buf = r.buf[n:] // Adjust the buffer
		log.Printf("Channel Reader: Returning early (internal buffer >= read buffer), %d bytes from buffer\n", n)

		return n, nil
	}

	log.Printf("Channel Reader: no early return, continue to read from buffer and channel\n")

	var nBuf, nChan int
	// If the buffer is not empty but contains some data, start by filling bytes with it.
	if len(r.buf) > 0 {
		log.Printf("Channel Reader: Reading from internal buffer\n")

		nBuf = copy(bytes, r.buf)
		r.buf = r.buf[nBuf:] // Adjust the buffer

		if nBuf == len(bytes) {
			// If the buffer fulfilled the bytes, return early
			log.Printf("Channel Reader: Returning early (internal buffer = read buffer), %d bytes from buffer\n", nBuf)

			return nBuf, nil
		}
	}

	log.Printf("Channel Reader: Continue reading from channel\n")

	// Read from the channel if the buffer is empty or insufficient
	chanBytes, ok := <-r.ch
	if !ok {
		log.Printf("Channel Reader: Channel closed returning EOF")

		return nBuf, io.EOF // Return any remaining buffer content before EOF
	}

	log.Printf("Channel Reader: Channel read %d bytes. Continue calculating total read count\n", len(chanBytes))

	// Calculate the total bytes to copy to bytes, considering any existing content from the buffer
	totalNeeded := len(bytes) - len(r.buf)
	nChan = copy(bytes[nBuf:], chanBytes)

	// If there are excess bytes, append them to the buffer
	if totalNeeded < len(chanBytes) {
		r.buf = append(r.buf, chanBytes[totalNeeded:]...)
	}

	log.Printf("Channel Reader: Read %d bytes from internal buffer and %d bytes from channel\n", nBuf, nChan)

	return nBuf + nChan, nil
}

type chanWriter struct {
	ch chan<- []byte
}

func (w *chanWriter) Write(bytes []byte) (int, error) {
	chanBytes := make([]byte, len(bytes))
	copy(chanBytes, bytes)
	w.ch <- chanBytes

	return len(bytes), nil
}

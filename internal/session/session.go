package session

import "io"

// session implements module.Session for remote sessions between the agent
// and the client.
type Session struct {
	printCh  chan string
	stdinCh  chan []byte
	stdoutCh chan []byte
	stderrCh chan []byte
	Err      error
}

func (s *Session) Print(text string) {
	s.printCh <- text
}

//nolint:nonamedreturns
func (s *Session) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	return &chanReader{s.stdinCh}, &chanWriter{s.stdoutCh}, &chanWriter{s.stderrCh}
}

type chanReader struct {
	ch <-chan []byte
}

func (r *chanReader) Read(p []byte) (int, error) {
	b, ok := <-r.ch
	if !ok {
		return 0, io.EOF
	}

	n := copy(p, b)

	return n, nil
}

type chanWriter struct {
	ch chan<- []byte
}

func (w *chanWriter) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	w.ch <- b

	return len(p), nil
}

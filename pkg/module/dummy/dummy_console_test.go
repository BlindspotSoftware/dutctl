// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dummy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// newSession builds a mock session whose Stdin delivers input and then io.EOF,
// as a pipe at its end does.
func newSession(input []byte) (*mock.Session, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	s := &mock.Session{
		Stdin:  io.NopCloser(bytes.NewReader(input)),
		Stdout: stdout,
		Stderr: stderr,
	}

	return s, stdout, stderr
}

func TestConsoleRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		input      []byte
		wantMode   module.ConsoleMode
		wantStdout []byte
	}{
		{
			name:       "raw echoes bytes as received",
			input:      []byte{'a', 0x04, 0xff, 0x1b, '[', 'A', '\r', 0x03},
			wantMode:   module.ConsoleRaw,
			wantStdout: []byte{'a', 0x04, 0xff, 0x1b, '[', 'A', '\r', 0x03},
		},
		{
			name:     "raw ends on a lone Ctrl-D without echoing it",
			input:    []byte{0x04},
			wantMode: module.ConsoleRaw,
		},
		{
			name:     "raw ends at the end of input",
			args:     []string{"raw"},
			wantMode: module.ConsoleRaw,
		},
		{
			name:       "hex renders a chunk as one line",
			args:       []string{"hex"},
			input:      []byte{0x1b, '[', 'A'},
			wantMode:   module.ConsoleRaw,
			wantStdout: []byte("1b 5b 41\r\n"),
		},
		{
			name:       "hex shows the lone Ctrl-D before it ends",
			args:       []string{"hex"},
			input:      []byte{0x04},
			wantMode:   module.ConsoleRaw,
			wantStdout: []byte("04\r\n"),
		},
		{
			name:       "line echoes lines and ends at the end of input",
			args:       []string{"line"},
			input:      []byte("hello\nworld\n"),
			wantMode:   module.ConsoleLine,
			wantStdout: []byte(lineGreeting + "> hello\n> world\n"),
		},
		{
			name:       "line ends on quit",
			args:       []string{"line"},
			input:      []byte("hello\nquit\nignored\n"),
			wantMode:   module.ConsoleLine,
			wantStdout: []byte(lineGreeting + "> hello\n"),
		},
		{
			name:       "line echoes a final line without newline",
			args:       []string{"line"},
			input:      []byte("hello\ntail"),
			wantMode:   module.ConsoleLine,
			wantStdout: []byte(lineGreeting + "> hello\n> tail\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, stdout, stderr := newSession(tt.input)

			err := (&Console{}).Run(context.Background(), s, tt.args...)
			if err != nil {
				t.Fatalf("Run: unexpected error: %v", err)
			}

			if !s.ConsoleOpened {
				t.Fatal("console not opened")
			}

			if s.ConsoleMode != tt.wantMode {
				t.Errorf("console mode = %v, want %v", s.ConsoleMode, tt.wantMode)
			}

			if !bytes.Equal(stdout.Bytes(), tt.wantStdout) {
				t.Errorf("stdout = %q, want %q", stdout.Bytes(), tt.wantStdout)
			}

			if tt.wantMode == module.ConsoleRaw && !bytes.HasSuffix(stderr.Bytes(), []byte("\r\n")) {
				t.Errorf("raw banner on stderr = %q, want it to end in CR LF", stderr.Bytes())
			}
		})
	}
}

// TestConsoleRawEndsBeforeInputEnds feeds the raw demo through a pipe that is
// never closed, so only the lone Ctrl-D can have ended it.
func TestConsoleRawEndsBeforeInputEnds(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	stdout := &bytes.Buffer{}
	s := &mock.Session{Stdin: pr, Stdout: stdout, Stderr: &bytes.Buffer{}}

	done := make(chan error, 1)

	go func() {
		done <- (&Console{}).Run(context.Background(), s)
	}()

	for _, chunk := range [][]byte{[]byte("ab"), {0x04, 'c'}, {0x04}} {
		if _, err := pw.Write(chunk); err != nil {
			t.Fatalf("write %q: %v", chunk, err)
		}
	}

	if err := <-done; err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	want := []byte{'a', 'b', 0x04, 'c'}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Errorf("stdout = %q, want %q", stdout.Bytes(), want)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestConsoleWriteError(t *testing.T) {
	wantErr := errors.New("stream broken")

	tests := []struct {
		name  string
		args  []string
		input []byte
	}{
		{name: "raw", input: []byte("x")},
		{name: "hex", args: []string{"hex"}, input: []byte("x")},
		{name: "line", args: []string{"line"}, input: []byte("x\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &mock.Session{
				Stdin:  io.NopCloser(bytes.NewReader(tt.input)),
				Stdout: failingWriter{err: wantErr},
				Stderr: &bytes.Buffer{},
			}

			err := (&Console{}).Run(context.Background(), s, tt.args...)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Run: error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestConsoleBadArguments(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"raw", "hex"}} {
		s, _, _ := newSession(nil)

		err := (&Console{}).Run(context.Background(), s, args...)
		if err == nil {
			t.Errorf("Run(%q): expected an error", args)
		}

		if s.ConsoleOpened {
			t.Errorf("Run(%q): console opened despite bad arguments", args)
		}
	}
}

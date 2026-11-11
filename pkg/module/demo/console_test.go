// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

// consoleSession returns a session whose console reads stdin and writes to
// the returned buffers.
func consoleSession(stdin io.ReadCloser) (*mock.Session, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	return &mock.Session{Stdin: stdin, Stdout: stdout, Stderr: stderr}, stdout, stderr
}

func TestConsoleShowsTheBacklogAndEndsOnLogout(t *testing.T) {
	b := freshBoard(t)
	b.Switch(board.Power, true)

	s, stdout, stderr := consoleSession(io.NopCloser(strings.NewReader("version\rexit\r")))

	if err := (&Console{}).Run(context.Background(), s); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.ConsoleMode != module.ConsoleRaw {
		t.Error("the console is not a raw one")
	}

	want := "Welcome to the demo board. Type help for its commands.\r\n" + board.Prompt +
		"version\r\n" + board.Factory().Summary() + "\r\n" + board.Prompt + "exit\r\nlogout\r\n"
	if got := stdout.String(); !strings.HasSuffix(got, want) || !strings.Contains(got, "Booting") {
		t.Errorf("stdout %q, want the boot messages, then %q", got, want)
	}

	if got := stderr.String(); !strings.Contains(got, "Connected to the demo board, which is on") ||
		!strings.HasSuffix(got, "--- Connection closed ---\r\n") {
		t.Errorf("stderr %q, want the markers", got)
	}
}

func TestConsoleOfAnOffBoardEndsWithItsInput(t *testing.T) {
	freshBoard(t)

	s, stdout, stderr := consoleSession(io.NopCloser(strings.NewReader("help\r")))

	if err := (&Console{}).Run(context.Background(), s); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stdout.Len() != 0 {
		t.Errorf("an off board printed %q", stdout.String())
	}

	if got := stderr.String(); !strings.Contains(got, "which is off") || !strings.Contains(got, "Connection closed") {
		t.Errorf("stderr %q, want the markers", got)
	}
}

// failingWriter fails every write as a torn down session does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestConsoleTakesATeardownAsItsEnd(t *testing.T) {
	b := freshBoard(t)
	b.Switch(board.Power, true)

	s := &mock.Session{Stdin: io.NopCloser(strings.NewReader("")), Stdout: failingWriter{}, Stderr: failingWriter{}}

	if err := (&Console{}).Run(context.Background(), s); err != nil {
		t.Errorf("Run returned %v, want nil after a teardown", err)
	}
}

// chunkWriter hands every write to chunks.
type chunkWriter struct {
	chunks chan string
}

func (w chunkWriter) Write(p []byte) (int, error) {
	w.chunks <- string(p)

	return len(p), nil
}

// await reads chunks until their concatenation contains want.
func (w chunkWriter) await(t *testing.T, want string) {
	t.Helper()

	var got string

	for !strings.Contains(got, want) {
		select {
		case chunk := <-w.chunks:
			got += chunk
		case <-time.After(5 * time.Second):
			t.Fatalf("got %q, which lacks %q", got, want)
		}
	}
}

// The console shows a boot while it is attached, and once the shell logged
// out, Run ends the read that blocks on the still open input before it
// returns.
func TestConsoleFollowsTheBoardAndStopsItsReader(t *testing.T) {
	b := freshBoard(t)

	stdin, keys := io.Pipe()
	defer keys.Close()

	stdout := chunkWriter{chunks: make(chan string, 100)}
	s := &mock.Session{Stdin: stdin, Stdout: stdout, Stderr: io.Discard}

	done := make(chan error, 1)

	go func() { done <- (&Console{}).Run(context.Background(), s) }()

	b.Switch(board.Power, true)
	stdout.await(t, board.Prompt)

	if _, err := keys.Write([]byte("exit\r")); err != nil {
		t.Fatalf("typing: %v", err)
	}

	stdout.await(t, "logout")

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the logout, its reader still blocked")
	}
}

// A console with Expect ends once the board printed it, also when the text
// spans two chunks of the board's output, and stops its reader.
func TestConsoleEndsOnTheExpectedText(t *testing.T) {
	b := freshBoard(t)
	b.Switch(board.Power, true)

	stdin, keys := io.Pipe()
	defer keys.Close()

	stdout := chunkWriter{chunks: make(chan string, 100)}
	s := &mock.Session{Stdin: stdin, Stdout: stdout, Stderr: io.Discard}

	done := make(chan error, 1)

	go func() { done <- (&Console{Expect: "echo ab"}).Run(context.Background(), s) }()

	stdout.await(t, board.Prompt)

	if _, err := keys.Write([]byte("echo a")); err != nil {
		t.Fatalf("typing: %v", err)
	}

	// The board's echo is a chunk of output of its own, so that the next
	// chunk completes the expected text.
	stdout.await(t, "echo a")

	if _, err := keys.Write([]byte("b\r")); err != nil {
		t.Fatalf("typing: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the board printed the expected text")
	}
}

func TestConsoleRejectsArguments(t *testing.T) {
	if err := (&Console{}).Run(context.Background(), &mock.Session{}, "x"); err == nil {
		t.Error("Run accepted an argument")
	}
}

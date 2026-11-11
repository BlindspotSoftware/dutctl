// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func TestWrapPrintPrintsItsArguments(t *testing.T) {
	s := &mock.Session{}

	if err := (&WrapPrint{}).Run(context.Background(), s, "a b", "c"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.PrintText != "a b c\n" {
		t.Errorf("printed %q", s.PrintText)
	}
}

func TestWrapPromptGreetsByTheLineEntered(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"a line", "Ada\nmore\n", "Hello, Ada!\n"},
		{"a last line without its newline", "Ada", "Hello, Ada!\n"},
		{"no line", "", "\nNo name, no greeting.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &mock.Session{Stdin: io.NopCloser(strings.NewReader(tt.input)), Stdout: io.Discard, Stderr: io.Discard}

			if err := (&WrapPrompt{}).Run(context.Background(), s); err != nil {
				t.Fatalf("Run: %v", err)
			}

			if s.ConsoleMode != module.ConsoleLine {
				t.Error("the console is not a line console")
			}

			if s.PrintText != tt.want {
				t.Errorf("last output %q, want %q", s.PrintText, tt.want)
			}
		})
	}
}

func TestWrapRawShowsEveryChunk(t *testing.T) {
	var out bytes.Buffer

	pr, pw := io.Pipe()
	defer pw.Close()

	s := &mock.Session{Stdin: pr, Stdout: &out, Stderr: io.Discard}

	done := make(chan error, 1)

	go func() { done <- (&WrapRaw{}).Run(context.Background(), s) }()

	// The input stays open: only the lone Ctrl-D ends the console.
	for _, chunk := range []string{"\x1b[A", "\x04x", "\x04"} {
		if _, err := pw.Write([]byte(chunk)); err != nil {
			t.Fatalf("typing %q: %v", chunk, err)
		}
	}

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.ConsoleMode != module.ConsoleRaw {
		t.Error("the console is not a raw one")
	}

	if got, want := out.String(), "1b 5b 41\r\n04 78\r\n04\r\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}

	if !strings.Contains(s.PrintText, "Raw console") {
		t.Errorf("Print %q, want the instructions", s.PrintText)
	}
}

func TestWrapRawEndsWithTheInputOrTheSession(t *testing.T) {
	var out bytes.Buffer

	s := &mock.Session{Stdin: io.NopCloser(strings.NewReader("ab")), Stdout: &out, Stderr: io.Discard}
	if err := (&WrapRaw{}).Run(context.Background(), s); err != nil || out.String() != "61 62\r\n" {
		t.Errorf("Run returned %v with output %q, want nil after the input's end", err, out.String())
	}

	s = &mock.Session{Stdin: io.NopCloser(strings.NewReader("x")), Stdout: failingWriter{}, Stderr: io.Discard}
	if err := (&WrapRaw{}).Run(context.Background(), s); err != nil {
		t.Errorf("Run returned %v, want nil after a teardown", err)
	}
}

// brokenWriter fails every write with err.
type brokenWriter struct {
	err error
}

func (w brokenWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWrapRawReportsAFailedWrite(t *testing.T) {
	wantErr := errors.New("stream broken")
	s := &mock.Session{Stdin: io.NopCloser(strings.NewReader("x")), Stdout: brokenWriter{err: wantErr}, Stderr: io.Discard}

	if err := (&WrapRaw{}).Run(context.Background(), s); !errors.Is(err, wantErr) {
		t.Errorf("Run returned %v, want %v", err, wantErr)
	}
}

func TestWrapUploadTellsSizeAndChecksum(t *testing.T) {
	s := &mock.Session{RequestedFileResponse: strings.NewReader("abc")}

	if err := (&WrapUpload{}).Run(context.Background(), s, "a.txt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := "Received a.txt: 3 bytes, sha256 ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad.\n"
	if s.RequestedFileName != "a.txt" || s.PrintText != want {
		t.Errorf("requested %q and printed %q, want a.txt and %q", s.RequestedFileName, s.PrintText, want)
	}

	declined := errors.New("no such file")
	if err := (&WrapUpload{}).Run(context.Background(), &mock.Session{RequestFileErr: declined}, "a.txt"); !errors.Is(err, declined) {
		t.Errorf("a declined file: Run returned %v, want %v", err, declined)
	}
}

func TestWrapDownloadSendsItsText(t *testing.T) {
	s := &mock.Session{}

	if err := (&WrapDownload{}).Run(context.Background(), s, "note.txt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.SentFileName != "note.txt" || string(s.SentFileContent) != downloadText {
		t.Errorf("sent %q: %q", s.SentFileName, s.SentFileContent)
	}
}

func TestWrapModulesRejectBadArguments(t *testing.T) {
	tests := []struct {
		mod  module.Module
		args []string
	}{
		{&WrapPrompt{}, []string{"x"}},
		{&WrapRaw{}, []string{"x"}},
		{&WrapUpload{}, nil},
		{&WrapUpload{}, []string{"a", "b"}},
		{&WrapDownload{}, nil},
		{&WrapFault{}, nil},
		{&WrapFault{}, []string{"explode"}},
	}

	for _, tt := range tests {
		if err := tt.mod.Run(context.Background(), &mock.Session{}, tt.args...); err == nil {
			t.Errorf("%T accepted %q", tt.mod, tt.args)
		}
	}
}

func TestWrapFaultFailsAsAsked(t *testing.T) {
	err := (&WrapFault{}).Run(context.Background(), &mock.Session{}, "error", "it", "broke")
	if err == nil || err.Error() != "it broke" {
		t.Errorf("error fault returned %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("the panic fault did not panic")
		}
	}()

	_ = (&WrapFault{}).Run(context.Background(), &mock.Session{}, "panic")
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mock provides implementations of dutctl entities that can be used for unit-testing modules.
package mock

import (
	"fmt"
	"io"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Session is a mock implementation of the module.Session interface for testing purposes.
//
// OpenConsole and RequestFile panic when the field backing the requested value is
// not set (Stdin/Stdout/Stderr for OpenConsole, RequestedFileResponse for
// RequestFile). This is a deliberate test-double contract: an unset field is test
// misuse and surfaces as a test failure rather than a silent zero value.
//
// A test feeds Stdin with an io.Pipe, closing the writer to deliver io.EOF, or
// with io.NopCloser over a bytes.Reader; a module's Stdin.Close reaches the field
// as-is, so a closable pipe reader ends a parked Read like the real session does.
type Session struct {
	PrintCalled bool
	PrintText   string

	// ConsoleOpened records whether OpenConsole was called, and ConsoleMode the
	// mode the module asked for.
	ConsoleOpened bool
	ConsoleMode   module.ConsoleMode
	Stdin         io.ReadCloser
	Stdout        io.Writer
	Stderr        io.Writer

	RequestFileCalled     bool
	RequestedFileName     string
	RequestedFileResponse io.Reader
	RequestFileErr        error
	SendFileCalled        bool
	SentFileName          string
	SentFileContent       []byte
}

var _ module.Session = &Session{}

// Print records the call in PrintCalled and stores fmt.Sprint(a...) in PrintText.
func (m *Session) Print(a ...any) {
	m.PrintCalled = true
	m.PrintText = fmt.Sprint(a...)
}

// Printf records the call in PrintCalled and stores fmt.Sprintf(format, a...) in PrintText.
func (m *Session) Printf(format string, a ...any) {
	m.PrintCalled = true
	m.PrintText = fmt.Sprintf(format, a...)
}

// Println records the call in PrintCalled and stores fmt.Sprintln(a...) in PrintText.
func (m *Session) Println(a ...any) {
	m.PrintCalled = true
	m.PrintText = fmt.Sprintln(a...)
}

// OpenConsole records the call in ConsoleOpened and the requested mode in
// ConsoleMode, and returns a console over Stdin, Stdout and Stderr. It panics if
// any of those fields is unset; see the Session type documentation.
func (m *Session) OpenConsole(opts module.ConsoleOptions) module.Console {
	m.ConsoleOpened = true
	m.ConsoleMode = opts.Mode

	if m.Stdin == nil {
		panic("mock.Session: Stdin not set")
	}

	if m.Stdout == nil {
		panic("mock.Session: Stdout not set")
	}

	if m.Stderr == nil {
		panic("mock.Session: Stderr not set")
	}

	return module.Console{Stdin: m.Stdin, Stdout: m.Stdout, Stderr: m.Stderr}
}

// RequestFile records the call in RequestFileCalled and the argument in RequestedFileName.
// It returns RequestFileErr if that field is set; otherwise it returns RequestedFileResponse,
// panicking if that field is unset.
func (m *Session) RequestFile(name string) (io.Reader, error) {
	m.RequestFileCalled = true
	m.RequestedFileName = name

	if m.RequestFileErr != nil {
		return nil, m.RequestFileErr
	}

	if m.RequestedFileResponse == nil {
		panic("mock.Session: RequestedFileResponse not set")
	}

	return m.RequestedFileResponse, nil
}

// SendFile records the call in SendFileCalled and the argument in SentFileName, then reads
// all of r into SentFileContent. It returns any error from reading r without recording content.
func (m *Session) SendFile(name string, r io.Reader) error {
	m.SendFileCalled = true
	m.SentFileName = name

	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	m.SentFileContent = content

	return nil
}

// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The mock package provides implementations of dutctl entities that can be used for unit-testing modules.
package mock

import (
	"io"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Session is a mock implementation of the module.Session interface for testing purposes.
type Session struct {
	PrintCalled           bool
	PrintText             string
	ConsoleCalled         bool
	Stdin                 io.Reader
	Stdout                io.Writer
	Stderr                io.Writer
	RequestFileCalled     bool
	RequestedFileName     string
	RequestedFileResponse io.Reader
	SendFileCalled        bool
	SentFileName          string
	SentFileContent       []byte
}

var _ module.Session = &Session{}

func (m *Session) Print(text string) {
	m.PrintCalled = true
	m.PrintText = text
}

//nolint:nonamedreturns
func (m *Session) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	m.ConsoleCalled = true

	if m.Stdin == nil {
		panic("mock.Session: Stdin not set")
	}

	if m.Stdout == nil {
		panic("mock.Session: Stdout not set")
	}

	if m.Stderr == nil {
		panic("mock.Session: Stderr not set")
	}

	return m.Stdin, m.Stdout, m.Stderr
}

func (m *Session) RequestFile(name string) (io.Reader, error) {
	m.RequestFileCalled = true
	m.RequestedFileName = name

	if m.RequestedFileResponse == nil {
		panic("mock.Session: RequestedFileResponse not set")
	}

	return m.RequestedFileResponse, nil
}

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

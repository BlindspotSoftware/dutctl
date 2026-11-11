// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-download",
		New: func() module.Module { return &WrapDownload{} },
	})
}

// downloadText is the content of the file WrapDownload sends.
const downloadText = "This file was sent by the wrap-download module.\n"

// WrapDownload wraps SendFile of module.Session: it sends a file it makes to
// the user's machine.
type WrapDownload struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapDownload{}

// Help describes the argument.
func (w *WrapDownload) Help() string {
	return `Send a short text file to your machine.

ARGUMENTS:
	<file>  the name the file is saved under
`
}

// Init has nothing to set up.
func (w *WrapDownload) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapDownload) Deinit(_ context.Context) error {
	return nil
}

// Run sends downloadText as the file named by its argument.
func (w *WrapDownload) Run(_ context.Context, s module.Session, args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("expected one file, got %d arguments", len(args))
	}

	name := args[0]

	err := s.SendFile(name, strings.NewReader(downloadText))
	if err != nil {
		return fmt.Errorf("sending %s: %w", name, err)
	}

	s.Printf("Sent %s: %d bytes.\n", name, len(downloadText))

	return nil
}

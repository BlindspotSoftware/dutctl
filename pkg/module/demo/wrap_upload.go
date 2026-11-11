// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wrap-upload",
		New: func() module.Module { return &WrapUpload{} },
	})
}

// WrapUpload wraps RequestFile of module.Session: it receives a file from
// the user's machine and tells its size and checksum. A file the user cannot
// provide makes the request fail.
type WrapUpload struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapUpload{}

// Help describes the argument.
func (w *WrapUpload) Help() string {
	return `Receive a file from your machine and tell its size and SHA-256.

ARGUMENTS:
	<file>
`
}

// Init has nothing to set up.
func (w *WrapUpload) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (w *WrapUpload) Deinit(_ context.Context) error {
	return nil
}

// Run requests the file named by its argument and reads it to its end.
func (w *WrapUpload) Run(_ context.Context, s module.Session, args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("expected one file, got %d arguments", len(args))
	}

	name := args[0]

	r, err := s.RequestFile(name)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", name, err)
	}

	// Hashing while reading keeps any size of file out of memory.
	sum := sha256.New()

	size, err := io.Copy(sum, r)
	if err != nil {
		return fmt.Errorf("receiving %s: %w", name, err)
	}

	s.Printf("Received %s: %d bytes, sha256 %s.\n", name, size, hex.EncodeToString(sum.Sum(nil)))

	return nil
}

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

// mib is the step in which WrapUpload reports a file's progress.
const mib = 1 << 20

// WrapUpload wraps RequestFile of module.Session: it receives a file from
// the user's machine and tells its size and checksum. It reports each MiB as
// it arrives, since a file moves in chunks and output goes on meanwhile. A file
// the user cannot provide makes the request fail.
type WrapUpload struct{}

// Ensure implementing the Module interface.
var _ module.Module = &WrapUpload{}

// Help describes the argument.
func (w *WrapUpload) Help() string {
	return `Receive a file from your machine and tell its size and SHA-256,
reporting each MiB as it arrives.

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
func (w *WrapUpload) Run(ctx context.Context, s module.Session, args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("expected one file, got %d arguments", len(args))
	}

	name := args[0]

	// Hashing as the file arrives keeps any size of file out of memory.
	sum := sha256.New()
	progress := &progress{s: s, name: name}

	err := s.RequestFile(ctx, name, io.MultiWriter(sum, progress))
	if err != nil {
		return fmt.Errorf("requesting %s: %w", name, err)
	}

	s.Printf("Received %s: %d bytes, sha256 %s.\n", name, progress.size, hex.EncodeToString(sum.Sum(nil)))

	return nil
}

// progress counts the bytes of a file and reports each MiB as it arrives.
type progress struct {
	s    module.Session
	name string
	size int64
}

func (p *progress) Write(data []byte) (int, error) {
	before := p.size / mib
	p.size += int64(len(data))

	if now := p.size / mib; now > before {
		p.s.Printf("Received %d MiB of %s.\n", now, p.name)
	}

	return len(data), nil
}

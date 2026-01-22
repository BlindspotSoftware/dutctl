// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dummy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "dummy-ft",
		New: func() module.Module { return &FT{} },
	})
}

// FT is a dummy file transfer module.
// It requests a file from the client and writes it to a file, processes it, and sends it back.
type FT struct{}

// Ensure implementing the Module interface.
var _ module.Module = &FT{}

func (d *FT) Help() string {
	return "This dummy module demonstrates file transfer."
}

func (d *FT) Init(_ context.Context) error {
	return nil
}

func (d *FT) Deinit(_ context.Context) error {
	return nil
}

func (d *FT) Run(ctx context.Context, s module.Session, args ...string) error {
	// The logger on ctx is already scoped to this module by the agent; the
	// module just logs what it does and passes the logger to its helpers.
	l := log.FromContext(ctx)

	s.Println("Hello from dummy file transfer module")
	s.Printf("Called with %d arguments\n", len(args))

	const expectedArgsCnt = 2
	if len(args) != expectedArgsCnt {
		return fmt.Errorf("expected 2 arguments, got %d", len(args))
	}

	inFile := args[0]
	s.Printf("Requesting file %q passed in arg[0] as input\n", inFile)

	fileReader, err := s.RequestFile(inFile)
	if err != nil {
		return fmt.Errorf("file request failed: %v", err)
	}

	l.Debug(fmt.Sprintf("reading input file %q", inFile))

	raw, err := io.ReadAll(fileReader)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	err = save(l, raw, inFile)
	if err != nil {
		return fmt.Errorf("failed to save file: %v", err)
	}

	result, err := process(raw)
	if err != nil {
		return fmt.Errorf("failed to process file: %v", err)
	}

	outFile := args[1]

	l.Debug(fmt.Sprintf("sending back processed file %q (%d bytes)", outFile, len(result)))

	err = s.SendFile(outFile, int64(len(result)), bytes.NewBuffer(result))
	if err != nil {
		return fmt.Errorf("failed to send file: %v", err)
	}

	l.Info(fmt.Sprintf("processed and returned %q", outFile))
	s.Printf("File operated successfully, delivered %q as passed in arg[1] as output\n", outFile)

	return nil
}

func save(l *slog.Logger, raw []byte, path string) error {
	dir, err := os.MkdirTemp("", "dutagent-out")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}

	dest := filepath.Join(dir, filepath.Base(path))

	perm := 0600

	err = os.WriteFile(dest, raw, fs.FileMode(perm))
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	l.Debug(fmt.Sprintf("wrote received file to %q", dest))

	return nil
}

func process(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	// Dummy processing
	input = append(input, []byte("\n\nprocessed by dummy.FT module\n")...)

	return input, nil
}

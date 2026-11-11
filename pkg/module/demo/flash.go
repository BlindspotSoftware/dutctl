// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

func init() {
	module.Register(module.Record{
		ID:  "demo-flash",
		New: func() module.Module { return &Flash{} },
	})
}

// Flash writes firmware images to the demo board's flash and reads the flash
// back, as the flash module does with a real board's SPI flash. It shows the
// file transfer of module.Session: RequestFile for an image on the user's
// machine, SendFile for a copy of the flash.
type Flash struct{}

// Ensure implementing the Module interface.
var _ module.Module = &Flash{}

// Help describes the two operations.
func (f *Flash) Help() string {
	return fmt.Sprintf(`Write a firmware image to the flash of the simulated demo board, or read the flash.

ARGUMENTS:
	write <image> | read <file>

  write  copies <image> from your machine into the flash; the board must be
         off, the flash not write-protected, and the image must fit the
         flash of %d bytes
  read   copies the flash into <file> on your machine

The board boots what is in its flash: its console shows the image's name and
the start of its SHA-256.
`, board.FlashSize)
}

// Init has nothing to set up.
func (f *Flash) Init(_ context.Context) error {
	return nil
}

// Deinit has nothing to release.
func (f *Flash) Deinit(_ context.Context) error {
	return nil
}

// Run writes or reads the flash, as args say.
func (f *Flash) Run(_ context.Context, s module.Session, args ...string) error {
	const wantArgs = 2
	if len(args) != wantArgs {
		return fmt.Errorf("expected write <image> or read <file>, got %d arguments", len(args))
	}

	switch op, name := args[0], args[1]; op {
	case "write":
		return writeFlash(s, name)
	case "read":
		return readFlash(s, name)
	default:
		return fmt.Errorf("unknown operation %q, want write or read", op)
	}
}

// writeFlash requests the image name from the user and writes it to the flash.
func writeFlash(s module.Session, name string) error {
	// Checked before the transfer too, so that a user is not kept waiting for
	// a transfer that cannot be used.
	err := demoBoard.Writable()
	if err != nil {
		return err
	}

	r, err := s.RequestFile(name)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", name, err)
	}

	data, err := io.ReadAll(io.LimitReader(r, board.FlashSize+1))
	if err != nil {
		return fmt.Errorf("receiving %s: %w", name, err)
	}

	if len(data) > board.FlashSize {
		return fmt.Errorf("%s does not fit the flash of %d bytes", name, board.FlashSize)
	}

	img := board.Image{Name: filepath.Base(name), Data: data}

	err = demoBoard.Flash(img)
	if err != nil {
		return err
	}

	s.Printf("Wrote %s to the flash.\n", img.Summary())

	return nil
}

// readFlash sends the content of the flash to the user as name.
func readFlash(s module.Session, name string) error {
	img := demoBoard.Firmware()

	err := s.SendFile(name, bytes.NewReader(img.Data))
	if err != nil {
		return fmt.Errorf("sending the flash as %s: %w", name, err)
	}

	s.Print("Read ", img.Summary(), " from the flash into ", name, ".\n")

	return nil
}

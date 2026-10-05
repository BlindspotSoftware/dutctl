// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flash

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/BlindspotSoftware/dutctl/internal/procexec"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// writeWithOptions writes the uploaded image with skip-unchanged and recover.
func (f *Flash) writeWithOptions(ctx context.Context, sesh module.Session) error {
	sum, size, err := fileSHA256(f.localImagePath)
	if err != nil {
		return fmt.Errorf("hash flash image: %w", err)
	}

	f.imageSum, f.imageSize = sum, size

	// A programmer that hangs is recovered before the decision, so a hang does
	// not turn a skipping write into a full write.
	if f.Recover != nil && f.probe(ctx) != nil {
		sesh.Println("The programmer does not answer, recovering it first")

		err = f.Recover.powerCycle(ctx, sesh, f.probe)
		if err != nil {
			return err
		}
	}

	err = f.write(ctx, sesh)
	if err == nil || f.Recover == nil || ctx.Err() != nil {
		return err
	}

	sesh.Printf("The write failed: %v\nRecovering the programmer, then writing the whole chip again\n", err)

	rerr := f.Recover.powerCycle(ctx, sesh, f.probe)
	if rerr != nil {
		return fmt.Errorf("%w; recovery failed: %w", err, rerr)
	}

	return f.fullWrite(ctx, sesh)
}

// write skips the unchanged regions when it can and writes the whole chip otherwise.
func (f *Flash) write(ctx context.Context, sesh module.Session) error {
	s := f.SkipUnchanged
	if s == nil {
		return f.fullWrite(ctx, sesh)
	}

	layout, err := s.layout(f.imageSize)
	if err != nil {
		return err
	}

	reason := f.skipBlocker(ctx, layout)
	if reason != "" {
		sesh.Printf("Writing the whole chip, because %s\n", reason)

		return f.fullWrite(ctx, sesh)
	}

	err = writeLayout(layoutPath, layout)
	if err != nil {
		return fmt.Errorf("write layout file: %w", err)
	}

	sesh.Printf("The chip holds this image from a verified full write: writing only %s\n", strings.Join(s.Always, ", "))

	always := make([]region, 0, len(s.Always))
	for _, r := range layout {
		for _, a := range s.Always {
			if r.name == a {
				always = append(always, r)
			}
		}
	}

	// -N verifies the written regions only.
	args := append([]string{"-p", f.Programmer, "-l", layoutPath}, includeArgs(always)...)
	args = append(args, "-N", "-w", f.localImagePath)

	// A failure here touched only the always regions, so the stored hash stays
	// valid for the skipped ones.
	return f.runTool(ctx, sesh, true, args...)
}

// skipBlocker returns why the write cannot skip, or "" if it can.
func (f *Flash) skipBlocker(ctx context.Context, layout []region) string {
	stored := storedHash(f.SkipUnchanged.State)

	switch {
	case stored == "":
		return "no verified full write is recorded"
	case stored != f.imageSum:
		return "the last verified full write used a different image"
	}

	skip := heads(skipped(layout, f.SkipUnchanged.Always))

	err := writeLayout(headsPath, skip)
	if err != nil {
		return fmt.Sprintf("the head layout could not be written: %v", err)
	}

	args := append([]string{"-p", f.Programmer, "-l", headsPath}, includeArgs(skip)...)
	args = append(args, "-N", "-v", f.localImagePath)

	err = f.runTool(ctx, nil, false, args...)
	if err != nil {
		return "the start of a skipped region on the chip differs from the image"
	}

	return ""
}

// fullWrite writes the whole chip. With skip-unchanged it forgets the stored hash
// first and records the new one only after the tool reported success, so a
// failed or cancelled write always leads to a full write next time.
func (f *Flash) fullWrite(ctx context.Context, sesh module.Session) error {
	if f.SkipUnchanged != nil {
		err := forgetHash(f.SkipUnchanged.State)
		if err != nil {
			return fmt.Errorf("forget stored image hash: %w", err)
		}
	}

	err := f.runTool(ctx, sesh, true, "-p", f.Programmer, "-w", f.localImagePath)
	if err != nil {
		return err
	}

	if f.SkipUnchanged != nil {
		err := storeHash(f.SkipUnchanged.State, f.imageSum)
		if err != nil {
			return fmt.Errorf("store image hash: %w", err)
		}
	}

	return nil
}

// probe asks the flash tool to identify the chip.
func (f *Flash) probe(ctx context.Context) error {
	return f.runTool(ctx, nil, false, "-p", f.Programmer, "--flash-name")
}

// runTool runs the flash tool. With a session the command line and the output are
// shown to the client; without one the output is only used for the error.
func (f *Flash) runTool(ctx context.Context, sesh module.Session, show bool, args ...string) error {
	if show && sesh != nil {
		sesh.Printf("Executing: %s %s\n", f.Tool, strings.Join(args, " "))

		return execute(ctx, sesh, f.Tool, args...)
	}

	cmd := procexec.Command(ctx, syscall.SIGTERM, flashCancelGrace, f.Tool, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")

		return fmt.Errorf("%w: %s", err, lines[len(lines)-1])
	}

	return nil
}

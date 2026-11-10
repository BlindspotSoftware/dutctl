// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flashemulate provides a dutagent module that loads a firmware image into an SPI flash emulator.
// This module is a wrapper around an emulation tool (default: em100) that is executed on a dutagent.
package flashemulate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/procexec"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "flash-emulate",
		New: func() module.Module {
			return &FlashEmulate{
				Tool: defaultTool,
			}
		},
	})
}

const defaultTool = "em100"

// FlashEmulate is a module that loads a firmware image into an SPI flash emulator connected to the DUT.
type FlashEmulate struct {
	// Tool is the path to the emulation tool on the dutagent. Defaults to "em100".
	Tool string `yaml:"tool"`
	// Chip is the SPI flash chip type identifier to emulate (e.g. "N25Q256A13"). Required.
	Chip string `yaml:"chip"`
	// Device is the USB device number used when multiple emulators are connected. Optional.
	Device string `yaml:"device"`
}

// Ensure implementing the Module interface.
var _ module.Module = &FlashEmulate{}

const abstract = `Load a firmware image into an SPI flash emulator on the DUT.
`

const usage = `
ARGUMENTS:
	<image>

`

const description = `
<image> is the local filepath to the firmware image at the client.

The image is transferred to the dutagent and loaded into the SPI flash emulator.
Any running emulation session is stopped first, then the new image is loaded and
emulation is started.

This module wraps an emulation tool on the dutagent (default: em100). The tool
must be installed on the dutagent and a compatible emulator (e.g. EM100Pro-G2
from Dediprog) must be connected to the DUT's SPI flash bus.

`

func (e *FlashEmulate) Help() string {
	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description)
	fmt.Fprintf(&help, "Using %q as emulation tool with chip %q.\n", e.Tool, e.Chip)

	if e.Device != "" {
		fmt.Fprintf(&help, "Using USB device %q.\n", e.Device)
	}

	return help.String()
}

func (e *FlashEmulate) Init(ctx context.Context) error {
	if e.Tool == "" {
		e.Tool = defaultTool
		log.FromContext(ctx).Debug("no tool configured, using default " + defaultTool)
	}

	toolPath, err := exec.LookPath(e.Tool)
	if err != nil {
		return fmt.Errorf("emulation tool %q: %w", e.Tool, err)
	}

	if e.Chip == "" {
		return errors.New("chip must be configured (e.g. \"N25Q256A13\")")
	}

	log.FromContext(ctx).Debug(fmt.Sprintf("using emulation tool %s at %s", e.Tool, toolPath))

	return nil
}

// Deinit has nothing to clean up: every run removes its own image.
func (e *FlashEmulate) Deinit(_ context.Context) error {
	return nil
}

// Run loads the image the client names in args into the emulator. The image lives in
// a directory of its own for as long as the run lasts; once the tool has loaded it,
// the emulator holds a copy of its own.
func (e *FlashEmulate) Run(ctx context.Context, sesh module.Session, args ...string) error {
	l := log.FromContext(ctx)

	if len(args) < 1 {
		return errors.New("missing argument: image file path")
	}

	dir, err := os.MkdirTemp("", "flash-emulate-")
	if err != nil {
		return fmt.Errorf("create directory for the image on dutagent: %w", err)
	}

	defer func() {
		// The run's result stands; a leftover directory only takes up space.
		err := os.RemoveAll(dir)
		if err != nil {
			l.Warn(fmt.Sprintf("remove image directory: %v", err))
		}
	}()

	localImagePath := filepath.Join(dir, "image")

	err = uploadImage(sesh, args[0], localImagePath)
	if err != nil {
		return err
	}

	cmdArgs := e.cmdline(localImagePath)

	l.Info(fmt.Sprintf("loading image into emulator with %s", e.Tool))
	l.Debug(fmt.Sprintf("executing %s %s", e.Tool, strings.Join(cmdArgs, " ")))
	sesh.Printf("Executing: %s %s\n", e.Tool, strings.Join(cmdArgs, " "))

	err = execute(ctx, sesh, e.Tool, cmdArgs...)
	if err != nil {
		return fmt.Errorf("emulation failed: %w", err)
	}

	sesh.Println("Emulation started successfully")

	return nil
}

// cmdline returns the argument list for the emulation tool to load image:
// [--device <n>] --stop --set <chip> -d <image> -v --start.
func (e *FlashEmulate) cmdline(image string) []string {
	var args []string

	if e.Device != "" {
		args = append(args, "--device", e.Device)
	}

	args = append(args, "--stop", "--set", e.Chip, "-d", image, "-v", "--start")

	return args
}

// uploadImage receives the firmware image file from sesh and saves it locally.
func uploadImage(sesh module.Session, remote, local string) error {
	img, err := sesh.RequestFile(remote)
	if err != nil {
		return fmt.Errorf("request image from client: %w", err)
	}

	imgFile, err := os.Create(local)
	if err != nil {
		return fmt.Errorf("save image on dutagent: %w", err)
	}

	_, err = io.Copy(imgFile, img)
	// A failed close can mean the image did not reach the disk in full.
	err = errors.Join(err, imgFile.Close())
	if err != nil {
		return fmt.Errorf("save image on dutagent: %w", err)
	}

	return nil
}

func execute(ctx context.Context, sesh module.Session, tool string, args ...string) error {
	// Run in its own process group and SIGKILL the group on cancel, so a cancelled
	// Run kills the emulator tool and anything it spawned. Emulation is not a
	// destructive operation, so an unconditional kill is fine here.
	cmd := procexec.Command(ctx, syscall.SIGKILL, procexec.DefaultGrace, tool, args...)

	output, err := cmd.CombinedOutput()

	if len(output) > 0 {
		sesh.Print(string(output))
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("emulation tool exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute %s: %w", tool, err)
	}

	return nil
}

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

// tmpDirPerm is the permission bits used when creating the temporary image directory.
const tmpDirPerm = 0o700

// FlashEmulate is a module that loads a firmware image into an SPI flash emulator connected to the DUT.
type FlashEmulate struct {
	// Tool is the path to the emulation tool on the dutagent. Defaults to "em100".
	Tool string `yaml:"tool"`
	// Chip is the SPI flash chip type identifier to emulate (e.g. "N25Q256A13"). Required.
	Chip string `yaml:"chip"`
	// Device is the USB device number used when multiple emulators are connected. Optional.
	Device string `yaml:"device"`

	localImagePath  string // localImagePath is the path to the firmware image on the dutagent
	clientImagePath string // clientImagePath is the image path as named by the client
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
	help.WriteString(fmt.Sprintf("Using %q as emulation tool with chip %q.\n", e.Tool, e.Chip))

	if e.Device != "" {
		help.WriteString(fmt.Sprintf("Using USB device %q.\n", e.Device))
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

func (e *FlashEmulate) Deinit(_ context.Context) error {
	return os.RemoveAll(e.localImagePath)
}

func (e *FlashEmulate) Run(ctx context.Context, sesh module.Session, args ...string) error {
	l := log.FromContext(ctx)

	if len(args) < 1 {
		return errors.New("missing argument: image file path")
	}

	e.clientImagePath = args[0]

	// Clean up any image file left over from a previous Run call on this instance.
	if e.localImagePath != "" {
		_ = os.RemoveAll(e.localImagePath)
		e.localImagePath = ""
	}

	tmpDir := os.TempDir()

	err := os.MkdirAll(tmpDir, tmpDirPerm)
	if err != nil {
		return fmt.Errorf("create temp dir for image: %w", err)
	}

	tmpFile, err := os.CreateTemp(tmpDir, "flash-emulate-image-*")
	if err != nil {
		return fmt.Errorf("create temp image file: %w", err)
	}

	tmpFile.Close()

	e.localImagePath = tmpFile.Name()

	err = uploadImage(sesh, e.clientImagePath, e.localImagePath)
	if err != nil {
		_ = os.RemoveAll(e.localImagePath)
		e.localImagePath = ""

		return err
	}

	cmdArgs := e.cmdline()

	l.Info(fmt.Sprintf("loading image into emulator with %s", e.Tool))
	l.Debug(fmt.Sprintf("executing %s %s", e.Tool, strings.Join(cmdArgs, " ")))
	sesh.Print(fmt.Sprintf("Executing: %s %s", e.Tool, strings.Join(cmdArgs, " ")))

	err = execute(ctx, sesh, e.Tool, cmdArgs...)
	if err != nil {
		_ = os.RemoveAll(e.localImagePath)
		e.localImagePath = ""

		return fmt.Errorf("emulation failed: %w", err)
	}

	sesh.Print("Emulation started successfully")

	return nil
}

// cmdline returns the argument list for the emulation tool:
// [--device <n>] --stop --set <chip> -d <image> -v --start.
func (e *FlashEmulate) cmdline() []string {
	var args []string

	if e.Device != "" {
		args = append(args, "--device", e.Device)
	}

	args = append(args, "--stop", "--set", e.Chip, "-d", e.localImagePath, "-v", "--start")

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
	if err != nil {
		_ = imgFile.Close()
		_ = os.Remove(local)

		return fmt.Errorf("save image on dutagent: %w", err)
	}

	err = imgFile.Close()
	if err != nil {
		_ = os.Remove(local)

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

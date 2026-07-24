// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flash provides a dutagent module that reads or writes the SPI flash on the DUT.
// This module is a wrapper around a flash tool that is executed on a dutagent.
package flash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/internal/procexec"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// flashCancelGrace is how long a cancelled flash tool is given to stop on SIGTERM
// before it is killed. Flashing must not be cut mid-write, so the tool is asked
// to exit cleanly first and only killed hard if it overruns this window.
const flashCancelGrace = 10 * time.Second

func init() {
	module.Register(module.Record{
		ID: "flash",
		New: func() module.Module {
			return &Flash{
				supportedTools: []string{"flashrom", "flashprog", "dpcmd"},
			}
		},
	})
}

// localImagePath is the local path on the dutagent used to temporarily store the flash
// image during read/write operations.
const localImagePath = "./image"

// op represents the flash operation.
type op string

const (
	opWrite op = "write"
	opRead  op = "read"
)

// Flash is a module that reads or writes the SPI flash on the DUT.
type Flash struct {
	// Tool is the path to the underlying flash-tool on the dutagent.
	// It must name (or point to) one of these tools:
	//
	//  flashrom
	//  flashprog
	//  dpcmd
	Tool string `yaml:"tool"`
	// Programmer is the name of the flasher hardware.
	// For flashrom/flashprog: passed via -p flag (e.g., "dediprog", "ch341a_spi").
	// For dpcmd: optional, used with --device flag to select specific USB device number.
	Programmer string `yaml:"programmer"`

	op              op
	localImagePath  string
	clientImagePath string
	supportedTools  []string // supportedTools is a list of base names of supported flash tools
}

// Ensure implementing the Module interface.
var _ module.Module = &Flash{}

const abstract = `Read and write the SPI flash.
`

const usage = `
ARGUMENTS:
	[read | write] <image>

`

const description = `
For read operation, <image> sets the filepath the read image is saved at the client.
For write operation, <image> is the local filepath to the image at the client.

This module is a wrapper around a flasher tool on the dutagent. The flasher tool
must be installed on the dutagent and a suitable flasher hardware must be hooked up to
the DUT.

`

func (f *Flash) Help() string {
	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description)
	help.WriteString(fmt.Sprintf("Using %q as flash tool with programmer %q.\n", f.Tool, f.Programmer))

	return help.String()
}

// Init validates the module configuration. Tool must name a supported flash tool that is
// resolvable on the dutagent's PATH, and Programmer must be set unless the tool is dpcmd,
// which auto-detects its hardware.
func (f *Flash) Init(ctx context.Context) error {
	if f.Tool == "" {
		return fmt.Errorf("tool must be configured; supported tools are %v", f.supportedTools)
	}

	if !f.isSupported(f.Tool) {
		return fmt.Errorf("%q unsupported; supported tools are %v", f.Tool, f.supportedTools)
	}

	toolPath, err := exec.LookPath(f.Tool)
	if err != nil {
		return fmt.Errorf("flash tool %q: %w", f.Tool, err)
	}

	log.FromContext(ctx).Debug(fmt.Sprintf("using flash tool %s at %s", f.Tool, toolPath))

	// dpcmd auto-detects hardware, so programmer is optional
	// flashrom/flashprog require a programmer to be specified
	base := filepath.Base(f.Tool)
	if base != "dpcmd" && f.Programmer == "" {
		return fmt.Errorf("programmer must be configured for %q", base)
	}

	return nil
}

func (f *Flash) isSupported(tool string) bool {
	base := filepath.Base(tool)

	return slices.Contains(f.supportedTools, base)
}

// Deinit removes the temporary flash image file stored locally on the dutagent.
func (f *Flash) Deinit(_ context.Context) error {
	return os.RemoveAll(f.localImagePath)
}

// Run performs a flash operation. args must be "read" or "write" followed by an image path.
// For a write, the image is uploaded from the client before flashing; for a read, the image
// is downloaded to the client afterward.
//
//nolint:cyclop
func (f *Flash) Run(ctx context.Context, sesh module.Session, args ...string) error {
	l := log.FromContext(ctx)

	if len(args) < 1 {
		return errors.New("missing argument: flash operation")
	}

	switch op := args[0]; op {
	case string(opRead):
		f.op = opRead
	case string(opWrite):
		f.op = opWrite
	default:
		return fmt.Errorf("unknown operation %q", op)
	}

	//nolint:mnd
	if len(args) < 2 {
		return errors.New("missing argument: image file name")
	}

	f.clientImagePath = args[1]
	f.localImagePath = localImagePath

	if f.op == opWrite {
		err := uploadImage(sesh, f.clientImagePath, f.localImagePath)
		if err != nil {
			return err
		}
	}

	action := "reading"
	if f.op == opWrite {
		action = "writing"
	}

	l.Info(fmt.Sprintf("%s flash with %s", action, f.Tool))

	cmdStr := fmt.Sprintf("%s %s", f.Tool, strings.Join(f.cmdline(), " "))

	l.Debug(fmt.Sprintf("executing %s", cmdStr))
	sesh.Print(fmt.Sprintf("Executing: %s", cmdStr))

	err := execute(ctx, sesh, f.Tool, f.cmdline()...)
	if err != nil {
		return fmt.Errorf("flash operation failed: %w", err)
	}

	sesh.Print("Flash operation completed successfully")

	time.Sleep(1 * time.Second)

	if f.op == opRead {
		err := downloadImage(sesh, f.localImagePath, f.clientImagePath)
		if err != nil {
			return err
		}
	}

	return nil
}

func execute(ctx context.Context, sesh module.Session, tool string, args ...string) error {
	// Run in its own process group and, on cancel, ask the whole group to stop with
	// SIGTERM — flashing must not be cut mid-write, so the tool is signalled
	// gracefully and only force-killed by os/exec if it overruns flashCancelGrace.
	shell := procexec.Command(ctx, syscall.SIGTERM, flashCancelGrace, tool, args...)

	output, err := shell.CombinedOutput()

	if len(output) > 0 {
		sesh.Print(string(output))
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Provide helpful error messages for common issues
			if strings.Contains(string(output), "Programmer initialization failed") {
				return errors.New("programmer initialization failed, check if the programmer hardware is connected and supported")
			}

			return fmt.Errorf("flash tool exited with code %d", exitErr.ExitCode())
		}

		return fmt.Errorf("failed to execute %s: %w", tool, err)
	}

	return nil
}

// cmdline returns the arg list for the wrapped flash tool.
// flashrom/flashprog use: -p <programmer> -r/-w <file>.
// dpcmd uses: -r/-u <file> [--device <n>].
func (f *Flash) cmdline() []string {
	base := filepath.Base(f.Tool)

	var args []string

	// dpcmd has different CLI syntax than flashrom/flashprog
	if base == "dpcmd" {
		// dpcmd auto-detects hardware, but can optionally specify device number
		if f.Programmer != "" {
			args = append(args, "--device", f.Programmer)
		}

		switch f.op {
		case opWrite:
			// dpcmd uses -u (update: erase then program) instead of -w
			args = append(args, "-u", f.localImagePath)
		case opRead:
			args = append(args, "-r", f.localImagePath)
		}
	} else {
		// flashrom and flashprog use the same CLI
		args = []string{"-p", f.Programmer}

		switch f.op {
		case opWrite:
			args = append(args, "-w", f.localImagePath)
		case opRead:
			args = append(args, "-r", f.localImagePath)
		}
	}

	return args
}

// uploadImage receives the flash image file from sesh and saves it locally.
func uploadImage(sesh module.Session, remote, local string) error {
	img, err := sesh.RequestFile(remote)
	if err != nil {
		return fmt.Errorf("request flash image from client for write operation: %w", err)
	}

	imgFile, err := os.Create(local)
	if err != nil {
		return fmt.Errorf("save flash image on dutagent for write operation: %w", err)
	}

	_, err = io.Copy(imgFile, img)
	if err != nil {
		return fmt.Errorf("save flash image on dutagent for write operation: %w", err)
	}

	return nil
}

// downloadImage sends the local flash image file to sesh.
func downloadImage(sesh module.Session, local, remote string) error {
	// The session takes ownership of the file via SendFile (chunked, async) and
	// closes it when the transfer completes, so we must NOT close it here.
	file, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("open flash image on dutagent after read operation: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat flash image on dutagent after read operation: %w", err)
	}

	err = sesh.SendFile(remote, fileInfo.Size(), file)
	if err != nil {
		return fmt.Errorf("send flash image to client after read operation: %w", err)
	}

	return nil
}

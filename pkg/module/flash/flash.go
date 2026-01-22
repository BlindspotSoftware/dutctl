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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

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

// localImagePath is the local path on the dutagent to temporally store the flash image
// during read/write operations.
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
	// It must be set to one of the following:
	// - flashrom
	// - flashprog
	// - dpcmd
	Tool string `yaml:"tool"`
	// Programmer is the name of the flasher hardware.
	// For flashrom/flashprog: passed via -p flag (e.g., "dediprog", "ch341a_spi").
	// For dpcmd: optional, used with --device flag to select specific USB device number.
	Programmer string `yaml:"programmer"`

	op              op       // op holds the current flash operation
	localImagePath  string   // localImagePath is the path to SPI image file at the dutagent
	clientImagePath string   // clientImagePath is image path named by the client
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
	log.Println("flash module: Help called")

	help := strings.Builder{}
	help.WriteString(abstract)
	help.WriteString(usage)
	help.WriteString(description)
	help.WriteString(fmt.Sprintf("Using %q as flash tool with programmer %q.\n", f.Tool, f.Programmer))

	return help.String()
}

func (f *Flash) Init() error {
	log.Println("flash module: Init called")

	if f.Tool == "" {
		return fmt.Errorf("tool must be configured; supported tools are %v", f.supportedTools)
	}

	if !f.isSupported(f.Tool) {
		return fmt.Errorf("%q unsupported; supported tools are %v", f.Tool, f.supportedTools)
	}

	_, err := exec.LookPath(f.Tool)
	if err != nil {
		return fmt.Errorf("flash tool %q: %w", f.Tool, err)
	}

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

func (f *Flash) Deinit() error {
	log.Println("flash module: Deinit called")

	return os.RemoveAll(f.localImagePath)
}

//nolint:cyclop
func (f *Flash) Run(_ context.Context, sesh module.Session, args ...string) error {
	log.Println("flash module: Run called")

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

	cmdStr := fmt.Sprintf("%s %s", f.Tool, strings.Join(f.cmdline(), " "))

	log.Printf("flash module: Executing command: %s", cmdStr)
	sesh.Print(fmt.Sprintf("Executing: %s", cmdStr))

	err := execute(sesh, f.Tool, f.cmdline()...)
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

func execute(sesh module.Session, tool string, args ...string) error {
	//nolint:noctx
	shell := exec.Command(tool, args...)

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

// uploadImage receives and saves the flash image from client.
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

// downloadImage sends the flash image to client.
func downloadImage(sesh module.Session, local, remote string) error {
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

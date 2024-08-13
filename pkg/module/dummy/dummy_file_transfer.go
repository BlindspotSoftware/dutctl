package dummy

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// DummyFT is a dummy file transfer module.
// It requests a file from the client and writes it to a file, processes it, and sends it back.
type DummyFT struct{}

// Ensure implementing the Module interface.
var _ module.Module = &DummyFT{}

func (d *DummyFT) Help() string {
	log.Println("DummyFT module: Help called")

	return "This dummy module demonstrates file transfer."
}

func (d *DummyFT) Init() error {
	log.Println("DummyFT module: Init called")

	return nil
}

func (d *DummyFT) Deinit() error {
	log.Println("DummyFT module: Deinit called")

	return nil
}

func (d *DummyFT) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("DummyFT module: Run called")

	s.Print("Hello from dummy file transfer module")

	str := fmt.Sprintf("Called with %d arguments", len(args))
	s.Print(str)

	if len(args) != 1 {
		return fmt.Errorf("expected 1 argument, got %d", len(args))
	}

	inFile := args[0]
	str = fmt.Sprintf("Requesting file: %s", inFile)
	s.Print(str)

	fr, err := s.RequestFile(inFile)
	if err != nil {
		return fmt.Errorf("file request failed: %v", err)
	}

	log.Printf("Dummy-Module: Reading file: %s", inFile)
	raw, err := io.ReadAll(fr)
	if err != nil {
		log.Printf("Dummy-Module: Failed to read file: %v", err)
		return fmt.Errorf("failed to read file: %v", err)
	}

	log.Printf("Dummy-Module: prepare writing file")
	dir, err := os.MkdirTemp("", "dutagent-out")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}

	path := filepath.Join(dir, filepath.Base(inFile))

	err = os.WriteFile(path, raw, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("Dummy-Module: Wrote file to: %s", path)

	s.Print("File written successfully")

	return nil
}

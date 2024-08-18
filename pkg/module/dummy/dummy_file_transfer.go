package dummy

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Info{
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
	log.Println("dummy.FT module: Help called")

	return "This dummy module demonstrates file transfer."
}

func (d *FT) Init() error {
	log.Println("dummy.FT module: Init called")

	return nil
}

func (d *FT) Deinit() error {
	log.Println("dummy.FT module: Deinit called")

	return nil
}

func (d *FT) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("dummy.FT module: Run called")

	s.Print("Hello from dummy file transfer module")

	str := fmt.Sprintf("Called with %d arguments", len(args))
	s.Print(str)

	if len(args) != 1 {
		return fmt.Errorf("expected 1 argument, got %d", len(args))
	}

	inFile := args[0]
	str = "Requesting file: " + inFile
	s.Print(str)

	fileReq, err := s.RequestFile(inFile)
	if err != nil {
		return fmt.Errorf("file request failed: %v", err)
	}

	log.Printf("dummy.FT module: Reading file: %s", inFile)

	raw, err := io.ReadAll(fileReq)
	if err != nil {
		log.Printf("dummy.FT module: Failed to read file: %v", err)

		return fmt.Errorf("failed to read file: %v", err)
	}

	log.Printf("dummy.FT module: prepare writing file")

	dir, err := os.MkdirTemp("", "dutagent-out")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}

	path := filepath.Join(dir, filepath.Base(inFile))

	perm := 0600

	err = os.WriteFile(path, raw, fs.FileMode(perm))
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("dummy.FT module: Wrote file to: %s", path)

	s.Print("File written successfully")

	return nil
}

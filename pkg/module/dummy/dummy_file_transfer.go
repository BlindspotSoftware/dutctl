package dummy

import (
	"bytes"
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

	fileReader, err := s.RequestFile(inFile)
	if err != nil {
		return fmt.Errorf("file request failed: %v", err)
	}

	log.Printf("dummy.FT module: Reading file: %s", inFile)

	raw, err := io.ReadAll(fileReader)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	err = save(raw, inFile)
	if err != nil {
		return fmt.Errorf("failed to save file: %v", err)
	}

	result, err := process(raw)
	if err != nil {
		return fmt.Errorf("failed to process file: %v", err)
	}

	log.Print("dummy.FT module: Sending processed file")

	err = s.SendFile("processed.txt", bytes.NewBuffer(result))
	if err != nil {
		return fmt.Errorf("failed to send file: %v", err)
	}

	s.Print("File operated successfully")

	return nil
}

func save(raw []byte, path string) error {
	log.Printf("dummy.FT module: Save received content on disk")

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

	log.Printf("dummy.FT module: Wrote file to: %s", dest)

	return nil
}

func process(input []byte) ([]byte, error) {
	log.Printf("dummy.FT module: Process received content")

	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	// Dummy processing
	input = append(input, []byte("\n\nprocessed by dummy.FT module\n")...)

	log.Printf("dummy.FT module: Processed content")

	return input, nil
}

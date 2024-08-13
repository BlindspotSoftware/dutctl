// Dummy module implementation.
package dummy

import (
	"context"
	"fmt"
	"log"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// DummyStatus prints status information about itself and the environment.
// It demonstrates the use of the Print method of module.Session to send messages to the client.
type DummyStatus struct{}

// Ensure implementing the Module interface.
var _ module.Module = &DummyStatus{}

func (d *DummyStatus) Help() string {
	log.Println("DummyStatus module: Help called")

	return "This dummy module prints status information about itself and the environment."
}

func (d *DummyStatus) Init() error {
	log.Println("DummyStatus module: Init called")

	return nil
}

func (d *DummyStatus) Deinit() error {
	log.Println("DummyStatus module: Deinit called")

	return nil
}

func (d *DummyStatus) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("DummyStatus module: Run called")

	s.Print("Hello from dummy status module")

	str := fmt.Sprintf("Called with %d arguments", len(args))
	s.Print(str)

	for i, arg := range args {
		str := fmt.Sprintf("Arg %d: %s", i, arg)
		s.Print(str)
	}

	return nil
}

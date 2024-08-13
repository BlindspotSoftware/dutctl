package dummy

import (
	"bufio"
	"context"
	"log"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// DummyRepeat repeats the input from the client.
// It demonstrates the use of the Console method of module.Session to interact with the client.
type DummyRepeat struct{}

// Ensure implementing the Module interface.
var _ module.Module = &DummyRepeat{}

func (d *DummyRepeat) Help() string {
	log.Println("DummyRepeat module: Help called")

	return "This dummy module repeats the input from the client."
}

func (d *DummyRepeat) Init() error {
	log.Println("DummyRepeat module: Init called")

	return nil
}

func (d *DummyRepeat) Deinit() error {
	log.Println("DummyRepeat module: Deinit called")

	return nil
}

func (d *DummyRepeat) Run(_ context.Context, s module.Session, args ...string) error {
	log.Println("DummyRepeat module: Run called")

	cin, cout, cerr := s.Console()

	_, err := cout.Write([]byte("Hello from dummy repeat module!\nEnter one word per line. (Two words will terminate)\n"))
	if err != nil {
		log.Println("dummy error writing to client: ", err)

		return err
	}

	r := bufio.NewReader(cin)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			log.Println("dummy error reading from client: ", err)

			return err
		}

		line = strings.TrimSuffix(line, "\n")

		words := strings.Split(line, " ")
		if len(words) > 1 {
			_, err = cerr.Write([]byte("Oh no! Can only handle one word per line.\n"))
			if err != nil {
				log.Println("dummy error writing to client: ", err)

				return err
			}
			return nil
		}

		_, err = cout.Write([]byte(words[0] + "\n"))
		if err != nil {
			log.Println("dummy error writing to client: ", err)

			return err
		}
	}
}

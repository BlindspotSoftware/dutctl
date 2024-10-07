// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package dummy

import (
	"bufio"
	"context"
	"log"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Info{
		ID:  "dummy-repeat",
		New: func() module.Module { return &Repeat{} },
	})
}

// Repeat repeats the input from the client.
// It demonstrates the use of the Console method of module.Session to interact with the client.
type Repeat struct{}

// Ensure implementing the Module interface.
var _ module.Module = &Repeat{}

func (d *Repeat) Help() string {
	log.Println("dummy.Repeat module: Help called")

	return "This dummy module repeats the input from the client."
}

func (d *Repeat) Init() error {
	log.Println("dummy.Repeat module: Init called")

	return nil
}

func (d *Repeat) Deinit() error {
	log.Println("dummy.Repeat module: Deinit called")

	return nil
}

func (d *Repeat) Run(_ context.Context, s module.Session, _ ...string) error {
	log.Println("dummy.Repeat module: Run called")

	cin, cout, cerr := s.Console()

	_, err := cout.Write([]byte("Hello from dummy repeat module!\nEnter one word per line. (Two words will terminate)\n"))
	if err != nil {
		log.Println("dummy.Repeat module: error writing to client: ", err)

		return err
	}

	r := bufio.NewReader(cin)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			log.Println("dummy.Repeat module: error reading from client: ", err)

			return err
		}

		line = strings.TrimSuffix(line, "\n")

		words := strings.Split(line, " ")
		if len(words) > 1 {
			_, err = cerr.Write([]byte("Oh no! Can only handle one word per line.\n"))
			if err != nil {
				log.Println("dummy.Repeat module: error writing to client: ", err)

				return err
			}

			return nil
		}

		_, err = cout.Write([]byte(words[0] + "\n"))
		if err != nil {
			log.Println("dummy.Repeat module: error writing to client: ", err)

			return err
		}
	}
}

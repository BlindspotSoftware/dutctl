package dutagent

import (
	"fmt"
	"log"

	"github.com/BlindspotSoftware/dutctl/pkg/dut"
)

// ModuleInitError is a container for errors that occur during module
// initialization.
type ModuleInitError struct {
	Errs map[string]error
}

func (e *ModuleInitError) Error() string {
	return fmt.Sprintf("\n%d initialization errors", len(e.Errs))
}

// Init runs the Init function of all modules for all commands of the provided
// devices. All init functions are called, even if an error occurs. In this case
// the an ModuleInitErr is returned that holds all errors reported by the modules.
func Init(devices dut.Devlist) error {
	var ierr = &ModuleInitError{
		Errs: map[string]error{},
	}

	for devname, device := range devices {
		for cmdname, cmd := range device.Cmds {
			for _, module := range cmd.Modules {
				err := module.Init()
				if err != nil {
					m := fmt.Sprintf("dev: %s, cmd: %s, mod: %s", devname, cmdname, module.Config.Name)
					ierr.Errs[m] = err
				}
			}
		}
	}

	if len(ierr.Errs) != 0 {
		return ierr
	}

	log.Print("Module Initialization OK")

	return nil
}

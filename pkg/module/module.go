// Package module provides a plugin system for the DUT package.
// Modules are the building blocks of a command and host the actual implementation
// of the steps that are executed on a device-under-test (DUT).
// The core of the plugin system is the Module interface.
package module

import (
	"context"
	"fmt"
	"io"
	"sync"
)

//nolint:gochecknoglobals
var (
	modules = make(map[string]Info)
	mutex   sync.RWMutex
)

// Module is a building block of a command running on a device-under-test (DUT).
// Implementations of this interface are the actual steps that are executed on a DUT.
type Module interface {
	// Help returns a formatted string with the capabilities of the module.
	// It provides any user information required to interact with the module.
	Help() string
	// Init is called when the module is loaded by dutagent on an execution request for a command that uses this module.
	Init() error
	// Deinit is called when the module is unloaded by dutagent after the execution of a command that uses this module.
	// It is used to clean up any resources that were allocated during the Init phase.
	Deinit() error
	// Run is the entry point and executes the module with the given arguments.
	Run(ctx context.Context, s Session, args ...string) error
}

// Session provides an environment / a context for a module.
// Via the Session interface, modules can interact with the client during execution.
type Session interface {
	Print(text string)
	Console() (stdin io.Reader, stdout, stderr io.Writer)
	RequestFile(name string) (io.Reader, error)
	SendFile(name string, r io.Reader) error
}

// Info holds the information required to register a module.
type Info struct {
	// ID is the unique identifier of the module.
	// It is used to reference the module in the dutagent configuration.
	ID string
	// New is the factory function that creates a new instance of the module.
	New func() Module
}

// Register registers a module for use in dutagent.
func Register(mod Info) {
	if mod.ID == "" {
		panic("module ID missing")
	}

	if mod.ID == "help" || mod.ID == "info" {
		panic(fmt.Sprintf("module ID '%s' is reserved", mod.ID))
	}

	if mod.New == nil {
		panic("missing factory function 'New func() Module'")
	}

	mutex.Lock()
	defer mutex.Unlock()

	if _, ok := modules[mod.ID]; ok {
		panic(fmt.Sprintf("module already registered: %s", mod.ID))
	}

	modules[mod.ID] = mod
}

// New creates a new instance of a former registered module by its ID.
// func New(id string) (Module, error) {
// 	if id == "" {
// 		return nil, errors.New("module ID must not be empty")
// 	}

// 	mod, ok := modules[id]
// 	if !ok {
// 		return nil, errors.New("module not found")
// 	}

// 	return mod.New(), nil
// }

// New creates a new instance of a former registered module by its ID.
// Unsecure! Add error handling to this function.
func New(id string) Module {
	mod, ok := modules[id]
	if !ok {
		return nil
	}

	return mod.New()
}

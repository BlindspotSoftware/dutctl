// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package module provides a plugin system for the DUT package.
// Modules are the building blocks of a command and host the actual implementation
// of the steps that are executed on a device-under-test (DUT).
// The core of the plugin system is the Module interface.
package module

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

//nolint:gochecknoglobals
var (
	modules = make(map[string]Record)
	mutex   sync.RWMutex
)

// Module is a building block of a command running on a device-under-test (DUT).
// Implementations of this interface are the actual steps that are executed on a DUT.
type Module interface {
	// Help provides usage information.
	// The returned string should contain a description of the module the supported
	// command line arguments and any other information required to interact with the module.
	// The returned string should be formatted in a way that it can be displayed to the user.
	//
	// Implementations should consider the module's concrete configuration and potentially
	// return individual help messages based on the configuration. It is not the purpose
	// of this method to provide a generic help message for all possible configurations,
	// but rather usage information for the current configuration.
	Help() string
	// Init is called once when the dutagent services is started.
	// It's a good place to establish connections or allocate resources and check whether
	// the module is configured functional. It is also called when a command containing this
	// module is called as a dry-run to check the configuration.
	Init() error
	// Deinit is called when the module is unloaded by dutagent or an internal error occurs.
	// It is used to clean up any resources that were allocated during the Init phase and
	// shall guarantee a graceful shutdown of the service.
	Deinit() error
	// Run is the entry point and executes the module with the given arguments.
	Run(ctx context.Context, s Session, args ...string) error
}

// Session provides an environment / a context for a module.
// Via the Session interface, modules can interact with the client during execution.
type Session interface {
	// Print sends a message to the client. Implementations should wrap [fmt.Sprint].
	// The message is displayed in the console or GUI of the client.
	Print(a ...any)
	// Printf sends a formatted message to the client. Implementations should wrap [fmt.Sprintf].
	// The message is displayed in the console or GUI of the client.
	Printf(format string, a ...any)
	// Println sends a message with appended newline to the client. Implementations should wrap [fmt.Sprintln].
	// The message is displayed in the console or GUI of the client.
	Println(a ...any)
	// Console returns the stdin, stdout and stderr streams for the module.
	// It thus indicates to the client that the module may wants to interact with the user
	// via standard input and output streams.
	Console() (stdin io.Reader, stdout, stderr io.Writer)
	// RequestFile requests a file from the client.
	// The file is identified by its name and is made available to the module via the returned io.Reader.
	RequestFile(name string) (io.Reader, error)
	// SendFile sends a file to the client.
	SendFile(name string, r io.Reader) error
}

// Record holds the information required to register a module.
type Record struct {
	// ID is the unique identifier of the module.
	// It is used to reference the module in the dutagent configuration.
	ID string
	// New is the factory function that creates a new instance of the module.
	// Most of the time, this function will return a pointer to a newly allocated struct
	// that implements the Module interface. It is not supposed to run initialization code
	// with side effects. The actual initialization should be done in the Init method of the Module.
	// Instead the factory function may serve as a constructor for the module and can be used to
	// allocate internal resources, like maps and slices or set up the initial state of the module.
	New func() Module
}

// Register registers a module for use in dutagent.
func Register(r Record) {
	if r.ID == "" {
		panic("module ID missing")
	}

	if r.ID == "help" || r.ID == "info" {
		panic(fmt.Sprintf("module ID '%s' is reserved", r.ID))
	}

	if r.New == nil {
		panic("missing factory function 'New func() Module'")
	}

	mutex.Lock()
	defer mutex.Unlock()

	if _, ok := modules[r.ID]; ok {
		panic(fmt.Sprintf("module already registered: %s", r.ID))
	}

	modules[r.ID] = r
}

// New creates a new instance of a former registered module by its unique name.
func New(name string) (Module, error) {
	if name == "" {
		return nil, errors.New("module name must not be empty")
	}

	mod, ok := modules[name]
	if !ok {
		const helpURL = "https://github.com/BlindspotSoftware/dutctl/blob/main/docs/module_guide.md#registration"

		return nil, fmt.Errorf("module %q not found, maybe not registered, see %s", name, helpURL)
	}

	return mod.New(), nil
}

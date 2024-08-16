// Package module provides a plugin system for the DUT package.
// Modules are the building blocks of a command and host the actual implementation
// of the steps that are executed on a device-under-test (DUT).
// The core of the plugin system is the Module interface.
package module

import (
	"context"
	"io"
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

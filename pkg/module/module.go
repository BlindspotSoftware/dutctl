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
	Help() string
	Init() error
	Deinit() error
	Run(ctx context.Context, s Session, args ...string) error
}

// Session provides an environment / a context for a module.
// With the Session interface, modules can interact with the DUT.
type Session interface {
	Print(text string)
	Console() (stdin io.Reader, stdout, stderr io.Writer)
	RequestFile(name string) (io.Reader, error)
	SendFile(name string, r io.Reader) error
}

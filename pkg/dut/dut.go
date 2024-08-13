// Package dut provides representation of the device-under-test (DUT).
package dut

import "github.com/BlindspotSoftware/dutctl/pkg/module"

// Device is the representation of a device-under-test (DUT).
type Device struct {
	Desc string
	Cmds map[string]Command
}

// Command represents a task that can be executed on a device-under-test (DUT).
// This task is composed of one or multiple steps. The steps are implemented by
// modules and are executed in the order they are defined.
type Command struct {
	Desc    string
	Modules []Module
}

// Module is a wrapper for any module implementation.
type Module struct {
	Name    string
	Main    bool
	Args    []string
	Options map[string]any

	module.Module
}

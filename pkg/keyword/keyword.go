// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keyword is the single source of truth for names reserved by dutctl
// client-side dispatch. It is shared by the dutctl client (which dispatches the
// keywords) and by dutagent config validation via pkg/dut (which rejects device
// or command names that collide with a keyword).
//
// A keyword carries both its reservation metadata (name, description) and the
// handler that dispatches it. Handlers operate through the Client interface so
// this package stays free of client-binary internals.
package keyword

import "errors"

// Client is implemented by the dutctl application. Keyword handlers dispatch
// through it instead of referencing the client binary directly, which would
// otherwise be impossible to import.
type Client interface {
	Lock(device string, args []string) error
	Unlock(device string) error
	Details(device, command, kw string) error
	PrintUsage() error
}

// Position identifies where a command keyword appears on the command line.
type Position int

const (
	// CommandSlot keywords occupy the command position and operate on the
	// device, e.g. "dutctl <device> lock".
	CommandSlot Position = iota
	// AfterCommand keywords follow a command, e.g. "dutctl <device> <cmd> help".
	AfterCommand
)

// CommandHandlerFunc dispatches a command keyword. device and command are
// args[0] and args[1]; args holds the trailing tokens (args[2:]).
type CommandHandlerFunc func(c Client, device, command string, args []string) error

// DeviceHandlerFunc dispatches a keyword in the device position, e.g.
// "dutctl help".
type DeviceHandlerFunc func(c Client, name string) error

// CommandKeyword is a name reserved at the command position. A module command
// must not be configured with such a name; the dutagent refuses the config at
// startup.
type CommandKeyword struct {
	Name        string
	Description string
	Position    Position
	Handler     CommandHandlerFunc
}

// DeviceKeyword is a name reserved at the device position. A device must not be
// configured with such a name.
type DeviceKeyword struct {
	Name        string
	Description string
	Handler     DeviceHandlerFunc
}

// CommandKeywords lists every name reserved at the command position.
//
//nolint:gochecknoglobals // single source of truth for reserved command names.
var CommandKeywords = []CommandKeyword{
	{
		Name:        "help",
		Description: "Show usage for the command.",
		Position:    AfterCommand,
		Handler: func(c Client, device, command string, _ []string) error {
			return c.Details(device, command, "help")
		},
	},
	{
		Name:        "lock",
		Description: "Reserve the device for exclusive use.",
		Position:    CommandSlot,
		Handler: func(c Client, device, _ string, args []string) error {
			return c.Lock(device, args)
		},
	},
	{
		Name:        "unlock",
		Description: "Release a reservation on the device.",
		Position:    CommandSlot,
		Handler: func(c Client, device, _ string, _ []string) error {
			return c.Unlock(device)
		},
	},
}

// DeviceKeywords lists every name reserved at the device position.
//
//nolint:gochecknoglobals // single source of truth for reserved device names.
var DeviceKeywords = []DeviceKeyword{
	{
		Name:        "help",
		Description: "Show dutctl usage information.",
		Handler: func(c Client, _ string) error {
			return c.PrintUsage()
		},
	},
}

// ErrReservedName is returned when a device or command in a dutagent
// configuration is named with a reserved keyword.
var ErrReservedName = errors.New("name is reserved")

// IsReservedCommandName reports whether name collides with a command keyword.
func IsReservedCommandName(name string) bool {
	for _, kw := range CommandKeywords {
		if kw.Name == name {
			return true
		}
	}

	return false
}

// IsReservedDeviceName reports whether name collides with a device keyword.
func IsReservedDeviceName(name string) bool {
	for _, kw := range DeviceKeywords {
		if kw.Name == name {
			return true
		}
	}

	return false
}

// CommandHandler returns the handler for the command keyword named name at the
// given position, if one exists.
func CommandHandler(name string, pos Position) (CommandHandlerFunc, bool) {
	for _, kw := range CommandKeywords {
		if kw.Name == name && kw.Position == pos {
			return kw.Handler, true
		}
	}

	return nil, false
}

// DeviceHandler returns the handler for the device keyword named name, if one
// exists.
func DeviceHandler(name string) (DeviceHandlerFunc, bool) {
	for _, kw := range DeviceKeywords {
		if kw.Name == name {
			return kw.Handler, true
		}
	}

	return nil, false
}

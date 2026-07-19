// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keyword is the single source of truth for the command-line keywords
// that dutctl reserves. It is imported by the dutctl client, which dispatches
// the keywords, and by dutagent configuration validation (via pkg/dut), which
// rejects device or command names that would collide with one.
//
// Reservation is scoped by grammar position, so it restricts device and module
// command naming no more than necessary. A device is addressed by the first
// positional argument, so a device named like a device-position keyword (list,
// version) is unreachable and rejected. A command is the second positional, so
// a command named like a command-position keyword (lock, unlock) is unreachable
// and rejected; help is additionally reserved as a command name so that
// "dutctl <device> help" is never ambiguous. Names outside their colliding
// position stay usable: a device may be named "lock", a command "list".
package keyword

import "errors"

// The keywords dutctl reserves on the command line. Every site that dispatches
// or validates a keyword references these constants rather than a string
// literal, so the vocabulary lives in exactly one place.
const (
	// Version prints version information: "dutctl version".
	Version = "version"
	// List lists all available devices: "dutctl list".
	List = "list"
	// Lock reserves a device: "dutctl <device> lock [duration]".
	Lock = "lock"
	// Unlock releases a device: "dutctl <device> unlock [force]".
	Unlock = "unlock"
	// Help shows a command's usage: "dutctl <device> <command> help".
	Help = "help"
	// Force breaks another owner's lock: "dutctl <device> unlock force".
	Force = "force"
)

// ErrReservedName is wrapped in a configuration error when a device or command
// is named with a reserved keyword. Match it with errors.Is.
var ErrReservedName = errors.New("name is reserved")

// IsReservedDeviceName reports whether name is reserved from use as a device
// name. list and version are dispatched in the device position (the first
// positional argument) and would shadow a device so named.
func IsReservedDeviceName(name string) bool {
	switch name {
	case List, Version:
		return true
	default:
		return false
	}
}

// IsReservedCommandName reports whether name is reserved from use as a module
// command name. lock and unlock are dispatched in the command position and
// would shadow a command so named; help is additionally reserved so that
// "dutctl <device> help" is never ambiguous between a command and the help
// keyword.
func IsReservedCommandName(name string) bool {
	switch name {
	case Lock, Unlock, Help:
		return true
	default:
		return false
	}
}

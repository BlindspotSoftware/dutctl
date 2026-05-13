// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import "errors"

// CommandKeyword is a name reserved for client-side dispatch when it
// appears as the first argument after a command (e.g. "<device> <cmd>
// help"). A module command must not be configured with such a name; the
// dutagent will refuse the config at startup.
type CommandKeyword struct {
	Name        string
	Description string
}

// DeviceKeyword is a name reserved for client-side dispatch when it
// appears as the only argument after a device (e.g. "<device>
// description"). A device must not be configured with such a name.
type DeviceKeyword struct {
	Name        string
	Description string
}

// CommandKeywords lists every name reserved at the command-keyword
// position. This list is the single source of truth used by both
// dutctl dispatch and dutagent config validation.
//
//nolint:gochecknoglobals // single source of truth for reserved names.
var CommandKeywords = []CommandKeyword{
	{Name: "help", Description: "Show usage for the command."},
}

// DeviceKeywords lists every name reserved at the device-keyword
// position.
//
//nolint:gochecknoglobals // single source of truth for reserved names.
var DeviceKeywords = []DeviceKeyword{
	{Name: "help", Description: "Show dutctl usage information."},
}

// ErrReservedName is returned when a device or command in a dutagent
// configuration is named with a reserved keyword.
var ErrReservedName = errors.New("name is reserved")

// IsReservedCommandName reports whether name collides with a command
// keyword.
func IsReservedCommandName(name string) bool {
	for _, kw := range CommandKeywords {
		if kw.Name == name {
			return true
		}
	}

	return false
}

// IsReservedDeviceName reports whether name collides with a device
// keyword.
func IsReservedDeviceName(name string) bool {
	for _, kw := range DeviceKeywords {
		if kw.Name == name {
			return true
		}
	}

	return false
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dut provides representation of the device-under-test (DUT).
package dut

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

var (
	ErrDeviceNotFound  = errors.New("no such device")
	ErrCommandNotFound = errors.New("no such command")
	ErrNoMainForArgs   = errors.New("arguments provided but command has no main module to receive them")
)

// Devlist is a list of devices-under-test.
type Devlist map[string]Device

// Names returns the names of all devices in the list.
// The names are sorted alphabetically.
func (devs *Devlist) Names() []string {
	names := make([]string, 0, len(*devs))
	for d := range *devs {
		names = append(names, d)
	}

	slices.Sort(names)

	return names
}

// CmdNames returns the names of all commands available for a device or
// ErrDeviceNotFound if the device is not found. The names are sorted alphabetically.
// A device with no commands will not report an error but return an empty slice.
func (devs *Devlist) CmdNames(device string) ([]string, error) {
	dev, ok := (*devs)[device]
	if !ok {
		return []string{}, ErrDeviceNotFound
	}

	cmds := make([]string, 0, len(dev.Cmds))
	for c := range dev.Cmds {
		cmds = append(cmds, c)
	}

	slices.Sort(cmds)

	return cmds, nil
}

// FindCmd returns the device and command for a given device and command name.
// If the device is not found, it returns ErrDeviceNotFound, if the command is not found,
// it returns ErrCommandNotFound. If the requested command has no modules, it returns ErrNoModules.
// If the requested command has multiple main modules, it returns ErrMultipleMainModules.
func (devs *Devlist) FindCmd(device, command string) (Device, Command, error) {
	dev, ok := (*devs)[device]
	if !ok {
		return Device{}, Command{}, ErrDeviceNotFound
	}

	cmd, ok := dev.Cmds[command]
	if !ok {
		return dev, Command{}, ErrCommandNotFound
	}

	if len(cmd.Modules) == 0 {
		return dev, cmd, ErrNoModules
	}

	if cmd.countMain() > 1 {
		return dev, cmd, ErrMultipleMainModules
	}

	return dev, cmd, nil
}

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
	Modules []Module `yaml:"uses"`
}

// HasMain reports whether the command has a main module.
func (c *Command) HasMain() bool {
	return c.countMain() > 0
}

func (c *Command) countMain() int {
	count := 0

	for _, mod := range c.Modules {
		if mod.Config.Main {
			count++
		}
	}

	return count
}

// ModuleArgs builds the argument list for each module in the command.
// Main modules receive runtimeArgs directly. Non-main modules
// receive their statically configured Args. The returned slice has the same length
// and ordering as c.Modules.
func (c *Command) ModuleArgs(runtimeArgs []string) ([][]string, error) {
	if len(runtimeArgs) > 0 && !c.HasMain() {
		return nil, ErrNoMainForArgs
	}

	result := make([][]string, len(c.Modules))

	for i, mod := range c.Modules {
		if mod.Config.Main {
			result[i] = runtimeArgs
		} else {
			result[i] = mod.Config.Args
		}
	}

	return result, nil
}

// HelpText returns the help string of the main module.
// If no main module exists, returns an overview of all modules.
func (c *Command) HelpText() string {
	for _, mod := range c.Modules {
		if mod.Config.Main {
			return mod.Help()
		}
	}

	// If no main module, provide overview of all modules

	moduleNames := make([]string, 0, len(c.Modules))
	for _, module := range c.Modules {
		moduleNames = append(moduleNames, module.Config.Name)
	}

	helpStr := fmt.Sprintf("Command with %d module(s): %s",
		len(c.Modules), strings.Join(moduleNames, ", "))

	return helpStr
}

// Module is a wrapper for any module implementation.
type Module struct {
	module.Module

	Config ModuleConfig
}

type ModuleConfig struct {
	Name    string `yaml:"module"`
	Main    bool
	Args    []string
	Options map[string]any `yaml:"with"`
}

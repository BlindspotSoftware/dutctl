// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dut provides the types that represent a device-under-test (DUT) and
// its command configuration.
package dut

import (
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Sentinel errors returned by the Devlist lookups (Find, CmdNames, FindCmd) and
// by Command.ModuleArgs; match them with errors.Is.
var (
	ErrDeviceNotFound    = errors.New("no such device")
	ErrCommandNotFound   = errors.New("no such command")
	ErrNoReceiverForArgs = errors.New("arguments provided but command has neither a passthrough module nor declared arguments to receive them")
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

// Find returns the named device or ErrDeviceNotFound if it is not present. It is
// the device-level companion to FindCmd and returns the bare sentinel; callers add
// their own context before mapping it to a status code (see the RPC handlers).
func (devs *Devlist) Find(device string) (Device, error) {
	dev, ok := (*devs)[device]
	if !ok {
		return Device{}, ErrDeviceNotFound
	}

	return dev, nil
}

// FindCmd returns the device and command for a given device and command name.
// If the device is not found, it returns ErrDeviceNotFound, if the command is not found,
// it returns ErrCommandNotFound. If the requested command has no modules, it returns ErrNoModules.
// If the requested command has multiple passthrough modules, it returns ErrMultiplePassthroughModules.
//
// ErrNoModules and ErrMultiplePassthroughModules are defensive: configuration
// validation already rejects both at load time (see config.go), so on the request
// path they are effectively unreachable and the handlers map them to an internal
// error code by design.
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

	if cmd.countPassthrough() > 1 {
		return dev, cmd, ErrMultiplePassthroughModules
	}

	return dev, cmd, nil
}

// ModuleRef locates a single module within the device list: the device and
// command it belongs to, together with the module itself.
type ModuleRef struct {
	Device  string
	Command string
	Module  Module
}

// AllModules iterates every module across all devices and commands. Iteration
// order is unspecified (it follows Go map iteration). It serves whole-system
// sweeps such as agent startup/shutdown; request-path logic addresses a specific
// module through FindCmd instead.
func (devs *Devlist) AllModules() iter.Seq[ModuleRef] {
	return func(yield func(ModuleRef) bool) {
		for devName, dev := range *devs {
			for cmdName, cmd := range dev.Cmds {
				for _, mod := range cmd.Modules {
					if !yield(ModuleRef{Device: devName, Command: cmdName, Module: mod}) {
						return
					}
				}
			}
		}
	}
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
	Args    []ArgDecl
	Modules []Module `yaml:"uses"`
}

// ArgDecl declares a command argument with its name and description.
type ArgDecl struct {
	Name string `yaml:"name"`
	Desc string `yaml:"desc"`
}

// HasPassthrough reports whether the command has a passthrough module.
func (c *Command) HasPassthrough() bool {
	return c.countPassthrough() > 0
}

func (c *Command) countPassthrough() int {
	count := 0

	for _, mod := range c.Modules {
		if mod.Config.Passthrough {
			count++
		}
	}

	return count
}

// ModuleArgs builds the argument list for each module in the command.
// Passthrough modules receive runtimeArgs directly. Non-passthrough modules
// receive their statically configured Args with template references substituted
// using runtimeArgs. The returned slice has the same length and ordering as c.Modules.
//
// It returns ErrNoReceiverForArgs when runtimeArgs are supplied but the command has
// neither a passthrough module nor declared Args to receive them, and otherwise
// propagates any error from SubstituteArgs (for example when the number of
// runtime arguments does not match the command's declared Args).
func (c *Command) ModuleArgs(runtimeArgs []string) ([][]string, error) {
	// Runtime args may be consumed either by a passthrough module or by
	// command-level templating (declared c.Args substituted via ${name}).
	// Only reject when neither can receive them.
	if len(runtimeArgs) > 0 && !c.HasPassthrough() && len(c.Args) == 0 {
		return nil, ErrNoReceiverForArgs
	}

	result := make([][]string, len(c.Modules))

	for idx, mod := range c.Modules {
		if mod.Config.Passthrough {
			result[idx] = runtimeArgs
		} else {
			// Apply argument substitution for non-passthrough modules
			substituted, err := c.SubstituteArgs(mod.Config.Args, runtimeArgs)
			if err != nil {
				return nil, err
			}

			result[idx] = substituted
		}
	}

	return result, nil
}

// HelpText returns the help string of the passthrough module.
// If no passthrough module exists, returns an overview of all modules.
func (c *Command) HelpText() string {
	for _, mod := range c.Modules {
		if mod.Config.Passthrough {
			return mod.Help()
		}
	}

	// If no passthrough module, provide overview of all modules

	moduleNames := make([]string, 0, len(c.Modules))
	for _, module := range c.Modules {
		moduleNames = append(moduleNames, module.Config.Name)
	}

	helpStr := fmt.Sprintf("Command with %d module(s): %s",
		len(c.Modules), strings.Join(moduleNames, ", "))

	// Append command args documentation if declared
	if len(c.Args) > 0 {
		helpStr += "\n\nArguments:\n"
		for _, arg := range c.Args {
			helpStr += fmt.Sprintf("  %s: %s\n", arg.Name, arg.Desc)
		}
	}

	return helpStr
}

// Module is a wrapper for any module implementation.
type Module struct {
	module.Module

	Config ModuleConfig
}

// ModuleConfig holds the module-agnostic settings decoded from a module's YAML
// entry: the registered module name, whether it is the command's passthrough
// module, its static args, and the raw "with" options (Options) that are later
// re-decoded into the concrete module type.
type ModuleConfig struct {
	Name        string `yaml:"module"`
	Passthrough bool   `yaml:"passthrough"`
	Args        []string
	Options     map[string]any `yaml:"with"`
}

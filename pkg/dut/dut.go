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
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

var (
	ErrDeviceNotFound      = errors.New("no such device")
	ErrCommandNotFound     = errors.New("no such command")
	ErrNoModules           = errors.New("command has no modules")
	ErrMultipleMainModules = errors.New("command has multiple main modules")
	ErrNoMainForArgs       = errors.New("arguments provided but command has no main module to receive them")
)

// Devlist is a list of devices-under-test.
type Devlist map[string]Device

// Names returns the names of all devices in the list.
// The names are sorted alphabetically.
func (devs Devlist) Names() []string {
	names := make([]string, 0, len(devs))
	for d := range devs {
		names = append(names, d)
	}

	slices.Sort(names)

	return names
}

// CmdNames returns the names of all commands available for a device or
// ErrDeviceNotFound if the device is not found. The names are sorted alphabetically.
// A device with no commands will not report an error but return an empty slice.
func (devs Devlist) CmdNames(device string) ([]string, error) {
	dev, ok := devs[device]
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
func (devs Devlist) FindCmd(device, command string) (Device, Command, error) {
	dev, ok := devs[device]
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

	if cmd.CountMain() > 1 {
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
	Args    []ArgDecl
	Modules []Module `yaml:"uses"`
}

// ArgDecl declares a command argument with its name and description.
type ArgDecl struct {
	Name string `yaml:"name"`
	Desc string `yaml:"desc"`
}

// commandAlias is used when parsing YAML to avoid recursion.
type commandAlias Command

// UnmarshalYAML unmarshals a Command from a YAML node and adds custom validation.
func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	var cmd commandAlias

	err := node.Decode(&cmd)
	if err != nil {
		return err
	}

	*c = Command(cmd)

	// Check presence of main module
	if len(c.Modules) == 0 {
		return errors.New("command must have at least one module")
	}

	if c.CountMain() > 1 {
		return errors.New("command must have at most one main module")
	}

	// Validate mutual exclusion: cannot have both main module AND command args
	if c.CountMain() > 0 && len(c.Args) > 0 {
		return errors.New("command cannot have both main module and args declaration")
	}

	// Check for presence of args in non-main modules only
	for _, mod := range c.Modules {
		if mod.Config.Main && len(mod.Config.Args) > 0 {
			return errors.New("main module should not have args set. They are passed as command line arguments via the dutctl client")
		}
	}

	// Validate template references in module args
	err = c.validateTemplateReferences()
	if err != nil {
		return err
	}

	return nil
}

// CountMain returns the number of modules marked as main in the command.
func (c *Command) CountMain() int {
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
// receive their statically configured Args with template references substituted
// using runtimeArgs. The returned slice has the same length and ordering as c.Modules.
func (c *Command) ModuleArgs(runtimeArgs []string) ([][]string, error) {
	if len(runtimeArgs) > 0 && c.CountMain() == 0 {
		return nil, ErrNoMainForArgs
	}

	result := make([][]string, len(c.Modules))

	for idx, mod := range c.Modules {
		if mod.Config.Main {
			result[idx] = runtimeArgs
		} else {
			// Apply argument substitution for non-interactive modules
			substituted, err := c.SubstituteArgs(mod.Config.Args, runtimeArgs)
			if err != nil {
				return nil, err
			}

			result[idx] = substituted
		}
	}

	return result, nil
}

// HelpText returns the help string of the main module.
// If no main module exists, returns an overview of all modules and false.
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

type ModuleConfig struct {
	Name    string `yaml:"module"`
	Main    bool
	Args    []string
	Options map[string]any `yaml:"with"`
}

// UnmarshalYAML unmarshals a Module from a YAML node and adds custom validation.
func (m *Module) UnmarshalYAML(node *yaml.Node) error {
	err := node.Decode(&m.Config)
	if err != nil {
		return err
	}

	m.Module, err = module.New(m.Config.Name)
	if err != nil {
		return err
	}

	options, err := yaml.Marshal(m.Config.Options)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(options, m.Module)
	if err != nil {
		return err
	}

	// validate Module configuration
	validate := validator.New()

	err = validate.Struct(m.Module)
	if err != nil {
		return wrapValidatorErrors(err, node)
	}

	return nil
}

var ErrModuleValidation = errors.New("validation error")

func wrapValidatorErrors(err error, node *yaml.Node) error {
	if err == nil {
		return nil
	}

	var valErrors validator.ValidationErrors
	if !errors.As(err, &valErrors) {
		// not of type ValidationErrors
		return err
	}

	errMsg := make([]string, 0, len(valErrors))
	for _, valErr := range valErrors {
		errMsg = append(errMsg,
			fmt.Sprintf("yaml: line %d: Field validation for '%s' failed on the '%s' tag",
				node.Line, valErr.Field(), valErr.Tag()))
	}

	return fmt.Errorf("%w:\n%s", ErrModuleValidation, strings.Join(errMsg, "\n"))
}

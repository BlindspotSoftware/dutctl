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
	ErrDeviceNotFound             = errors.New("no such device")
	ErrCommandNotFound            = errors.New("no such command")
	ErrNoModules                  = errors.New("command has no modules")
	ErrMultipleForwardArgsModules = errors.New("command has multiple forwardArgs modules")
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
// If the requested command has multiple forwardArgs modules, it returns ErrMultipleForwardArgsModules.
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

	if cmd.CountForwardArgs() > 1 {
		return dev, cmd, ErrMultipleForwardArgsModules
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

	// Check presence of forwardArgs module
	if len(c.Modules) == 0 {
		return errors.New("command must have at least one module")
	}

	if c.CountForwardArgs() > 1 {
		return errors.New("command must have at most one forwardArgs module")
	}

	// Check for presence of args in non-forwardArgs modules only
	for _, mod := range c.Modules {
		if mod.Config.ForwardArgs && len(mod.Config.Args) > 0 {
			return errors.New("forwardArgs module should not have args set. They are passed as command line arguments via the dutctl client")
		}
	}

	// Validate template references in module args
	err = c.validateTemplateReferences()
	if err != nil {
		return err
	}

	return nil
}

// CountForwardArgs returns the number of modules marked as forwardArgs in the command.
func (c *Command) CountForwardArgs() int {
	count := 0

	for _, mod := range c.Modules {
		if mod.Config.ForwardArgs {
			count++
		}
	}

	return count
}

// ModuleArgs builds the argument list for each module in the command.
// Runtime args are split: the first len(c.Args) are used for template substitution
// in non-forwardArgs modules; any remaining args are passed to the forwardArgs module.
// When no command args are declared, all runtime args go to the forwardArgs module.
func (c *Command) ModuleArgs(runtimeArgs []string) ([][]string, error) {
	result := make([][]string, len(c.Modules))

	// Split runtime args into named (for template substitution) and forwarded portions.
	namedArgCount := len(c.Args)
	namedArgs := runtimeArgs

	var forwardedArgs []string

	if namedArgCount == 0 {
		forwardedArgs = runtimeArgs
		namedArgs = nil
	} else if len(runtimeArgs) > namedArgCount {
		namedArgs = runtimeArgs[:namedArgCount]
		forwardedArgs = runtimeArgs[namedArgCount:]
	}

	for idx, mod := range c.Modules {
		if mod.Config.ForwardArgs {
			result[idx] = forwardedArgs
		} else {
			// Apply argument substitution for non-forwardArgs modules
			substituted, err := c.SubstituteArgs(mod.Config.Args, namedArgs)
			if err != nil {
				return nil, err
			}

			result[idx] = substituted
		}
	}

	return result, nil
}

// HelpText returns a user-facing help string for the command.
// It includes the command description (if set), a usage synopsis, named arg
// documentation (if declared), and the forwardArgs module's Help() output (if present).
func (c *Command) HelpText(name string) string {
	var helpText strings.Builder

	if c.Desc != "" {
		helpText.WriteString(c.Desc)
		helpText.WriteString("\n")
	}

	// Usage synopsis: <name> <arg1> <arg2> [args...]
	helpText.WriteString("\nUsage: ")
	helpText.WriteString(name)

	for _, arg := range c.Args {
		helpText.WriteString(" <" + arg.Name + ">")
	}

	if c.CountForwardArgs() > 0 {
		helpText.WriteString(" [args...]")
	}

	helpText.WriteString("\n")

	// Named args block with aligned descriptions
	if len(c.Args) > 0 {
		maxLen := 0
		for _, arg := range c.Args {
			if len(arg.Name) > maxLen {
				maxLen = len(arg.Name)
			}
		}

		helpText.WriteString("\nArguments:\n")

		for _, arg := range c.Args {
			helpText.WriteString(fmt.Sprintf("  %-*s  %s\n", maxLen, arg.Name, arg.Desc))
		}
	}

	// ForwardArgs module help
	for _, mod := range c.Modules {
		if mod.Config.ForwardArgs {
			helpText.WriteString("\n")
			helpText.WriteString(mod.Help())

			break
		}
	}

	return helpText.String()
}

// Module is a wrapper for any module implementation.
type Module struct {
	module.Module

	Config ModuleConfig
}

type ModuleConfig struct {
	Name        string `yaml:"module"`
	ForwardArgs bool   `yaml:"forwardArgs"`
	Args        []string
	Options     map[string]any `yaml:"with"`
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

// Copyright 2024 Blindspot Software
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
	ErrDeviceNotFound  = errors.New("no such device")
	ErrCommandNotFound = errors.New("no such command")
	ErrInvalidCommand  = errors.New("command not implemented - no modules or no main module set")
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
// it returns ErrCommandNotFound. If the requested command has no modules, or no main module,
// it returns ErrInvalidCommand.
func (devs Devlist) FindCmd(device, command string) (Device, Command, error) {
	dev, ok := devs[device]
	if !ok {
		return Device{}, Command{}, ErrDeviceNotFound
	}

	cmd, ok := dev.Cmds[command]
	if !ok {
		return dev, Command{}, ErrCommandNotFound
	}

	if len(cmd.Modules) == 0 || cmd.countMain() != 1 {
		return dev, cmd, ErrInvalidCommand
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
	Modules []Module
}

// commandAlias is used when parsing YAML to avoid recursion.
type commandAlias Command

// UnmarshalYAML unmarshals a Command from a YAML node and adds custom validation.
func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	var cmd commandAlias
	if err := node.Decode(&cmd); err != nil {
		return err
	}

	*c = Command(cmd)

	// Check presence of main module
	switch len(c.Modules) {
	case 0:
		return errors.New("command must have at least one module")
	case 1:
		// Implicitly sets the only module as main
		c.Modules[0].Config.Main = true
	default:
		if c.countMain() != 1 {
			return errors.New("command must have exactly one main module")
		}
	}

	// Check for presence of args in non-main modules only
	for _, mod := range c.Modules {
		if mod.Config.Main && mod.Config.Args != "" {
			return errors.New("main module should not have args set. They are passed as command line arguments via the dutctl client")
		}
	}

	return nil
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

// Module is a wrapper for any module implementation.
type Module struct {
	Config ModuleConfig

	module.Module
}

type ModuleConfig struct {
	Name    string `yaml:"module"`
	Main    bool
	Args    string
	Options map[string]any
}

// UnmarshalYAML unmarshals a Module from a YAML node and adds custom validation.
func (m *Module) UnmarshalYAML(node *yaml.Node) error {
	if err := node.Decode(&m.Config); err != nil {
		return err
	}

	var err error
	if m.Module, err = module.New(m.Config.Name); err != nil {
		return err
	}

	options, err := yaml.Marshal(m.Config.Options)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(options, m.Module); err != nil {
		return err
	}

	// validate Module options
	validate := validator.New()
	if err := validate.Struct(m.Module); err != nil {
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

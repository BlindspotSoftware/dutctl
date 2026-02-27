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
	ErrDeviceNotFound  = errors.New("no such device")
	ErrCommandNotFound = errors.New("no such command")
	ErrInvalidCommand  = errors.New("command not implemented - no modules or no main module set")
)

// Devlist is a list of devices-under-test.
//
//nolint:recvcheck // pointer receiver required to initialize the map; other methods use value receivers intentionally
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

// UnmarshalYAML unmarshals a Devlist from a YAML node. It decodes each device
// individually so that errors can be annotated with the device name.
func (devs *Devlist) UnmarshalYAML(node *yaml.Node) error {
	*devs = make(Devlist)

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		var dev Device

		err := valueNode.Decode(&dev)
		if err != nil {
			return fmt.Errorf("device %q: %w", keyNode.Value, err)
		}

		(*devs)[keyNode.Value] = dev
	}

	return nil
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

// deviceAlias is used when parsing YAML to avoid recursion.
type deviceAlias struct {
	Desc string
}

// UnmarshalYAML unmarshals a Device from a YAML node. It decodes each command
// individually so that errors can be annotated with the command name.
func (d *Device) UnmarshalYAML(node *yaml.Node) error {
	var alias deviceAlias

	err := node.Decode(&alias)
	if err != nil {
		return err
	}

	d.Desc = alias.Desc
	d.Cmds = make(map[string]Command)

	// Find the "cmds" value node in the device mapping.
	var cmdsNode *yaml.Node

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "cmds" {
			cmdsNode = node.Content[i+1]

			break
		}
	}

	if cmdsNode == nil {
		return nil // no commands defined
	}

	for i := 0; i+1 < len(cmdsNode.Content); i += 2 {
		keyNode := cmdsNode.Content[i]
		valueNode := cmdsNode.Content[i+1]

		var cmd Command

		err := valueNode.Decode(&cmd)
		if err != nil {
			return fmt.Errorf("command %q: %w", keyNode.Value, err)
		}

		d.Cmds[keyNode.Value] = cmd
	}

	return nil
}

// Command represents a task that can be executed on a device-under-test (DUT).
// This task is composed of one or multiple steps. The steps are implemented by
// modules and are executed in the order they are defined.
type Command struct {
	Desc    string
	Modules []Module `yaml:"uses"`
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
	switch len(c.Modules) {
	case 0:
		return fmt.Errorf("yaml: line %d: command must have at least one module", node.Line)
	case 1:
		// Implicitly sets the only module as main
		c.Modules[0].Config.Main = true
	default:
		if c.countMain() != 1 {
			return fmt.Errorf("yaml: line %d: command must have exactly one main module", node.Line)
		}
	}

	// Check for presence of args in non-main modules only
	for _, mod := range c.Modules {
		if mod.Config.Main && len(mod.Config.Args) > 0 {
			return fmt.Errorf("yaml: line %d: main module should not have args set."+
				" They are passed as command line arguments via the dutctl client", node.Line)
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

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dut provides representation of the device-under-test (DUT).
package dut

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/BlindspotSoftware/dutctl/internal/template"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

var (
	ErrDeviceNotFound  = errors.New("no such device")
	ErrCommandNotFound = errors.New("no such command")
	ErrInvalidCommand  = errors.New("command not implemented - no modules set")

	identifierRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
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
// it returns ErrCommandNotFound. If the requested command has no modules,
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

	if len(cmd.Modules) == 0 {
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
//
// Args defines named arguments that can be referenced in module args using ${argname} syntax.
// Positional arguments from the client are mapped to these named args by position.
type Command struct {
	Desc    string
	Args    map[string]string // Named arguments: map of arg name to description
	Modules []Module
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

	// Validate command has at least one module
	if len(c.Modules) == 0 {
		return errors.New("command must have at least one module")
	}

	// Validate Args keys are valid identifiers
	for argName := range c.Args {
		if !identifierRegex.MatchString(argName) {
			return fmt.Errorf("invalid argument name %q: must contain only letters, numbers, underscores, and hyphens", argName)
		}
	}

	// Validate all template placeholders in module args reference defined Args
	for _, mod := range c.Modules {
		for _, arg := range mod.Config.Args {
			placeholders, err := template.Parse(arg)
			if err != nil {
				return fmt.Errorf("module %q has invalid template in arg %q: %w", mod.Config.Name, arg, err)
			}

			for _, placeholder := range placeholders.Placeholders() {
				if _, exists := c.Args[placeholder]; !exists {
					return fmt.Errorf("module %q references undefined argument %q in template", mod.Config.Name, placeholder)
				}
			}
		}
	}

	return nil
}

// Module is a wrapper for any module implementation.
type Module struct {
	module.Module

	Config ModuleConfig
}

type ModuleConfig struct {
	Name    string `yaml:"module"`
	Args    []string
	Options map[string]any
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

	// validate Module options
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

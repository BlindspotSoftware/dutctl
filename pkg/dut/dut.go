// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dut provides representation of the device-under-test (DUT).
package dut

import (
	"errors"
	"fmt"
	"os"
	"regexp"
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
	ErrMultipleInteractiveModules = errors.New("command has multiple interactive modules")

	// templateRefRegex matches ${varname} template references.
	templateRefRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)
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
// If the requested command has multiple interactive modules, it returns ErrMultipleInteractiveModules.
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

	if cmd.CountInteractive() > 1 {
		return dev, cmd, ErrMultipleInteractiveModules
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

	// Check presence of interactive module
	if len(c.Modules) == 0 {
		return errors.New("command must have at least one module")
	}

	if c.CountInteractive() > 1 {
		return errors.New("command must have at most one interactive module")
	}

	// Validate mutual exclusion: cannot have both interactive module AND command args
	if c.CountInteractive() > 0 && len(c.Args) > 0 {
		return errors.New("command cannot have both interactive module and args declaration")
	}

	// Check for presence of args in non-interactive modules only
	for _, mod := range c.Modules {
		if mod.Config.Interactive && len(mod.Config.Args) > 0 {
			return errors.New("interactive module should not have args set. They are passed as command line arguments via the dutctl client")
		}
	}

	// Validate template references in module args
	err = c.validateTemplateReferences()
	if err != nil {
		return err
	}

	return nil
}

// CountInteractive returns the number of modules marked as interactive in the command.
func (c *Command) CountInteractive() int {
	count := 0

	for _, mod := range c.Modules {
		if mod.Config.Interactive {
			count++
		}
	}

	return count
}

// validateTemplateReferences checks that all template references in module args
// correspond to declared command args.
func (c *Command) validateTemplateReferences() error {
	// Build map of declared arg names for lookup
	argNames := make(map[string]bool)
	for _, arg := range c.Args {
		argNames[arg.Name] = true
	}

	for _, mod := range c.Modules {
		// Skip interactive modules (they receive raw args)
		if mod.Config.Interactive {
			continue
		}

		for _, arg := range mod.Config.Args {
			refs := extractTemplateReferences(arg)
			for _, ref := range refs {
				if !argNames[ref] {
					return fmt.Errorf("module %q references undefined argument %q (available: %v)",
						mod.Config.Name, ref, c.argNamesList())
				}
			}
		}
	}

	return nil
}

// argNamesList returns list of declared argument names for error messages.
func (c *Command) argNamesList() []string {
	names := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		names = append(names, arg.Name)
	}

	return names
}

// extractTemplateReferences finds all ${name} references in a string using regex.
// Returns slice of referenced names.
func extractTemplateReferences(s string) []string {
	matches := templateRefRegex.FindAllStringSubmatch(s, -1)

	refs := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			refs = append(refs, match[1]) // match[1] is the captured group
		}
	}

	return refs
}

// SubstituteArgs replaces template references in args with runtime values using os.Expand.
// runtimeArgs are mapped positionally to command args declaration (preserves declaration order).
// Returns error if runtime args count doesn't match declaration.
func (c *Command) SubstituteArgs(args []string, runtimeArgs []string) ([]string, error) {
	// If no command args declared, return args unchanged
	if len(c.Args) == 0 {
		return args, nil
	}

	// Build substitution map: arg name -> runtime value
	argMap := make(map[string]string)

	if len(runtimeArgs) != len(c.Args) {
		return nil, fmt.Errorf("expected %d argument(s) but got %d", len(c.Args), len(runtimeArgs))
	}

	// Map runtime args to declared args by position
	for i, argDecl := range c.Args {
		argMap[argDecl.Name] = runtimeArgs[i]
	}

	// Substitute templates in each arg using os.Expand
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = os.Expand(arg, func(name string) string {
			// os.Expand calls this function for each ${var} reference
			if val, ok := argMap[name]; ok {
				return val
			}
			// Return empty string for undefined variables (shouldn't happen due to validation)
			return ""
		})
	}

	return result, nil
}

// Module is a wrapper for any module implementation.
type Module struct {
	module.Module

	Config ModuleConfig
}

type ModuleConfig struct {
	Name        string `yaml:"module"`
	Interactive bool
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

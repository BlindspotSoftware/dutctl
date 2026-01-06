// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// ConfigError represents a YAML configuration parsing or validation error
// with contextual information about where the error occurred.
type ConfigError struct {
	Device  string // device name (empty if not yet known)
	Command string // command name (empty if not yet known)
	Line    int    // YAML line number (0 if unknown)
	Err     error  // underlying error
}

func (e *ConfigError) Error() string {
	var msg strings.Builder

	if e.Device != "" {
		fmt.Fprintf(&msg, "device %q: ", e.Device)
	}

	if e.Command != "" {
		fmt.Fprintf(&msg, "command %q: ", e.Command)
	}

	if e.Line > 0 {
		fmt.Fprintf(&msg, "line %d: ", e.Line)
	}

	msg.WriteString(e.Err.Error())

	return msg.String()
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

var (
	ErrNoModules           = errors.New("command must have at least one module")
	ErrMultipleMainModules = errors.New("command must have at most one main module")
	ErrMainModuleWithArgs  = errors.New("main module must not have args set")
	ErrModuleNotFound      = errors.New("module not found")
	ErrEmptyDevices        = errors.New("devices must not be empty")
	ErrNoCommands          = errors.New("device must have at least one command")
)

// UnmarshalYAML unmarshals a Devlist from a YAML node, wrapping errors
// with the device name where they occurred.
//
// Instead of letting yaml.v3 decode the map automatically, this method
// iterates the mapping node's children manually. A MappingNode stores
// key/value pairs as flat alternating entries in node.Content:
//
//	node.Content[0] = key ("device1")
//	node.Content[1] = value (Device mapping)
//	node.Content[2] = key ("device2")
//	node.Content[3] = value (Device mapping)
//
// By decoding each device individually, errors from deeper levels
// (Device, Command, Module) can be annotated with the device name
// before being returned.
func (d *Devlist) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		// Unexpected node type — fall back to default decoding.
		// The type conversion avoids infinite recursion.
		return node.Decode((*map[string]Device)(d))
	}

	if len(node.Content) == 0 {
		return &ConfigError{Err: ErrEmptyDevices}
	}

	*d = make(Devlist, len(node.Content)/2) //nolint:mnd // MappingNode stores key/value as alternating pairs

	for idx := 0; idx < len(node.Content); idx += 2 {
		devName := node.Content[idx].Value

		var dev Device

		// Decode triggers Device.UnmarshalYAML on the value node.
		// Note: yaml.v3 skips UnmarshalYAML for !!null nodes, so a
		// null device value silently produces a zero Device.
		err := node.Content[idx+1].Decode(&dev)
		if err != nil {
			// If the error is already a ConfigError (from a deeper level),
			// annotate it with the device name. Otherwise wrap it.
			var configErr *ConfigError
			if errors.As(err, &configErr) {
				configErr.Device = devName

				return err
			}

			return &ConfigError{Device: devName, Err: err}
		}

		// Catch null device values where Device.UnmarshalYAML was skipped.
		if len(dev.Cmds) == 0 {
			return &ConfigError{Device: devName, Err: ErrNoCommands}
		}

		(*d)[devName] = dev
	}

	return nil
}

// deviceAlias is a type alias for Device used to avoid infinite recursion
// during YAML unmarshalling. When Device.UnmarshalYAML calls node.Decode,
// decoding into a Device would call UnmarshalYAML again. Decoding into
// deviceAlias instead uses the default decoder since deviceAlias has no
// UnmarshalYAML method.
type deviceAlias Device

// UnmarshalYAML unmarshals a Device from a YAML node, wrapping command
// errors with the command name where they occurred.
//
// The Device mapping is walked field-by-field so that the "cmds" sub-mapping
// can be iterated manually (same technique as Devlist.UnmarshalYAML).
// This allows annotating errors with the command name that caused them.
func (d *Device) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		var dev deviceAlias

		err := node.Decode(&dev)
		if err != nil {
			return err
		}

		*d = Device(dev)
	} else {
		// Walk the Device fields manually.
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1]

			switch key {
			case "desc":
				d.Desc = val.Value
			case "cmds":
				cmds, err := decodeCmds(val)
				if err != nil {
					return err
				}

				d.Cmds = cmds
			}
		}
	}

	if len(d.Cmds) == 0 {
		return &ConfigError{Err: ErrNoCommands}
	}

	return nil
}

// decodeCmds decodes a YAML mapping node into a command map, annotating
// errors with the command name that caused them.
func decodeCmds(node *yaml.Node) (map[string]Command, error) {
	if node.Kind != yaml.MappingNode {
		var cmds map[string]Command

		return cmds, node.Decode(&cmds)
	}

	if len(node.Content) == 0 {
		return nil, &ConfigError{Err: ErrNoCommands}
	}

	cmds := make(map[string]Command, len(node.Content)/2) //nolint:mnd // MappingNode stores key/value as alternating pairs

	// Iterate command entries to capture the command name for errors.
	for i := 0; i < len(node.Content); i += 2 {
		cmdName := node.Content[i].Value

		var cmd Command

		// Decode triggers Command.UnmarshalYAML.
		err := node.Content[i+1].Decode(&cmd)
		if err != nil {
			var configErr *ConfigError
			if errors.As(err, &configErr) {
				configErr.Command = cmdName

				return nil, err
			}

			return nil, &ConfigError{Command: cmdName, Err: err}
		}

		cmds[cmdName] = cmd
	}

	return cmds, nil
}

// commandAlias is a type alias for Command used to avoid infinite recursion
// during YAML unmarshalling. When Command.UnmarshalYAML calls node.Decode,
// decoding into a Command would call UnmarshalYAML again. Decoding into
// commandAlias instead uses the default decoder since commandAlias has no
// UnmarshalYAML method.
type commandAlias Command

// UnmarshalYAML unmarshals a Command from a YAML node and adds custom validation.
// Unlike Devlist and Device, this uses the alias trick rather than manual node
// iteration because Command fields are not maps that need key-level error context.
func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	// Decode into alias to get all fields without recursion.
	var cmd commandAlias

	err := node.Decode(&cmd)
	if err != nil {
		return err
	}

	*c = Command(cmd)

	// Check presence of main module
	if len(c.Modules) == 0 {
		return &ConfigError{Line: node.Line, Err: ErrNoModules}
	}

	if c.countMain() > 1 {
		return &ConfigError{Line: node.Line, Err: ErrMultipleMainModules}
	}

	// Check for presence of args in non-main modules only
	for _, mod := range c.Modules {
		if mod.Config.Main && len(mod.Config.Args) > 0 {
			return &ConfigError{Line: node.Line, Err: ErrMainModuleWithArgs}
		}
	}

	return nil
}

// UnmarshalYAML unmarshals a Module from a YAML node and adds custom validation.
//
// The module-specific options (the "with" key) are decoded in two passes:
// first into a generic map via ModuleConfig, then re-marshalled and unmarshalled
// into the concrete module implementation. This is necessary because the concrete
// type is only known after module.New() resolves the module name.
func (m *Module) UnmarshalYAML(node *yaml.Node) error {
	// First pass: decode the fixed fields (module, main, args, with).
	err := node.Decode(&m.Config)
	if err != nil {
		return err
	}

	// Look up the module implementation by name.
	m.Module, err = module.New(m.Config.Name)
	if err != nil {
		return &ConfigError{Line: node.Line, Err: fmt.Errorf("%w: %w", ErrModuleNotFound, err)}
	}

	// Second pass: re-marshal the generic options map and unmarshal it into
	// the concrete module type so that module-specific fields are populated.
	options, err := yaml.Marshal(m.Config.Options)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(options, m.Module)
	if err != nil {
		return err
	}

	// Validate module struct tags (e.g. required fields).
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

	return &ConfigError{
		Line: node.Line,
		Err:  fmt.Errorf("%w:\n%s", ErrModuleValidation, strings.Join(errMsg, "\n")),
	}
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dut

import (
	"fmt"
	"os"
	"regexp"
)

// templateRefRegex matches ${varname} template references.
var templateRefRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)

// validateTemplateReferences checks that all template references in module args
// correspond to declared command args.
func (c *Command) validateTemplateReferences() error {
	// Build map of declared arg names for lookup
	argNames := make(map[string]bool)
	for _, arg := range c.Args {
		argNames[arg.Name] = true
	}

	for _, mod := range c.Modules {
		// Skip main modules (they receive raw args)
		if mod.Config.Main {
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

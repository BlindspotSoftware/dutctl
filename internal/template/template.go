package template

import (
	"fmt"
	"regexp"
	"strings"
)

// placeholderRegex matches ${argname} placeholders in templates.
var placeholderRegex = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)

// argNameRegex validates argument names.
var argNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Template represents a string template with named placeholders.
type Template struct {
	raw          string
	placeholders []string
}

// Parse creates a Template from a string and extracts all placeholders.
func Parse(template string) (*Template, error) {
	matches := placeholderRegex.FindAllStringSubmatch(template, -1)

	placeholders := make([]string, 0, len(matches))
	seen := make(map[string]bool)

	for _, match := range matches {
		argName := match[1]
		if !seen[argName] {
			placeholders = append(placeholders, argName)
			seen[argName] = true
		}
	}

	return &Template{
		raw:          template,
		placeholders: placeholders,
	}, nil
}

// Placeholders returns the list of unique placeholder names in the template.
func (t *Template) Placeholders() []string {
	return t.placeholders
}

// Expand replaces all placeholders with values from the args map
// Missing arguments are replaced with empty strings.
func (t *Template) Expand(args map[string]string) string {
	result := t.raw

	for _, placeholder := range t.placeholders {
		value := args[placeholder]
		result = strings.ReplaceAll(result, fmt.Sprintf("${%s}", placeholder), value)
	}

	return result
}

// ValidateArgNames checks if all argument names follow valid syntax.
func ValidateArgNames(names []string) error {
	for _, name := range names {
		if !argNameRegex.MatchString(name) {
			return fmt.Errorf("invalid argument name '%s': must match [a-zA-Z0-9_-]+", name)
		}
	}

	return nil
}

// ValidatePlaceholders checks if all placeholders in templates reference valid argument names.
func ValidatePlaceholders(templates []*Template, validArgs []string) error {
	validArgsMap := make(map[string]bool)
	for _, arg := range validArgs {
		validArgsMap[arg] = true
	}

	for _, tmpl := range templates {
		for _, placeholder := range tmpl.Placeholders() {
			if !validArgsMap[placeholder] {
				return fmt.Errorf("unknown argument '${%s}' (available: %s)",
					placeholder, strings.Join(validArgs, ", "))
			}
		}
	}

	return nil
}

// ExpandAll expands multiple template strings using the same args map.
func ExpandAll(templates []string, args map[string]string) ([]string, error) {
	result := make([]string, len(templates))

	for i, tmplStr := range templates {
		tmpl, err := Parse(tmplStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template at index %d: %w", i, err)
		}

		result[i] = tmpl.Expand(args)
	}

	return result, nil
}

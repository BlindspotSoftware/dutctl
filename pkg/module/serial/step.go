// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// stepKind identifies the operation a step performs.
type stepKind int

const (
	stepExpect stepKind = iota
	stepSend
	stepSendRaw
)

// step is one operation in a scripted send/expect sequence.
type step struct {
	kind    stepKind
	expect  *regexp.Regexp // set for stepExpect
	payload []byte         // set for stepSend / stepSendRaw (escapes decoded; EOL appended for stepSend)
	src     string         // original argument; shown (truncated) via label() in markers and errors
}

// maxLabelLen bounds how many runes of a step's source argument appear in
// progress markers and error messages, so a long pattern or payload does not
// bloat the log line.
const maxLabelLen = 20

// label returns the step's source argument, truncated for display in progress
// markers and error messages.
func (s step) label() string {
	return truncate(s.src, maxLabelLen)
}

// truncate shortens text to at most limit runes, appending an ellipsis when it
// had to cut. It counts runes (not bytes) so it never splits a multi-byte rune.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return string(runes[:limit]) + "..."
}

// scriptConfig is the parsed result of a serial module invocation: the global
// flags plus the ordered step sequence.
type scriptConfig struct {
	timeout     time.Duration
	keepEscapes bool
	steps       []step
}

const defaultEOL = "cr"

// parseArgs parses the module arguments: the leading flags (-t, -eol) followed
// by the step sequence (optionally after a "--" separator). It returns local
// state only — nothing is stored on the module, which is reused across runs.
func parseArgs(args []string) (scriptConfig, error) {
	fs := flag.NewFlagSet("serial", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress default error output.

	var (
		timeout     time.Duration
		eolName     string
		keepEscapes bool
	)

	fs.DurationVar(&timeout, "t", 0, "global timeout for the whole run (e.g. 30s, 3m); 0 = no timeout")
	fs.StringVar(&eolName, "eol", defaultEOL, "line ending appended by 'send': cr|lf|crlf|none")
	fs.BoolVar(&keepEscapes, "keep-escapes", false, "keep terminal escape sequences in the output instead of stripping them")

	err := fs.Parse(args)
	if err != nil {
		return scriptConfig{}, fmt.Errorf("failed to parse arguments: %w", err)
	}

	eol, err := resolveEOL(eolName)
	if err != nil {
		return scriptConfig{}, err
	}

	steps, err := parseSteps(fs.Args(), eol)
	if err != nil {
		return scriptConfig{}, err
	}

	return scriptConfig{timeout: timeout, keepEscapes: keepEscapes, steps: steps}, nil
}

// resolveEOL maps the -eol flag value to the bytes appended by a 'send' step.
func resolveEOL(name string) ([]byte, error) {
	switch strings.ToLower(name) {
	case "cr":
		return []byte("\r"), nil
	case "lf":
		return []byte("\n"), nil
	case "crlf":
		return []byte("\r\n"), nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid -eol %q: want cr, lf, crlf, or none", name)
	}
}

// parseSteps scans the verb token stream left-to-right into ordered steps.
// Each verb consumes exactly the next token as its argument.
func parseSteps(tokens []string, eol []byte) ([]step, error) {
	var steps []step

	for idx := 0; idx < len(tokens); {
		verb := tokens[idx]
		idx++

		switch verb {
		case "expect":
			if idx >= len(tokens) {
				return nil, fmt.Errorf("%q requires a pattern argument", verb)
			}

			pat := tokens[idx]
			idx++

			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("invalid regular expression %q: %w", pat, err)
			}

			steps = append(steps, step{kind: stepExpect, expect: re, src: pat})
		case "send", "send-raw":
			if idx >= len(tokens) {
				return nil, fmt.Errorf("%q requires a data argument", verb)
			}

			raw := tokens[idx]
			idx++

			data, err := decodeEscapes(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid data %q: %w", raw, err)
			}

			kind := stepSend
			if verb == "send-raw" {
				kind = stepSendRaw
			} else {
				data = append(data, eol...)
			}

			steps = append(steps, step{kind: kind, payload: data, src: raw})
		default:
			return nil, fmt.Errorf("unknown step verb %q (want expect, send, or send-raw)", verb)
		}
	}

	// An empty sequence is valid: it selects monitor mode (stream until
	// cancelled). Run handles len(steps) == 0.
	return steps, nil
}

// decodeEscapes decodes the supported backslash escapes in a send payload:
// \r \n \t \\ and \xNN (two hex digits). Other escapes are an error so typos
// surface instead of silently passing through.
//
//nolint:cyclop // a flat escape dispatcher; splitting it would not aid clarity
func decodeEscapes(s string) ([]byte, error) {
	const (
		hexBase    = 16
		hexBitSize = 8
		hexDigits  = 2
	)

	out := make([]byte, 0, len(s))

	for idx := 0; idx < len(s); idx++ {
		if s[idx] != '\\' {
			out = append(out, s[idx])

			continue
		}

		idx++
		if idx >= len(s) {
			return nil, fmt.Errorf("trailing backslash")
		}

		switch s[idx] {
		case 'r':
			out = append(out, '\r')
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case '\\':
			out = append(out, '\\')
		case 'x':
			if idx+hexDigits >= len(s) {
				return nil, fmt.Errorf(`\x requires two hex digits`)
			}

			v, err := strconv.ParseUint(s[idx+1:idx+1+hexDigits], hexBase, hexBitSize)
			if err != nil {
				return nil, fmt.Errorf(`invalid \x escape %q: %w`, s[idx+1:idx+1+hexDigits], err)
			}

			out = append(out, byte(v))
			idx += hexDigits
		default:
			return nil, fmt.Errorf("unknown escape %q", `\`+string(s[idx]))
		}
	}

	return out, nil
}

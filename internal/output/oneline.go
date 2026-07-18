// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// OneLineFormatter formats output as single lines in a CSV-like format.
// It's designed for dense, machine-readable output.
type OneLineFormatter struct {
	stdout    io.Writer
	stderr    io.Writer
	verbose   bool
	buffering bool
	stdBuffer strings.Builder
	errBuffer strings.Builder
	separator string // Character(s) used to separate fields
}

// newOneLineFormatter creates a new formatter that outputs content in a single-line CSV-like format.
func newOneLineFormatter(config Config) *OneLineFormatter {
	config = withDefaultWriters(config)

	return &OneLineFormatter{
		stdout:    config.Stdout,
		stderr:    config.Stderr,
		verbose:   config.Verbose,
		buffering: false,
		separator: ",", // Default separator is comma
	}
}

// WriteContent formats and outputs structured content as a single line.
func (f *OneLineFormatter) WriteContent(content Content) {
	var line strings.Builder

	timestamp := time.Now().Format(time.RFC3339)
	line.WriteString(timestamp)
	line.WriteString(f.separator)
	line.WriteString(string(content.Type))
	line.WriteString(f.separator)

	if content.IsError {
		line.WriteString("ERROR")
	} else {
		line.WriteString("INFO")
	}

	if f.verbose && len(content.Metadata) > 0 {
		writeMetadataToLine(&line, content.Metadata, f.separator)
	}

	// Add content data at the end
	line.WriteString(f.separator)
	line.WriteString(formatDataValue(content.Data, f.separator))

	line.WriteString("\n")

	f.output(line.String(), content.IsError)
}

// Write sends text to standard output.
func (f *OneLineFormatter) Write(text string) {
	f.WriteContent(Content{
		Type: TypeGeneral,
		Data: text,
	})
}

// WriteErr sends text to standard error.
func (f *OneLineFormatter) WriteErr(text string) {
	f.WriteContent(Content{
		Type:    TypeGeneral,
		Data:    text,
		IsError: true,
	})
}

// Buffer starts accumulating content instead of immediate output.
func (f *OneLineFormatter) Buffer() {
	f.buffering = true
}

// IsBuffering returns whether the formatter is currently buffering output.
func (f *OneLineFormatter) IsBuffering() bool {
	return f.buffering
}

// formatQuotedString formats a string with proper quoting if it contains special characters.
func formatQuotedString(value, separator string) string {
	if strings.ContainsAny(value, separator+" ") {
		return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
	}

	return value
}

// formatDataValue formats different data types for oneline output.
func formatDataValue(data interface{}, separator string) string {
	switch dataValue := data.(type) {
	case string:
		return formatQuotedString(dataValue, separator)
	case []string:
		// Join array elements with a pipe
		joined := strings.Join(dataValue, "|")

		return formatQuotedString(joined, separator)
	case []byte:
		return formatQuotedString(string(dataValue), separator)
	case []DeviceEntry:
		entries := make([]string, 0, len(dataValue))
		for _, d := range dataValue {
			entries = append(entries, deviceEntryString(d))
		}

		return formatQuotedString(strings.Join(entries, "|"), separator)
	case DeviceEntry:
		return formatQuotedString(deviceEntryString(dataValue), separator)
	case FileTransfer:
		// Path goes last so it stays unambiguous even when it contains the
		// separator or a colon (e.g. Windows paths like C:\...); the whole
		// field is quoted by formatQuotedString.
		token := fmt.Sprintf("%s %d %s", dataValue.Direction, dataValue.Bytes, dataValue.Path)

		return formatQuotedString(token, separator)
	default:
		// Convert anything else to string
		return formatQuotedString(fmt.Sprintf("%v", dataValue), separator)
	}
}

// deviceEntryString renders a DeviceEntry as a compact "name" or
// "name=locked:owner" token for single-line output.
func deviceEntryString(d DeviceEntry) string {
	if !d.Locked {
		return d.Name
	}

	return fmt.Sprintf("%s=locked:%s", d.Name, d.Owner)
}

// output writes the formatted line to the appropriate destination.
func (f *OneLineFormatter) output(line string, isError bool) {
	if f.buffering {
		if isError {
			f.errBuffer.WriteString(line)
		} else {
			f.stdBuffer.WriteString(line)
		}
	} else {
		fmt.Fprint(streamFor(f.stdout, f.stderr, isError), line)
	}
}

// writeMetadataToLine formats and adds metadata key-value pairs to the output line.
func writeMetadataToLine(line *strings.Builder, metadata map[string]string, separator string) {
	// Get all metadata keys and sort them for consistent output
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	// Format key-value pairs
	for _, key := range keys {
		line.WriteString(separator)
		line.WriteString(key)
		line.WriteString("=")

		// Quote values that contain the separator or spaces
		value := metadata[key]
		line.WriteString(formatQuotedString(value, separator))
	}
}

// Flush ensures all buffered output is written. A write failure is logged and the
// affected buffer dropped; there is no error to act on.
func (f *OneLineFormatter) Flush() {
	if !f.buffering {
		return
	}

	// Write buffered content to the appropriate streams
	if f.stdBuffer.Len() > 0 {
		_, err := fmt.Fprint(f.stdout, f.stdBuffer.String())
		if err != nil {
			slog.Warn("failed to write buffered output", "stream", "stdout", "err", err)
		}

		f.stdBuffer.Reset()
	}

	if f.errBuffer.Len() > 0 {
		_, err := fmt.Fprint(f.stderr, f.errBuffer.String())
		if err != nil {
			slog.Warn("failed to write buffered output", "stream", "stderr", "err", err)
		}

		f.errBuffer.Reset()
	}

	f.buffering = false
}

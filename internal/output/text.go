// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// TextFormatter implements Formatter with plain text formatting capabilities.
// It supports:
// - Separate stdout and stderr streams.
// - Buffering mode for deferred output.
// - Metadata handling with intelligent caching to avoid repetitive headers.
// - Verbose mode to control metadata visibility.
type TextFormatter struct {
	stdout              io.Writer
	stderr              io.Writer
	verbose             bool
	buffering           bool
	stdBuffer           bytes.Buffer
	errBuffer           bytes.Buffer
	metadataCache       map[string]string // Cache to remember last printed metadata
	isLastWriteToStderr bool              // Tracks if the last metadata write was to stderr (true) or stdout (false)
	invertedMetadata    bool              // Controls whether metadata is displayed with inverted colors
}

// newTextFormatter creates a new TextFormatter instance configured according to the provided Config.
// If config.Stdout or config.Stderr are nil, it defaults to os.Stdout and os.Stderr respectively.
// The formatter starts in non-buffered mode with an empty metadata cache.
func newTextFormatter(config Config) *TextFormatter {
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}

	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}

	return &TextFormatter{
		stdout:           config.Stdout,
		stderr:           config.Stderr,
		verbose:          config.Verbose,
		buffering:        false,
		metadataCache:    make(map[string]string),
		invertedMetadata: !config.NoColor, // Enable inverted metadata by default unless NoColor is set
	}
}

// WriteContent formats and outputs structured content.
func (f *TextFormatter) WriteContent(content Content) {
	// Get appropriate writer based on buffering mode and error state
	var writer io.Writer

	if f.buffering {
		if content.IsError {
			writer = &f.errBuffer
		} else {
			writer = &f.stdBuffer
		}
	} else {
		if content.IsError {
			writer = f.stderr
		} else {
			writer = f.stdout
		}
	}

	// Format and write content based on type, regardless of error state
	switch content.Type {
	case TypeDeviceList:
		f.writeDeviceListTo(content, writer)
	case TypeCommandList:
		f.writeCommandListTo(content, writer)
	case TypeVersion:
		f.writeVersionTo(content, writer)
	case TypeCommandDetail:
		f.writeDetailTo(content, writer)
	case TypeModuleOutput:
		f.writeModuleOutputTo(content, writer)
	default:
		// For general text or unrecognized types
		f.writeGeneralTo(content, writer)
	}
}

// Write sends text to standard output.
func (f *TextFormatter) Write(text string) {
	if f.buffering {
		fmt.Fprint(&f.stdBuffer, text)
	} else {
		fmt.Fprint(f.stdout, text)
	}
}

// WriteErr sends text to standard error.
func (f *TextFormatter) WriteErr(text string) {
	if f.buffering {
		fmt.Fprint(&f.errBuffer, text)
	} else {
		fmt.Fprint(f.stderr, text)
	}
}

// Buffer starts accumulating content instead of immediate output.
func (f *TextFormatter) Buffer() {
	f.buffering = true
}

// IsBuffering returns true if the formatter is in buffered mode.
func (f *TextFormatter) IsBuffering() bool {
	return f.buffering
}

// Flush ensures all buffered output is written.
func (f *TextFormatter) Flush() error {
	if !f.buffering {
		return nil
	}

	// Write all buffered content to the appropriate streams
	if f.stdBuffer.Len() > 0 {
		_, err := f.stdBuffer.WriteTo(f.stdout)
		if err != nil {
			return fmt.Errorf("error writing stdout buffer: %v", err)
		}
	}

	if f.errBuffer.Len() > 0 {
		_, err := f.errBuffer.WriteTo(f.stderr)
		if err != nil {
			return fmt.Errorf("error writing stderr buffer: %v", err)
		}
	}

	// Reset buffering state and clear metadata cache
	f.buffering = false
	f.clearMetadataCache()

	return nil
}

// Helper methods for different content types

// writeDeviceListTo formats and writes a list of devices with bullet points.
func (f *TextFormatter) writeDeviceListTo(content Content, writer io.Writer) {
	if devices, ok := content.Data.([]string); ok {
		// Print metadata before content
		f.writeMetadata(content, writer)

		for _, device := range devices {
			fmt.Fprintf(writer, "- %s\n", device)
		}
	} else {
		f.writeGeneralTo(content, writer)
	}
}

// writeCommandListTo formats and writes a list of commands with bullet points.
func (f *TextFormatter) writeCommandListTo(content Content, writer io.Writer) {
	if commands, ok := content.Data.([]string); ok {
		// Print metadata before content
		f.writeMetadata(content, writer)

		for _, cmd := range commands {
			fmt.Fprintf(writer, "- %s\n", cmd)
		}
	} else {
		f.writeGeneralTo(content, writer)
	}
}

// writeVersionTo formats and writes version information.
func (f *TextFormatter) writeVersionTo(content Content, writer io.Writer) {
	if version, ok := content.Data.(string); ok {
		// Print metadata before content
		f.writeMetadata(content, writer)

		fmt.Fprintln(writer, version)
	} else {
		f.writeGeneralTo(content, writer)
	}
}

// writeDetailTo formats and writes command detail information.
func (f *TextFormatter) writeDetailTo(content Content, writer io.Writer) {
	if details, ok := content.Data.(string); ok {
		// Print metadata before content
		f.writeMetadata(content, writer)

		fmt.Fprintln(writer, details)
	} else {
		f.writeGeneralTo(content, writer)
	}
}

// writeModuleOutputTo formats and writes module execution output.
func (f *TextFormatter) writeModuleOutputTo(content Content, writer io.Writer) {
	if output, ok := content.Data.(string); ok {
		// Print metadata before content
		f.writeMetadata(content, writer)

		// If output doesn't end with a newline, add one to separate from next output
		if len(output) > 0 && output[len(output)-1] != '\n' {
			fmt.Fprintln(writer, output)
		} else {
			fmt.Fprint(writer, output)
		}
	} else {
		f.writeGeneralTo(content, writer)
	}
}

// writeGeneralTo handles general-purpose content formatting for various data types.
func (f *TextFormatter) writeGeneralTo(content Content, writer io.Writer) {
	// Print metadata before content
	f.writeMetadata(content, writer)

	switch data := content.Data.(type) {
	case string:
		fmt.Fprint(writer, data)
	case []byte:
		fmt.Fprint(writer, string(data))
	case []string:
		for _, line := range data {
			fmt.Fprintln(writer, line)
		}
	default:
		fmt.Fprintf(writer, "%v", data)
	}
}

// writeMetadata prints metadata if verbose mode is enabled and metadata has changed.
func (f *TextFormatter) writeMetadata(content Content, writer io.Writer) {
	// Quick return if not verbose or no metadata
	if !f.verbose || len(content.Metadata) == 0 {
		return
	}

	if !f.hasMetadataChanged(content.Metadata, writer) {
		// No change, no output
		return
	}

	// ANSI escape codes for inverted text and reset - only used if invertedMetadata is true
	const (
		invertCode = "\033[7m" // Invert colors
		resetCode  = "\033[0m" // Reset formatting
	)

	// Process metadata that should be printed
	knownMetadata, otherMetadata := splitMetadata(content.Metadata)

	if sentence := metadataText(knownMetadata); sentence != "" {
		if f.invertedMetadata {
			fmt.Fprintf(writer, "%s# %s%s\n", invertCode, sentence, resetCode)
		} else {
			fmt.Fprintf(writer, "# %s\n", sentence)
		}
	}

	// Print all remaining metadata on a single line, sorted by keys
	if len(otherMetadata) > 0 {
		keys := make([]string, 0, len(otherMetadata))
		for key := range otherMetadata {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		for _, key := range keys {
			if f.invertedMetadata {
				fmt.Fprintf(writer, "%s# %s: %s%s\n", invertCode, key, otherMetadata[key], resetCode)
			} else {
				fmt.Fprintf(writer, "# %s: %s\n", key, otherMetadata[key])
			}
		}
	}

	f.updateMetadataCache(content.Metadata)
}

// updateMetadataCache saves the current metadata to the cache.
func (f *TextFormatter) updateMetadataCache(metadata map[string]string) {
	// Clear existing cache
	for key := range f.metadataCache {
		delete(f.metadataCache, key)
	}

	// Copy new metadata to cache
	for key, value := range metadata {
		f.metadataCache[key] = value
	}
}

// splitMetadata separates known metadata keys from the rest.
func splitMetadata(metadata map[string]string) (map[string]string, map[string]string) {
	knownKeys := []string{"server", "msg", "device", "command", "args"}
	known := make(map[string]string)
	other := make(map[string]string)

	// Copy the original map to avoid modifying it
	for key, value := range metadata {
		isKnown := false

		for _, knownKey := range knownKeys {
			if key == knownKey {
				known[key] = value
				isKnown = true

				break
			}
		}

		if !isKnown {
			other[key] = value
		}
	}

	return known, other
}

// metadataText creates a descriptive sentence from any combination of known metadata keys.
func metadataText(known map[string]string) string {
	var parts []string

	if server, ok := known["server"]; ok {
		parts = append(parts, fmt.Sprintf("connected to %s", server))
	}

	if msg, ok := known["msg"]; ok {
		parts = append(parts, fmt.Sprintf("(%s)", msg))
	}

	if dev, ok := known["device"]; ok {
		parts = append(parts, fmt.Sprintf("device %q", dev))
	}

	if cmd, ok := known["command"]; ok {
		if args, ok := known["args"]; ok && args != "" {
			parts = append(parts, fmt.Sprintf("executing '%s %s'", cmd, args))
		} else {
			parts = append(parts, fmt.Sprintf("executing '%s'", cmd))
		}
	}

	return strings.Join(parts, " ")
}

// clearMetadataCache empties the metadata cache.
func (f *TextFormatter) clearMetadataCache() {
	for key := range f.metadataCache {
		delete(f.metadataCache, key)
	}
}

// hasMetadataChanged checks if the metadata has changed since the last time it was printed.
// It returns true if metadata should be printed (is different from cache) or if the writer has changed.
func (f *TextFormatter) hasMetadataChanged(metadata map[string]string, writer io.Writer) bool {
	// Always print metadata if the cache is empty
	if len(f.metadataCache) == 0 {
		return true
	}

	if len(metadata) != len(f.metadataCache) {
		return true
	}

	if hasWriterChanged(f, writer) {
		f.isLastWriteToStderr = !f.isLastWriteToStderr

		return true
	}

	return hasMetadataValueChanged(f, metadata)
}

// hasWriterChanged checks if the output writer has changed between stderr and stdout.
func hasWriterChanged(f *TextFormatter, writer io.Writer) bool {
	isStderr := (writer == f.stderr) || (f.buffering && writer == &f.errBuffer)

	return (isStderr && !f.isLastWriteToStderr) || (!isStderr && f.isLastWriteToStderr)
}

// hasMetadataValueChanged checks if any metadata value has changed from the cached version.
func hasMetadataValueChanged(f *TextFormatter, metadata map[string]string) bool {
	for key, value := range metadata {
		cachedValue, exists := f.metadataCache[key]
		if !exists || cachedValue != value {
			return true
		}
	}

	// No changes detected - don't print metadata again
	return false
}

// Skip the separate metadataValuesChanged function to simplify the implementation

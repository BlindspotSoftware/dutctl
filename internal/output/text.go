// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package output

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/style"
)

// TextFormatter implements Formatter with plain text formatting capabilities.
// It supports separate stdout and stderr streams, a buffering mode for
// deferred output, metadata caching to avoid repeating unchanged headers, and
// a verbose mode that controls metadata visibility.
type TextFormatter struct {
	stdout              io.Writer
	stderr              io.Writer
	verbose             bool
	buffering           bool
	stdBuffer           bytes.Buffer
	errBuffer           bytes.Buffer
	metadataCache       map[string]string // Cache to remember last printed metadata
	isLastWriteToStderr bool              // Tracks if the last metadata write was to stderr (true) or stdout (false)
	useColor            bool              // Whether colored output is enabled
}

// newTextFormatter creates a new TextFormatter instance configured according to
// the provided Config. The formatter starts in non-buffered mode with an empty
// metadata cache.
func newTextFormatter(config Config) *TextFormatter {
	config = withDefaultWriters(config)

	return &TextFormatter{
		stdout:        config.Stdout,
		stderr:        config.Stderr,
		verbose:       config.Verbose,
		buffering:     false,
		metadataCache: make(map[string]string),
		useColor:      !config.NoColor,
	}
}

// selectWriter returns the writer for content based on buffering mode and
// error state.
func (f *TextFormatter) selectWriter(content Content) io.Writer {
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

	return writer
}

// WriteContent formats and outputs structured content.
func (f *TextFormatter) WriteContent(content Content) {
	writer := f.selectWriter(content)

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
	case TypeLockResult:
		f.writeLockResultTo(content, writer)
	case TypeFileTransfer:
		f.writeFileTransferTo(content, writer)
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

// Flush ensures all buffered output is written. A write failure is logged and the
// affected buffer dropped; there is no error to act on.
func (f *TextFormatter) Flush() {
	if !f.buffering {
		return
	}

	// Write all buffered content to the appropriate streams
	if f.stdBuffer.Len() > 0 {
		_, err := f.stdBuffer.WriteTo(f.stdout)
		if err != nil {
			slog.Warn("failed to write buffered output", "stream", "stdout", "err", err)
		}
	}

	if f.errBuffer.Len() > 0 {
		_, err := f.errBuffer.WriteTo(f.stderr)
		if err != nil {
			slog.Warn("failed to write buffered output", "stream", "stderr", "err", err)
		}
	}

	// Reset buffering state and clear metadata cache
	f.buffering = false
	f.clearMetadataCache()
}

// Helper methods for different content types

// humanDuration renders dur as a compact "1h30m"-style string, rounded to the
// minute. A non-positive duration renders as "0m".
func humanDuration(dur time.Duration) string {
	dur = dur.Round(time.Minute)
	if dur <= 0 {
		return "0m"
	}

	hours := dur / time.Hour
	minutes := (dur % time.Hour) / time.Minute

	switch {
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// humanBytes renders a byte count as a compact "1.2 MiB"-style string using
// binary (1024) units. Counts below 1 KiB are shown as plain bytes.
func humanBytes(byteCount int) string {
	const unit = 1024

	if byteCount < unit {
		return fmt.Sprintf("%d B", byteCount)
	}

	div, exp := int64(unit), 0
	for v := int64(byteCount) / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(byteCount)/float64(div), "KMGTPE"[exp])
}

// lockAnnotation renders the bracketed lock note for a locked device, e.g.
// ` [locked by "alice@host" for 25m]`. A lock with no expiry (ExpiresAt of 0),
// such as a device busy with a running command, renders as "in use" instead.
func lockAnnotation(entry DeviceEntry) string {
	if entry.ExpiresAt == 0 {
		return fmt.Sprintf(" [in use by %q]", entry.Owner)
	}

	remaining := humanDuration(time.Until(time.Unix(entry.ExpiresAt, 0)))

	return fmt.Sprintf(" [locked by %q for %s]", entry.Owner, remaining)
}

// writeDeviceListTo formats and writes a list of devices with bullet points.
func (f *TextFormatter) writeDeviceListTo(content Content, writer io.Writer) {
	devices, ok := content.Data.([]DeviceEntry)
	if !ok {
		f.writeGeneralTo(content, writer)

		return
	}

	// Print metadata before content
	f.writeMetadata(content, writer)

	for _, device := range devices {
		if device.Locked {
			// The device name is the payload; the lock note is secondary context,
			// so it is muted (gray).
			annotation := style.Colorize(f.useColor, style.Gray, lockAnnotation(device))
			fmt.Fprintf(writer, "- %s%s\n", device.Name, annotation)
		} else {
			fmt.Fprintf(writer, "- %s\n", device.Name)
		}
	}
}

// writeLockResultTo formats and writes the result of a lock or unlock operation.
func (f *TextFormatter) writeLockResultTo(content Content, writer io.Writer) {
	entry, ok := content.Data.(DeviceEntry)
	if !ok {
		f.writeGeneralTo(content, writer)

		return
	}

	f.writeMetadata(content, writer)

	var msg string

	switch {
	case !entry.Locked:
		msg = fmt.Sprintf("Device %q unlocked", entry.Name)
	case entry.ExpiresAt == 0:
		msg = fmt.Sprintf("Device %q in use by %q", entry.Name, entry.Owner)
	default:
		remaining := humanDuration(time.Until(time.Unix(entry.ExpiresAt, 0)))
		msg = fmt.Sprintf("Device %q locked by %q for %s", entry.Name, entry.Owner, remaining)
	}

	line := style.MarkerSuccess + " " + msg
	fmt.Fprintln(writer, style.Colorize(f.useColor, style.Green, line))
}

// writeFileTransferTo formats and writes a file-transfer progress line, e.g.
// `↑ sent "firmware.bin" (1.2 MiB)` / `↓ received "result.log" (4.0 KiB)`.
func (f *TextFormatter) writeFileTransferTo(content Content, writer io.Writer) {
	transfer, ok := content.Data.(FileTransfer)
	if !ok {
		f.writeGeneralTo(content, writer)

		return
	}

	f.writeMetadata(content, writer)

	marker := style.MarkerSent
	if transfer.Direction == "received" {
		marker = style.MarkerReceived
	}

	line := fmt.Sprintf("%s %s %q (%s)", marker, transfer.Direction, transfer.Path, humanBytes(transfer.Bytes))
	fmt.Fprintln(writer, style.Colorize(f.useColor, style.Cyan, line))
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

		fmt.Fprint(writer, output)
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
		// A string flagged as an error gets the error marker/color; IsError here
		// means "client error" because device stderr is TypeModuleOutput and is
		// rendered elsewhere.
		if content.IsError {
			line := style.MarkerError + " " + strings.TrimRight(data, "\n")
			fmt.Fprintln(writer, style.Colorize(f.useColor, style.Red, line))
		} else {
			fmt.Fprint(writer, data)
		}
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

	// Process metadata that should be printed. Context lines are muted (gray)
	// so they sit visually below the payload.
	knownMetadata, otherMetadata := splitMetadata(content.Metadata)

	if sentence := metadataText(knownMetadata); sentence != "" {
		line := style.MarkerContext + " " + sentence
		fmt.Fprintln(writer, style.Colorize(f.useColor, style.Gray, line))
	}

	// Print all remaining metadata on a single line, sorted by keys
	if len(otherMetadata) > 0 {
		keys := make([]string, 0, len(otherMetadata))
		for key := range otherMetadata {
			keys = append(keys, key)
		}

		slices.Sort(keys)

		for _, key := range keys {
			line := fmt.Sprintf("%s %s: %s", style.MarkerContext, key, otherMetadata[key])
			fmt.Fprintln(writer, style.Colorize(f.useColor, style.Gray, line))
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

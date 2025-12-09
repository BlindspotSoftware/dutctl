// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"errors"
	"fmt"
	"strconv"
)

// validateConfig validates the File configuration (called in Init).
func (f *File) validateConfig() error {
	// Validate mode string format if specified
	if f.Mode != "" {
		// Mode must start with "0" to indicate octal format
		if f.Mode[0] != '0' {
			return fmt.Errorf("invalid mode %q: must be octal format starting with '0' (e.g., '0644')", f.Mode)
		}

		_, err := strconv.ParseUint(f.Mode, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid mode %q: must be octal format (e.g., '0644'): %w", f.Mode, err)
		}
	}

	// Validate operation is set
	if f.Operation == "" {
		return fmt.Errorf("operation must be set in config: must be 'upload' or 'download'")
	}

	// Validate operation value
	if f.Operation != string(opUpload) && f.Operation != string(opDownload) {
		return fmt.Errorf("invalid operation %q: must be 'upload' or 'download'", f.Operation)
	}

	return nil
}

// validateArguments validates the runtime arguments (called in Run).
func (f *File) validateArguments(args []string) error {
	if len(args) != 1 {
		if len(args) == 0 {
			return errors.New("missing argument: path required")
		}

		return fmt.Errorf("invalid argument count: expected 1 arg, got %d", len(args))
	}

	return nil
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"fmt"
	"strconv"
)

// validateConfig validates the File configuration (called in Init).
func (f *File) validateConfig() error {
	// Validate permission string format if specified
	if f.Permission != "" {
		// Permission must start with "0" to indicate octal format
		if f.Permission[0] != '0' {
			return fmt.Errorf("invalid permission %q: must be octal format starting with '0' (e.g., '0644')", f.Permission)
		}

		_, err := strconv.ParseUint(f.Permission, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid permission %q: must be octal format (e.g., '0644'): %w", f.Permission, err)
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

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package buildinfo provides a way to read the build information
// embedded in a Go binary. It is based on [debug.ReadBuildInfo],
// but provides a simplified interface to access the build information.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// VersionString returns a formatted string containing the version and
// build information of the application.
func VersionString() string {
	var i info

	return i.String()
}

// info is a wrapper for [debug.BuildInfo].
type info struct {
	semver   string    // Semantic release version of the application.
	revision string    // Revision from the version control system.
	time     time.Time // Date when the application was built.
	compiler string    // Version of the Go compiler used to build the application.

	once sync.Once // Ensures that read is called only once.
}

func (i *info) String() string {
	i.once.Do(i.read)

	var timeStr string
	if i.time.IsZero() {
		timeStr = "------"
	} else {
		timeStr = i.time.Format(time.UnixDate)
	}

	return fmt.Sprintf("Version: %s\nCode Revision %s from %s built with %s\n",
		i.semver, i.revision, timeStr, i.compiler)
}

func (i *info) read() {
	const unknown = "unknown"

	i.semver = Version

	if bi, ok := debug.ReadBuildInfo(); ok {
		i.revision = cvsShortHash(findSetting("vcs.revision", bi.Settings))
		i.time = parseTime(findSetting("vcs.time", bi.Settings))
		i.compiler = bi.GoVersion
	} else {
		i.revision = unknown
		i.time = time.Time{}
		i.compiler = unknown
	}
}

func findSetting(want string, s []debug.BuildSetting) string {
	for _, setting := range s {
		if setting.Key == want {
			return setting.Value
		}
	}

	return ""
}

func cvsShortHash(revision string) string {
	// Trim first: a whitespace-only value must collapse to "unset", and the length
	// guard below must see the trimmed length.
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "unset"
	}

	// Return the leftmost characters — the common short git hash length — but never
	// slice past the end for a shorter revision.
	const shortLen = 7
	if len(revision) < shortLen {
		return revision
	}

	return revision[:shortLen]
}

func parseTime(rfc3339 string) time.Time {
	if rfc3339 == "" {
		return time.Time{}
	}

	// Build time is exported in RFC3339 format according to [debug.BuildSetting].
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return time.Time{}
	}

	return t
}

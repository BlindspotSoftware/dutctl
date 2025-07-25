// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package buildinfo provides a way to read the build information
// embedded in a Go binary. It is based on the `debug` package's
// `ReadBuildInfo` function, but provides a simplified interface
// to access the build information.
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
	revision string    // Revision of the CVS.
	time     time.Time // Date when the application was built.
	compiler string    // Version of the Go compiler used to build the application.

	once sync.Once // Ensures that populate is called only once.
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

	if bi, ok := debug.ReadBuildInfo(); ok {
		i.semver = bi.Main.Version
		i.revision = cvsShortHash(findSetting("vcs.revision", bi.Settings))
		i.time = parseTime(findSetting("vcs.time", bi.Settings))
		i.compiler = bi.GoVersion
	} else {
		i.semver = unknown
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
	if revision == "" {
		return "unset"
	}

	// Trim any leading whitespace and get the leftmost 7 characters,
	// which is the common length for a git commit hash.
	revision = strings.TrimSpace(revision)
	revision = revision[:7]

	return revision
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

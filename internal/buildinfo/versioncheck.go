// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildinfo

import (
	"strings"

	"golang.org/x/mod/semver"
)

// VersionHeader is the header both sides use to advertise their dutctl build
// version on every RPC: clients stamp it on the request, agents on the response.
const VersionHeader = "X-Dutctl-Version"

// Compatibility classifies how two dutctl build versions relate.
type Compatibility int

const (
	// Compatible means the versions may interact freely; nothing to report.
	Compatible Compatibility = iota
	// Tolerated means the versions differ in a way that is allowed but worth a
	// warning (minor, pre-release or unparsable difference).
	Tolerated
	// Incompatible means the versions must not interact; the agent rejects the
	// call.
	Incompatible
)

// Comparison is the result of comparing the local version against a peer's.
type Comparison struct {
	// Level is the compatibility verdict.
	Level Compatibility
	// Reason names the kind of difference (e.g. "major version mismatch"); it is
	// empty when Level is Compatible.
	Reason string
	// Delta is semver.Compare(self, peer): -1 when self is older, +1 when self is
	// newer, 0 when equal or either version is not comparable. It lets callers
	// phrase a directional message ("agent is newer, upgrade the client").
	Delta int
}

// CompareVersions compares the local build version (self) against a peer's
// advertised version and reports how they relate. The comparison is symmetric;
// callers add the role context (which side is client vs. agent) when phrasing a
// message. Empty or unparsable versions never yield Incompatible — they are
// Tolerated so a missing header can only ever warn, never break a call.
func CompareVersions(self, peer string) Comparison {
	selfVer := normalize(self)
	peerVer := normalize(peer)

	if !semver.IsValid(selfVer) || !semver.IsValid(peerVer) {
		return Comparison{Level: Tolerated, Reason: "versions not comparable"}
	}

	delta := semver.Compare(selfVer, peerVer)

	if semver.Major(selfVer) != semver.Major(peerVer) {
		return Comparison{Level: Incompatible, Reason: "major version mismatch", Delta: delta}
	}

	if semver.MajorMinor(selfVer) != semver.MajorMinor(peerVer) {
		return Comparison{Level: Tolerated, Reason: "minor version mismatch", Delta: delta}
	}

	// Same major.minor. A differing patch is tolerated silently; a difference
	// only in the pre-release tag (e.g. 1.0.0-alpha.1 vs 1.0.0-alpha.5) warns.
	if core(selfVer) == core(peerVer) && delta != 0 {
		return Comparison{Level: Tolerated, Reason: "pre-release version mismatch", Delta: delta}
	}

	return Comparison{Level: Compatible}
}

// normalize trims the value and ensures the leading "v" that
// golang.org/x/mod/semver requires.
func normalize(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.HasPrefix(version, "v") {
		return version
	}

	return "v" + version
}

// core returns the vMAJOR.MINOR.PATCH portion of a canonical semver, dropping
// any pre-release and build metadata.
func core(version string) string {
	version = semver.Canonical(version)
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))

	return version
}

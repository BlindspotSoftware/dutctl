// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package compat is the dutctl version-compatibility policy layered on top of
// golang.org/x/mod/semver. It answers a single question — given two build
// version strings, how do they relate and may they interact — without any
// knowledge of the transport, connect, or dutctl app terms that consume it.
// Message wording is deliberately left to the caller (see Result.Field).
package compat

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Verdict classifies how two build versions may interact. It is the policy
// outcome; the thresholds are encoded once in Compare.
type Verdict int

const (
	// Compatible means the versions may interact freely; nothing to report.
	Compatible Verdict = iota
	// Tolerated means the versions differ in a way that is allowed but worth a
	// warning (minor, pre-release, or unparsable difference).
	Tolerated
	// Incompatible means the versions must not interact.
	Incompatible
)

// Field names the most significant semver component in which two versions
// differ. It is a LABEL for callers to phrase a message from — NEVER a policy
// threshold: the tolerate/reject policy is non-monotonic (a pre-release-only
// difference warns while a patch difference is silent), so Field must not be
// compared with < or >. The policy verdict lives in Result.Verdict.
type Field int

const (
	// Equal means the versions are identical (build metadata aside).
	Equal Field = iota
	// Patch means they differ only in the patch number.
	Patch
	// Prerelease means they share MAJOR.MINOR.PATCH but differ in the pre-release tag.
	Prerelease
	// Minor means they share the major number but differ in the minor number.
	Minor
	// Major means they differ in the major number.
	Major
)

// Result reports how a local version (self) relates to a peer's.
type Result struct {
	// Verdict is the compatibility policy outcome.
	Verdict Verdict
	// Field is the most significant differing semver component (a label, not a
	// threshold — see [Field]). Equal when the versions match or are not comparable.
	Field Field
	// Cmp is semver.Compare(self, peer): -1 when self is older, +1 when self is
	// newer, 0 when equal or not comparable. It lets callers phrase a directional
	// message ("agent is newer, upgrade the client").
	Cmp int
	// Valid reports whether both self and peer parsed as semver. When false the
	// verdict is Tolerated and Field is Equal, so a Field==Major guard never
	// fires on unparsable input.
	Valid bool
}

// Compare compares the local build version (self) against a peer's advertised
// version and reports how they relate. The comparison is symmetric; callers add
// the role context (which side is client vs. agent) when phrasing a message.
// Empty or unparsable versions never yield Incompatible — they are Tolerated so
// a missing version can only ever warn, never break an interaction.
func Compare(self, peer string) Result {
	selfVer := normalize(self)
	peerVer := normalize(peer)

	if !semver.IsValid(selfVer) || !semver.IsValid(peerVer) {
		return Result{Verdict: Tolerated, Field: Equal, Valid: false}
	}

	res := Result{Cmp: semver.Compare(selfVer, peerVer), Valid: true}

	switch {
	case semver.Major(selfVer) != semver.Major(peerVer):
		res.Field, res.Verdict = Major, Incompatible
	case semver.MajorMinor(selfVer) != semver.MajorMinor(peerVer):
		res.Field, res.Verdict = Minor, Tolerated
	case core(selfVer) != core(peerVer):
		// Same major.minor, differing patch: a compatible bugfix bump (silent).
		res.Field, res.Verdict = Patch, Compatible
	case res.Cmp != 0:
		// Same major.minor.patch but not equal: only the pre-release tag differs.
		res.Field, res.Verdict = Prerelease, Tolerated
	default:
		res.Field, res.Verdict = Equal, Compatible
	}

	return res
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

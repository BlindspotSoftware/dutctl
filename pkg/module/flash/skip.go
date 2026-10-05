// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// presetOpenBMCStatic names the static OpenBMC flash layouts from the Linux
// device trees openbmc-flash-layout-64.dtsi and openbmc-flash-layout-128.dtsi.
// The image size selects the layout.
const presetOpenBMCStatic = "openbmc-static"

// headSize is how much of each skipped region is compared with the image before a
// skip. Kernel FIT images and squashfs file systems carry build timestamps in
// their first bytes, so a foreign write of another build shows up here.
const headSize = 4096

// Region names of the static OpenBMC layouts.
const (
	regionUBoot    = "u-boot"
	regionUBootEnv = "u-boot-env"
	regionKernel   = "kernel"
	regionROFS     = "rofs"
	regionRWFS     = "rwfs"
)

// openBMCStaticSizes are the image sizes with a static OpenBMC layout.
func openBMCStaticSizes() []int64 { return []int64{64 << 20, 128 << 20} }

// openBMCStaticLayout returns the static OpenBMC layout for an image size.
//
//nolint:mnd
func openBMCStaticLayout(size int64) ([]region, bool) {
	var rofsEnd int64

	switch size {
	case 64 << 20:
		rofsEnd = 0x29fffff
	case 128 << 20:
		rofsEnd = 0x5ffffff
	default:
		return nil, false
	}

	return []region{
		{name: regionUBoot, start: 0x0, end: 0xdffff},
		{name: regionUBootEnv, start: 0xe0000, end: 0xfffff},
		{name: regionKernel, start: 0x100000, end: 0x9fffff},
		{name: regionROFS, start: 0xa00000, end: rofsEnd},
		{name: regionRWFS, start: rofsEnd + 1, end: size - 1},
	}, true
}

// openBMCStaticAlways returns the regions the BMC writes at runtime.
func openBMCStaticAlways() []string { return []string{regionUBootEnv, regionRWFS} }

// SkipUnchanged configures writes that skip the regions the chip already holds.
// After a full write that the flash tool verified, the module stores the SHA-256
// of the image. A later write of the same image writes only the Always regions,
// provided the start of every skipped region on the chip still matches the image.
type SkipUnchanged struct {
	// Preset selects a built-in layout. Supported: "openbmc-static".
	Preset string `yaml:"preset"`
	// Regions is an explicit layout, used when Preset is empty.
	Regions []Region `yaml:"regions"`
	// Always lists the regions that are written on every write. The preset sets
	// a default.
	Always []string `yaml:"always"`
	// State is the file that holds the hash of the last verified full write.
	// Relative paths are relative to the dutagent working directory.
	State string `yaml:"state"`
}

// Region is one named address range of the flash chip, both ends inclusive.
type Region struct {
	Name  string `yaml:"name"`
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type region struct {
	name       string
	start, end int64
}

func (r region) layoutLine() string {
	return fmt.Sprintf("%08x:%08x %s", r.start, r.end, r.name)
}

// validate checks the configuration that does not depend on the image.
func (s *SkipUnchanged) validate() error {
	switch {
	case s.Preset != "" && len(s.Regions) > 0:
		return errors.New("skipUnchanged: set either preset or regions, not both")
	case s.Preset == "" && len(s.Regions) == 0:
		return errors.New("skipUnchanged: set a preset or regions")
	case s.Preset != "":
		return s.validatePreset()
	}

	layout, err := parseRegions(s.Regions)
	if err != nil {
		return err
	}

	return checkLayout(layout, s.Always)
}

func (s *SkipUnchanged) validatePreset() error {
	if s.Preset != presetOpenBMCStatic {
		return fmt.Errorf("skipUnchanged: unknown preset %q, supported: %q", s.Preset, presetOpenBMCStatic)
	}

	if len(s.Always) == 0 {
		s.Always = openBMCStaticAlways()
	}

	for _, size := range openBMCStaticSizes() {
		layout, _ := openBMCStaticLayout(size)

		err := checkLayout(layout, s.Always)
		if err != nil {
			return err
		}
	}

	return nil
}

// layout returns the regions for an image of the given size.
func (s *SkipUnchanged) layout(size int64) ([]region, error) {
	if s.Preset == presetOpenBMCStatic {
		layout, ok := openBMCStaticLayout(size)
		if !ok {
			return nil, fmt.Errorf("skipUnchanged: preset %q has no layout for an image of %d bytes", s.Preset, size)
		}

		return layout, nil
	}

	layout, err := parseRegions(s.Regions)
	if err != nil {
		return nil, err
	}

	if last := layout[len(layout)-1].end; last >= size {
		return nil, fmt.Errorf("skipUnchanged: region %q ends at 0x%x, beyond the image of %d bytes",
			layout[len(layout)-1].name, last, size)
	}

	return layout, nil
}

func parseRegions(in []Region) ([]region, error) {
	out := make([]region, 0, len(in))

	for _, r := range in {
		if r.Name == "" {
			return nil, errors.New("skipUnchanged: every region needs a name")
		}

		start, err := strconv.ParseInt(r.Start, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("skipUnchanged: region %q: start %q: %w", r.Name, r.Start, err)
		}

		end, err := strconv.ParseInt(r.End, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("skipUnchanged: region %q: end %q: %w", r.Name, r.End, err)
		}

		if start < 0 || end < start {
			return nil, fmt.Errorf("skipUnchanged: region %q: end 0x%x is before start 0x%x", r.Name, end, start)
		}

		out = append(out, region{name: r.Name, start: start, end: end})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })

	for i := 1; i < len(out); i++ {
		if out[i].start <= out[i-1].end {
			return nil, fmt.Errorf("skipUnchanged: regions %q and %q overlap", out[i-1].name, out[i].name)
		}
	}

	return out, nil
}

// checkLayout makes sure every Always region exists and at least one region is
// left to skip.
func checkLayout(layout []region, always []string) error {
	if len(always) == 0 {
		return errors.New("skipUnchanged: always must list at least one region")
	}

	names := make([]string, 0, len(layout))
	for _, r := range layout {
		names = append(names, r.name)
	}

	for _, a := range always {
		if !slices.Contains(names, a) {
			return fmt.Errorf("skipUnchanged: always region %q is not in the layout %v", a, names)
		}
	}

	if len(always) >= len(layout) {
		return errors.New("skipUnchanged: always covers every region, so nothing can be skipped")
	}

	return nil
}

// skipped returns the layout regions that are not in always.
func skipped(layout []region, always []string) []region {
	var out []region

	for _, r := range layout {
		if !slices.Contains(always, r.name) {
			out = append(out, r)
		}
	}

	return out
}

// heads returns a small region at the start of each region in rs.
func heads(rs []region) []region {
	out := make([]region, 0, len(rs))

	for _, r := range rs {
		end := min(r.start+headSize-1, r.end)
		out = append(out, region{name: r.name + "-head", start: r.start, end: end})
	}

	return out
}

// writeLayout writes a flashrom/flashprog layout file for rs.
func writeLayout(path string, rs []region) error {
	lines := make([]string, 0, len(rs))
	for _, r := range rs {
		lines = append(lines, r.layoutLine())
	}

	//nolint:gosec,mnd // the layout is not secret
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// includeArgs returns "-i <name>" for every region.
func includeArgs(rs []region) []string {
	args := make([]string, 0, 2*len(rs)) //nolint:mnd
	for _, r := range rs {
		args = append(args, "-i", r.name)
	}

	return args
}

// fileSHA256 returns the hex SHA-256 of the file at path and its size.
func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hasher := sha256.New()

	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// storedHash returns the hash in the state file, or "" if there is none.
func storedHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}

func storeHash(path, sum string) error {
	dir := filepath.Dir(path)
	if dir != "." {
		err := os.MkdirAll(dir, 0o755) //nolint:mnd
		if err != nil {
			return err
		}
	}

	//nolint:gosec,mnd // the hash is not secret
	return os.WriteFile(path, []byte(sum+"\n"), 0o644)
}

func forgetHash(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

// defaultState returns a state file name that is unique per programmer, so two
// flash commands on one dutagent do not share a hash.
func defaultState(programmer string) string {
	sum := sha256.Sum256([]byte(programmer))

	return "flash-skip-" + hex.EncodeToString(sum[:4]) + ".sha256"
}

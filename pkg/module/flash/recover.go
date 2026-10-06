// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

const (
	defaultRecoverOff     = 15 * time.Second
	defaultRecoverTimeout = 60 * time.Second
	probeTimeout          = 10 * time.Second
)

const (
	// defaultSettle is the wait between enumeration and the first probe. A probe
	// right after enumeration can fail and hang the programmer again.
	defaultSettle = 5 * time.Second
	// defaultEnumerate bounds the wait for the programmer to reappear on USB.
	defaultEnumerate = 30 * time.Second
	defaultInterval  = 2 * time.Second
	// defaultSysfsUSBDevices lists the USB devices.
	defaultSysfsUSBDevices = "/sys/bus/usb/devices"
)

// uhubctlTool switches USB hub port power.
const uhubctlTool = "uhubctl"

// Recover configures the recovery of a programmer that stopped responding. The
// module cuts USB power on all ports of the listed hubs, waits for the
// programmer to appear and to answer a probe, and then writes the whole chip
// again. On boards that gang the power of several ports (for example the
// Raspberry Pi 4), VBUS drops only when every port of every hub in the gang is off.
type Recover struct {
	// Hubs are uhubctl locations (for example "1-1" and "2") whose ports are all
	// powered off.
	Hubs []string `yaml:"hubs"`
	// Device is the USB vendor:product ID of the programmer, for example
	// "0483:dada" for a DediProg SF600PG2.
	Device string `yaml:"device"`
	// Off is how long the ports stay off. Default 15s. A few seconds were not
	// enough to reset a hung DediProg.
	Off string `yaml:"off"`
	// Timeout is how long to wait for the programmer to answer after the power
	// cut. Default 60s.
	Timeout string `yaml:"timeout"`

	off, timeout time.Duration
	// Waits and the sysfs root. validate sets defaults; tests shorten them.
	settle, enumerate, interval time.Duration
	sysfs                       string
}

func (r *Recover) validate() error {
	if len(r.Hubs) == 0 {
		return errors.New("recover: hubs must list at least one uhubctl location")
	}

	if !regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$`).MatchString(r.Device) {
		return fmt.Errorf("recover: device must be a USB vendor:product ID like \"0483:dada\", got %q", r.Device)
	}

	var err error

	r.off, err = durationOr(r.Off, defaultRecoverOff)
	if err != nil {
		return fmt.Errorf("recover: off: %w", err)
	}

	r.timeout, err = durationOr(r.Timeout, defaultRecoverTimeout)
	if err != nil {
		return fmt.Errorf("recover: timeout: %w", err)
	}

	_, err = exec.LookPath(uhubctlTool)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	r.settle, r.enumerate, r.interval, r.sysfs = defaultSettle, defaultEnumerate, defaultInterval, defaultSysfsUSBDevices

	return nil
}

func durationOr(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}

	if dur <= 0 {
		return 0, fmt.Errorf("%q must be positive", s)
	}

	return dur, nil
}

// powerCycle cuts USB power on all ports of the hubs for r.off, then waits until
// probe succeeds. The ports are always switched on again, also when ctx is
// cancelled during the cut, so a cancelled job cannot leave them off.
func (r *Recover) powerCycle(ctx context.Context, sesh module.Session, probe func(context.Context) error) error {
	sesh.Printf("Cutting USB power on all ports of hubs %s for %s\n", strings.Join(r.Hubs, ", "), r.off)

	powerOn := func() {
		// Power must come back even if the job was cancelled.
		onCtx := context.WithoutCancel(ctx)

		for _, hub := range r.Hubs {
			out, err := uhubctl(onCtx, hub, "on")
			if err != nil {
				sesh.Printf("Warning: uhubctl could not power on hub %s: %v\n%s", hub, err, out)
			}
		}
	}

	for _, hub := range r.Hubs {
		out, err := uhubctl(ctx, hub, "off")
		if err != nil {
			powerOn()

			return fmt.Errorf("power off hub %s: %w\n%s", hub, err, out)
		}
	}

	// Halfway through the cut the programmer must be gone from USB. If it is
	// still there, the kernel powered the port again (see the module README).
	half := r.off / 2 //nolint:mnd
	if !sleep(ctx, half) {
		powerOn()

		return ctx.Err()
	}

	if r.usbPresent() {
		sesh.Printf("Warning: %s is still on USB %s into the power cut\n", r.Device, half)
	} else {
		sesh.Printf("%s is off USB\n", r.Device)
	}

	ok := sleep(ctx, r.off-half)

	powerOn()

	if !ok {
		return ctx.Err()
	}

	return r.waitForProgrammer(ctx, sesh, probe)
}

func (r *Recover) waitForProgrammer(ctx context.Context, sesh module.Session, probe func(context.Context) error) error {
	deadline := time.Now().Add(r.enumerate)
	for !r.usbPresent() {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not appear on USB within %s after the power cut", r.Device, r.enumerate)
		}

		if !sleep(ctx, time.Second) {
			return ctx.Err()
		}
	}

	if !sleep(ctx, r.settle) {
		return ctx.Err()
	}

	deadline = time.Now().Add(r.timeout)

	for {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := probe(probeCtx)

		cancel()

		if err == nil {
			sesh.Println("The programmer answers again")

			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("the programmer does not answer %s after the power cut: %w", r.timeout, err)
		}

		if !sleep(ctx, r.interval) {
			return ctx.Err()
		}
	}
}

func uhubctl(ctx context.Context, hub, action string) (string, error) {
	out, err := exec.CommandContext(ctx, uhubctlTool, "-l", hub, "-a", action).CombinedOutput()

	return string(out), err
}

// usbPresent reports whether the programmer's vendor:product ID is attached.
func (r *Recover) usbPresent() bool {
	vendor, product, _ := strings.Cut(strings.ToLower(r.Device), ":")

	entries, err := os.ReadDir(r.sysfs)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		dir := filepath.Join(r.sysfs, entry.Name())

		vendorID, err := os.ReadFile(filepath.Join(dir, "idVendor"))
		if err != nil {
			continue
		}

		productID, err := os.ReadFile(filepath.Join(dir, "idProduct"))
		if err != nil {
			continue
		}

		if strings.TrimSpace(string(vendorID)) == vendor && strings.TrimSpace(string(productID)) == product {
			return true
		}
	}

	return false
}

// sleep waits for d and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

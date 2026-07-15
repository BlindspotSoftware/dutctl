// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ipmi provides a dutagent module that allows IPMI commands to be sent to a DUT's BMC.
package ipmi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
	"github.com/bougou/go-ipmi"
)

func init() {
	module.Register(module.Record{
		ID: "ipmi",
		New: func() module.Module {
			return &IPMI{}
		},
	})
}

// IPMI is a module that provides basic power management functions for a DUT via IPMI.
// It allows sending power on, off, cycle, reset, and status commands to the device's BMC.
type IPMI struct {
	Host     string // Host is the hostname or IP address of the DUT's BMC
	Port     int    // Port is the port of the IPMI interface on the BMC. Default: 623
	User     string // User is used for IPMI authentication
	Password string // Password is used for IPMI authentication. WARNING: stored unsafely as plaintext.
	Timeout  string // Timeout is the duration for IPMI commands. Default: 10 seconds

	timeout   time.Duration // timeout is the resolved command timeout; set in Init
	client    *ipmi.Client  // client is the current IPMI session; nil until the first command
	connected bool          // connected tracks whether client holds a live session
}

// Ensure implementing the Module interface.
var _ module.Module = &IPMI{}

func (i *IPMI) Help() string {
	help := strings.Builder{}
	help.WriteString("IPMI Power Management Module\n")
	help.WriteString("\nUsage:\n")
	help.WriteString("  ipmi [on|off|cycle|reset|status]\n\n")
	help.WriteString("Commands:\n")
	help.WriteString("  on      - Power on the device\n")
	help.WriteString("  off     - Power off the device\n")
	help.WriteString("  cycle   - Power cycle (off, then on)\n")
	help.WriteString("  reset   - Reset the device (if supported)\n")
	help.WriteString("  status  - Show current power status\n")
	help.WriteString("\n")
	help.WriteString("This module provides basic power control functions via IPMI.\n")
	help.WriteString("\n")
	help.WriteString("Commands are sent to BMC with hostname/ip: " + i.Host + "\n")

	return help.String()
}

const (
	defaultPort    = 623              // Default IPMI port
	defaultTimeout = 10 * time.Second // Default timeout for IPMI commands
	trials         = 3                // Number of retry attempts for IPMI commands
	on             = "on"
	off            = "off"
	cycle          = "cycle"
	reset          = "reset"
	status         = "status"
)

// Init validates and normalizes the configuration, applying the default port and
// command timeout when unset. It returns an error if Host is empty. The IPMI
// session is opened lazily on the first command, not here (see connect).
func (i *IPMI) Init(ctx context.Context) error {
	l := log.FromContext(ctx)

	if i.Port == 0 {
		i.Port = defaultPort
		l.Debug(fmt.Sprintf("no port configured, using default %d", defaultPort))
	}

	// Parse custom timeout if provided; an unparseable value falls back to the default.
	i.timeout = defaultTimeout

	if i.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(i.Timeout)
		if err == nil {
			i.timeout = parsedTimeout
			l.Debug(fmt.Sprintf("using custom timeout %s", i.timeout))
		} else {
			l.Debug(fmt.Sprintf("invalid timeout %q, using default %s", i.Timeout, defaultTimeout))
		}
	} else {
		l.Debug(fmt.Sprintf("no timeout configured, using default %s", defaultTimeout))
	}

	if i.Host == "" {
		return fmt.Errorf("IPMI Host is not set")
	}

	// Deliberately do NOT open the IPMI session here. The BMC may be
	// unreachable at agent startup (e.g. the board's outer power is off), and
	// the dutagent treats a failed Init() as fatal — it shuts down and
	// systemd crash-loops the whole agent, taking down every other device on
	// the worker too. The session is opened lazily on the first command and
	// re-opened automatically if it goes stale (see connect / withSession).
	l.Debug(fmt.Sprintf("init completed for %s:%d (BMC session deferred to first use)", i.Host, i.Port))

	return nil
}

// connect ensures a live IPMI session exists. It is a no-op when already
// connected; otherwise it builds a fresh client and opens a new session. A new
// client is used each time so a previously stale session is fully discarded.
// Called lazily on the first command (not in Init) so an unreachable BMC
// surfaces as a normal command error instead of a fatal module-init failure.
func (i *IPMI) connect(ctx context.Context) error {
	if i.connected && i.client != nil {
		return nil
	}

	client, err := ipmi.NewClient(i.Host, i.Port, i.User, i.Password)
	if err != nil {
		return fmt.Errorf("failed to create IPMI client: %v", err)
	}

	client.WithTimeout(i.timeout)
	client.WithRetry(trials)

	err = client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to IPMI BMC %s: %w", i.Host, err)
	}

	i.client = client
	i.connected = true

	return nil
}

// disconnect closes the current session (best-effort) and clears it so the next
// connect opens a fresh one. Used to recover from a session that has gone stale.
func (i *IPMI) disconnect(ctx context.Context) {
	if i.client != nil {
		_ = i.client.Close(ctx)
	}

	i.client = nil
	i.connected = false
}

// withSession runs op against a live IPMI session, transparently re-opening the
// session once if op fails. IPMI/RMCP sessions idle-time-out and the BMC drops
// inactive ones, so a cached session goes stale between commands; without this
// the module would keep using the dead session and every command would time out
// until the agent was restarted. On the first failure the session is dropped,
// reconnected, and op is retried once so a stale session self-heals.
func (i *IPMI) withSession(ctx context.Context, op func() error) error {
	err := i.connect(ctx)
	if err != nil {
		return err
	}

	err = op()
	if err == nil {
		return nil
	}

	// First attempt failed — most likely a stale session. Drop it, reconnect,
	// and retry once against a fresh session.
	log.FromContext(ctx).Debug(fmt.Sprintf("command failed (%v); re-opening session and retrying once", err))

	i.disconnect(ctx)

	cerr := i.connect(ctx)
	if cerr != nil {
		return fmt.Errorf("%v; reconnect failed: %w", err, cerr)
	}

	return op()
}

// Deinit closes the IPMI session if one is open, and is a no-op otherwise. The
// session is opened lazily on the first command, so a module that never ran a
// command has nothing to close.
func (i *IPMI) Deinit(ctx context.Context) error {
	if i.client == nil || !i.connected {
		return nil
	}

	err := i.client.Close(ctx)
	if err != nil {
		log.FromContext(ctx).Debug(fmt.Sprintf("Deinit failed to close client: %v", err))
	}

	i.client = nil
	i.connected = false

	return err
}

// Run executes a single IPMI command taken from the first argument: on, off,
// cycle, reset or status. A missing or unknown command is reported to the
// session and returns a nil error; a failure talking to the BMC returns an
// error.
func (i *IPMI) Run(ctx context.Context, s module.Session, args ...string) error {
	if len(args) == 0 {
		s.Println("No command specified. Try 'help' for usage.")

		return nil
	}

	command := strings.ToLower(args[0])

	switch command {
	case on, off, cycle, reset:
		return i.handlePowerCommand(ctx, s, command)
	case status:
		return i.handleStatusCommand(ctx, s)
	default:
		s.Println("Unknown command: " + command)
		s.Println("Available commands: on, off, cycle, reset, status")

		return nil
	}
}

func (i *IPMI) handlePowerCommand(ctx context.Context, s module.Session, command string) error {
	var (
		controlType ipmi.ChassisControl
		message     string
	)

	switch command {
	case on:
		controlType = ipmi.ChassisControlPowerUp
		message = "Power ON command sent"
	case off:
		controlType = ipmi.ChassisControlPowerDown
		message = "Power OFF command sent"
	case cycle:
		controlType = ipmi.ChassisControlPowerCycle
		message = "Power CYCLE command sent"
	case reset:
		controlType = ipmi.ChassisControlHardReset
		message = "Power RESET command sent"
	}

	err := i.withSession(ctx, func() error {
		_, cerr := i.client.ChassisControl(ctx, controlType)

		return cerr
	})
	if err != nil {
		return fmt.Errorf("power %s command failed: %v", command, err)
	}

	log.FromContext(ctx).Info(fmt.Sprintf("chassis %s (BMC %s)", command, i.Host))
	s.Println(message)

	return nil
}

func (i *IPMI) handleStatusCommand(ctx context.Context, s module.Session) error {
	var powerIsOn bool

	err := i.withSession(ctx, func() error {
		chassis, cerr := i.client.GetChassisStatus(ctx)
		if cerr != nil {
			return cerr
		}

		powerIsOn = chassis.PowerIsOn

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to get chassis status: %v", err)
	}

	powerStatus := "Off"
	if powerIsOn {
		powerStatus = "On"
	}

	s.Printf("Device power status: %s\n", powerStatus)

	return nil
}

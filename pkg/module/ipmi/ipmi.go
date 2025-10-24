// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ipmi provides a dutagent module that allows IPMI commands to be sent to a DUT's BMC.
package ipmi

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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
	Password string // Password is used for IPMI authentication. WARNING: Unsavely stored as plaintext
	Timeout  string // Timeout is the duration for IPMI commands. Default: 10 seconds

	client *ipmi.Client // client is the module's internal entity to forward IPMI commands
}

// Ensure implementing the Module interface.
var _ module.Module = &IPMI{}

func (i *IPMI) Help() string {
	log.Println("ipmi module: Help called")

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

func (i *IPMI) Init() error {
	log.Printf("ipmi module: Init starting for BMC %s", i.Host)

	port := i.Port
	if port == 0 {
		port = defaultPort
		log.Printf("ipmi module: Using default port %d", defaultPort)
	}

	// Parse custom timeout if provided
	timeout := defaultTimeout

	if i.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(i.Timeout)
		if err == nil {
			timeout = parsedTimeout
			log.Printf("ipmi module: Using custom timeout %v", timeout)
		} else {
			log.Printf("ipmi module: Invalid timeout format '%s', using default %v", i.Timeout, defaultTimeout)
		}
	}

	if i.Host == "" {
		return fmt.Errorf("IPMI Host is not set")
	}

	ipmiClient, err := ipmi.NewClient(i.Host, port, i.User, i.Password)
	if err != nil {
		return fmt.Errorf("failed to create IPMI client: %v", err)
	}

	ipmiClient.WithTimeout(timeout)
	ipmiClient.WithRetry(trials, time.Second)

	err = ipmiClient.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("failed to connect to IPMI BMC %s:%d: %v", i.Host, port, err)
	}

	i.client = ipmiClient
	log.Printf("ipmi module: Init completed successfully for %s:%d", i.Host, port)

	return nil
}

func (i *IPMI) Deinit() error {
	if i.client == nil {
		return nil
	}

	err := i.client.Close(context.Background())
	if err != nil {
		log.Printf("ipmi module: Deinit failed to close client: %v", err)
	}

	return err
}

func (i *IPMI) Run(ctx context.Context, s module.Session, args ...string) error {
	if i.client == nil {
		return fmt.Errorf("IPMI client not initialized")
	}

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

	_, err := i.client.ChassisControl(ctx, controlType)
	if err != nil {
		return fmt.Errorf("power %s command failed: %v", command, err)
	}

	s.Println(message)

	return nil
}

func (i *IPMI) handleStatusCommand(ctx context.Context, s module.Session) error {
	status, err := i.client.GetChassisStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chassis status: %v", err)
	}

	powerStatus := "Off"
	if status.PowerIsOn {
		powerStatus = "On"
	}

	s.Printf("Device power status: %s\n", powerStatus)

	return nil
}

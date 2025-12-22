// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pikvm provides a dutagent module that allows control of a PiKVM device via HTTP API.
package pikvm

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "pikvm",
		New: func() module.Module {
			return &PiKVM{}
		},
	})
}

// PiKVM is a module that provides power management, keyboard control, and virtual media
// functionality for a DUT via PiKVM device.
type PiKVM struct {
	Host     string // Host is the address of the PiKVM device (e.g., "192.168.1.100" or "https://pikvm.local")
	User     string // User for authentication (default: "admin")
	Password string // Password for authentication
	Timeout  string // Timeout for HTTP requests (default: "10s")

	client  *http.Client
	baseURL *url.URL
}

// Ensure implementing the Module interface.
var _ module.Module = &PiKVM{}

const (
	defaultUser     = "admin"
	defaultTimeout  = 10 * time.Second
	minArgsRequired = 2 // Minimum arguments required for commands with parameters

	// Status values.
	statusUnknown = "Unknown"
	statusOn      = "On"
	statusOff     = "Off"
	mediaNone     = "None"
)

// Command constants.
const (
	// Power management.
	on         = "on"
	off        = "off"
	forceOff   = "force-off"
	reset      = "reset"
	forceReset = "force-reset"
	status     = "status"

	// Keyboard control.
	typeCmd  = "type"
	key      = "key"
	keyCombo = "key-combo"
	paste    = "paste"

	// Virtual media.
	mount       = "mount"
	mountURL    = "mount-url"
	unmount     = "unmount"
	mediaStatus = "media-status"

	// Screenshot.
	screenshot = "screenshot"
)

func (p *PiKVM) Help() string {
	log.Println("pikvm module: Help called")

	help := strings.Builder{}
	help.WriteString("PiKVM Control Module\n\n")
	help.WriteString("Power Management:\n")
	help.WriteString("  pikvm on           - Power on via short ATX power button press\n")
	help.WriteString("  pikvm off          - Graceful shutdown via short ATX power button press\n")
	help.WriteString("  pikvm force-off    - Force power off via long ATX power button press (4-5s)\n")
	help.WriteString("  pikvm reset        - Reset via short ATX reset button press\n")
	help.WriteString("  pikvm force-reset  - Force reset via long ATX reset button press\n")
	help.WriteString("  pikvm status       - Query current power state\n\n")
	help.WriteString("Keyboard Control:\n")
	help.WriteString("  pikvm type <text>      - Type a text string\n")
	help.WriteString("  pikvm key <key>        - Send a single key (e.g., Enter, Escape, F12)\n")
	help.WriteString("  pikvm key-combo <keys> - Send key combination (e.g., Ctrl+Alt+Delete)\n")
	help.WriteString("  pikvm paste            - Paste text from stdin\n\n")
	help.WriteString("Virtual Media:\n")
	help.WriteString("  pikvm mount <path>      - Mount an image file from local filesystem\n")
	help.WriteString("  pikvm mount-url <url>   - Mount an image from a URL\n")
	help.WriteString("  pikvm unmount           - Unmount current virtual media\n")
	help.WriteString("  pikvm media-status      - Show mounted media information\n\n")
	help.WriteString("Screenshot:\n")
	help.WriteString("  pikvm screenshot        - Capture a screenshot (saved to current directory)\n\n")
	help.WriteString("This module provides comprehensive control of a PiKVM device.\n")
	help.WriteString("Configured PiKVM: " + p.Host + "\n")

	return help.String()
}

func (p *PiKVM) Init() error {
	log.Printf("pikvm module: Init starting for host %s", p.Host)

	if p.Host == "" {
		return fmt.Errorf("pikvm: host is not set")
	}

	// Set default user if not provided
	if p.User == "" {
		p.User = defaultUser
		log.Printf("pikvm module: Using default user '%s'", defaultUser)
	}

	// Parse custom timeout if provided
	timeout := defaultTimeout

	if p.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(p.Timeout)
		if err == nil {
			timeout = parsedTimeout
			log.Printf("pikvm module: Using custom timeout %v", timeout)
		} else {
			log.Printf("pikvm module: Invalid timeout format '%s', using default %v", p.Timeout, defaultTimeout)
		}
	}

	p.client = &http.Client{Timeout: timeout}

	// Normalize host: add scheme if missing
	host := strings.TrimSpace(p.Host)
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}

	baseURL, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil {
		return fmt.Errorf("pikvm: invalid host URL: %v", err)
	}

	p.baseURL = baseURL

	log.Printf("pikvm module: Init completed successfully for %s", p.baseURL.String())

	return nil
}

func (p *PiKVM) Deinit() error {
	log.Println("pikvm module: Deinit called")

	return nil
}

func (p *PiKVM) Run(ctx context.Context, s module.Session, args ...string) error {
	if p.client == nil {
		return fmt.Errorf("pikvm: client not initialized")
	}

	if len(args) == 0 {
		s.Println("No command specified. Try 'help' for usage.")

		return nil
	}

	command := strings.ToLower(args[0])

	switch command {
	case on, off, forceOff, reset, forceReset, status:
		return p.handlePowerCommand(ctx, s, command)
	case typeCmd, key, keyCombo, paste:
		return p.handleKeyboardCommand(ctx, s, command, args)
	case mount, mountURL, unmount, mediaStatus:
		return p.handleMediaCommand(ctx, s, command, args)
	case screenshot:
		return p.handleScreenshot(ctx, s)
	default:
		return p.showUnknownCommand(s, command)
	}
}

// showUnknownCommand displays an error message for unknown commands.
func (p *PiKVM) showUnknownCommand(s module.Session, command string) error {
	s.Println("Unknown command: " + command)
	s.Println("Available commands:")
	s.Println("  Power: on, off, force-off, reset, force-reset, status")
	s.Println("  Keyboard: type, key, key-combo, paste")
	s.Println("  Media: mount, mount-url, unmount, media-status")
	s.Println("  Screenshot: screenshot")

	return nil
}

// doRequest creates and executes an HTTP request with authentication.
// contentType is optional; if empty, no Content-Type header is set.
func (p *PiKVM) doRequest(ctx context.Context, method, urlPath string, body io.Reader, contentType string) (*http.Response, error) {
	u := *p.baseURL
	u.Path = path.Join(u.Path, urlPath)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if p.User != "" && p.Password != "" {
		req.SetBasicAuth(p.User, p.Password)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		return nil, fmt.Errorf("pikvm: API returned %s: %s", resp.Status, string(bodyBytes))
	}

	return resp, nil
}

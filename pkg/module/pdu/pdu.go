// Copyright 2025-2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdu provides a dutagent module that allows power control of a PDU via HTTP requests.
package pdu

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "pdu",
		New: func() module.Module {
			return &PDU{}
		},
	})
}

type switchable interface {
	setPower(ctx context.Context, s module.Session, state string) error
	getState(ctx context.Context, s module.Session) error
	init() error
}

// PDU is a module that provides basic power management functions for a PDU (Power Distribution Unit).
// NOTE: This implementation currently supports only Intellinet ATM PDUs.
type PDU struct {
	Host     string // Host is the address of the PDU
	User     string // User is used for authentication, if supported by the PDU
	Password string // Password is used for authentication, if supported by the PDU
	Outlet   int    // Outlet is the outlet to control, if the PDU supports multiple outlets. Defaults to 0 (first outlet).
	PDUType  string // Type, Currently `intellinet` is the only supportet type

	client     *http.Client // internal HTTP client for request to the PDU
	controlURL *url.URL     // controlURL is the URL for controlling the PDU outlet
	statusURL  *url.URL     // statusURL is the URL for getting the PDU status

	pduInterface switchable
}

func (p *PDU) Help() string {
	log.Println("pdu module: Help called")

	help := strings.Builder{}

	help.WriteString("PDU Power Management Module\n")
	help.WriteString("\nUsage:\n")
	help.WriteString("  pdu-power [on|off|toggle|status]\n\n")
	help.WriteString("Commands:\n")
	help.WriteString("  on      - Power on the outlet\n")
	help.WriteString("  off     - Power off the outlet\n")
	help.WriteString("  toggle  - Toggle the outlet power\n")
	help.WriteString("  status  - Get current power state\n")
	help.WriteString("\n")
	help.WriteString("This module provides basic power control functions via HTTP to a PDU.\n")
	help.WriteString("The configured PDU has IP: " + p.Host + "\n")
	help.WriteString(fmt.Sprintf("The configured outlet is: %d\n", p.Outlet))

	return help.String()
}

const (
	defaultTimeout = 10 * time.Second // Default timeout for HTTP requests
	on             = "on"
	off            = "off"
	toggle         = "toggle"
	status         = "status"
	intellinetPDU  = "intellinet"
	gudePDU        = "gude"
)

func (p *PDU) Init() error {
	log.Printf("pdu module: Init called - Host: %s, User: %s, Outlet: %d, Type: %s", p.Host, p.User, p.Outlet, p.PDUType)

	if p.Outlet < 0 {
		return fmt.Errorf("invalid outlet number %d: outlet must be 0 or greater", p.Outlet)
	}

	p.client = &http.Client{Timeout: defaultTimeout}

	switch p.PDUType {
	case gudePDU:
		p.pduInterface = &gude{pdu: p}
	case intellinetPDU:
		p.pduInterface = &intellinet{pdu: p}
	default: // Legacy configs dont contain PDUType and are meant for Intillinet (style) PDUs
		p.pduInterface = &intellinet{pdu: p}
	}

	err := p.pduInterface.init()
	if err != nil {
		return err
	}

	log.Printf("pdu module: Init completed - controlURL: %s, statusURL: %s", p.controlURL.String(), p.statusURL.String())

	return nil
}

func (p *PDU) Deinit() error {
	log.Println("pdu module: Deinit called")

	return nil
}

func (p *PDU) Run(ctx context.Context, s module.Session, args ...string) error {
	if p.client == nil {
		return fmt.Errorf("PDU client not initialized")
	}

	if p.Host == "" {
		return fmt.Errorf("PDU host address not configured")
	}

	if len(args) == 0 {
		s.Println("No command specified. Call 'help' for usage.")

		return nil
	}

	cmd := strings.ToLower(args[0])

	switch cmd {
	case on, off, toggle:
		return p.setPower(ctx, s, cmd)
	case status:
		return p.status(ctx, s)
	default:
		s.Println("Unknown command: " + cmd)
		s.Println("Available commands: on, off, toggle, status")

		return nil
	}
}

// doRequest creates and executes an HTTP request with authentication and validates the response.
func doRequest(p *PDU, ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if p.User != "" && p.Password != "" {
		req.SetBasicAuth(p.User, p.Password)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		return nil, fmt.Errorf("PDU command failed with status %s: %s", resp.Status, string(body))
	}

	return resp, nil
}

func (p *PDU) setPower(ctx context.Context, s module.Session, state string) error {
	err := p.pduInterface.setPower(ctx, s, state)
	if err != nil {
		return err
	}

	return nil
}

func (p *PDU) status(ctx context.Context, s module.Session) error {
	err := p.pduInterface.getState(ctx, s)

	if err != nil {
		return err
	}

	return nil
}

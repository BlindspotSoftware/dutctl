// Copyright 2025 Blindspot Software
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

// PDU is a module that provides basic power management functions for a PDU (Power Distribution Unit).
// NOTE: This implementation currently supports only Intellinet ATM PDUs.
type PDU struct {
	Host     string // Host is the address of the PDU
	User     string // User is used for authentication, if supported by the PDU
	Password string // Password is used for authentication, if supported by the PDU
	Outlet   int    // Outlet is the outlet to control, if the PDU supports multiple outlets. Defaults to 0 (first outlet).

	client     *http.Client // internal HTTP client for request to the PDU
	controlURL *url.URL     // controlURL is the URL for controlling the PDU outlet
	statusURL  *url.URL     // statusURL is the URL for getting the PDU status
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
)

func (p *PDU) Init() error {
	log.Printf("pdu module: Init called - Host: %s, User: %s, Outlet: %d", p.Host, p.User, p.Outlet)

	if p.Outlet < 0 {
		return fmt.Errorf("invalid outlet number %d: outlet must be 0 or greater", p.Outlet)
	}

	p.client = &http.Client{Timeout: defaultTimeout}

	controlURL, err := url.Parse(strings.TrimRight(p.Host, "/") + "/control_outlet.htm")
	if err != nil {
		return err
	}

	p.controlURL = controlURL

	statusURL, err := url.Parse(strings.TrimRight(p.Host, "/") + "/status.xml")
	if err != nil {
		return err
	}

	p.statusURL = statusURL

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
		s.Print("No command specified. Call 'help' for usage.")

		return nil
	}

	cmd := strings.ToLower(args[0])

	switch cmd {
	case on, off, toggle:
		return p.setPower(ctx, s, cmd)
	case status:
		return p.status(ctx, s)
	default:
		s.Print("Unknown command: " + cmd)
		s.Print("Available commands: on, off, toggle, status")

		return nil
	}
}

// doRequest creates and executes an HTTP request with authentication and validates the response.
func (p *PDU) doRequest(ctx context.Context, url string) (*http.Response, error) {
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
	opState, err := parseOp(state)
	if err != nil {
		return err
	}

	q := p.controlURL.Query()
	q.Set(fmt.Sprintf("outlet%d", p.Outlet), "1")
	q.Set("op", opState.String())
	p.controlURL.RawQuery = q.Encode()

	resp, err := p.doRequest(ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	s.Print(fmt.Sprintf("PDU outlet%d power set to '%s' successfully", p.Outlet, state))

	return nil
}

func (p *PDU) status(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, p.statusURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	outletValue, err := p.parseOutletStatus(body)
	if err != nil {
		return err
	}

	s.Print(fmt.Sprintf("PDU outlet%d state: %s", p.Outlet, outletValue))

	return nil
}

// parseOutletStatus extracts the outlet status from XML response body.
func (p *PDU) parseOutletStatus(body []byte) (string, error) {
	bodyStr := string(body)

	outletTag := fmt.Sprintf("<outletStat%d>", p.Outlet)
	outletEndTag := fmt.Sprintf("</outletStat%d>", p.Outlet)

	startIdx := strings.Index(bodyStr, outletTag)
	if startIdx == -1 {
		return "", fmt.Errorf("outlet %d not found in PDU status", p.Outlet)
	}

	startIdx += len(outletTag)

	endIdx := strings.Index(bodyStr[startIdx:], outletEndTag)
	if endIdx == -1 {
		return "", fmt.Errorf("malformed XML for outlet %d", p.Outlet)
	}

	outletValue := strings.TrimSpace(bodyStr[startIdx : startIdx+endIdx])

	if outletValue != on && outletValue != off {
		return "", fmt.Errorf("unexpected outlet state '%s' for outlet %d", outletValue, p.Outlet)
	}

	return outletValue, nil
}

type op string

const (
	opOn     op = "0"
	opOff    op = "1"
	opToggle op = "2"
)

func (o op) String() string {
	return string(o)
}

func parseOp(state string) (op, error) {
	switch state {
	case on:
		return opOn, nil
	case off:
		return opOff, nil
	case toggle:
		return opToggle, nil
	default:
		return "", fmt.Errorf("invalid PDU operation: %s", state)
	}
}

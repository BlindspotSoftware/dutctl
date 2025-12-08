// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// handlePowerCommandRouter routes power commands based on the first argument.
func (p *PiKVM) handlePowerCommandRouter(ctx context.Context, s module.Session, args []string) error {
	if len(args) == 0 {
		s.Println("Power command requires an action: on|off|force-off|reset|force-reset|status")

		return nil
	}

	command := strings.ToLower(args[0])

	return p.handlePowerCommand(ctx, s, command)
}

// handlePowerCommand dispatches power management commands.
func (p *PiKVM) handlePowerCommand(ctx context.Context, s module.Session, command string) error {
	switch command {
	case on:
		return p.sendATXPower(ctx, s, "on")
	case off:
		return p.sendATXPower(ctx, s, "off")
	case forceOff:
		return p.sendATXPower(ctx, s, "off_hard")
	case reset:
		return p.sendATXClick(ctx, s, "reset")
	case forceReset:
		return p.sendATXPower(ctx, s, "reset_hard")
	case status:
		return p.handleStatusCommand(ctx, s)
	default:
		return fmt.Errorf("unknown power action: %s (must be: on, off, force-off, reset, force-reset, status)", command)
	}
}

// sendATXPower sends an ATX power action using the /api/atx/power endpoint.
// This endpoint is intelligent: 'on' does nothing if already on, 'off' is graceful shutdown.
func (p *PiKVM) sendATXPower(ctx context.Context, s module.Session, action string) error {
	endpoint := fmt.Sprintf("/api/atx/power?action=%s", action)

	resp, err := p.doRequest(ctx, http.MethodPost, endpoint, nil, "")
	if err != nil {
		return fmt.Errorf("ATX power %s failed: %v", action, err)
	}
	defer resp.Body.Close()

	s.Printf("ATX power action '%s' completed\n", action)

	return nil
}

// sendATXClick sends an ATX button click using the /api/atx/click endpoint.
func (p *PiKVM) sendATXClick(ctx context.Context, s module.Session, button string) error {
	endpoint := fmt.Sprintf("/api/atx/click?button=%s", button)

	resp, err := p.doRequest(ctx, http.MethodPost, endpoint, nil, "")
	if err != nil {
		return fmt.Errorf("ATX %s button click failed: %v", button, err)
	}
	defer resp.Body.Close()

	s.Printf("ATX %s button clicked\n", button)

	return nil
}

// ATXStatus represents the PiKVM ATX status response.
type ATXStatus struct {
	Ok     bool            `json:"ok"`
	Result ATXStatusResult `json:"result"`
}

// ATXStatusResult contains the actual ATX status data.
type ATXStatusResult struct {
	Enabled bool `json:"enabled"`
	Leds    struct {
		Power bool `json:"power"`
		HDD   bool `json:"hdd"`
	} `json:"leds"`
	Busy bool `json:"busy"`
}

func (p *PiKVM) handleStatusCommand(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/atx", nil, "")
	if err != nil {
		return fmt.Errorf("failed to get ATX status: %v", err)
	}
	defer resp.Body.Close()

	var status ATXStatus

	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		return fmt.Errorf("failed to parse status response: %v", err)
	}

	if !status.Ok {
		return fmt.Errorf("ATX status response not ok")
	}

	// Extract power state from response
	powerState := statusOff
	if status.Result.Leds.Power {
		powerState = statusOn
	}

	s.Printf("Device power status: %s\n", powerState)

	return nil
}

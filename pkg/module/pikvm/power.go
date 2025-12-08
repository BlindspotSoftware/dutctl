// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// handlePowerCommand dispatches power management commands.
func (p *PiKVM) handlePowerCommand(ctx context.Context, s module.Session, command string) error {
	switch command {
	case on:
		return p.sendATXClick(ctx, s, "power", "short")
	case off:
		return p.sendATXClick(ctx, s, "power", "short")
	case forceOff:
		return p.sendATXClick(ctx, s, "power", "long")
	case reset:
		return p.sendATXClick(ctx, s, "reset", "short")
	case forceReset:
		return p.sendATXClick(ctx, s, "reset", "long")
	case status:
		return p.handleStatusCommand(ctx, s)
	default:
		return fmt.Errorf("unknown power command: %s", command)
	}
}

func (p *PiKVM) sendATXClick(ctx context.Context, s module.Session, button, action string) error {
	payload := map[string]interface{}{
		"button": button,
		"wait":   action == "long",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/atx/click", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("ATX %s %s command failed: %v", button, action, err)
	}
	defer resp.Body.Close()

	s.Printf("ATX %s button %s press completed\n", button, action)

	return nil
}

func (p *PiKVM) handleStatusCommand(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/atx", nil, "")
	if err != nil {
		return fmt.Errorf("failed to get ATX status: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var status map[string]interface{}

	err = json.Unmarshal(body, &status)
	if err != nil {
		return fmt.Errorf("failed to parse status response: %v", err)
	}

	// Extract power state from response
	powerState := p.extractPowerState(status)
	s.Printf("Device power status: %s\n", powerState)

	return nil
}

// extractPowerState extracts the power status from the API response.
func (p *PiKVM) extractPowerState(status map[string]interface{}) string {
	result, ok := status["result"].(map[string]interface{})
	if !ok {
		return statusUnknown
	}

	leds, ok := result["leds"].(map[string]interface{})
	if !ok {
		return statusUnknown
	}

	power, ok := leds["power"].(bool)
	if !ok {
		return statusUnknown
	}

	if power {
		return statusOn
	}

	return statusOff
}

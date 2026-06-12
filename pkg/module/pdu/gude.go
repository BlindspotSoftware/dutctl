// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

type gudeCommands int

const (
	gudeSwitchCommand gudeCommands = iota
	gudeBatchModeCommand
	gudeResetCommand
)

func (g gudeCommands) String() string {
	switch g {
	case gudeSwitchCommand:
		return "1"
	case gudeBatchModeCommand:
		return "2"
	case gudeResetCommand:
		return "12"
	default:
		return ""
	}
}

type gudeState int

const (
	gudeStateOff gudeState = iota
	gudeStateOn
)

func (g gudeState) String() string {
	switch g {
	case gudeStateOff:
		return off
	case gudeStateOn:
		return on
	default:
		return ""
	}
}

func (g gudeState) getAPIParameter() string {
	switch g {
	case gudeStateOff:
		return "0"
	case gudeStateOn:
		return "1"
	default:
		return ""
	}
}

func newGudeStateFromInt(state int) (gudeState, error) {
	switch state {
	case 1:
		return gudeStateOn, nil
	case 0:
		return gudeStateOff, nil
	default:
		return -1, fmt.Errorf("invalid state: %d", state)
	}
}

func newGudeStateFromString(state string) (gudeState, error) {
	switch state {
	case "on":
		return gudeStateOn, nil
	case "off":
		return gudeStateOff, nil
	default:
		return -1, fmt.Errorf("invalid state: %s", state)
	}
}

// gudeStateResponse represents the JSON response from Gude PDU status endpoint.
type gudeStateResponse struct {
	Outputs []gudeOutput `json:"outputs"`
}

// gudeOutput represents a single power output in the Gude PDU.
type gudeOutput struct {
	Name  string `json:"name"`
	State int    `json:"state"`  // 0 = off, 1 = on.
	SwCnt int    `json:"sw_cnt"` //nolint:revive // JSON field name is defined by device API.
	Type  int    `json:"type"`
	Batch []int  `json:"batch"`
	Wdog  []any  `json:"wdog"`
}

type gude struct{}

func (g gude) getOutletAPIParameter(p *PDU) string {
	outlet := p.Outlet + 1
	return strconv.Itoa(outlet)
}

func (g gude) init(p *PDU) error {
	controlURL, err := url.Parse(strings.TrimRight(p.Host, "/") + "/ov.html")
	if err != nil {
		return err
	}

	p.controlURL = controlURL

	statusURL, err := url.Parse(strings.TrimRight(p.Host, "/") + "/statusjsn.js?components=1")
	if err != nil {
		return err
	}

	p.statusURL = statusURL

	return nil
}

func (g gude) setPower(ctx context.Context, s module.Session, p *PDU, state string) error {
	var err error

	switch state {
	case on, off:
		err = g.switchPower(ctx, p, state)
	case toggle:
		err = g.togglePower(ctx, p)
	}

	if err != nil {
		return err
	}

	p.printPowerSet(s, state)

	return nil
}

func (g gude) fetchState(ctx context.Context, s module.Session, p *PDU) error {
	state, err := g.fetchOutletState(ctx, p)
	if err != nil {
		return err
	}

	p.printState(s, state.String())

	return nil
}

func (g gude) switchPower(ctx context.Context, p *PDU, newState string) error {
	state, err := newGudeStateFromString(newState)
	if err != nil {
		return err
	}

	q := p.controlURL.Query()
	q.Set("cmd", gudeSwitchCommand.String())
	q.Set("p", g.getOutletAPIParameter(p))
	q.Set("s", state.getAPIParameter())

	p.controlURL.RawQuery = q.Encode()

	resp, err := p.doRequest(ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (g gude) togglePower(ctx context.Context, p *PDU) error {
	currentState, err := g.fetchOutletState(ctx, p)
	if err != nil {
		return err
	}

	var nextState gudeState
	nextState = gudeState((currentState + 1) % 2)

	q := p.controlURL.Query()
	q.Set("cmd", gudeSwitchCommand.String())
	q.Set("p", g.getOutletAPIParameter(p))
	q.Set("s", nextState.getAPIParameter())

	p.controlURL.RawQuery = q.Encode()

	resp, err := p.doRequest(ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (g gude) fetchOutletState(ctx context.Context, p *PDU) (gudeState, error) {
	resp, err := p.doRequest(ctx, p.statusURL.String())
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, err
	}

	value, err := g.parseOutletStatus(p, body)
	if err != nil {
		return -1, err
	}

	state, err := newGudeStateFromInt(value)
	if err != nil {
		return -1, err
	}

	return state, nil
}

// extract the outlet status from JSON response body.
func (g gude) parseOutletStatus(p *PDU, body []byte) (int, error) {
	var status gudeStateResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return -1, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if len(status.Outputs) == 0 {
		return -1, fmt.Errorf("no outputs found in PDU status response")
	}

	if p.Outlet >= len(status.Outputs) {
		return -1, fmt.Errorf("outlet %d not found in PDU status (only %d outlets available)", p.Outlet, len(status.Outputs))
	}

	output := status.Outputs[p.Outlet]

	return output.State, nil
}

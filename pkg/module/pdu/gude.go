// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdu provides a dutagent module that allows power control of a PDU via HTTP requests.
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

var gudecommandString = map[gudeCommands]string{
	gudeSwitchCommand:    "1",
	gudeBatchModeCommand: "2",
	gudeResetCommand:     "12",
}

func (g gudeCommands) String() string {
	return gudecommandString[g]
}

type gudeState int

const (
	gudeStateOff gudeState = 0
	gudeStateOn  gudeState = 1
)

var gudeStateString = map[gudeState]string{
	gudeStateOff: off,
	gudeStateOn:  on,
}

func (g gudeState) String() string {
	return gudeStateString[g]
}

var gudeStateParameter = map[gudeState]string{
	gudeStateOff: "0",
	gudeStateOn:  "1",
}

func (g gudeState) getAPIParameter() string {
	return gudeStateParameter[g]
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

// gudeStateResponse represents the JSON response from Gude PDU status endpoint
type gudeStateResponse struct {
	Outputs []gudeOutput `json:"outputs"`
}

// gudeOutput represents a single power output in the Gude PDU
type gudeOutput struct {
	Name  string `json:"name"`
	State int    `json:"state"` // 0 = off, 1 = on
	SwCnt int    `json:"sw_cnt"`
	Type  int    `json:"type"`
	Batch []int  `json:"batch"`
	Wdog  []any  `json:"wdog"`
}

type gude struct {
	pdu *PDU
}

func (g *gude) getOutletAPIParameter() string {
	outlet := g.pdu.Outlet + 1
	return strconv.Itoa(outlet)
}

func (g *gude) init() error {
	p := g.pdu

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

func (g *gude) setPower(ctx context.Context, s module.Session, state string) error {
	p := g.pdu

	var err error

	switch state {
	case on, off:
		err = g.switchPower(ctx, s, state)
	case toggle:
		err = g.togglePower(ctx, s, state)
	}

	if err != nil {
		return err
	}

	s.Printf("PDU outlet%d power set to '%s' successfully\n", p.Outlet, state)

	return nil
}

func (g *gude) getState(ctx context.Context, s module.Session) error {
	p := g.pdu

	state, err := g.fetchOutletState(ctx)
	if err != nil {
		return err
	}

	s.Printf("PDU outlet %d state: %s\n", p.Outlet, state.String())

	return nil
}

func (g *gude) switchPower(ctx context.Context, s module.Session, newState string) error {
	p := g.pdu

	state, err := newGudeStateFromString(newState)
	if err != nil {
		return err
	}

	q := p.controlURL.Query()
	q.Set("cmd", gudeSwitchCommand.String())
	q.Set("p", g.getOutletAPIParameter())
	q.Set("s", state.getAPIParameter())

	p.controlURL.RawQuery = q.Encode()

	resp, err := doRequest(p, ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (g *gude) togglePower(ctx context.Context, s module.Session, state string) error {
	p := g.pdu

	currentState, err := g.fetchOutletState(ctx)
	if err != nil {
		return err
	}

	var nextState gudeState
	nextState = gudeState((currentState + 1) % 2)

	q := p.controlURL.Query()
	q.Set("cmd", gudeSwitchCommand.String())
	q.Set("p", g.getOutletAPIParameter())
	q.Set("s", nextState.getAPIParameter())

	p.controlURL.RawQuery = q.Encode()

	resp, err := doRequest(p, ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (g *gude) fetchOutletState(ctx context.Context) (gudeState, error) {
	resp, err := doRequest(g.pdu, ctx, g.pdu.statusURL.String())
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, err
	}

	value, err := g.parseOutletStatus(body)
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
func (g *gude) parseOutletStatus(body []byte) (int, error) {
	p := g.pdu

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

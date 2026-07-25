// Copyright 2025 Blindspot Software
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
)

// gudeCmdSwitch is the Gude "cmd" query value that switches an outlet on or off.
const gudeCmdSwitch = "1"

// gude drives Gude PDUs via their statusjsn.js and ov.html HTTP+JSON API.
type gude struct {
	req  *requester
	base *url.URL
}

// setPower implements the backend interface for Gude PDUs. Gude has no native
// toggle, so toggle is emulated by reading the current state and switching to
// its opposite via the ov.html endpoint.
func (g gude) setPower(ctx context.Context, outlet int, act action) (state, error) {
	var newState gudeState

	switch act {
	case turnOn:
		newState = gudeOn
	case turnOff:
		newState = gudeOff
	case toggle:
		current, err := g.readState(ctx, outlet)
		if err != nil {
			return off, fmt.Errorf("could not read outlet %d state to toggle it: %w", outlet, err)
		}

		newState = current.toggled()
	default:
		return off, fmt.Errorf("invalid PDU operation: %s", act)
	}

	endpoint := g.base.JoinPath("ov.html")
	// Outlet is a 0-based index (0 = first outlet), matching Gude's status array.
	// Gude's switch API numbers ports from 1, so "p" is Outlet+1.
	endpoint.RawQuery = url.Values{
		"cmd": {gudeCmdSwitch},
		"p":   {strconv.Itoa(outlet + 1)},
		"s":   {newState.switchValue()},
	}.Encode()

	resp, err := g.req.get(ctx, endpoint.String())
	if err != nil {
		return off, err
	}

	resp.Body.Close()

	// newState is the value just written — for a toggle it was derived from a
	// fresh read above — so it is the resulting state and needs no post-write
	// read-back (unlike Intellinet's fire-and-forget toggle).
	return newState.common(), nil
}

// outletState implements the backend interface for Gude PDUs, reading the
// outlet state from the statusjsn.js endpoint.
func (g gude) outletState(ctx context.Context, outlet int) (state, error) {
	current, err := g.readState(ctx, outlet)
	if err != nil {
		return off, err
	}

	return current.common(), nil
}

// readState fetches the outlet's current state from the status API in Gude's
// own encoding, which setPower needs to compute a toggle.
func (g gude) readState(ctx context.Context, outlet int) (gudeState, error) {
	endpoint := g.base.JoinPath("statusjsn.js")
	endpoint.RawQuery = "components=1"

	resp, err := g.req.get(ctx, endpoint.String())
	if err != nil {
		return gudeOff, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return gudeOff, err
	}

	return g.parseOutlet(body, outlet)
}

// parseOutlet extracts the outlet's state from a statusjsn.js JSON response body.
func (g gude) parseOutlet(body []byte, outlet int) (gudeState, error) {
	var response gudeStatusResponse

	err := json.Unmarshal(body, &response)
	if err != nil {
		return gudeOff, fmt.Errorf("failed to parse Gude status response: %w", err)
	}

	if len(response.Outputs) == 0 {
		return gudeOff, fmt.Errorf("no outputs found in PDU status response")
	}

	if outlet < 0 || outlet >= len(response.Outputs) {
		return gudeOff, fmt.Errorf("outlet %d not found in PDU status (only %d outlets available)", outlet, len(response.Outputs))
	}

	return gudeStateFromInt(response.Outputs[outlet].State)
}

// gudeStatusResponse is the subset of the Gude status endpoint's JSON response
// that this module consumes.
type gudeStatusResponse struct {
	Outputs []struct {
		State int `json:"state"` // 0 = off, 1 = on.
	} `json:"outputs"`
}

// gudeState is Gude's internal encoding of an outlet's power state. The device
// uses this state in two forms across its two endpoints: the integer "state"
// field of the statusjsn.js response (read via gudeStateFromInt) and the "s"
// query parameter of the ov.html switch request (written via switchValue).
// Both use 0 = off and 1 = on, so gudeState's integer value maps to the wire directly.
type gudeState int

const (
	gudeOff gudeState = iota
	gudeOn
)

// switchValue returns the value Gude's switch request expects in its "s"
// parameter ("1" for on, "0" for off).
func (g gudeState) switchValue() string {
	return strconv.Itoa(int(g))
}

// toggled returns the opposite state.
func (g gudeState) toggled() gudeState {
	if g == gudeOn {
		return gudeOff
	}

	return gudeOn
}

// common converts Gude's encoding to the vendor-neutral state.
func (g gudeState) common() state {
	if g == gudeOn {
		return on
	}

	return off
}

// gudeStateFromInt maps Gude's integer "state" field (0 = off, 1 = on) to a gudeState.
func gudeStateFromInt(value int) (gudeState, error) {
	if value != int(gudeOff) && value != int(gudeOn) {
		return gudeOff, fmt.Errorf("invalid Gude outlet state: %d", value)
	}

	return gudeState(value), nil
}

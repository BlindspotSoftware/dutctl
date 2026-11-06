// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

const (
	netioOff    = 0
	netioOn     = 1
	netioToggle = 4
)

type netio struct {
	req  *requester
	base *url.URL
}

func (n netio) setPower(ctx context.Context, outlet int, act action) (state, error) {
	var value int

	expected := off

	switch act {
	case turnOn:
		value = netioOn
		expected = on
	case turnOff:
		value = netioOff
	case toggle:
		value = netioToggle
	default:
		return off, fmt.Errorf("invalid PDU operation: %s", act)
	}

	// NETIO IDs are 1-based; dutctl outlet indexes are 0-based for every vendor.
	command := netioCommand{Outputs: []netioOutputCommand{{ID: outlet + 1, Action: value}}}

	body, err := json.Marshal(command)
	if err != nil {
		return off, fmt.Errorf("failed to encode NETIO command: %w", err)
	}

	resp, err := n.req.post(ctx, n.base.JoinPath("netio.json").String(), bytes.NewReader(body))
	if err != nil {
		return off, err
	}
	defer resp.Body.Close()

	current, err := n.parseOutlet(resp.Body, outlet)
	if err != nil {
		return off, err
	}

	if act != toggle && current != expected {
		return off, fmt.Errorf("NETIO outlet %d reports %s after %s", outlet, current, act)
	}

	return current, nil
}

func (n netio) outletState(ctx context.Context, outlet int) (state, error) {
	resp, err := n.req.get(ctx, n.base.JoinPath("netio.json").String())
	if err != nil {
		return off, err
	}
	defer resp.Body.Close()

	return n.parseOutlet(resp.Body, outlet)
}

func (n netio) parseOutlet(body io.Reader, outlet int) (state, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return off, fmt.Errorf("failed to read NETIO status response: %w", err)
	}

	var response netioStatusResponse

	err = json.Unmarshal(data, &response)
	if err != nil {
		return off, fmt.Errorf("failed to parse NETIO status response: %w", err)
	}

	for _, output := range response.Outputs {
		if output.ID != outlet+1 {
			continue
		}

		if output.State == nil {
			return off, fmt.Errorf("missing state for NETIO output ID %d", output.ID)
		}

		switch *output.State {
		case netioOff:
			return off, nil
		case netioOn:
			return on, nil
		default:
			return off, fmt.Errorf("invalid NETIO state %d for output ID %d", *output.State, output.ID)
		}
	}

	return off, fmt.Errorf("NETIO output ID %d not found in status response", outlet+1)
}

//nolint:tagliatelle // NETIO's JSON API requires capitalized field names.
type netioCommand struct {
	Outputs []netioOutputCommand `json:"Outputs"`
}

//nolint:tagliatelle // NETIO's JSON API requires capitalized field names.
type netioOutputCommand struct {
	ID     int `json:"ID"`
	Action int `json:"Action"`
}

//nolint:tagliatelle // NETIO's JSON API requires capitalized field names.
type netioStatusResponse struct {
	Outputs []struct {
		ID    int  `json:"ID"`
		State *int `json:"State"`
	} `json:"Outputs"`
}

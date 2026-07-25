// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// intellinet drives Intellinet-style PDUs (also the LogiLink PDU8P01 and
// compatibles) via their control_outlet.htm and status.xml HTTP endpoints.
type intellinet struct {
	req  *requester
	base *url.URL
}

// setPower implements the backend interface for Intellinet-style PDUs, using the
// native on/off/toggle "op" parameter of the control_outlet.htm endpoint.
func (i intellinet) setPower(ctx context.Context, outlet int, act action) (state, error) {
	op, ok := intellinetOpFor(act)
	if !ok {
		return off, fmt.Errorf("invalid PDU operation: %s", act)
	}

	// control_outlet.htm selects the target outlet with "outlet<N>=1" and the
	// action with "op".
	outletParam := fmt.Sprintf("outlet%d", outlet)

	endpoint := i.base.JoinPath("control_outlet.htm")
	endpoint.RawQuery = url.Values{
		outletParam: {"1"},
		"op":        {string(op)},
	}.Encode()

	resp, err := i.req.get(ctx, endpoint.String())
	if err != nil {
		return off, err
	}

	resp.Body.Close()

	// The control endpoint does not echo the resulting state: for on/off it is
	// the action's state, but a toggle must be read back.
	if act == toggle {
		result, err := i.outletState(ctx, outlet)
		if err != nil {
			return off, fmt.Errorf("toggled outlet %d but could not read back the resulting state: %w", outlet, err)
		}

		return result, nil
	}

	if act == turnOn {
		return on, nil
	}

	return off, nil
}

// outletState implements the backend interface for Intellinet-style PDUs,
// parsing the outlet state from the status.xml endpoint.
func (i intellinet) outletState(ctx context.Context, outlet int) (state, error) {
	endpoint := i.base.JoinPath("status.xml")

	resp, err := i.req.get(ctx, endpoint.String())
	if err != nil {
		return off, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return off, err
	}

	return i.parseOutlet(body, outlet)
}

// parseOutlet extracts the outlet's state from a status.xml response body. The
// Intellinet status reports the state as the same "on"/"off" words the common
// state uses, so no vendor-specific state type is needed.
func (i intellinet) parseOutlet(body []byte, outlet int) (state, error) {
	bodyStr := string(body)

	outletTag := fmt.Sprintf("<outletStat%d>", outlet)
	outletEndTag := fmt.Sprintf("</outletStat%d>", outlet)

	startIdx := strings.Index(bodyStr, outletTag)
	if startIdx == -1 {
		return off, fmt.Errorf("outlet %d not found in PDU status", outlet)
	}

	startIdx += len(outletTag)

	endIdx := strings.Index(bodyStr[startIdx:], outletEndTag)
	if endIdx == -1 {
		return off, fmt.Errorf("malformed XML for outlet %d", outlet)
	}

	value := strings.TrimSpace(bodyStr[startIdx : startIdx+endIdx])

	result, ok := parseState(value)
	if !ok {
		return off, fmt.Errorf("unexpected outlet state %q for outlet %d", value, outlet)
	}

	return result, nil
}

// intellinetOp is the value the Intellinet control_outlet.htm endpoint expects in
// its "op" query parameter. The encoding is action-based rather than a boolean
// state, and notably inverts what one might expect: 0 = switch on, 1 = switch
// off, 2 = toggle.
type intellinetOp string

const (
	opOn     intellinetOp = "0"
	opOff    intellinetOp = "1"
	opToggle intellinetOp = "2"
)

// intellinetOpFor maps a power action to its Intellinet op value; ok is false for an
// unknown action.
func intellinetOpFor(act action) (intellinetOp, bool) {
	switch act {
	case turnOn:
		return opOn, true
	case turnOff:
		return opOff, true
	case toggle:
		return opToggle, true
	default:
		return "", false
	}
}

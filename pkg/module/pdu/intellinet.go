// Copyright 2026 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdu provides a dutagent module that allows power control of a PDU via HTTP requests.
package pdu

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

type intellinet struct{}

func (i intellinet) init(p *PDU) error {
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

	return nil
}

func (i intellinet) setPower(ctx context.Context, s module.Session, p *PDU, state string) error {
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

	p.printPowerSet(s, state)

	return nil
}

func (i intellinet) fetchState(ctx context.Context, s module.Session, p *PDU) error {
	resp, err := p.doRequest(ctx, p.statusURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	outletValue, err := i.parseOutletStatus(p, body)
	if err != nil {
		return err
	}

	p.printState(s, outletValue)

	return nil
}

// parseOutletStatus extracts the outlet status from XML response body.
func (i intellinet) parseOutletStatus(p *PDU, body []byte) (string, error) {
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

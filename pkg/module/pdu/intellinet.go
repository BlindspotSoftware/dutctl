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

type intellinet struct {
	pdu *PDU
}

func (i *intellinet) init() error {
	p := i.pdu

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

func (i *intellinet) setPower(ctx context.Context, s module.Session, state string) error {
	p := i.pdu

	opState, err := parseOp(state)
	if err != nil {
		return err
	}

	q := p.controlURL.Query()
	q.Set(fmt.Sprintf("outlet%d", p.Outlet), "1")
	q.Set("op", opState.String())
	p.controlURL.RawQuery = q.Encode()

	resp, err := doRequest(p, ctx, p.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	s.Printf("PDU outlet%d power set to '%s' successfully\n", p.Outlet, state)

	return nil
}

func (i *intellinet) getState(ctx context.Context, s module.Session) error {
	p := i.pdu

	resp, err := doRequest(p, ctx, p.statusURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	outletValue, err := i.parseOutletStatus(body)
	if err != nil {
		return err
	}

	s.Printf("PDU outlet%d state: %s\n", p.Outlet, outletValue)

	return nil
}

// parseOutletStatus extracts the outlet status from XML response body.
func (i *intellinet) parseOutletStatus(body []byte) (string, error) {
	p := i.pdu

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

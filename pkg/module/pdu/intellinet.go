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

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

type intellinet struct{}

func (i intellinet) init(pdu *PDU) error {
	controlURL, err := url.Parse(strings.TrimRight(pdu.Host, "/") + "/control_outlet.htm")
	if err != nil {
		return err
	}

	pdu.controlURL = controlURL

	statusURL, err := url.Parse(strings.TrimRight(pdu.Host, "/") + "/status.xml")
	if err != nil {
		return err
	}

	pdu.statusURL = statusURL

	return nil
}

func (i intellinet) setPower(ctx context.Context, s module.Session, pdu *PDU, state string) error {
	opState, err := parseOp(state)
	if err != nil {
		return err
	}

	q := pdu.controlURL.Query()
	q.Set(fmt.Sprintf("outlet%d", pdu.Outlet), "1")
	q.Set("op", opState.String())
	pdu.controlURL.RawQuery = q.Encode()

	resp, err := pdu.doRequest(ctx, pdu.controlURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	pdu.printPowerSet(s, state)

	return nil
}

func (i intellinet) fetchState(ctx context.Context, s module.Session, pdu *PDU) error {
	resp, err := pdu.doRequest(ctx, pdu.statusURL.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	outletValue, err := i.parseOutletStatus(pdu, body)
	if err != nil {
		return err
	}

	pdu.printState(s, outletValue)

	return nil
}

// parseOutletStatus extracts the outlet status from XML response body.
func (i intellinet) parseOutletStatus(pdu *PDU, body []byte) (string, error) {
	bodyStr := string(body)

	outletTag := fmt.Sprintf("<outletStat%d>", pdu.Outlet)
	outletEndTag := fmt.Sprintf("</outletStat%d>", pdu.Outlet)

	startIdx := strings.Index(bodyStr, outletTag)
	if startIdx == -1 {
		return "", fmt.Errorf("outlet %d not found in PDU status", pdu.Outlet)
	}

	startIdx += len(outletTag)

	endIdx := strings.Index(bodyStr[startIdx:], outletEndTag)
	if endIdx == -1 {
		return "", fmt.Errorf("malformed XML for outlet %d", pdu.Outlet)
	}

	outletValue := strings.TrimSpace(bodyStr[startIdx : startIdx+endIdx])

	if outletValue != on && outletValue != off {
		return "", fmt.Errorf("unexpected outlet state '%s' for outlet %d", outletValue, pdu.Outlet)
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

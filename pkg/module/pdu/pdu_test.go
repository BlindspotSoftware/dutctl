// Copyright 2024 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"testing"
)

func TestParseOutletStatus(t *testing.T) {
	tests := []struct {
		name     string
		outlet   int
		xmlBody  string
		expected string
		err      bool
	}{
		{
			name:   "outlet 0 on",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			expected: "on",
			err:      false,
		},
		{
			name:   "outlet 1 off",
			outlet: 1,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			expected: "off",
			err:      false,
		},
		{
			name:   "outlet 6 on with whitespace",
			outlet: 6,
			xmlBody: `<response>
<outletStat6>  on  </outletStat6>
</response>`,
			expected: "on",
			err:      false,
		},
		{
			name:   "outlet not found",
			outlet: 5,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			expected: "",
			err:      true,
		},
		{
			name:   "malformed XML - missing end tag",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>on
</response>`,
			expected: "",
			err:      true,
		},
		{
			name:   "unexpected outlet state",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>unknown</outletStat0>
</response>`,
			expected: "",
			err:      true,
		},
		{
			name:   "real PDU response example",
			outlet: 6,
			xmlBody: `<response>
<cur0>0.2</cur0>
<stat0>normal</stat0>
<curBan>0.2</curBan>
<tempBan>30</tempBan>
<humBan>31</humBan>
<statBan>normal</statBan>
<outletStat0>on</outletStat0>
<outletStat1>on</outletStat1>
<outletStat2>on</outletStat2>
<outletStat3>on</outletStat3>
<outletStat4>on</outletStat4>
<outletStat5>on</outletStat5>
<outletStat6>on</outletStat6>
<outletStat7>off</outletStat7>
<userVerifyRes>0</userVerifyRes>
</response>`,
			expected: "on",
			err:      false,
		},
		{
			name:     "empty XML",
			outlet:   0,
			xmlBody:  "",
			expected: "",
			err:      true,
		},
		{
			name:   "outlet number out of range",
			outlet: 99,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
</response>`,
			expected: "",
			err:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PDU{
				Outlet: tt.outlet,
			}

			result, err := p.parseOutletStatus([]byte(tt.xmlBody))

			if tt.err {
				if err == nil {
					t.Errorf("parseOutletStatus() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("parseOutletStatus() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("parseOutletStatus() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestParseOp(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected op
		err      bool
	}{
		{
			name:     "on command",
			input:    "on",
			expected: opOn,
			err:      false,
		},
		{
			name:     "off command",
			input:    "off",
			expected: opOff,
			err:      false,
		},
		{
			name:     "toggle command",
			input:    "toggle",
			expected: opToggle,
			err:      false,
		},
		{
			name:     "invalid command",
			input:    "invalid",
			expected: "",
			err:      true,
		},
		{
			name:     "empty command",
			input:    "",
			expected: "",
			err:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseOp(tt.input)

			if tt.err {
				if err == nil {
					t.Errorf("parseOp() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("parseOp() unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("parseOp() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestOpString(t *testing.T) {
	tests := []struct {
		name     string
		op       op
		expected string
	}{
		{
			name:     "opOn",
			op:       opOn,
			expected: "0",
		},
		{
			name:     "opOff",
			op:       opOff,
			expected: "1",
		},
		{
			name:     "opToggle",
			op:       opToggle,
			expected: "2",
		},
		{
			name:     "invalid op",
			op:       op("invalid"),
			expected: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.op.String()
			if result != tt.expected {
				t.Errorf("op.String() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

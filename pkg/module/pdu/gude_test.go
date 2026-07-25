// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// newTestGude builds a gude backend pointed at srv, sharing its client so the
// tests exercise the real request-building path.
func newTestGude(t *testing.T, srv *httptest.Server) gude {
	t.Helper()

	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", srv.URL, err)
	}

	return gude{req: &requester{client: srv.Client()}, base: base}
}

func TestGudeStateSwitchValue(t *testing.T) {
	if got := gudeOff.switchValue(); got != "0" {
		t.Errorf("gudeOff.switchValue() = %q, want %q", got, "0")
	}

	if got := gudeOn.switchValue(); got != "1" {
		t.Errorf("gudeOn.switchValue() = %q, want %q", got, "1")
	}
}

func TestGudeStateToggled(t *testing.T) {
	if got := gudeOn.toggled(); got != gudeOff {
		t.Errorf("gudeOn.toggled() = %v, want gudeOff", got)
	}

	if got := gudeOff.toggled(); got != gudeOn {
		t.Errorf("gudeOff.toggled() = %v, want gudeOn", got)
	}
}

func TestGudeStateCommon(t *testing.T) {
	if got := gudeOn.common(); got != on {
		t.Errorf("gudeOn.common() = %v, want on", got)
	}

	if got := gudeOff.common(); got != off {
		t.Errorf("gudeOff.common() = %v, want off", got)
	}
}

func TestGudeStateFromInt(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    gudeState
		wantErr bool
	}{
		{name: "0 is off", input: 0, want: gudeOff},
		{name: "1 is on", input: 1, want: gudeOn},
		{name: "2 is invalid", input: 2, wantErr: true},
		{name: "negative is invalid", input: -1, wantErr: true},
		{name: "large is invalid", input: 999, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gudeStateFromInt(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("gudeStateFromInt() expected error but got none")
				}

				return
			}

			if err != nil {
				t.Errorf("gudeStateFromInt() unexpected error: %v", err)

				return
			}

			if got != tt.want {
				t.Errorf("gudeStateFromInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGudeParseOutlet(t *testing.T) {
	tests := []struct {
		name     string
		outlet   int
		jsonBody string
		want     gudeState
		wantErr  bool
	}{
		{
			name:   "outlet 0 off",
			outlet: 0,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0},
					{"name": "Power Port", "state": 1}
				]
			}`,
			want: gudeOff,
		},
		{
			name:   "outlet 1 on",
			outlet: 1,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0},
					{"name": "Power Port", "state": 1}
				]
			}`,
			want: gudeOn,
		},
		{
			name:   "real PDU response - 4 outlets",
			outlet: 2,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0, "sw_cnt": 8, "type": 1, "batch": [0,0], "wdog": [0,3,null,32]},
					{"name": "Power Port", "state": 0},
					{"name": "Power Port", "state": 1},
					{"name": "Power Port", "state": 0}
				]
			}`,
			want: gudeOn,
		},
		{
			name:   "outlet not found - out of range",
			outlet: 5,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 0},
					{"name": "Power Port", "state": 1}
				]
			}`,
			wantErr: true,
		},
		{
			name:     "malformed JSON - missing closing brace",
			outlet:   0,
			jsonBody: `{"outputs": [{"name": "Power Port", "state": 0}`,
			wantErr:  true,
		},
		{
			name:     "empty outputs array",
			outlet:   0,
			jsonBody: `{"outputs": []}`,
			wantErr:  true,
		},
		{
			name:     "empty JSON",
			outlet:   0,
			jsonBody: ``,
			wantErr:  true,
		},
		{
			name:     "invalid JSON - not an object",
			outlet:   0,
			jsonBody: `null`,
			wantErr:  true,
		},
		{
			name:   "missing outputs field",
			outlet: 0,
			jsonBody: `{
				"other_field": "value"
			}`,
			wantErr: true,
		},
		{
			name:   "unexpected state value returns error",
			outlet: 0,
			jsonBody: `{
				"outputs": [
					{"name": "Power Port", "state": 7}
				]
			}`,
			wantErr: true,
		},
		{
			name:   "outlet at exact boundary - last outlet",
			outlet: 3,
			jsonBody: `{
				"outputs": [
					{"name": "Port 1", "state": 0},
					{"name": "Port 2", "state": 1},
					{"name": "Port 3", "state": 0},
					{"name": "Port 4", "state": 1}
				]
			}`,
			want: gudeOn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gude{}.parseOutlet([]byte(tt.jsonBody), tt.outlet)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseOutlet() expected error but got none")
				}

				return
			}

			if err != nil {
				t.Errorf("parseOutlet() unexpected error: %v", err)

				return
			}

			if got != tt.want {
				t.Errorf("parseOutlet() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGudeOutletState pins Gude's status contract: the state is read from
// GET /statusjsn.js?components=1 and taken from the outlet's "state" field.
func TestGudeOutletState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/statusjsn.js" {
			t.Errorf("request path = %q, want /statusjsn.js", r.URL.Path)
		}

		if got := r.URL.Query().Get("components"); got != "1" {
			t.Errorf("components query = %q, want %q", got, "1")
		}

		fmt.Fprint(w, `{"outputs":[{"state":0},{"state":1}]}`)
	}))
	defer srv.Close()

	got, err := newTestGude(t, srv).outletState(context.Background(), 1)
	if err != nil {
		t.Fatalf("outletState() unexpected error: %v", err)
	}

	if got != on {
		t.Errorf("outletState() = %v, want on", got)
	}
}

// TestGudeSetPowerRequests pins Gude's switch contract on GET /ov.html: cmd=1,
// the 1-based port index p=outlet+1, and s=1/0 for on/off. Gude has no native
// toggle, so a toggle first reads /statusjsn.js and then writes the opposite s.
func TestGudeSetPowerRequests(t *testing.T) {
	const outlet = 2 // Gude's switch API is 1-based, so requests must carry p=3.

	tests := []struct {
		name      string
		act       action
		current   gudeState // state /statusjsn.js reports; only a toggle reads it back
		wantS     string    // expected "s" switch parameter
		wantState state     // expected resulting state
	}{
		{name: "on", act: turnOn, wantS: "1", wantState: on},
		{name: "off", act: turnOff, wantS: "0", wantState: off},
		{name: "toggle from on switches off", act: toggle, current: gudeOn, wantS: "0", wantState: off},
		{name: "toggle from off switches on", act: toggle, current: gudeOff, wantS: "1", wantState: on},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				switchQuery url.Values
				statusHits  int
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/statusjsn.js":
					statusHits++

					fmt.Fprintf(w, `{"outputs":[{"state":0},{"state":0},{"state":%d}]}`, tt.current)
				case "/ov.html":
					switchQuery = r.URL.Query()

					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request path %q", r.URL.Path)
				}
			}))
			defer srv.Close()

			got, err := newTestGude(t, srv).setPower(context.Background(), outlet, tt.act)
			if err != nil {
				t.Fatalf("setPower() unexpected error: %v", err)
			}

			if got != tt.wantState {
				t.Errorf("setPower() = %v, want %v", got, tt.wantState)
			}

			if switchQuery.Get("cmd") != gudeCmdSwitch {
				t.Errorf("ov.html cmd = %q, want %q", switchQuery.Get("cmd"), gudeCmdSwitch)
			}

			if switchQuery.Get("p") != "3" {
				t.Errorf("ov.html p = %q, want %q (1-based index of outlet %d)", switchQuery.Get("p"), "3", outlet)
			}

			if switchQuery.Get("s") != tt.wantS {
				t.Errorf("ov.html s = %q, want %q", switchQuery.Get("s"), tt.wantS)
			}

			wantStatusHits := 0
			if tt.act == toggle {
				wantStatusHits = 1 // a toggle must read the current state first
			}

			if statusHits != wantStatusHits {
				t.Errorf("statusjsn.js hits = %d, want %d", statusHits, wantStatusHits)
			}
		})
	}
}

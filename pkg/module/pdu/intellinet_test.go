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

// newTestIntellinet builds an intellinet backend pointed at srv, sharing its
// client so the tests exercise the real request-building path.
func newTestIntellinet(t *testing.T, srv *httptest.Server) intellinet {
	t.Helper()

	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", srv.URL, err)
	}

	return intellinet{req: &requester{client: srv.Client()}, base: base}
}

func TestIntellinetParseOutlet(t *testing.T) {
	tests := []struct {
		name    string
		outlet  int
		xmlBody string
		want    state
		wantErr bool
	}{
		{
			name:   "outlet 0 on",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			want: on,
		},
		{
			name:   "outlet 1 off",
			outlet: 1,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			want: off,
		},
		{
			name:   "outlet 6 on with whitespace",
			outlet: 6,
			xmlBody: `<response>
<outletStat6>  on  </outletStat6>
</response>`,
			want: on,
		},
		{
			name:   "outlet not found",
			outlet: 5,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
<outletStat1>off</outletStat1>
</response>`,
			wantErr: true,
		},
		{
			name:   "malformed XML - missing end tag",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>on
</response>`,
			wantErr: true,
		},
		{
			name:   "unexpected outlet state",
			outlet: 0,
			xmlBody: `<response>
<outletStat0>unknown</outletStat0>
</response>`,
			wantErr: true,
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
			want: on,
		},
		{
			name:    "empty XML",
			outlet:  0,
			xmlBody: "",
			wantErr: true,
		},
		{
			name:   "outlet number out of range",
			outlet: 99,
			xmlBody: `<response>
<outletStat0>on</outletStat0>
</response>`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := intellinet{}.parseOutlet([]byte(tt.xmlBody), tt.outlet)

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

func TestIntellinetOpFor(t *testing.T) {
	tests := []struct {
		name string
		a    action
		want intellinetOp
		ok   bool
	}{
		{name: "turn on", a: turnOn, want: opOn, ok: true},
		{name: "turn off", a: turnOff, want: opOff, ok: true},
		{name: "toggle", a: toggle, want: opToggle, ok: true},
		{name: "unknown action", a: action(99), want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := intellinetOpFor(tt.a)
			if ok != tt.ok {
				t.Fatalf("intellinetOpFor(%v) ok = %v, want %v", tt.a, ok, tt.ok)
			}

			if got != tt.want {
				t.Errorf("intellinetOpFor(%v) = %q, want %q", tt.a, got, tt.want)
			}
		})
	}
}

// TestIntellinetOutletState pins Intellinet's status contract: the state is
// read from GET /status.xml and parsed from the outlet's <outletStatN> element.
func TestIntellinetOutletState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status.xml" {
			t.Errorf("request path = %q, want /status.xml", r.URL.Path)
		}

		fmt.Fprint(w, `<response><outletStat0>on</outletStat0><outletStat1>off</outletStat1></response>`)
	}))
	defer srv.Close()

	got, err := newTestIntellinet(t, srv).outletState(context.Background(), 1)
	if err != nil {
		t.Fatalf("outletState() unexpected error: %v", err)
	}

	if got != off {
		t.Errorf("outletState() = %v, want off", got)
	}
}

// TestIntellinetSetPowerRequests pins Intellinet's switch contract on
// GET /control_outlet.htm: the outlet is selected with outlet<N>=1 and the
// action with the inverted op code (0=on, 1=off, 2=toggle). The endpoint does
// not echo the result, so a toggle is read back from /status.xml.
func TestIntellinetSetPowerRequests(t *testing.T) {
	const outlet = 6 // control_outlet.htm selects the outlet with "outlet6=1".

	tests := []struct {
		name      string
		act       action
		wantOp    string
		readBack  state // state /status.xml reports after a toggle read-back
		wantState state
	}{
		{name: "on", act: turnOn, wantOp: "0", wantState: on},
		{name: "off", act: turnOff, wantOp: "1", wantState: off},
		{name: "toggle reads the resulting state back", act: toggle, wantOp: "2", readBack: on, wantState: on},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				controlQuery url.Values
				statusHits   int
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/control_outlet.htm":
					controlQuery = r.URL.Query()

					w.WriteHeader(http.StatusOK)
				case "/status.xml":
					statusHits++

					fmt.Fprintf(w, `<response><outletStat6>%s</outletStat6></response>`, tt.readBack)
				default:
					t.Errorf("unexpected request path %q", r.URL.Path)
				}
			}))
			defer srv.Close()

			got, err := newTestIntellinet(t, srv).setPower(context.Background(), outlet, tt.act)
			if err != nil {
				t.Fatalf("setPower() unexpected error: %v", err)
			}

			if got != tt.wantState {
				t.Errorf("setPower() = %v, want %v", got, tt.wantState)
			}

			if controlQuery.Get("outlet6") != "1" {
				t.Errorf("control_outlet.htm outlet6 = %q, want %q", controlQuery.Get("outlet6"), "1")
			}

			if controlQuery.Get("op") != tt.wantOp {
				t.Errorf("control_outlet.htm op = %q, want %q", controlQuery.Get("op"), tt.wantOp)
			}

			wantStatusHits := 0
			if tt.act == toggle {
				wantStatusHits = 1 // only a toggle reads the state back
			}

			if statusHits != wantStatusHits {
				t.Errorf("status.xml hits = %d, want %d", statusHits, wantStatusHits)
			}
		})
	}
}

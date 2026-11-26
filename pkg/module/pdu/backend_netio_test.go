// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newTestNetio(t *testing.T, host string, outlet int) netio {
	t.Helper()

	p := PDU{Vendor: "netio", Host: host, Outlet: outlet, User: "test-user", Password: "test-password"}
	if err := p.Init(t.Context()); err != nil {
		t.Fatal(err)
	}

	n, ok := p.backend.(netio)
	if !ok {
		t.Fatalf("backend = %T, want netio", p.backend)
	}

	return n
}

func TestNetioSetPower(t *testing.T) {
	tests := []struct {
		name       string
		act        action
		wantAction float64
		state      int
		want       state
	}{
		{name: "on", act: turnOn, wantAction: 1, state: 1, want: on},
		{name: "off", act: turnOff, wantAction: 0, state: 0, want: off},
		{name: "toggle to off", act: toggle, wantAction: 4, state: 0, want: off},
		{name: "toggle to on", act: toggle, wantAction: 4, state: 1, want: on},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var methods []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				if r.URL.Path != "/api/netio.json" {
					t.Errorf("path = %q, want /api/netio.json", r.URL.Path)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				if user, password, ok := r.BasicAuth(); !ok || user != "test-user" || password != "test-password" {
					t.Error("missing or incorrect Basic Auth")
				}

				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				wantBody := map[string]any{
					"Outputs": []any{map[string]any{"ID": float64(3), "Action": tt.wantAction}},
				}
				if !reflect.DeepEqual(body, wantBody) {
					t.Errorf("POST body = %#v, want %#v", body, wantBody)
				}

				fmt.Fprintf(w, `{"Outputs":[{"ID":3,"State":%d,"Action":6},{"ID":1,"State":1}]}`, tt.state)
			}))
			defer srv.Close()

			n := newTestNetio(t, srv.URL+"/api", 2)
			got, err := n.setPower(t.Context(), 2, tt.act)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("state = %s, want %s", got, tt.want)
			}
			if !reflect.DeepEqual(methods, []string{http.MethodPost}) {
				t.Errorf("methods = %v, want one POST without a read-modify-write toggle", methods)
			}
		})
	}
}

func TestNetioOutletState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/netio.json" {
			t.Errorf("request = %s %s, want GET /netio.json", r.Method, r.URL.Path)
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "test-user" || password != "test-password" {
			t.Error("missing or incorrect Basic Auth")
		}
		fmt.Fprint(w, `{"Agent":{"NumOutputs":3},"Outputs":[{"ID":3,"State":0},{"ID":1,"State":1,"Action":6}]}`)
	}))
	defer srv.Close()

	got, err := newTestNetio(t, srv.URL, 0).outletState(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != on {
		t.Errorf("state = %s, want on for output ID 1", got)
	}
}

func TestNetioInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"Outputs":`},
		{name: "trailing garbage", body: `{"Outputs":[{"ID":1,"State":1}]}invalid`},
		{name: "second JSON value", body: `{"Outputs":[{"ID":1,"State":1}]}{}`},
		{name: "empty response"},
		{name: "null response", body: `null`},
		{name: "missing outputs", body: `{}`},
		{name: "empty outputs", body: `{"Outputs":[]}`},
		{name: "wrong output ID", body: `{"Outputs":[{"ID":2,"State":1}]}`},
		{name: "missing ID", body: `{"Outputs":[{"State":1}]}`},
		{name: "missing state", body: `{"Outputs":[{"ID":1,"Action":6}]}`},
		{name: "null state", body: `{"Outputs":[{"ID":1,"State":null}]}`},
		{name: "invalid state", body: `{"Outputs":[{"ID":1,"State":6}]}`},
		{name: "negative state", body: `{"Outputs":[{"ID":1,"State":-1}]}`},
		{name: "string state", body: `{"Outputs":[{"ID":1,"State":"on"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			n := newTestNetio(t, srv.URL, 0)
			if _, err := n.outletState(t.Context(), 0); err == nil {
				t.Error("status accepted an invalid response")
			}
			if _, err := n.setPower(t.Context(), 0, turnOn); err == nil {
				t.Error("power command accepted an invalid response")
			}
		})
	}
}

func TestNetioPowerStateMismatch(t *testing.T) {
	for _, act := range []action{turnOn, turnOff} {
		t.Run(act.String(), func(t *testing.T) {
			returned := 0
			if act == turnOff {
				returned = 1
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"Outputs":[{"ID":1,"State":%d}]}`, returned)
			}))
			defer srv.Close()

			_, err := newTestNetio(t, srv.URL, 0).setPower(t.Context(), 0, act)
			if err == nil || !strings.Contains(err.Error(), "reports") {
				t.Errorf("state mismatch error = %v", err)
			}
		})
	}
}

func TestNetioHTTPError(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "NETIO test error", code)
			}))
			defer srv.Close()

			n := newTestNetio(t, srv.URL, 0)
			for _, act := range []action{turnOn, toggle} {
				if _, err := n.setPower(t.Context(), 0, act); err == nil || !strings.Contains(err.Error(), http.StatusText(code)) {
					t.Errorf("%s HTTP error = %v", act, err)
				}
			}
			if _, err := n.outletState(t.Context(), 0); err == nil || !strings.Contains(err.Error(), http.StatusText(code)) {
				t.Errorf("status HTTP error = %v", err)
			}
		})
	}
}

func TestNetioCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("cancelled request reached the server")
	}))
	defer srv.Close()

	n := newTestNetio(t, srv.URL, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := n.outletState(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("status error = %v, want cancellation", err)
	}
	if _, err := n.setPower(ctx, 0, turnOn); !errors.Is(err, context.Canceled) {
		t.Errorf("power command error = %v, want cancellation", err)
	}
}

func TestNetioInvalidAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("invalid action reached the server")
	}))
	defer srv.Close()

	if _, err := newTestNetio(t, srv.URL, 0).setPower(t.Context(), 0, action(-1)); err == nil {
		t.Error("invalid action accepted")
	}
}

func TestNetioReadError(t *testing.T) {
	if _, err := (netio{}).parseOutlet(io.MultiReader(strings.NewReader(`{"Outputs":`), failingNetioReader{}), 0); err == nil {
		t.Error("response read error ignored")
	}
}

type failingNetioReader struct{}

func (failingNetioReader) Read([]byte) (int, error) {
	return 0, errors.New("NETIO test read error")
}

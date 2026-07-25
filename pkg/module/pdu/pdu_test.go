// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBackendSelection exercises the config decode + Init path that selects the
// vendor backend, mirroring how pkg/dut unmarshals a module's "with" options.
func TestBackendSelection(t *testing.T) {
	tests := []struct {
		name    string
		options string
		want    backend
		initErr bool
	}{
		{
			name:    "explicit intellinet",
			options: "vendor: intellinet\nhost: http://10.0.0.1\noutlet: 0\n",
			want:    intellinet{},
		},
		{
			name:    "gude",
			options: "vendor: gude\nhost: http://10.0.0.1\noutlet: 0\n",
			want:    gude{},
		},
		{
			name:    "legacy config without vendor defaults to intellinet",
			options: "host: http://10.0.0.1\noutlet: 0\n",
			want:    intellinet{},
		},
		{
			name:    "unknown vendor is rejected at Init",
			options: "vendor: acme\nhost: http://10.0.0.1\n",
			initErr: true,
		},
		{
			name:    "user and password together are accepted",
			options: "vendor: gude\nhost: http://10.0.0.1\nuser: admin\npassword: secret\n",
			want:    gude{},
		},
		{
			name:    "user without password is rejected",
			options: "host: http://10.0.0.1\nuser: admin\n",
			initErr: true,
		},
		{
			name:    "password without user is rejected",
			options: "host: http://10.0.0.1\npassword: secret\n",
			initErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p PDU
			if err := yaml.Unmarshal([]byte(tt.options), &p); err != nil {
				t.Fatalf("yaml.Unmarshal() unexpected error: %v", err)
			}

			err := p.Init(context.Background())

			if tt.initErr {
				if err == nil {
					t.Fatalf("Init() expected error but got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("Init() unexpected error: %v", err)
			}

			if gotType, wantType := typeName(p.backend), typeName(tt.want); gotType != wantType {
				t.Errorf("backend = %s, want %s", gotType, wantType)
			}
		})
	}
}

func typeName(b backend) string {
	switch b.(type) {
	case intellinet:
		return "intellinet"
	case gude:
		return "gude"
	default:
		return "nil"
	}
}

func TestParseAction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  action
		ok    bool
	}{
		{name: "on", input: "on", want: turnOn, ok: true},
		{name: "off", input: "off", want: turnOff, ok: true},
		{name: "toggle", input: "toggle", want: toggle, ok: true},
		{name: "status is not an action", input: "status", ok: false},
		{name: "unknown", input: "bogus", ok: false},
		{name: "empty", input: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAction(tt.input)
			if ok != tt.ok {
				t.Fatalf("parseAction(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}

			if ok && got != tt.want {
				t.Errorf("parseAction(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseState(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  state
		ok    bool
	}{
		{name: "on", input: "on", want: on, ok: true},
		{name: "off", input: "off", want: off, ok: true},
		{name: "toggle is not a state", input: "toggle", ok: false},
		{name: "unknown", input: "bogus", ok: false},
		{name: "empty", input: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseState(tt.input)
			if ok != tt.ok {
				t.Fatalf("parseState(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}

			if ok && got != tt.want {
				t.Errorf("parseState(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	if got := on.String(); got != "on" {
		t.Errorf("on.String() = %q, want %q", got, "on")
	}

	if got := off.String(); got != "off" {
		t.Errorf("off.String() = %q, want %q", got, "off")
	}
}

func TestActionString(t *testing.T) {
	tests := []struct {
		a    action
		want string
	}{
		{a: turnOn, want: "on"},
		{a: turnOff, want: "off"},
		{a: toggle, want: "toggle"},
	}

	for _, tt := range tests {
		if got := tt.a.String(); got != tt.want {
			t.Errorf("action.String() = %q, want %q", got, tt.want)
		}
	}
}

// TestRequesterGet documents the transport contract every backend relies on:
// HTTP Basic Auth is sent only when both credentials are configured, and any
// non-200 response is turned into an error with no response handed back.
func TestRequesterGet(t *testing.T) {
	t.Run("sends basic auth when credentials are set", func(t *testing.T) {
		var (
			gotUser, gotPass string
			gotOK            bool
		)

		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, gotOK = r.BasicAuth()
		}))
		defer srv.Close()

		req := &requester{client: srv.Client(), user: "admin", password: "secret"}

		resp, err := req.get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("get() unexpected error: %v", err)
		}
		resp.Body.Close()

		if !gotOK || gotUser != "admin" || gotPass != "secret" {
			t.Errorf("basic auth = (%q, %q, ok=%v), want (admin, secret, ok=true)", gotUser, gotPass, gotOK)
		}
	})

	t.Run("omits basic auth when credentials are empty", func(t *testing.T) {
		var gotOK bool

		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, _, gotOK = r.BasicAuth()
		}))
		defer srv.Close()

		req := &requester{client: srv.Client()}

		resp, err := req.get(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("get() unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotOK {
			t.Errorf("basic auth was sent, want none")
		}
	})

	t.Run("non-200 status is an error and returns no response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer srv.Close()

		req := &requester{client: srv.Client()}

		resp, err := req.get(context.Background(), srv.URL)
		if err == nil {
			resp.Body.Close()
			t.Fatal("get() expected error for non-200 status, got nil")
		}

		if resp != nil {
			t.Errorf("get() returned a response alongside the error, want nil")
		}
	})
}

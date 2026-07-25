// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdu provides a dutagent module that allows power control of a PDU via HTTP requests.
package pdu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "pdu",
		New: func() module.Module {
			return &PDU{}
		},
	})
}

// Supported PDU vendors, selected via the "vendor" configuration option. A
// value names the device's HTTP API family rather than a single product:
// "intellinet" also covers compatible units such as the LogiLink PDU8P01.
const (
	vendorIntellinet = "intellinet"
	vendorGude       = "gude"
)

const defaultTimeout = 10 * time.Second // Default timeout for HTTP requests.

// status is the query command that reports the current power state. Unlike the
// power actions it does not change state, so it is dispatched on its own.
const status = "status"

// state is an outlet's power state. It is the common, vendor-neutral type that
// crosses the backend interface; each backend converts it to and from its own
// wire encoding.
type state int

const (
	off state = iota
	on
)

func (s state) String() string {
	switch s {
	case on:
		return "on"
	case off:
		return "off"
	default:
		return ""
	}
}

// parseState converts a device-reported power word ("on"/"off") to a state.
func parseState(word string) (state, bool) {
	switch word {
	case on.String():
		return on, true
	case off.String():
		return off, true
	default:
		return off, false
	}
}

// action is a power command requested by the user. Its verb-like values
// distinguish it from state: an outlet is never "in the toggle state".
type action int

const (
	turnOn action = iota
	turnOff
	toggle
)

func (a action) String() string {
	switch a {
	case turnOn:
		return "on"
	case turnOff:
		return "off"
	case toggle:
		return "toggle"
	default:
		return ""
	}
}

// parseAction converts a user command word to a power action.
func parseAction(word string) (action, bool) {
	switch word {
	case turnOn.String():
		return turnOn, true
	case turnOff.String():
		return turnOff, true
	case toggle.String():
		return toggle, true
	default:
		return turnOn, false
	}
}

// commandList returns the supported commands as a comma-separated string,
// derived from the action and status commands so usage messages cannot drift.
func commandList() string {
	return strings.Join([]string{turnOn.String(), turnOff.String(), toggle.String(), status}, ", ")
}

// PDU is a module that provides basic power management functions for a PDU
// (Power Distribution Unit). It supports Intellinet-style PDUs (e.g. Intellinet
// 163682, LogiLink PDU8P01) and Gude PDUs; the concrete device is selected via
// the Vendor option.
type PDU struct {
	Vendor   string `yaml:"vendor"` // Vendor selects the PDU HTTP API: "intellinet" (default) or "gude".
	Host     string // Host is the base address of the PDU.
	User     string // User for HTTP Basic Auth; set together with Password, or leave both empty for no auth.
	Password string // Password for HTTP Basic Auth; set together with User, or leave both empty for no auth.
	Outlet   int    // Outlet is the outlet to control, if the PDU supports multiple outlets. Defaults to 0 (first outlet).

	backend backend // vendor-specific API, selected in Init.
}

func (p *PDU) Help() string {
	help := strings.Builder{}

	help.WriteString("PDU module: control of a Power Distribution Unit (PDU) via HTTP.\n")
	help.WriteString("\nUsage:\n")
	help.WriteString("  pdu [on|off|toggle|status]\n\n")
	help.WriteString("Commands:\n")
	help.WriteString("  on      - Power on the outlet\n")
	help.WriteString("  off     - Power off the outlet\n")
	help.WriteString("  toggle  - Toggle the outlet power\n")
	help.WriteString("  status  - Report the current power state\n")
	help.WriteString("\n")
	fmt.Fprintf(&help, "Controls outlet %d of the PDU at %s via the %s API.\n", p.Outlet, p.Host, p.vendorLabel())

	return help.String()
}

// vendorLabel returns the configured vendor for display, flagging when the
// default (see newBackend) applies because no vendor was set.
func (p *PDU) vendorLabel() string {
	if p.Vendor == "" {
		return vendorIntellinet + " (default)"
	}

	return p.Vendor
}

func (p *PDU) Init(_ context.Context) error {
	if p.Host == "" {
		return fmt.Errorf("PDU host address not configured")
	}

	if p.Outlet < 0 {
		return fmt.Errorf("invalid outlet number %d: outlet must be 0 or greater", p.Outlet)
	}

	// Basic Auth needs both parts; only one set is a misconfiguration that would
	// otherwise send no credentials and fail with an opaque 401 at request time.
	if (p.User == "") != (p.Password == "") {
		return fmt.Errorf("PDU authentication requires both user and password to be set, or neither")
	}

	req := &requester{
		client:   &http.Client{Timeout: defaultTimeout},
		user:     p.User,
		password: p.Password,
	}

	backend, err := newBackend(p.Vendor, req, p.Host)
	if err != nil {
		return err
	}

	p.backend = backend

	return nil
}

func (p *PDU) Deinit(_ context.Context) error {
	return nil
}

func (p *PDU) Run(ctx context.Context, s module.Session, args ...string) error {
	if p.backend == nil {
		return fmt.Errorf("PDU backend not initialized: Init must run successfully before Run")
	}

	if len(args) == 0 {
		return fmt.Errorf("no command specified, available commands: %s", commandList())
	}

	cmd := strings.ToLower(args[0])

	if cmd == status {
		return p.report(ctx, s)
	}

	act, ok := parseAction(cmd)
	if !ok {
		return fmt.Errorf("unknown command %q, available commands: %s", cmd, commandList())
	}

	return p.apply(ctx, s, act)
}

// apply performs the power action via the backend and reports the resulting state to the client.
func (p *PDU) apply(ctx context.Context, s module.Session, act action) error {
	result, err := p.backend.setPower(ctx, p.Outlet, act)
	if err != nil {
		return err
	}

	log.FromContext(ctx).Info("power command applied", "outlet", p.Outlet, "action", act, "state", result)
	s.Printf("PDU outlet %d powered %s\n", p.Outlet, result)

	return nil
}

// report queries the outlet state via the backend and reports it to the client.
func (p *PDU) report(ctx context.Context, s module.Session) error {
	current, err := p.backend.outletState(ctx, p.Outlet)
	if err != nil {
		return err
	}

	log.FromContext(ctx).Debug("power state queried", "outlet", p.Outlet, "state", current)
	s.Printf("PDU outlet %d state: %s\n", p.Outlet, current)

	return nil
}

// backend abstracts a vendor-specific PDU HTTP API. Implementations are chosen
// by newBackend and carry everything they need to reach the device, so the PDU
// module can drive any supported vendor through the same two operations.
type backend interface {
	// setPower applies the action to the outlet and returns the resulting state.
	// Backends without a native toggle implement it as a read-modify-write.
	setPower(ctx context.Context, outlet int, act action) (state, error)
	// outletState reports the outlet's current power state.
	outletState(ctx context.Context, outlet int) (state, error)
}

//nolint:ireturn // factory returns different backends behind one interface for vendor polymorphism.
func newBackend(vendor string, req *requester, host string) (backend, error) {
	base, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid PDU host %q: %w", host, err)
	}

	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid PDU host %q: must be an absolute URL including scheme (e.g. http://10.0.0.5)", host)
	}

	switch vendor {
	case "", vendorIntellinet: // An empty vendor keeps legacy configs on the Intellinet API.
		return intellinet{req: req, base: base}, nil
	case vendorGude:
		return gude{req: req, base: base}, nil
	default:
		return nil, fmt.Errorf("unknown PDU vendor %q (supported: %q, %q)", vendor, vendorIntellinet, vendorGude)
	}
}

// requester performs authenticated GET requests against a PDU's HTTP API. It
// carries the credentials so vendor backends need not repeat the request setup.
type requester struct {
	client   *http.Client
	user     string
	password string
}

// get issues an authenticated GET against endpoint. On success the caller owns the
// response and must close its Body; on any error the returned response is nil (its
// body already closed if one existed). A non-200 status is reported as an error.
func (r *requester) get(ctx context.Context, endpoint string) (*http.Response, error) {
	authenticated := r.user != "" && r.password != ""

	log.FromContext(ctx).Debug("GET "+endpoint, "auth", authenticated)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	if authenticated {
		request.SetBasicAuth(r.user, r.password)
	}

	resp, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		return nil, fmt.Errorf("PDU request failed with status %s: %s", resp.Status, string(body))
	}

	return resp, nil
}

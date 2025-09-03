// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package wifisocket provides a dutagent module that allows power control of a WiFi socket via HTTP requests.
package wifisocket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID:  "wifisocket",
		New: func() module.Module { return &WifiSocket{} },
	})
}

// WifiSocket controls Tasmota-based WiFi sockets.
type WifiSocket struct {
	Host     string // base URL of the device, e.g. http://192.168.1.50
	User     string // optional HTTP basic auth user
	Password string // optional HTTP basic auth password
	Channel  int    // channel to control (1 = default single-outlet)

	client     *http.Client
	controlURL *url.URL
}

const (
	defaultTimeout = 10 * time.Second
	on             = "on"
	off            = "off"
	toggle         = "toggle"
	status         = "status"
)

func (w *WifiSocket) Help() string {
	log.Println("wifisocket module: Help called")

	help := strings.Builder{}

	help.WriteString("WiFi Socket (Tasmota) Module\n\n")
	help.WriteString("Usage:\n  wifisocket [on|off|toggle|status]\n\n")
	help.WriteString("Commands:\n")
	help.WriteString("  on      - Power on the socket\n")
	help.WriteString("  off     - Power off the socket\n")
	help.WriteString("  toggle  - Toggle the socket\n")
	help.WriteString("  status  - Query the current state\n\n")
	help.WriteString("Configuration:\n")
	help.WriteString("  host     - base URL of the socket (http://IP[:PORT])\n")
	help.WriteString("  user     - optional HTTP basic auth user\n")
	help.WriteString("  password - optional HTTP basic auth password\n")
	help.WriteString("  channel  - channel number (1 for single-outlet devices)\n")

	return help.String()
}

func (w *WifiSocket) Init() error {
	log.Printf("wifisocket module: Init called - Host: %s, Channel: %d", w.Host, w.Channel)

	if w.Host == "" {
		return fmt.Errorf("wifisocket: host must be configured")
	}

	if w.Channel <= 0 {
		w.Channel = 1
	}

	w.client = &http.Client{Timeout: defaultTimeout}

	// normalize host: trim spaces and add scheme if missing (allow bare IPs like 192.168.8.71)
	host := strings.TrimSpace(w.Host)
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}

	u, err := url.Parse(strings.TrimRight(host, "/") + "/cm")
	if err != nil {
		return err
	}

	w.controlURL = u
	w.Host = host

	log.Printf("wifisocket module: Init completed - controlURL: %s", w.controlURL.String())

	return nil
}

func (w *WifiSocket) Deinit() error {
	log.Println("wifisocket module: Deinit called")

	return nil
}

func (w *WifiSocket) Run(ctx context.Context, s module.Session, args ...string) error {
	if w.client == nil {
		return fmt.Errorf("wifisocket client not initialized")
	}

	if len(args) == 0 {
		s.Println("No command specified. Call 'help' for usage.")

		return nil
	}

	cmd := strings.ToLower(args[0])

	switch cmd {
	case on, off, toggle:
		return w.setPower(ctx, s, cmd)

	case status:
		return w.status(ctx, s)

	default:
		s.Println("Unknown command: " + cmd)
		s.Println("Available commands: on, off, toggle, status")

		return nil
	}
}

// doRequest performs an HTTP GET and ensures HTTP 200 OK.
func (w *WifiSocket) doRequest(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	if w.User != "" && w.Password != "" {
		req.SetBasicAuth(w.User, w.Password)
	}

	var resp *http.Response

	resp, err = w.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		return nil, fmt.Errorf("device returned %s: %s", resp.Status, string(body))
	}

	return resp, nil
}

func (w *WifiSocket) setPower(ctx context.Context, s module.Session, state string) error {
	opCmd, err := mapStateToTasmotaCmd(state, w.Channel)
	if err != nil {
		return err
	}

	// build URL copy
	u := *w.controlURL
	q := u.Query()
	q.Set("cmnd", opCmd)
	u.RawQuery = q.Encode()

	resp, err := w.doRequest(ctx, u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	confirmed, err := w.parseState(body)
	if err == nil {
		s.Printf("WiFi socket channel %d set to '%s'\n", w.Channel, confirmed)

		return nil
	}

	// fallback to reporting requested state when parsing fails
	s.Printf("WiFi socket channel %d set to '%s'\n", w.Channel, state)

	return nil
}

func (w *WifiSocket) status(ctx context.Context, s module.Session) error {
	cmdName := "Power"
	if w.Channel > 1 {
		cmdName = fmt.Sprintf("Power%d", w.Channel)
	}

	u := *w.controlURL
	q := u.Query()
	q.Set("cmnd", cmdName)
	u.RawQuery = q.Encode()

	resp, err := w.doRequest(ctx, u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	state, err := w.parseState(body)
	if err != nil {
		return err
	}

	s.Printf("WiFi socket channel %d state: %s\n", w.Channel, state)

	return nil
}

// parseState returns "on" or "off" when it can be determined.
func (w *WifiSocket) parseState(body []byte) (string, error) {
	trim := strings.TrimSpace(string(body))
	if trim == "" {
		return "", fmt.Errorf("empty response")
	}

	dec := json.NewDecoder(bytes.NewReader(body))

	var dataMap map[string]interface{}

	err := dec.Decode(&dataMap)
	if err != nil {
		return "", fmt.Errorf("invalid JSON response: %v", err)
	}

	keys := []string{"POWER", fmt.Sprintf("POWER%d", w.Channel)}

	for _, k := range keys {
		if v, ok := dataMap[k]; ok {
			str, ok := v.(string)
			if !ok {
				continue
			}

			sv := strings.ToUpper(strings.TrimSpace(str))
			if sv == "ON" {
				return on, nil
			}

			if sv == "OFF" {
				return off, nil
			}
		}
	}

	return "", fmt.Errorf("unexpected device response: %s", trim)
}

func mapStateToTasmotaCmd(state string, channel int) (string, error) {
	cmdName := "Power"
	if channel > 1 {
		cmdName = fmt.Sprintf("Power%d", channel)
	}

	switch strings.ToLower(state) {
	case on:
		return fmt.Sprintf("%s ON", cmdName), nil

	case off:
		return fmt.Sprintf("%s OFF", cmdName), nil

	case toggle:
		return fmt.Sprintf("%s TOGGLE", cmdName), nil

	default:
		return "", fmt.Errorf("invalid operation: %s", state)
	}
}

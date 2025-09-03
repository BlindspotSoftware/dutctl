// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wifisocket

import (
	"strings"
	"testing"
)

func TestMapStateToTasmotaCmd(t *testing.T) {
	c, err := mapStateToTasmotaCmd("on", 1)
	if err != nil || c != "Power ON" {
		t.Fatalf("unexpected: %v %v", c, err)
	}
	c, err = mapStateToTasmotaCmd("off", 2)
	if err != nil || c != "Power2 OFF" {
		t.Fatalf("unexpected: %v %v", c, err)
	}
	c, err = mapStateToTasmotaCmd("toggle", 3)
	if err != nil || c != "Power3 TOGGLE" {
		t.Fatalf("unexpected: %v %v", c, err)
	}
	if _, err := mapStateToTasmotaCmd("bad", 1); err == nil {
		t.Fatalf("expected error for invalid op")
	}
}

func TestParseStateJSONHTMLPlain(t *testing.T) {
	w := &WifiSocket{Channel: 1}

	// JSON with trailing junk
	body := []byte("{\"POWER\":\"OFF\"}%")
	st, err := w.parseState(body)
	if err != nil || st != off {
		t.Fatalf("json parse failed: %v %v", st, err)
	}

	// HTML-like fragment should be rejected (parseState expects JSON)
	body = []byte("<div>...<span>\">ON\"</span>...</div>")
	if _, err = w.parseState(body); err == nil {
		t.Fatalf("expected error for non-JSON html, got nil")
	}

	// plain text should be rejected
	body = []byte("off")
	if _, err = w.parseState(body); err == nil {
		t.Fatalf("expected error for non-JSON plain text, got nil")
	}
}

func TestInitControlURL(t *testing.T) {
	w := &WifiSocket{Host: "http://example.local:8080"}
	if err := w.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if w.controlURL == nil {
		t.Fatalf("controlURL nil")
	}
	if !strings.HasSuffix(w.controlURL.String(), "/cm") {
		t.Fatalf("controlURL must end with /cm: %s", w.controlURL.String())
	}
}

func TestHelpAndDeinit(t *testing.T) {
	w := &WifiSocket{}
	h := w.Help()
	if h == "" || !strings.Contains(h, "WiFi Socket") {
		t.Fatalf("help seems wrong")
	}
	if err := w.Deinit(); err != nil {
		t.Fatalf("Deinit returned error: %v", err)
	}
}

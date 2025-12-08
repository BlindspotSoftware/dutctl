// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// handleKeyboardCommand dispatches keyboard-related commands.
func (p *PiKVM) handleKeyboardCommand(ctx context.Context, s module.Session, command string, args []string) error {
	switch command {
	case typeCmd:
		if len(args) < minArgsRequired {
			s.Println("Error: 'type' command requires text argument")

			return nil
		}

		return p.handleType(ctx, s, strings.Join(args[1:], " "))
	case key:
		if len(args) < minArgsRequired {
			s.Println("Error: 'key' command requires key name argument")

			return nil
		}

		return p.handleKey(ctx, s, args[1])
	case keyCombo:
		if len(args) < minArgsRequired {
			s.Println("Error: 'key-combo' command requires key combination argument")

			return nil
		}

		return p.handleCombo(ctx, s, args[1])
	case paste:
		return p.handlePaste(ctx, s)
	default:
		return fmt.Errorf("unknown keyboard command: %s", command)
	}
}

func (p *PiKVM) handleType(ctx context.Context, s module.Session, text string) error {
	payload := map[string]interface{}{
		"keys": text,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/hid/events/send_key", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to type text: %v", err)
	}
	defer resp.Body.Close()

	s.Printf("Typed: %s\n", text)

	return nil
}

func (p *PiKVM) handleKey(ctx context.Context, s module.Session, keyName string) error {
	payload := map[string]interface{}{
		"key": keyName,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/hid/events/send_key", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to send key: %v", err)
	}
	defer resp.Body.Close()

	s.Printf("Key sent: %s\n", keyName)

	return nil
}

func (p *PiKVM) handleCombo(ctx context.Context, s module.Session, comboStr string) error {
	// Parse combo like "Ctrl+Alt+Delete" into array of keys
	keys := strings.Split(comboStr, "+")
	for i, k := range keys {
		keys[i] = strings.TrimSpace(k)
	}

	payload := map[string]interface{}{
		"keys": keys,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/hid/events/send_key", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to send key combo: %v", err)
	}
	defer resp.Body.Close()

	s.Printf("Key combination sent: %s\n", comboStr)

	return nil
}

func (p *PiKVM) handlePaste(ctx context.Context, s module.Session) error {
	stdin, _, _ := s.Console()

	data, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %v", err)
	}

	if len(data) == 0 {
		s.Println("No data to paste")

		return nil
	}

	return p.handleType(ctx, s, string(data))
}

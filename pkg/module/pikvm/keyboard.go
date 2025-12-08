// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

const (
	keyDelay           = 500 * time.Millisecond
	maxTextLength      = 1024
	minComboKeys       = 2
	invalidCharNul     = "\x00"
	invalidCharNewline = "\n"
	invalidCharReturn  = "\r"
)

// handleKeyboardCommandRouter routes keyboard commands based on the first argument.
func (p *PiKVM) handleKeyboardCommandRouter(ctx context.Context, s module.Session, args []string) error {
	if len(args) == 0 {
		s.Println("Keyboard command requires an action: type|key|key-combo")

		return nil
	}

	command := strings.ToLower(args[0])

	return p.handleKeyboardCommand(ctx, s, command, args)
}

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
	default:
		return fmt.Errorf("unknown keyboard action: %s (must be: type, key, key-combo)", command)
	}
}

func (p *PiKVM) handleType(ctx context.Context, s module.Session, text string) error {
	// Validate text input
	if text == "" {
		return fmt.Errorf("empty text input")
	}

	// Check for invalid characters
	for _, c := range []string{invalidCharNul, invalidCharNewline, invalidCharReturn} {
		if strings.Contains(text, c) {
			return fmt.Errorf("text contains invalid character: %q", c)
		}
	}

	// Limit text length
	if len(text) > maxTextLength {
		return fmt.Errorf("text input too long (%d chars, max is %d)", len(text), maxTextLength)
	}

	s.Printf("Typing text: %s\n", text)

	// Use the /api/hid/print endpoint for text input
	resp, err := p.doRequest(ctx, http.MethodPost, "/api/hid/print?slow=true", bytes.NewBufferString(text), "text/plain")
	if err != nil {
		return fmt.Errorf("failed to type text: %v", err)
	}
	defer resp.Body.Close()

	s.Println("Text typed successfully")

	return nil
}

func (p *PiKVM) handleKey(ctx context.Context, s module.Session, keyName string) error {
	// Map the key name to PiKVM format
	mappedKey, err := mapToKeyboardKey(keyName)
	if err != nil {
		return fmt.Errorf("error with key '%s': %w", keyName, err)
	}

	s.Printf("Sending key: %s\n", keyName)

	// Send key press (press and release)
	err = p.sendKeyRequest(ctx, fmt.Sprintf("/api/hid/events/send_key?key=%s", url.QueryEscape(mappedKey)))
	if err != nil {
		return err
	}

	s.Println("Key sent successfully")

	return nil
}

func (p *PiKVM) handleCombo(ctx context.Context, s module.Session, comboStr string) error {
	// Parse combo like "Ctrl+Alt+Delete" into array of keys
	keys := strings.Split(comboStr, "+")
	for idx := range keys {
		keys[idx] = strings.TrimSpace(keys[idx])
	}

	if len(keys) < minComboKeys {
		return fmt.Errorf("key combination must have at least %d keys", minComboKeys)
	}

	s.Printf("Sending key combination: %s\n", comboStr)

	// Validate and map keys
	modifiers := keys[:len(keys)-1]
	mainKey := keys[len(keys)-1]

	mainKeyMapped, mappedModifiers, err := p.validateAndMapComboKeys(modifiers, mainKey)
	if err != nil {
		return err
	}

	// Execute the key combination
	err = p.executeKeyCombo(ctx, mappedModifiers, mainKeyMapped)
	if err != nil {
		return err
	}

	s.Println("Key combination sent successfully")

	return nil
}

// validateAndMapComboKeys validates and maps the keys in a combination.
func (p *PiKVM) validateAndMapComboKeys(modifiers []string, mainKey string) (string, []string, error) {
	// Map main key
	mainKeyMapped, err := mapToKeyboardKey(mainKey)
	if err != nil {
		return "", nil, fmt.Errorf("error with key '%s': %w", mainKey, err)
	}

	// Check if the last key is not a modifier
	if isModifierKey(mainKeyMapped) {
		return "", nil, fmt.Errorf("last key in combination should not be a modifier: %s", mainKey)
	}

	// Map and validate modifiers
	mappedModifiers := make([]string, len(modifiers))
	for idx, mod := range modifiers {
		var err error

		mappedModifiers[idx], err = mapToKeyboardKey(mod)
		if err != nil {
			return "", nil, fmt.Errorf("error with modifier '%s': %w", mod, err)
		}

		if !isModifierKey(mappedModifiers[idx]) {
			return "", nil, fmt.Errorf("invalid modifier key: %s", mod)
		}
	}

	return mainKeyMapped, mappedModifiers, nil
}

// executeKeyCombo executes a key combination by pressing modifiers, main key, and releasing modifiers.
func (p *PiKVM) executeKeyCombo(ctx context.Context, modifiers []string, mainKey string) error {
	// Press down all modifier keys
	for _, modifier := range modifiers {
		err := p.sendKeyRequest(ctx, fmt.Sprintf("/api/hid/events/send_key?key=%s&state=1", url.QueryEscape(modifier)))
		if err != nil {
			return err
		}

		time.Sleep(keyDelay)
	}

	// Press and release the main key
	err := p.sendKeyRequest(ctx, fmt.Sprintf("/api/hid/events/send_key?key=%s", url.QueryEscape(mainKey)))
	if err != nil {
		return err
	}

	time.Sleep(keyDelay)

	// Release all modifier keys in reverse order
	for idx := len(modifiers) - 1; idx >= 0; idx-- {
		err := p.sendKeyRequest(ctx, fmt.Sprintf("/api/hid/events/send_key?key=%s&state=0", url.QueryEscape(modifiers[idx])))
		if err != nil {
			return err
		}

		time.Sleep(keyDelay)
	}

	return nil
}

// sendKeyRequest sends a key request to PiKVM.
func (p *PiKVM) sendKeyRequest(ctx context.Context, urlPath string) error {
	resp, err := p.doRequest(ctx, http.MethodPost, urlPath, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// Map of keyboard key names to PiKVM web_name values.
//
//nolint:gochecknoglobals // Keyboard mapping table is a legitimate use case for a package-level variable
var keyboardMap = map[string]string{
	// Letter keys
	"a": "KeyA", "b": "KeyB", "c": "KeyC", "d": "KeyD", "e": "KeyE",
	"f": "KeyF", "g": "KeyG", "h": "KeyH", "i": "KeyI", "j": "KeyJ",
	"k": "KeyK", "l": "KeyL", "m": "KeyM", "n": "KeyN", "o": "KeyO",
	"p": "KeyP", "q": "KeyQ", "r": "KeyR", "s": "KeyS", "t": "KeyT",
	"u": "KeyU", "v": "KeyV", "w": "KeyW", "x": "KeyX", "y": "KeyY",
	"z": "KeyZ",

	// Digits
	"0": "Digit0", "1": "Digit1", "2": "Digit2", "3": "Digit3", "4": "Digit4",
	"5": "Digit5", "6": "Digit6", "7": "Digit7", "8": "Digit8", "9": "Digit9",

	// Modifiers
	"ctrl": "ControlLeft", "control": "ControlLeft",
	"alt":   "AltLeft",
	"shift": "ShiftLeft",
	"meta":  "MetaLeft", "win": "MetaLeft", "windows": "MetaLeft", "cmd": "MetaLeft", "command": "MetaLeft",

	// Common keys
	"space": "Space",
	"enter": "Enter", "return": "Enter",
	"tab": "Tab",
	"esc": "Escape", "escape": "Escape",
	"backspace": "Backspace",
	"delete":    "Delete", "del": "Delete",
	"insert": "Insert", "ins": "Insert",
	"home":   "Home",
	"end":    "End",
	"pageup": "PageUp", "pgup": "PageUp",
	"pagedown": "PageDown", "pgdn": "PageDown",

	// Function keys
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4", "f5": "F5", "f6": "F6",
	"f7": "F7", "f8": "F8", "f9": "F9", "f10": "F10", "f11": "F11", "f12": "F12",

	// Arrow keys
	"up": "ArrowUp", "arrowup": "ArrowUp",
	"down": "ArrowDown", "arrowdown": "ArrowDown",
	"left": "ArrowLeft", "arrowleft": "ArrowLeft",
	"right": "ArrowRight", "arrowright": "ArrowRight",

	// Punctuation and symbols
	"-": "Minus", "minus": "Minus",
	"=": "Equal", "equals": "Equal",
	"[": "BracketLeft", "leftbracket": "BracketLeft",
	"]": "BracketRight", "rightbracket": "BracketRight",
	"\\": "Backslash", "backslash": "Backslash",
	";": "Semicolon", "semicolon": "Semicolon",
	"'": "Quote", "quote": "Quote",
	"`": "Backquote", "backquote": "Backquote",
	",": "Comma", "comma": "Comma",
	".": "Period", "period": "Period",
	"/": "Slash", "slash": "Slash",

	// Additional keys
	"capslock":    "CapsLock",
	"printscreen": "PrintScreen",
	"pause":       "Pause",
	"scrolllock":  "ScrollLock", "scroll": "ScrollLock",
	"numlock": "NumLock", "num": "NumLock",
	"contextmenu": "ContextMenu", "menu": "ContextMenu",

	// Numpad keys
	"numpad0": "Numpad0", "numpad1": "Numpad1", "numpad2": "Numpad2",
	"numpad3": "Numpad3", "numpad4": "Numpad4", "numpad5": "Numpad5",
	"numpad6": "Numpad6", "numpad7": "Numpad7", "numpad8": "Numpad8",
	"numpad9": "Numpad9", "numpad/": "NumpadDivide", "numpad*": "NumpadMultiply",
	"numpad-": "NumpadSubtract", "numpad+": "NumpadAdd",
	"numpadenter": "NumpadEnter", "numpad.": "NumpadDecimal",
}

// mapToKeyboardKey maps common key names to PiKVM keyboard key names.
func mapToKeyboardKey(key string) (string, error) {
	lowerKey := strings.ToLower(key)

	if value, exists := keyboardMap[lowerKey]; exists {
		return value, nil
	}

	return "", fmt.Errorf("unsupported key: %s", key)
}

// isModifierKey checks if a key is a modifier key.
func isModifierKey(key string) bool {
	return strings.HasPrefix(key, "Control") ||
		strings.HasPrefix(key, "Alt") ||
		strings.HasPrefix(key, "Shift") ||
		strings.HasPrefix(key, "Meta")
}

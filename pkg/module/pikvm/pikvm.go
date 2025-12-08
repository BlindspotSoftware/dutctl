// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pikvm provides a dutagent module that allows control of a PiKVM device via HTTP API.
package pikvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

func init() {
	module.Register(module.Record{
		ID: "pikvm",
		New: func() module.Module {
			return &PiKVM{}
		},
	})
}

// PiKVM is a module that provides power management, keyboard control, and virtual media
// functionality for a DUT via PiKVM device.
type PiKVM struct {
	Host     string // Host is the address of the PiKVM device (e.g., "192.168.1.100" or "https://pikvm.local")
	User     string // User for authentication (default: "admin")
	Password string // Password for authentication
	Timeout  string // Timeout for HTTP requests (default: "10s")

	client  *http.Client
	baseURL *url.URL
}

// Ensure implementing the Module interface.
var _ module.Module = &PiKVM{}

const (
	defaultUser     = "admin"
	defaultTimeout  = 10 * time.Second
	minArgsRequired = 2 // Minimum arguments required for commands with parameters

	// Status values.
	statusUnknown = "Unknown"
	statusOn      = "On"
	statusOff     = "Off"
	mediaNone     = "None"
)

// Command constants.
const (
	// Power management.
	on         = "on"
	off        = "off"
	forceOff   = "force-off"
	reset      = "reset"
	forceReset = "force-reset"
	status     = "status"

	// Keyboard control.
	typeCmd = "type"
	key     = "key"
	combo   = "combo"
	paste   = "paste"

	// Virtual media.
	mount       = "mount"
	mountURL    = "mount-url"
	unmount     = "unmount"
	mediaStatus = "media-status"
)

func (p *PiKVM) Help() string {
	log.Println("pikvm module: Help called")

	help := strings.Builder{}
	help.WriteString("PiKVM Control Module\n\n")
	help.WriteString("Power Management:\n")
	help.WriteString("  pikvm on           - Power on via short ATX power button press\n")
	help.WriteString("  pikvm off          - Graceful shutdown via short ATX power button press\n")
	help.WriteString("  pikvm force-off    - Force power off via long ATX power button press (4-5s)\n")
	help.WriteString("  pikvm reset        - Reset via short ATX reset button press\n")
	help.WriteString("  pikvm force-reset  - Force reset via long ATX reset button press\n")
	help.WriteString("  pikvm status       - Query current power state\n\n")
	help.WriteString("Keyboard Control:\n")
	help.WriteString("  pikvm type <text>  - Type a text string\n")
	help.WriteString("  pikvm key <key>    - Send a single key (e.g., Enter, Escape, F12)\n")
	help.WriteString("  pikvm combo <keys> - Send key combination (e.g., Ctrl+Alt+Delete)\n")
	help.WriteString("  pikvm paste        - Paste text from stdin\n\n")
	help.WriteString("Virtual Media:\n")
	help.WriteString("  pikvm mount <path>      - Mount an image file from local filesystem\n")
	help.WriteString("  pikvm mount-url <url>   - Mount an image from a URL\n")
	help.WriteString("  pikvm unmount           - Unmount current virtual media\n")
	help.WriteString("  pikvm media-status      - Show mounted media information\n\n")
	help.WriteString("This module provides comprehensive control of a PiKVM device.\n")
	help.WriteString("Configured PiKVM: " + p.Host + "\n")

	return help.String()
}

func (p *PiKVM) Init() error {
	log.Printf("pikvm module: Init starting for host %s", p.Host)

	if p.Host == "" {
		return fmt.Errorf("pikvm: host is not set")
	}

	// Set default user if not provided
	if p.User == "" {
		p.User = defaultUser
		log.Printf("pikvm module: Using default user '%s'", defaultUser)
	}

	// Parse custom timeout if provided
	timeout := defaultTimeout

	if p.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(p.Timeout)
		if err == nil {
			timeout = parsedTimeout
			log.Printf("pikvm module: Using custom timeout %v", timeout)
		} else {
			log.Printf("pikvm module: Invalid timeout format '%s', using default %v", p.Timeout, defaultTimeout)
		}
	}

	p.client = &http.Client{Timeout: timeout}

	// Normalize host: add scheme if missing
	host := strings.TrimSpace(p.Host)
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}

	baseURL, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil {
		return fmt.Errorf("pikvm: invalid host URL: %v", err)
	}

	p.baseURL = baseURL

	log.Printf("pikvm module: Init completed successfully for %s", p.baseURL.String())

	return nil
}

func (p *PiKVM) Deinit() error {
	log.Println("pikvm module: Deinit called")

	return nil
}

func (p *PiKVM) Run(ctx context.Context, s module.Session, args ...string) error {
	if p.client == nil {
		return fmt.Errorf("pikvm: client not initialized")
	}

	if len(args) == 0 {
		s.Println("No command specified. Try 'help' for usage.")

		return nil
	}

	command := strings.ToLower(args[0])

	switch command {
	case on, off, forceOff, reset, forceReset, status:
		return p.handlePowerCommand(ctx, s, command)
	case typeCmd, key, combo, paste:
		return p.handleKeyboardCommand(ctx, s, command, args)
	case mount, mountURL, unmount, mediaStatus:
		return p.handleMediaCommand(ctx, s, command, args)
	default:
		return p.showUnknownCommand(s, command)
	}
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
	case combo:
		if len(args) < minArgsRequired {
			s.Println("Error: 'combo' command requires key combination argument")

			return nil
		}

		return p.handleCombo(ctx, s, args[1])
	case paste:
		return p.handlePaste(ctx, s)
	default:
		return fmt.Errorf("unknown keyboard command: %s", command)
	}
}

// handleMediaCommand dispatches virtual media commands.
func (p *PiKVM) handleMediaCommand(ctx context.Context, s module.Session, command string, args []string) error {
	switch command {
	case mount:
		if len(args) < minArgsRequired {
			s.Println("Error: 'mount' command requires file path argument")

			return nil
		}

		return p.handleMount(ctx, s, args[1])
	case mountURL:
		if len(args) < minArgsRequired {
			s.Println("Error: 'mount-url' command requires URL argument")

			return nil
		}

		return p.handleMountURL(ctx, s, args[1])
	case unmount:
		return p.handleUnmount(ctx, s)
	case mediaStatus:
		return p.handleMediaStatus(ctx, s)
	default:
		return fmt.Errorf("unknown media command: %s", command)
	}
}

// showUnknownCommand displays an error message for unknown commands.
func (p *PiKVM) showUnknownCommand(s module.Session, command string) error {
	s.Println("Unknown command: " + command)
	s.Println("Available commands:")
	s.Println("  Power: on, off, force-off, reset, force-reset, status")
	s.Println("  Keyboard: type, key, combo, paste")
	s.Println("  Media: mount, mount-url, unmount, media-status")

	return nil
}

// doRequest creates and executes an HTTP request with authentication.
// contentType is optional; if empty, no Content-Type header is set.
func (p *PiKVM) doRequest(ctx context.Context, method, urlPath string, body io.Reader, contentType string) (*http.Response, error) {
	u := *p.baseURL
	u.Path = path.Join(u.Path, urlPath)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if p.User != "" && p.Password != "" {
		req.SetBasicAuth(p.User, p.Password)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		return nil, fmt.Errorf("pikvm: API returned %s: %s", resp.Status, string(bodyBytes))
	}

	return resp, nil
}

// Power Management Functions

func (p *PiKVM) handlePowerCommand(ctx context.Context, s module.Session, command string) error {
	switch command {
	case on:
		return p.sendATXClick(ctx, s, "power", "short")
	case off:
		return p.sendATXClick(ctx, s, "power", "short")
	case forceOff:
		return p.sendATXClick(ctx, s, "power", "long")
	case reset:
		return p.sendATXClick(ctx, s, "reset", "short")
	case forceReset:
		return p.sendATXClick(ctx, s, "reset", "long")
	case status:
		return p.handleStatusCommand(ctx, s)
	default:
		return fmt.Errorf("unknown power command: %s", command)
	}
}

func (p *PiKVM) sendATXClick(ctx context.Context, s module.Session, button, action string) error {
	payload := map[string]interface{}{
		"button": button,
		"wait":   action == "long",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/atx/click", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("ATX %s %s command failed: %v", button, action, err)
	}
	defer resp.Body.Close()

	s.Printf("ATX %s button %s press completed\n", button, action)

	return nil
}

func (p *PiKVM) handleStatusCommand(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/atx", nil, "")
	if err != nil {
		return fmt.Errorf("failed to get ATX status: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var status map[string]interface{}

	err = json.Unmarshal(body, &status)
	if err != nil {
		return fmt.Errorf("failed to parse status response: %v", err)
	}

	// Extract power state from response
	powerState := p.extractPowerState(status)
	s.Printf("Device power status: %s\n", powerState)

	return nil
}

// extractPowerState extracts the power status from the API response.
func (p *PiKVM) extractPowerState(status map[string]interface{}) string {
	result, ok := status["result"].(map[string]interface{})
	if !ok {
		return statusUnknown
	}

	leds, ok := result["leds"].(map[string]interface{})
	if !ok {
		return statusUnknown
	}

	power, ok := leds["power"].(bool)
	if !ok {
		return statusUnknown
	}

	if power {
		return statusOn
	}

	return statusOff
}

// Keyboard Control Functions

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

// Virtual Media Functions

func (p *PiKVM) handleMount(ctx context.Context, s module.Session, imagePath string) error {
	// Read the image file
	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed to open image file: %v", err)
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	s.Printf("Uploading image file: %s (%d bytes)\n", filepath.Base(imagePath), fileInfo.Size())

	// First, upload the image to PiKVM
	uploadResp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/write", file, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("failed to upload image: %v", err)
	}
	defer uploadResp.Body.Close()

	// Now mount the uploaded image
	mountPayload := map[string]interface{}{
		"image": filepath.Base(imagePath),
	}

	jsonData, err := json.Marshal(mountPayload)
	if err != nil {
		return err
	}

	mountResp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to mount image: %v", err)
	}
	defer mountResp.Body.Close()

	s.Printf("Image mounted successfully: %s\n", filepath.Base(imagePath))

	return nil
}

func (p *PiKVM) handleMountURL(ctx context.Context, s module.Session, imageURL string) error {
	payload := map[string]interface{}{
		"url": imageURL,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	s.Printf("Mounting image from URL: %s\n", imageURL)

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to mount image from URL: %v", err)
	}
	defer resp.Body.Close()

	s.Printf("Image mounted successfully from URL: %s\n", imageURL)

	return nil
}

func (p *PiKVM) handleUnmount(ctx context.Context, s module.Session) error {
	payload := map[string]interface{}{
		"connected": false,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected", bytes.NewReader(jsonData), "application/json")
	if err != nil {
		return fmt.Errorf("failed to unmount media: %v", err)
	}
	defer resp.Body.Close()

	s.Println("Virtual media unmounted successfully")

	return nil
}

func (p *PiKVM) handleMediaStatus(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/msd", nil, "")
	if err != nil {
		return fmt.Errorf("failed to get media status: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var status map[string]interface{}

	err = json.Unmarshal(body, &status)
	if err != nil {
		return fmt.Errorf("failed to parse media status: %v", err)
	}

	// Extract media info from response
	connected, imageName := p.extractMediaInfo(status)

	s.Printf("Virtual media status:\n")
	s.Printf("  Connected: %v\n", connected)
	s.Printf("  Image: %s\n", imageName)

	return nil
}

// extractMediaInfo extracts media connection status and image name from API response.
func (p *PiKVM) extractMediaInfo(status map[string]interface{}) (bool, string) {
	result, ok := status["result"].(map[string]interface{})
	if !ok {
		return false, statusUnknown
	}

	connected, ok := result["connected"].(bool)
	if !ok {
		connected = false
	}

	if !connected {
		return connected, mediaNone
	}

	imageName := p.extractImageName(result)

	return connected, imageName
}

// extractImageName extracts the mounted image name from the result data.
func (p *PiKVM) extractImageName(result map[string]interface{}) string {
	storage, ok := result["storage"].(map[string]interface{})
	if !ok {
		return mediaNone
	}

	images, ok := storage["images"].([]interface{})
	if !ok || len(images) == 0 {
		return mediaNone
	}

	img, ok := images[0].(map[string]interface{})
	if !ok {
		return mediaNone
	}

	name, ok := img["name"].(string)
	if !ok {
		return mediaNone
	}

	return name
}

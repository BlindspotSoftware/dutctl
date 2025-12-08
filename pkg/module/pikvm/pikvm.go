// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pikvm provides a dutagent module that allows control of a PiKVM device via HTTP API.
package pikvm

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
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
	Command  string // Required: Command type to execute (must be: "power", "keyboard", "media", or "screenshot")

	client  *http.Client
	baseURL *url.URL
}

// Ensure implementing the Module interface.
var _ module.Module = &PiKVM{}

// isValidCommand checks if the command is one of the valid command types.
func isValidCommand(command string) bool {
	switch command {
	case cmdTypePower, cmdTypeKeyboard, cmdTypeMedia, cmdTypeScreenshot:
		return true
	default:
		return false
	}
}

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

// Command type constants.
const (
	cmdTypePower      = "power"
	cmdTypeKeyboard   = "keyboard"
	cmdTypeMedia      = "media"
	cmdTypeScreenshot = "screenshot"
)

// Power command constants.
const (
	on         = "on"
	off        = "off"
	forceOff   = "force-off"
	reset      = "reset"
	forceReset = "force-reset"
	status     = "status"
)

// Keyboard command constants.
const (
	typeCmd  = "type"
	key      = "key"
	keyCombo = "key-combo"
)

// Media command constants.
const (
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
	help.WriteString("  pikvm on           - Power on (does nothing if already on)\n")
	help.WriteString("  pikvm off          - Graceful shutdown (soft power-off)\n")
	help.WriteString("  pikvm force-off    - Force power off (hard shutdown, 5+ second press)\n")
	help.WriteString("  pikvm reset        - Reset via ATX reset button\n")
	help.WriteString("  pikvm force-reset  - Force reset (hardware hot reset)\n")
	help.WriteString("  pikvm status       - Query current power state\n\n")
	help.WriteString("Keyboard Control:\n")
	help.WriteString("  pikvm type <text>      - Type a text string\n")
	help.WriteString("  pikvm key <key>        - Send a single key (e.g., Enter, Escape, F12)\n")
	help.WriteString("  pikvm key-combo <keys> - Send key combination (e.g., Ctrl+Alt+Delete)\n\n")
	help.WriteString("Virtual Media:\n")
	help.WriteString("  pikvm mount <path>      - Mount an image file from local filesystem\n")
	help.WriteString("  pikvm mount-url <url>   - Mount an image from a URL\n")
	help.WriteString("  pikvm unmount           - Unmount current virtual media\n")
	help.WriteString("  pikvm media-status      - Show mounted media information\n\n")
	help.WriteString("Screenshot:\n")
	help.WriteString("  pikvm screenshot        - Capture a screenshot (saved to current directory)\n\n")
	help.WriteString("This module provides comprehensive control of a PiKVM device.\n")
	help.WriteString("Configured PiKVM: " + p.Host + "\n")

	return help.String()
}

func (p *PiKVM) Init() error {
	log.Printf("pikvm module: Init starting for host %s", p.Host)

	if p.Host == "" {
		return fmt.Errorf("pikvm: host is not set")
	}

	// Validate that Command is set and is valid
	if p.Command == "" {
		return fmt.Errorf("pikvm: command option is required (must be: %s, %s, %s, or %s)",
			cmdTypePower, cmdTypeKeyboard, cmdTypeMedia, cmdTypeScreenshot)
	}

	if !isValidCommand(p.Command) {
		return fmt.Errorf("pikvm: invalid command %q (must be: %s, %s, %s, or %s)",
			p.Command, cmdTypePower, cmdTypeKeyboard, cmdTypeMedia, cmdTypeScreenshot)
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

	// Create HTTP client with TLS config that accepts self-signed certificates
	// This is necessary because PiKVM devices typically use self-signed certs
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // PiKVM devices use self-signed certificates
		},
	}
	p.client = &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

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

	// Route based on the configured command type
	switch p.Command {
	case cmdTypePower:
		return p.handlePowerCommandRouter(ctx, s, args)
	case cmdTypeKeyboard:
		return p.handleKeyboardCommandRouter(ctx, s, args)
	case cmdTypeMedia:
		return p.handleMediaCommandRouter(ctx, s, args)
	case cmdTypeScreenshot:
		return p.handleScreenshot(ctx, s)
	default:
		return fmt.Errorf("pikvm: unknown command type %q", p.Command)
	}
}

type requestOptions struct {
	contentLength int64
	noTimeout     bool
}

// doRequest creates and executes an HTTP request with authentication.
func (p *PiKVM) doRequest(ctx context.Context, method, urlPath string, body io.Reader, contentType string) (*http.Response, error) {
	return p.doRequestWithOptions(ctx, method, urlPath, body, contentType, requestOptions{})
}

// doRequestWithContentLength creates an HTTP request with explicit Content-Length (for large uploads).
func (p *PiKVM) doRequestWithContentLength(
	ctx context.Context,
	method, urlPath string,
	body io.Reader,
	contentType string,
	contentLength int64,
) (*http.Response, error) {
	return p.doRequestWithOptions(ctx, method, urlPath, body, contentType, requestOptions{
		contentLength: contentLength,
		noTimeout:     true,
	})
}

// doRequestNoTimeout creates an HTTP request without timeout (for long-running operations).
func (p *PiKVM) doRequestNoTimeout(
	ctx context.Context,
	method, urlPath string,
	body io.Reader,
	contentType string,
) (*http.Response, error) {
	return p.doRequestWithOptions(ctx, method, urlPath, body, contentType, requestOptions{noTimeout: true})
}

// doRequestWithOptions is the core request method that handles all HTTP requests.
func (p *PiKVM) doRequestWithOptions(
	ctx context.Context,
	method, urlPath string,
	body io.Reader,
	contentType string,
	opts requestOptions,
) (*http.Response, error) {
	// Build URL
	targetURL, err := p.buildRequestURL(urlPath)
	if err != nil {
		return nil, err
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, err
	}

	// Set headers
	p.setRequestHeaders(req, contentType, opts)

	// Choose client (with or without timeout)
	client := p.selectHTTPClient(opts)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Check status
	err = checkResponseStatus(resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// buildRequestURL constructs the full request URL from base URL and path.
func (p *PiKVM) buildRequestURL(urlPath string) (string, error) {
	targetURL := *p.baseURL

	parsedPath, err := url.Parse(urlPath)
	if err != nil {
		return "", fmt.Errorf("invalid URL path: %v", err)
	}

	targetURL.Path = path.Join(targetURL.Path, parsedPath.Path)
	targetURL.RawQuery = parsedPath.RawQuery

	return targetURL.String(), nil
}

// setRequestHeaders sets authentication and content type headers on the request.
func (p *PiKVM) setRequestHeaders(req *http.Request, contentType string, opts requestOptions) {
	if p.User != "" && p.Password != "" {
		req.SetBasicAuth(p.User, p.Password)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if opts.contentLength > 0 {
		req.ContentLength = opts.contentLength
	}
}

// selectHTTPClient returns the appropriate HTTP client based on options.
func (p *PiKVM) selectHTTPClient(opts requestOptions) *http.Client {
	if opts.noTimeout {
		return &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint:gosec // PiKVM uses self-signed certificates
				},
			},
		}
	}

	return p.client
}

// checkResponseStatus validates the HTTP response status code.
func checkResponseStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	return fmt.Errorf("pikvm: API returned %s: %s", resp.Status, string(bodyBytes))
}

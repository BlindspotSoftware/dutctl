// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// handleScreenshot captures a screenshot from the PiKVM and sends it to the client.
func (p *PiKVM) handleScreenshot(ctx context.Context, s module.Session) error {
	s.Println("Capturing screenshot...")

	resp, err := p.doRequest(ctx, http.MethodGet, "/api/streamer/snapshot", nil, "", requestOptions{})
	if err != nil {
		// Check if error contains "Service Unavailable" which indicates the device is likely off
		if strings.Contains(err.Error(), "Service Unavailable") || strings.Contains(err.Error(), "503") {
			return fmt.Errorf(
				"failed to take screenshot: device appears to be powered off or video stream is unavailable (503)",
			)
		}

		return fmt.Errorf("failed to capture screenshot: %v", err)
	}
	defer resp.Body.Close()

	// Read the screenshot data
	screenshotData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read screenshot data: %v", err)
	}

	// Generate filename with timestamp
	filename := fmt.Sprintf("screenshot_%s.jpg", time.Now().Format("20060102_150405"))

	s.Printf("Sending screenshot to client: %s (%d bytes)\n", filename, len(screenshotData))

	// Send the screenshot to the client
	err = s.SendFile(filename, bytes.NewReader(screenshotData))
	if err != nil {
		return fmt.Errorf("failed to send screenshot to client: %v", err)
	}

	s.Printf("Screenshot sent successfully: %s\n", filename)

	return nil
}

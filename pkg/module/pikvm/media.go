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
	"os"
	"path/filepath"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

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

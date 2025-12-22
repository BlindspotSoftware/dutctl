// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pikvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

const (
	safetyMargin       = 10 * 1024 * 1024 // 10 MB safety margin
	scanningDelay      = 5 * time.Second  // Delay after deleting to allow PiKVM storage to update
	maxDeletionRetries = 10               // Maximum number of images to delete before giving up
	bytesPerMB         = 1024 * 1024      // Bytes per megabyte
)

var ErrMissingImage = errors.New("image not found in storage")

// Status represents the PiKVM MSD status response.
type Status struct {
	Ok     bool         `json:"ok"`
	Result StatusResult `json:"result"`
}

// StatusResult contains the actual status data.
type StatusResult struct {
	Busy    bool  `json:"busy"`
	Enabled bool  `json:"enabled"`
	Online  bool  `json:"online"`
	Drive   Drive `json:"drive"`
	Storage struct {
		Images map[string]StorageImage `json:"images"`
		Parts  map[string]StoragePart  `json:"parts"`
	} `json:"storage"`
}

// Drive contains information about the virtual drive.
type Drive struct {
	Connected bool  `json:"connected"`
	Image     Image `json:"image"`
	Cdrom     bool  `json:"cdrom"`
	Rw        bool  `json:"rw"`
}

// Image contains information about the mounted image.
type Image struct {
	Name      string `json:"name"`
	Size      uint64 `json:"size"`
	Complete  bool   `json:"complete"`
	InStorage bool   `json:"in_storage"` //nolint:tagliatelle // PiKVM API uses snake_case
}

// StorageImage contains information about an image in storage.
type StorageImage struct {
	Complete  bool    `json:"complete"`
	ModTS     float32 `json:"mod_ts"` //nolint:tagliatelle // PiKVM API uses snake_case
	Removable bool    `json:"removable"`
	Size      uint64  `json:"size"`
}

// StoragePart contains information about a storage partition.
type StoragePart struct {
	Free     uint64 `json:"free"`
	Size     uint64 `json:"size"`
	Writable bool   `json:"writable"`
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

func (p *PiKVM) handleMount(ctx context.Context, s module.Session, imagePath string) error {
	s.Printf("Preparing to mount image: %s\n", filepath.Base(imagePath))

	// Calculate SHA256 hash of the image
	hashSum, err := calcSHA256(imagePath)
	if err != nil {
		return fmt.Errorf("failed to calculate image hash: %v", err)
	}

	// Get file info for size
	fileInfo, err := getFileInfo(imagePath)
	if err != nil {
		return err
	}

	// Unplug USB if currently connected
	err = p.unplugUSBIfConnected(ctx, s)
	if err != nil {
		return err
	}

	// Upload image if not already in storage
	err = p.uploadImageIfMissing(ctx, s, imagePath, hashSum, fileInfo.Size())
	if err != nil {
		return err
	}

	// Configure and plug USB
	err = p.configureAndPlugUSB(ctx, s, hashSum)
	if err != nil {
		return err
	}

	s.Printf("Image mounted successfully: %s\n", filepath.Base(imagePath))

	return nil
}

// getFileInfo opens a file and returns its FileInfo.
func getFileInfo(path string) (os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image file: %v", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %v", err)
	}

	return fileInfo, nil
}

// unplugUSBIfConnected checks if USB is connected and unplugs it if necessary.
func (p *PiKVM) unplugUSBIfConnected(ctx context.Context, s module.Session) error {
	status, err := p.getStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to check USB plug state: %v", err)
	}

	if !status.Result.Drive.Connected {
		return nil
	}

	s.Println("Unplugging USB port...")

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected?connected=0", nil, "")
	if err != nil {
		return fmt.Errorf("failed to unplug USB device: %v", err)
	}

	resp.Body.Close()

	return nil
}

// uploadImageIfMissing checks if image exists and uploads it if missing.
func (p *PiKVM) uploadImageIfMissing(ctx context.Context, s module.Session, imagePath, hashSum string, size int64) error {
	err := p.checkImageExists(ctx, hashSum)
	if err == nil {
		s.Println("Image already exists in storage.")

		return nil
	}

	if !errors.Is(err, ErrMissingImage) {
		return fmt.Errorf("failed to check if image exists: %v", err)
	}

	s.Println("Image not found in storage, preparing to upload.")

	// Ensure there's enough free space
	err = p.ensureFreeSpace(ctx, s, size)
	if err != nil {
		return fmt.Errorf("failed to ensure free space: %v", err)
	}

	s.Printf("Uploading image file: %s (%d bytes)\n", filepath.Base(imagePath), size)

	// Open file for upload
	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed to open image file for upload: %v", err)
	}
	defer file.Close()

	// Upload the image
	uploadResp, err := p.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/msd/write?image=%s", hashSum), file, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("failed to upload image: %v", err)
	}

	uploadResp.Body.Close()

	s.Println("Image uploaded successfully.")

	return nil
}

// configureAndPlugUSB configures the USB device and plugs it in.
func (p *PiKVM) configureAndPlugUSB(ctx context.Context, s module.Session, hashSum string) error {
	s.Println("Configuring image...")

	configResp, err := p.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/msd/set_params?image=%s&cdrom=0&rw=1", hashSum), nil, "")
	if err != nil {
		return fmt.Errorf("failed to configure USB device: %v", err)
	}

	configResp.Body.Close()

	s.Println("Plugging USB port...")

	plugResp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected?connected=1", nil, "")
	if err != nil {
		return fmt.Errorf("failed to plug USB device: %v", err)
	}

	plugResp.Body.Close()

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

// getStatus retrieves the current MSD status from PiKVM.
func (p *PiKVM) getStatus(ctx context.Context) (*Status, error) {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/msd", nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get MSD status: %w", err)
	}
	defer resp.Body.Close()

	var status Status

	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		return nil, fmt.Errorf("failed to decode status: %w", err)
	}

	if !status.Ok {
		return nil, fmt.Errorf("status response not ok")
	}

	if status.Result.Busy {
		return nil, fmt.Errorf("PiKVM mass-storage is busy")
	}

	if !status.Result.Enabled || !status.Result.Online {
		return nil, fmt.Errorf("PiKVM mass-storage is not enabled or online")
	}

	return &status, nil
}

// calcSHA256 calculates the SHA256 hash of a file.
func calcSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()

	_, err = io.Copy(hash, file)
	if err != nil {
		return "", fmt.Errorf("failed to calculate SHA256: %w", err)
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// checkImageExists checks if an image with the given hash already exists in storage.
func (p *PiKVM) checkImageExists(ctx context.Context, hashSum string) error {
	status, err := p.getStatus(ctx)
	if err != nil {
		return err
	}

	for imageHash := range status.Result.Storage.Images {
		if imageHash == hashSum {
			return nil
		}
	}

	return ErrMissingImage
}

// getFreeSpace returns the total free space across all writable storage partitions.
func getFreeSpace(status *Status) (uint64, error) {
	if len(status.Result.Storage.Parts) == 0 {
		return 0, fmt.Errorf("no storage partitions available")
	}

	var totalFree uint64

	foundWritable := false

	for _, part := range status.Result.Storage.Parts {
		if part.Writable {
			totalFree += part.Free
			foundWritable = true
		}
	}

	if !foundWritable {
		return 0, fmt.Errorf("no writable storage partition found")
	}

	return totalFree, nil
}

// deleteImage deletes an image from PiKVM storage by its name (hash).
func (p *PiKVM) deleteImage(ctx context.Context, imageName string) error {
	resp, err := p.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/msd/remove?image=%s", imageName), nil, "")
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}
	defer resp.Body.Close()

	var response struct {
		Ok bool `json:"ok"`
	}

	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return fmt.Errorf("failed to decode delete response: %w", err)
	}

	if !response.Ok {
		return fmt.Errorf("delete operation failed")
	}

	return nil
}

// findOldestImage finds the image with the oldest modification timestamp.
// If skipImage is not empty, that image will be excluded from consideration.
func findOldestImage(status *Status, skipImage string) (string, error) {
	if len(status.Result.Storage.Images) == 0 {
		return "", fmt.Errorf("no images available to delete")
	}

	var oldestName string

	var oldestTime float32

	var found bool

	for name, img := range status.Result.Storage.Images {
		// Skip the image we want to exclude
		if name == skipImage {
			continue
		}

		if !found || img.ModTS < oldestTime {
			oldestTime = img.ModTS
			oldestName = name
			found = true
		}
	}

	if oldestName == "" {
		return "", fmt.Errorf("no deletable images available (only the currently connected image exists)")
	}

	return oldestName, nil
}

// ensureFreeSpace checks if there's enough free space for the given size.
// If not, it keeps deleting the oldest images until there's enough space.
// Currently connected image (if any) is not deleted.
func (p *PiKVM) ensureFreeSpace(ctx context.Context, s module.Session, requiredSize int64) error {
	// Validate input
	if requiredSize < 0 {
		return fmt.Errorf("invalid required size: %d", requiredSize)
	}

	deletionCount := 0

	requiredSizeUint := uint64(requiredSize) // #nosec G115 -- validated above

	// Keep deleting oldest images until we have enough space
	for {
		status, err := p.getStatus(ctx)
		if err != nil {
			return err
		}

		freeSpace, err := getFreeSpace(status)
		if err != nil {
			return fmt.Errorf("failed to get free space: %w", err)
		}

		// Check if we have enough space
		if requiredSizeUint+safetyMargin <= freeSpace {
			if deletionCount > 0 {
				s.Printf("Freed sufficient space by deleting %d old image(s).\n", deletionCount)
			}

			break
		}

		// Safety check: prevent infinite loop
		if deletionCount >= maxDeletionRetries {
			return fmt.Errorf(
				"insufficient storage space after attempting to delete %d image(s): need %d bytes, have %d bytes free",
				deletionCount, requiredSize, freeSpace,
			)
		}

		// Delete one old image and continue
		err = p.deleteOldImage(ctx, s, status, requiredSizeUint, freeSpace)
		if err != nil {
			return err
		}

		deletionCount++

		time.Sleep(scanningDelay) // wait a bit for the storage to update
	}

	return nil
}

// deleteOldImage finds and deletes the oldest image to free up space.
func (p *PiKVM) deleteOldImage(ctx context.Context, s module.Session, status *Status, requiredSize, freeSpace uint64) error {
	// Determine which image to skip (if currently connected)
	var skipImage string
	if status.Result.Drive.Connected {
		skipImage = status.Result.Drive.Image.Name
	}

	// Find the oldest image to delete
	oldestImage, err := findOldestImage(status, skipImage)
	if err != nil {
		return fmt.Errorf("insufficient storage space: need %d bytes, have %d bytes free: %w", requiredSize, freeSpace, err)
	}

	s.Printf("Deleting old image to free space (need %d MB, have %d MB free)...\n",
		(requiredSize+safetyMargin)/bytesPerMB, freeSpace/bytesPerMB)

	err = p.deleteImage(ctx, oldestImage)
	if err != nil {
		return fmt.Errorf("failed to delete oldest image %q to free space: %w", oldestImage, err)
	}

	return nil
}

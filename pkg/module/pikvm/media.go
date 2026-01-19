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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"
)

// Media command constants.
const (
	mount       = "mount"
	mountURL    = "mount-url"
	unmount     = "unmount"
	mediaStatus = "media-status"

	mediaNone = "None"
)

const (
	safetyMargin         = 10 * 1024 * 1024 // 10 MB safety margin
	scanningDelay        = 5 * time.Second  // Delay after deleting to allow PiKVM storage to update
	maxDeletionRetries   = 10               // Maximum number of images to delete before giving up
	bytesPerMB           = 1024 * 1024      // Bytes per megabyte
	imageWaitTimeout     = 60 * time.Second // Timeout waiting for image to be ready
	imageWaitInterval    = 1 * time.Second  // Interval between image status checks
	remoteDownloadTimout = 3600             // Timeout in seconds for remote image downloads
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

// handleMediaCommandRouter routes media commands based on the first argument.
func (p *PiKVM) handleMediaCommandRouter(ctx context.Context, s module.Session, args []string) error {
	if len(args) == 0 {
		s.Println("Media command requires an action: mount|mount-url|unmount|media-status")

		return nil
	}

	command := strings.ToLower(args[0])

	return p.handleMediaCommand(ctx, s, command, args)
}

// handleMediaCommand dispatches virtual media commands.
func (p *PiKVM) handleMediaCommand(ctx context.Context, s module.Session, command string, args []string) error {
	switch command {
	case mount:
		if len(args) < minArgsRequired {
			s.Println("Error: 'mount' command requires file path argument")

			return nil
		}

		// Check if hash and size were provided (optimized workflow)
		var precomputedHash string
		var precomputedSize int64

		if len(args) >= 3 {
			precomputedHash = args[2]
		}

		if len(args) >= 4 {
			if size, err := strconv.ParseInt(args[3], 10, 64); err == nil {
				precomputedSize = size
			}
		}

		return p.handleMount(ctx, s, args[1], precomputedHash, precomputedSize)
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
		return fmt.Errorf("unknown media action: %s (must be: mount, mount-url, unmount, media-status)", command)
	}
}

func (p *PiKVM) handleMount(ctx context.Context, s module.Session, imagePath string, precomputedHash string, precomputedSize int64) error {
	s.Printf("Preparing to mount image: %s\n", filepath.Base(imagePath))

	var hashSum string
	var fileSize int64

	// If hash was precomputed by client, use it to check PiKVM first
	if precomputedHash != "" {
		hashSum = precomputedHash
		fileSize = precomputedSize

		s.Printf("Using precomputed hash: %s\n", hashSum)

		// Check if image already exists on PiKVM
		err := p.checkImageExists(ctx, hashSum)
		if err == nil {
			s.Println("Image already exists on PiKVM, skipping upload.")

			// Unplug USB if currently connected
			err = p.unplugUSBIfConnected(ctx, s)
			if err != nil {
				return err
			}

			// Configure and plug USB using image hash
			err = p.configureAndPlugUSB(ctx, s, hashSum)
			if err != nil {
				return err
			}

			s.Printf("Image mounted successfully: %s\n", filepath.Base(imagePath))

			return nil
		}

		if !errors.Is(err, ErrMissingImage) {
			return fmt.Errorf("failed to check if image exists: %v", err)
		}

		s.Println("Image not found on PiKVM, will transfer from client.")
	}

	// Request file from client
	s.Println("Requesting file from client...")
	fileReader, err := s.RequestFile(imagePath)
	if err != nil {
		return fmt.Errorf("failed to request file from client: %w", err)
	}

	// Create temporary file on dutagent to store the image
	tmpFile, err := os.CreateTemp("", "pikvm-image-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // Clean up temp file when done

	// Copy file from client to temporary location
	s.Println("Transferring file from client...")
	bytesWritten, err := io.Copy(tmpFile, fileReader)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to transfer file from client: %w", err)
	}
	tmpFile.Close()

	s.Printf("Transferred %d bytes from client\n", bytesWritten)

	// If hash wasn't precomputed, calculate it now
	if hashSum == "" {
		hashSum, err = calcSHA256(tmpPath)
		if err != nil {
			return fmt.Errorf("failed to calculate image hash: %v", err)
		}

		s.Printf("Image hash: %s\n", hashSum)
		fileSize = bytesWritten
	}

	// Unplug USB if currently connected
	err = p.unplugUSBIfConnected(ctx, s)
	if err != nil {
		return err
	}

	// Upload image if not already in storage
	err = p.uploadImageIfMissing(ctx, s, tmpPath, hashSum, fileSize)
	if err != nil {
		return err
	}

	// Configure and plug USB using image hash
	err = p.configureAndPlugUSB(ctx, s, hashSum)
	if err != nil {
		return err
	}

	s.Printf("Image mounted successfully: %s\n", filepath.Base(imagePath))

	return nil
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

	resp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected?connected=0", nil, "", requestOptions{})
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

	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed to open image file for upload: %v", err)
	}
	defer file.Close()

	// Upload the image file
	err = p.uploadImageFile(ctx, file, hashSum, size, filepath.Base(imagePath))
	if err != nil {
		return err
	}

	s.Println("Image uploaded successfully.")

	return nil
}

// uploadImageFile uploads an image file to PiKVM storage using multipart upload.
func (p *PiKVM) uploadImageFile(ctx context.Context, file *os.File, hashSum string, size int64, filename string) error {
	// Build multipart header and footer without streaming the body
	var buf bytes.Buffer

	writer := multipart.NewWriter(&buf)

	// Create the form file header
	_, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	header := make([]byte, buf.Len())
	copy(header, buf.Bytes())

	// Close writer to emit the closing boundary
	err = writer.Close()
	if err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	footer := buf.Bytes()[len(header):]

	contentLength := int64(len(header)) + size + int64(len(footer))
	body := io.MultiReader(bytes.NewReader(header), file, bytes.NewReader(footer))

	uploadResp, err := p.doRequest(
		ctx,
		http.MethodPost,
		fmt.Sprintf("/api/msd/write?image=%s", hashSum),
		body,
		writer.FormDataContentType(),
		requestOptions{contentLength: contentLength, noTimeout: true},
	)
	if err != nil {
		return fmt.Errorf("failed to upload image: %v", err)
	}
	defer uploadResp.Body.Close()

	return nil
}

// configureAndPlugUSB configures the USB device with the given image and plugs it in.
// The imageName can be either a hash (for uploaded images) or a filename (for URL-mounted images).
func (p *PiKVM) configureAndPlugUSB(ctx context.Context, s module.Session, imageName string) error {
	s.Println("Configuring image...")

	configResp, err := p.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/msd/set_params?image=%s&cdrom=0&rw=1", imageName), nil, "", requestOptions{})
	if err != nil {
		return fmt.Errorf("failed to configure USB device: %v", err)
	}

	configResp.Body.Close()

	s.Println("Plugging USB port...")

	plugResp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected?connected=1", nil, "", requestOptions{})
	if err != nil {
		return fmt.Errorf("failed to plug USB device: %v", err)
	}

	plugResp.Body.Close()

	return nil
}

func (p *PiKVM) handleMountURL(ctx context.Context, s module.Session, imageURL string) error {
	s.Printf("Mounting image from URL: %s\n", imageURL)

	// Ensure MSD is disconnected before remote download
	err := p.unplugUSBIfConnected(ctx, s)
	if err != nil {
		return err
	}

	imageName := filepath.Base(imageURL)

	// If image already exists, skip download and just mount
	exists, err := p.checkImageAlreadyExists(ctx, imageName)
	if err == nil && exists {
		s.Printf("Warning: image %s already exists on PiKVM, reusing without download\n", imageName)

		return p.configureAndPlugUSB(ctx, s, imageName)
	}

	// Download the image from URL
	err = p.downloadRemoteImage(ctx, s, imageURL, imageName)
	if err != nil {
		return err
	}

	// Configure and connect the downloaded (or existing) image by name after it is present/complete
	err = p.waitForImage(ctx, imageName, imageWaitTimeout, imageWaitInterval)
	if err != nil {
		return err
	}

	err = p.configureAndPlugUSB(ctx, s, imageName)
	if err != nil {
		return err
	}

	s.Printf("Image mounted successfully from URL: %s\n", imageURL)

	return nil
}

// checkImageAlreadyExists checks if an image with the given name already exists and is complete.
func (p *PiKVM) checkImageAlreadyExists(ctx context.Context, imageName string) (bool, error) {
	status, err := p.getStatus(ctx)
	if err != nil {
		return false, err
	}

	img, ok := status.Result.Storage.Images[imageName]

	return ok && img.Complete, nil
}

// downloadRemoteImage downloads an image from a URL to PiKVM storage.
func (p *PiKVM) downloadRemoteImage(ctx context.Context, s module.Session, imageURL, imageName string) error {
	// Construct endpoint with URL encoding
	endpoint := fmt.Sprintf("/api/msd/write_remote?url=%s&timeout=%d&image=%s",
		url.QueryEscape(imageURL),
		remoteDownloadTimout,
		url.QueryEscape(imageName))

	resp, err := p.doRequest(ctx, http.MethodPost, endpoint, nil, "", requestOptions{noTimeout: true})
	if err != nil {
		return fmt.Errorf("failed to mount image from URL: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read write_remote response: %v", err)
	}

	return p.handleRemoteDownloadResponse(s, imageName, bodyBytes)
}

// remoteImageResponse represents the response from the PiKVM remote image download API.
type remoteImageResponse struct {
	Ok     bool `json:"ok"`
	Result struct {
		Error string `json:"error"`
	} `json:"result"`
}

// handleRemoteDownloadResponse parses and handles the streaming JSON response from remote download.
func (p *PiKVM) handleRemoteDownloadResponse(s module.Session, imageName string, bodyBytes []byte) error {
	res, err := parseStreamingJSON(bodyBytes)
	if err != nil {
		return err
	}

	if !res.Ok {
		if res.Result.Error == "MsdImageExistsError" {
			s.Printf("Warning: image %s already exists on PiKVM, reusing without download\n", imageName)

			return nil
		}

		return fmt.Errorf("failed to mount image from URL: %s", string(bodyBytes))
	}

	return nil
}

// parseStreamingJSON parses the last valid JSON line from a streaming response.
func parseStreamingJSON(bodyBytes []byte) (*remoteImageResponse, error) {
	var res remoteImageResponse

	// PiKVM streams JSON progress updates line-by-line during download
	// Parse the last valid JSON line as the final result
	lines := bytes.Split(bodyBytes, []byte("\n"))

	// Try from the last line backwards to find valid JSON
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}

		// Try to parse this line as JSON
		err := json.Unmarshal(line, &res)
		if err == nil {
			return &res, nil
		}
	}

	maxLen := 500
	if len(bodyBytes) < maxLen {
		maxLen = len(bodyBytes)
	}

	return nil, fmt.Errorf("no valid JSON found in write_remote response. Response (first %d bytes): %s",
		maxLen, string(bodyBytes[:maxLen]))
}

func (p *PiKVM) handleUnmount(ctx context.Context, s module.Session) error {
	resp, err := p.doRequest(ctx, http.MethodPost, "/api/msd/set_connected?connected=0", nil, "", requestOptions{})
	if err != nil {
		return fmt.Errorf("failed to unmount media: %v", err)
	}
	defer resp.Body.Close()

	s.Println("Virtual media unmounted successfully")

	return nil
}

func (p *PiKVM) handleMediaStatus(ctx context.Context, s module.Session) error {
	status, err := p.getStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get media status: %v", err)
	}

	connected := status.Result.Drive.Connected

	imageName := mediaNone
	if connected && status.Result.Drive.Image.Name != "" {
		imageName = status.Result.Drive.Image.Name
	}

	s.Printf("Virtual media status:\n")
	s.Printf("  Connected: %v\n", connected)
	s.Printf("  Image: %s\n", imageName)

	return nil
}

// getStatus retrieves the current MSD status from PiKVM.
func (p *PiKVM) getStatus(ctx context.Context) (*Status, error) {
	resp, err := p.doRequest(ctx, http.MethodGet, "/api/msd", nil, "", requestOptions{})
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
	resp, err := p.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/msd/remove?image=%s", imageName), nil, "", requestOptions{})
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

	return err
}

// waitForImage polls MSD status until the image is present and complete or times out.
func (p *PiKVM) waitForImage(ctx context.Context, imageName string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		status, err := p.getStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get status while waiting for image: %w", err)
		}

		if img, ok := status.Result.Storage.Images[imageName]; ok && img.Complete {
			return nil
		}

		time.Sleep(interval)
	}

	return fmt.Errorf("image %s not ready after %v", imageName, timeout)
}

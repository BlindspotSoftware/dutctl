// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"connectrpc.com/connect"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	clientChunkSize   = 1024 * 1024 // 1MB chunks
	downloadFilePerms = 0o600       // Downloaded file permissions (user read/write only)
)

// StreamForClient is a type alias for the stream connection to reduce line length.
type StreamForClient = *connect.BidiStreamForClient[pb.RunRequest, pb.RunResponse]

// clientFileTransferState represents an active file transfer on the client.
type clientFileTransferState struct {
	transferID       string
	path             string
	file             *os.File
	direction        string // "upload" or "download"
	expectedChunkNum int32  // For validating chunk sequence on download
	mu               sync.Mutex
}

// clientFileTransferManager manages file transfers on the client side.
type clientFileTransferManager struct {
	transfers map[string]*clientFileTransferState
	mu        sync.RWMutex
	cmdArgs   []string // Command arguments for file path validation
}

func newClientFileTransferManager(cmdArgs []string) *clientFileTransferManager {
	return &clientFileTransferManager{
		transfers: make(map[string]*clientFileTransferState),
		cmdArgs:   cmdArgs,
	}
}

func (m *clientFileTransferManager) registerTransfer(transferID, path, direction string) *clientFileTransferState {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := &clientFileTransferState{
		transferID: transferID,
		path:       path,
		direction:  direction,
	}

	m.transfers[transferID] = state

	return state
}

func (m *clientFileTransferManager) getTransfer(transferID string) *clientFileTransferState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.transfers[transferID]
}

func (m *clientFileTransferManager) removeTransfer(transferID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state, exists := m.transfers[transferID]; exists {
		if state.file != nil {
			state.file.Close()
		}

		delete(m.transfers, transferID)
	}
}

// normalizePath expands ~ and converts to absolute path for consistent comparison.
// Returns the normalized path or logs error and returns original path.
func normalizePath(path string) string {
	// Expand ~ to home directory
	expanded := path
	if path != "" && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Printf("Warning: could not expand ~: %v, using path as-is", err)

			return path
		}

		expanded = filepath.Join(home, path[1:])
	}

	// Convert to absolute path
	abs, err := filepath.Abs(expanded)
	if err != nil {
		log.Printf("Warning: could not convert to absolute path %q: %v, using expanded path", expanded, err)

		return expanded
	}

	return abs
}

// isValidPath checks if a file path is explicitly mentioned in the command arguments.
// Normalizes both paths (expands ~ and converts to absolute) before comparison.
func (m *clientFileTransferManager) isValidPath(path string) bool {
	normalizedPath := normalizePath(path)

	for _, arg := range m.cmdArgs {
		normalizedArg := normalizePath(arg)

		if normalizedArg == normalizedPath {
			return true
		}
	}

	return false
}

// sendChunkToAgent sends a file chunk to the agent.
func (m *clientFileTransferManager) sendChunkToAgent(
	transferID string,
	chunkNum int32,
	data []byte,
	isFinal bool,
	stream StreamForClient,
) error {
	chunk := &pb.RunRequest{
		Msg: &pb.RunRequest_FileChunk{
			FileChunk: &pb.FileChunk{
				TransferId:  transferID,
				ChunkNumber: chunkNum,
				ChunkData:   data,
				ChunkOffset: int64(chunkNum) * int64(clientChunkSize),
				IsFinal:     isFinal,
			},
		},
	}

	return stream.Send(chunk)
}

// handleUploadRequest processes a request to upload a file to the agent.
func (m *clientFileTransferManager) handleUploadRequest(transferID, path string, stream StreamForClient) error {
	// Validate that the requested file is in the command arguments
	if !m.isValidPath(path) {
		errMsg := fmt.Sprintf("file %q not specified in command arguments - security violation prevented", path)
		log.Printf("Error: %s", errMsg)

		rejectErr := m.sendTransferError(transferID, errMsg, stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	_, statErr := os.Stat(path)
	if statErr != nil {
		log.Printf("Error accessing file %q: %v", path, statErr)

		rejectErr := m.sendTransferError(transferID, fmt.Sprintf("cannot access file: %v", statErr), stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		log.Printf("Error opening file %q: %v", path, err)

		rejectErr := m.sendTransferError(transferID, fmt.Sprintf("cannot open file: %v", err), stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	state := m.registerTransfer(transferID, path, "upload")
	state.file = file

	acceptErr := m.sendTransferAcceptance(transferID, stream)
	if acceptErr != nil {
		file.Close()
		m.removeTransfer(transferID)

		return fmt.Errorf("sending transfer acceptance: %w", acceptErr)
	}

	log.Printf("Uploading %q to device...", filepath.Base(path))

	m.sendUploadInChunks(transferID, path, file, stream)

	return nil
}

// handleDownloadRequest processes a request to download a file from the agent.
// The agent specifies what file it will send, and the destination path from
// command arguments is where we should save it.
func (m *clientFileTransferManager) handleDownloadRequest(transferID, destinationPath string, stream StreamForClient) error {
	// Validate that the destination file path is in the command arguments
	if !m.isValidPath(destinationPath) {
		errMsg := fmt.Sprintf("file %q not specified in command arguments - security violation prevented", destinationPath)
		log.Printf("Error: %s", errMsg)

		rejectErr := m.sendTransferError(transferID, errMsg, stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	// Register the download transfer
	m.registerTransfer(transferID, destinationPath, "download")

	log.Printf("Downloading file to %q...", filepath.Base(destinationPath))

	// Send acceptance to agent
	acceptErr := m.sendTransferAcceptance(transferID, stream)
	if acceptErr != nil {
		m.removeTransfer(transferID)

		return fmt.Errorf("sending transfer acceptance: %w", acceptErr)
	}

	return nil
}

// sendTransferAcceptance sends a transfer acceptance response.
func (m *clientFileTransferManager) sendTransferAcceptance(transferID string, stream StreamForClient) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId: transferID,
				Status:     pb.FileTransferResponse_ACCEPTED,
			},
		},
	}

	return stream.Send(res)
}

// sendUploadInChunks reads and sends a file in chunks to the agent.
func (m *clientFileTransferManager) sendUploadInChunks(transferID, path string, file *os.File, stream StreamForClient) {
	go func() {
		defer file.Close()
		defer m.removeTransfer(transferID)

		chunkNum := int32(0)

		for {
			chunkData := make([]byte, clientChunkSize)
			bytesRead, readErr := file.Read(chunkData)

			if readErr != nil && readErr != io.EOF {
				log.Printf("Error reading file %q: %v", path, readErr)

				return
			}

			isFinal := readErr == io.EOF

			if bytesRead > 0 {
				chunkData = chunkData[:bytesRead]
			} else {
				// Final empty chunk to signal EOF
				chunkData = []byte{}
			}

			chunkErr := m.sendChunkToAgent(transferID, chunkNum, chunkData, isFinal, stream)
			if chunkErr != nil {
				log.Printf("Error sending file chunk: %v", chunkErr)

				return
			}

			chunkNum++

			if isFinal {
				break
			}
		}
	}()
}

// handleFileTransferRequest handles a FileTransferRequest from the agent.
// This can be either:
// 1. A request for the client to upload a file to the agent (agent requesting from client)
// 2. A notification that the agent will send a file download
// The direction is explicitly specified in the FileTransferRequest message.
func (m *clientFileTransferManager) handleFileTransferRequest(ftReq *pb.FileTransferRequest, stream StreamForClient) error {
	transferID := ftReq.GetTransferId()
	metadata := ftReq.GetMetadata()
	path := metadata.GetPath()
	direction := ftReq.GetDirection()

	switch direction {
	case pb.FileTransferRequest_UPLOAD:
		// Agent is requesting a file from the client (client uploads to agent)
		return m.handleUploadRequest(transferID, path, stream)

	case pb.FileTransferRequest_DOWNLOAD:
		// Agent is sending a file to the client (client downloads from agent)
		return m.handleDownloadRequest(transferID, path, stream)

	default:
		// Unspecified or unknown direction
		errMsg := fmt.Sprintf("unknown transfer direction %v (agent/client version mismatch?)", direction)
		log.Printf("Error: %s", errMsg)

		return m.sendTransferError(transferID, errMsg, stream)
	}
}

// sendTransferError sends an error response for a failed transfer.
func (m *clientFileTransferManager) sendTransferError(transferID, message string, stream StreamForClient) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:   transferID,
				Status:       pb.FileTransferResponse_TRANSFER_REJECTED,
				ErrorMessage: message,
			},
		},
	}

	return stream.Send(res)
}

// sendChunkAcknowledgment sends an acknowledgment for a received chunk.
func (m *clientFileTransferManager) sendChunkAcknowledgment(transferID string, nextChunk int32, stream StreamForClient) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:        transferID,
				Status:            pb.FileTransferResponse_CHUNK_RECEIVED,
				NextChunkExpected: nextChunk,
			},
		},
	}

	return stream.Send(res)
}

// sendTransferComplete sends a transfer completion response.
func (m *clientFileTransferManager) sendTransferComplete(transferID string, stream StreamForClient) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId: transferID,
				Status:     pb.FileTransferResponse_TRANSFER_COMPLETE,
			},
		},
	}

	return stream.Send(res)
}

// validateChunkTransfer validates transfer existence, path authorization, and chunk sequence.
// Returns error if validation fails (error already sent to stream), nil otherwise.
func (m *clientFileTransferManager) validateChunkTransfer(
	chunk *pb.FileChunk,
	stream StreamForClient,
) (*clientFileTransferState, error) {
	transferID := chunk.GetTransferId()

	// Validate transfer exists.
	state := m.getTransfer(transferID)
	if state == nil {
		log.Printf("Error: received chunk for unknown transfer %s", transferID)

		sendErr := m.sendTransferError(transferID, "unknown transfer", stream)
		if sendErr != nil {
			return nil, fmt.Errorf("sending error response: %w", sendErr)
		}

		return nil, fmt.Errorf("unknown transfer")
	}

	// Validate path is authorized.
	if !m.isValidPath(state.path) {
		errMsg := fmt.Sprintf("file %q not in command arguments", state.path)
		log.Printf("Error: %s", errMsg)

		m.removeTransfer(transferID)

		sendErr := m.sendTransferError(transferID, errMsg, stream)
		if sendErr != nil {
			return nil, fmt.Errorf("sending error response: %w", sendErr)
		}

		return nil, fmt.Errorf("path not authorized")
	}

	// Validate chunk sequence.
	state.mu.Lock()

	if chunk.GetChunkNumber() != state.expectedChunkNum {
		expected := state.expectedChunkNum
		state.mu.Unlock()

		log.Printf("Error: chunk order violation for %s: expected %d, got %d",
			transferID, expected, chunk.GetChunkNumber())

		m.removeTransfer(transferID)

		sendErr := m.sendTransferError(transferID, "chunk sequence error", stream)
		if sendErr != nil {
			return nil, fmt.Errorf("sending error response: %w", sendErr)
		}

		return nil, fmt.Errorf("chunk sequence error")
	}

	state.mu.Unlock()

	return state, nil
}

// processChunkData creates file if needed and writes chunk data.
func (m *clientFileTransferManager) processChunkData(
	chunk *pb.FileChunk,
	state *clientFileTransferState,
	stream StreamForClient,
) error {
	transferID := chunk.GetTransferId()

	// Create file on first chunk.
	if chunk.GetChunkNumber() == 0 {
		file, err := os.OpenFile(state.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, downloadFilePerms)
		if err != nil {
			log.Printf("Error creating download file: %v", err)

			m.removeTransfer(transferID)

			sendErr := m.sendTransferError(transferID, fmt.Sprintf("cannot create file: %v", err), stream)
			if sendErr != nil {
				return fmt.Errorf("sending error response: %w", sendErr)
			}

			return fmt.Errorf("cannot create file")
		}

		state.mu.Lock()
		state.file = file
		state.mu.Unlock()
	}

	// Write chunk data.
	state.mu.Lock()
	file := state.file
	state.mu.Unlock()

	if file != nil && len(chunk.GetChunkData()) > 0 {
		_, writeErr := file.Write(chunk.GetChunkData())
		if writeErr != nil {
			log.Printf("Error writing to file: %v", writeErr)

			m.removeTransfer(transferID)

			sendErr := m.sendTransferError(transferID, fmt.Sprintf("write error: %v", writeErr), stream)
			if sendErr != nil {
				return fmt.Errorf("sending error response: %w", sendErr)
			}

			return fmt.Errorf("write error")
		}
	}

	return nil
}

// handleFileChunk handles a FileChunk from the agent (file download).
//
//nolint:nilerr // errors are already sent to stream, returning nil to continue processing
func (m *clientFileTransferManager) handleFileChunk(chunk *pb.FileChunk, stream StreamForClient) error {
	transferID := chunk.GetTransferId()

	state, err := m.validateChunkTransfer(chunk, stream)
	if err != nil {
		// Validation failed, error already sent to stream
		return nil
	}

	err = m.processChunkData(chunk, state, stream)
	if err != nil {
		// Error occurred, error already sent to stream
		return nil
	}

	// Update expected chunk number.
	state.mu.Lock()
	state.expectedChunkNum++
	state.mu.Unlock()

	// Send acknowledgment.
	ackErr := m.sendChunkAcknowledgment(transferID, chunk.GetChunkNumber()+1, stream)
	if ackErr != nil {
		return fmt.Errorf("sending chunk ack: %w", ackErr)
	}

	// Final chunk: close file and send completion.
	if chunk.GetIsFinal() {
		completeErr := m.sendTransferComplete(transferID, stream)
		if completeErr != nil {
			return fmt.Errorf("sending completion: %w", completeErr)
		}

		m.removeTransfer(transferID)
	}

	return nil
}

// handleFileTransferResponse handles a FileTransferResponse from the agent (acknowledgments).
// Silently processes responses; only logs errors.
func (m *clientFileTransferManager) handleFileTransferResponse(ftRes *pb.FileTransferResponse) {
	transferID := ftRes.GetTransferId()
	status := ftRes.GetStatus()

	switch status {
	case pb.FileTransferResponse_ERROR:
		log.Printf("File transfer error for %s: %s", transferID, ftRes.GetErrorMessage())
		m.removeTransfer(transferID)
	case pb.FileTransferResponse_TRANSFER_COMPLETE:
		m.removeTransfer(transferID)
	}
	// Other statuses (CHUNK_RECEIVED, ACCEPTED) are silently processed
}

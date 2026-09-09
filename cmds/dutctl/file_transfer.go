// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	clientChunkSize   = 1024 * 1024 // 1MB chunks
	downloadFilePerms = 0o600       // Downloaded file permissions (user read/write only)
)

// chunkStream is the send side of the run stream the file-transfer manager needs.
// sendStream satisfies it, and a test can substitute a recording fake.
type chunkStream interface {
	Send(req *pb.RunRequest) error
}

// sendStream serializes all sends on the run stream. Connect permits Send and
// Receive from separate goroutines but forbids concurrent Send calls, and the
// client sends from three: the receive loop (transfer acknowledgments), the
// stdin loop, and the goroutine sendUploadInChunks spawns. Receive is not
// wrapped — it stays on the raw stream, which is what makes the split legal.
type sendStream struct {
	mu     sync.Mutex
	stream chunkStream
}

func newSendStream(stream chunkStream) *sendStream {
	return &sendStream{stream: stream}
}

func (s *sendStream) Send(req *pb.RunRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stream.Send(req)
}

// clientFileTransferState represents an active file transfer on the client.
type clientFileTransferState struct {
	transferID       string
	path             string
	file             *os.File
	direction        string // "upload" or "download"
	expectedChunkNum int32  // For validating chunk sequence on download
	mu               sync.Mutex

	// ackCh carries the agent's per-chunk acknowledgment to the upload sender.
	// Buffered to one: the protocol keeps a single chunk outstanding, so the
	// sender is always the one waiting.
	ackCh chan int32

	// abort is closed when the transfer is dropped, so a sender waiting on an
	// acknowledgment that will never arrive stops instead of leaking.
	abort     chan struct{}
	abortOnce sync.Once
}

// stop releases anyone waiting on this transfer. Safe to call repeatedly.
func (s *clientFileTransferState) stop() {
	s.abortOnce.Do(func() { close(s.abort) })
}

// clientFileTransferManager manages file transfers on the client side.
type clientFileTransferManager struct {
	transfers map[string]*clientFileTransferState
	mu        sync.RWMutex

	// allowed holds the command arguments normalised once at construction. Every
	// chunk is checked against it, so normalising per lookup would repeat
	// filepath.Abs for each argument on every megabyte transferred.
	allowed map[string]struct{}
}

func newClientFileTransferManager(cmdArgs []string) *clientFileTransferManager {
	allowed := make(map[string]struct{}, len(cmdArgs))
	for _, arg := range cmdArgs {
		allowed[normalizePath(arg)] = struct{}{}
	}

	return &clientFileTransferManager{
		transfers: make(map[string]*clientFileTransferState),
		allowed:   allowed,
	}
}

func (m *clientFileTransferManager) registerTransfer(transferID, path, direction string) *clientFileTransferState {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := &clientFileTransferState{
		transferID: transferID,
		path:       path,
		direction:  direction,
		ackCh:      make(chan int32, 1),
		abort:      make(chan struct{}),
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
		state.stop()

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
			slog.Warn("could not expand ~, using path as-is", "err", err)

			return path
		}

		expanded = filepath.Join(home, path[1:])
	}

	// Convert to absolute path
	abs, err := filepath.Abs(expanded)
	if err != nil {
		slog.Warn("could not convert to absolute path, using expanded path", "path", expanded, "err", err)

		return expanded
	}

	return abs
}

// isValidPath reports whether path is one the user named on the command line.
// The candidate is normalised (~ expanded, made absolute) to match the
// allow-list, which was normalised the same way at construction.
func (m *clientFileTransferManager) isValidPath(path string) bool {
	_, ok := m.allowed[normalizePath(path)]

	return ok
}

// sendChunkToAgent sends a file chunk to the agent.
func (m *clientFileTransferManager) sendChunkToAgent(
	transferID string,
	chunkNum int32,
	data []byte,
	isFinal bool,
	stream chunkStream,
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
func (m *clientFileTransferManager) handleUploadRequest(transferID, path string, stream chunkStream) error {
	// Validate that the requested file is in the command arguments
	if !m.isValidPath(path) {
		errMsg := fmt.Sprintf("file %q not specified in command arguments - security violation prevented", path)
		slog.Warn(errMsg)

		rejectErr := m.sendTransferError(transferID, errMsg, stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		slog.Warn("error accessing file", "path", path, "err", statErr)

		rejectErr := m.sendTransferError(transferID, fmt.Sprintf("cannot access file: %v", statErr), stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		slog.Warn("error opening file", "path", path, "err", err)

		rejectErr := m.sendTransferError(transferID, fmt.Sprintf("cannot open file: %v", err), stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	state := m.registerTransfer(transferID, path, "upload")
	state.file = file

	// Answer with the file's real metadata rather than a bare acceptance: the
	// agent opened this transfer knowing only the path it asked for, so the size
	// is the client's to report.
	acceptErr := m.sendUploadMetadata(transferID, path, info.Size(), stream)
	if acceptErr != nil {
		file.Close()
		m.removeTransfer(transferID)

		return fmt.Errorf("announcing upload: %w", acceptErr)
	}

	slog.Debug("uploading file to device", "file", filepath.Base(path))

	m.sendUploadInChunks(transferID, path, file, state, stream)

	return nil
}

// handleDownloadRequest processes a request to download a file from the agent.
// The agent specifies what file it will send, and the destination path from
// command arguments is where we should save it.
func (m *clientFileTransferManager) handleDownloadRequest(transferID, destinationPath string, stream chunkStream) error {
	// Validate that the destination file path is in the command arguments
	if !m.isValidPath(destinationPath) {
		errMsg := fmt.Sprintf("file %q not specified in command arguments - security violation prevented", destinationPath)
		slog.Warn(errMsg)

		rejectErr := m.sendTransferError(transferID, errMsg, stream)
		if rejectErr != nil {
			return fmt.Errorf("sending transfer rejection: %w", rejectErr)
		}

		return nil
	}

	// Register the download transfer
	m.registerTransfer(transferID, destinationPath, "download")

	slog.Debug("downloading file", "file", filepath.Base(destinationPath))

	// Send acceptance to agent
	acceptErr := m.sendTransferAcceptance(transferID, stream)
	if acceptErr != nil {
		m.removeTransfer(transferID)

		return fmt.Errorf("sending transfer acceptance: %w", acceptErr)
	}

	return nil
}

// sendUploadMetadata accepts an upload request by echoing it back with the
// file's measured size, which the agent records against the transfer.
func (m *clientFileTransferManager) sendUploadMetadata(
	transferID, path string,
	size int64,
	stream chunkStream,
) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferRequest{
			FileTransferRequest: &pb.FileTransferRequest{
				TransferId: transferID,
				Direction:  pb.FileTransferRequest_DIRECTION_UPLOAD,
				Metadata: &pb.FileMetadata{
					Path: path,
					Name: filepath.Base(path),
					Size: size,
				},
			},
		},
	}

	return stream.Send(res)
}

// sendTransferAcceptance sends a transfer acceptance response.
func (m *clientFileTransferManager) sendTransferAcceptance(transferID string, stream chunkStream) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId: transferID,
				Status:     pb.FileTransferResponse_STATUS_ACCEPTED,
			},
		},
	}

	return stream.Send(res)
}

// sendUploadInChunks reads and sends a file in chunks to the agent, keeping one
// chunk outstanding at a time: each chunk waits for the agent's CHUNK_RECEIVED
// before the next is read and sent.
//
// The agent acknowledges a chunk only once the module has consumed it, so this
// is genuine end-to-end backpressure — the client cannot outrun the module, and
// the client's memory stays bounded at one chunk regardless of file size. The
// cost is one round trip per megabyte, which is far below the rate at which a
// module writes a chunk to hardware.
func (m *clientFileTransferManager) sendUploadInChunks(
	transferID, path string,
	file *os.File,
	state *clientFileTransferState,
	stream chunkStream,
) {
	go func() {
		defer file.Close()
		defer m.removeTransfer(transferID)

		chunkNum := int32(0)

		for {
			chunkData := make([]byte, clientChunkSize)
			bytesRead, readErr := file.Read(chunkData)

			isFinal := errors.Is(readErr, io.EOF)
			if readErr != nil && !isFinal {
				slog.Warn("error reading file", "path", path, "err", readErr)

				return
			}

			if bytesRead > 0 {
				chunkData = chunkData[:bytesRead]
			} else {
				// Final empty chunk to signal EOF
				chunkData = []byte{}
			}

			chunkErr := m.sendChunkToAgent(transferID, chunkNum, chunkData, isFinal, stream)
			if chunkErr != nil {
				slog.Warn("error sending file chunk", "err", chunkErr)

				return
			}

			chunkNum++

			// The final chunk is answered with TRANSFER_COMPLETE, not an ack.
			if isFinal {
				return
			}

			if !waitForChunkAck(state, chunkNum) {
				return
			}
		}
	}()
}

// waitForChunkAck blocks until the agent acknowledges the chunk before wantNext,
// or the transfer is dropped. It reports whether sending should continue.
//
// The transfer's abort channel is the only exit besides the acknowledgment: it
// closes when the transfer is removed, which covers a rejection, a stream error
// and the end of the run. Without it a sender would wait forever for an
// acknowledgment the agent can no longer send.
func waitForChunkAck(state *clientFileTransferState, wantNext int32) bool {
	for {
		select {
		case <-state.abort:
			slog.Debug("upload stopped while awaiting acknowledgment",
				"transfer_id", state.transferID, "chunk", wantNext-1)

			return false
		case next := <-state.ackCh:
			// Ignore a stale acknowledgment for a chunk already past; only the one
			// for the chunk in flight releases the sender.
			if next >= wantNext {
				return true
			}
		}
	}
}

// handleFileTransferRequest handles a FileTransferRequest from the agent.
// This can be either:
// 1. A request for the client to upload a file to the agent (agent requesting from client)
// 2. A notification that the agent will send a file download
// The direction is explicitly specified in the FileTransferRequest message.
func (m *clientFileTransferManager) handleFileTransferRequest(ftReq *pb.FileTransferRequest, stream chunkStream) error {
	transferID := ftReq.GetTransferId()
	metadata := ftReq.GetMetadata()
	path := metadata.GetPath()
	direction := ftReq.GetDirection()

	switch direction {
	case pb.FileTransferRequest_DIRECTION_UPLOAD:
		// Agent is requesting a file from the client (client uploads to agent)
		return m.handleUploadRequest(transferID, path, stream)

	case pb.FileTransferRequest_DIRECTION_DOWNLOAD:
		// Agent is sending a file to the client (client downloads from agent)
		return m.handleDownloadRequest(transferID, path, stream)

	default:
		// Unspecified or unknown direction
		errMsg := fmt.Sprintf("unknown transfer direction %v (agent/client version mismatch?)", direction)
		slog.Warn(errMsg)

		return m.sendTransferError(transferID, errMsg, stream)
	}
}

// sendTransferError sends an error response for a failed transfer.
func (m *clientFileTransferManager) sendTransferError(transferID, message string, stream chunkStream) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:   transferID,
				Status:       pb.FileTransferResponse_STATUS_TRANSFER_REJECTED,
				ErrorMessage: message,
			},
		},
	}

	return stream.Send(res)
}

// sendChunkAcknowledgment sends an acknowledgment for a received chunk.
func (m *clientFileTransferManager) sendChunkAcknowledgment(transferID string, nextChunk int32, stream chunkStream) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:        transferID,
				Status:            pb.FileTransferResponse_STATUS_CHUNK_RECEIVED,
				NextChunkExpected: nextChunk,
			},
		},
	}

	return stream.Send(res)
}

// sendTransferComplete sends a transfer completion response.
func (m *clientFileTransferManager) sendTransferComplete(transferID string, stream chunkStream) error {
	res := &pb.RunRequest{
		Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId: transferID,
				Status:     pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
			},
		},
	}

	return stream.Send(res)
}

// validateChunkTransfer validates transfer existence, path authorization, and chunk sequence.
// Returns error if validation fails (error already sent to stream), nil otherwise.
func (m *clientFileTransferManager) validateChunkTransfer(
	chunk *pb.FileChunk,
	stream chunkStream,
) (*clientFileTransferState, error) {
	transferID := chunk.GetTransferId()

	// Validate transfer exists.
	state := m.getTransfer(transferID)
	if state == nil {
		slog.Warn("received chunk for unknown transfer", "transfer_id", transferID)

		sendErr := m.sendTransferError(transferID, "unknown transfer", stream)
		if sendErr != nil {
			return nil, fmt.Errorf("sending error response: %w", sendErr)
		}

		return nil, fmt.Errorf("unknown transfer")
	}

	// Re-check the path on every chunk. It is redundant with the check made when
	// the transfer was registered, and deliberately so: this is the control that
	// stops the agent writing outside the paths the user named, and it is cheap
	// now that the allow-list is normalised once (see newClientFileTransferManager).
	if !m.isValidPath(state.path) {
		errMsg := fmt.Sprintf("file %q not in command arguments", state.path)
		slog.Warn(errMsg)

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

		slog.Warn("chunk order violation",
			"transfer_id", transferID, "expected", expected, "got", chunk.GetChunkNumber())

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
	stream chunkStream,
) error {
	transferID := chunk.GetTransferId()

	// Create file on first chunk.
	if chunk.GetChunkNumber() == 0 {
		file, err := os.OpenFile(state.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, downloadFilePerms)
		if err != nil {
			slog.Warn("error creating download file", "err", err)

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
			slog.Warn("error writing to file", "err", writeErr)

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
func (m *clientFileTransferManager) handleFileChunk(chunk *pb.FileChunk, stream chunkStream) error {
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
	case pb.FileTransferResponse_STATUS_ERROR:
		slog.Warn("file transfer error", "transfer_id", transferID, "message", ftRes.GetErrorMessage())
		m.removeTransfer(transferID)
	case pb.FileTransferResponse_STATUS_TRANSFER_REJECTED:
		// The agent refused the transfer. Dropping the state closes the file the
		// upload path opened; leaving it would hold the descriptor until exit.
		slog.Warn("file transfer rejected by agent",
			"transfer_id", transferID, "reason", ftRes.GetErrorMessage())
		m.removeTransfer(transferID)
	case pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE:
		m.removeTransfer(transferID)
	case pb.FileTransferResponse_STATUS_CHUNK_RECEIVED:
		// Release the upload sender to read and send the next chunk. A
		// non-blocking send keeps the receive loop moving: the channel is buffered
		// to one and only ever holds the acknowledgment for the chunk in flight.
		if state := m.getTransfer(transferID); state != nil {
			select {
			case state.ackCh <- ftRes.GetNextChunkExpected():
			default:
			}
		}
	case pb.FileTransferResponse_STATUS_ACCEPTED:
		// The agent is ready; the sender is already running.
	case pb.FileTransferResponse_STATUS_UNSPECIFIED:
		slog.Warn("file transfer response without a status", "transfer_id", transferID)
	}
}

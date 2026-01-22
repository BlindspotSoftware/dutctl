// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/google/uuid"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const (
	// chunkSize is the maximum size of a single file chunk (1MB).
	chunkSize = 1024 * 1024

	// channelBufferSize is the buffer size for internal channels.
	channelBufferSize = 128
)

// uploadState represents an active upload from client to agent.
type uploadState struct {
	transferID  string
	metadata    *pb.FileMetadata
	lastChunk   int32 // Track last received chunk number for sequence validation
	complete    bool
	file        *io.PipeWriter
	reader      *io.PipeReader
	requestSent bool // Track if initial FileTransferRequest has been sent
	mu          sync.Mutex
}

// downloadState represents an active download from agent to client.
type downloadState struct {
	transferID       string
	metadata         *pb.FileMetadata
	reader           io.Reader
	chunkNumber      int32 // Chunk being sent
	awaitingFinalAck bool  // Waiting for client TRANSFER_COMPLETE
}

// session implements the module.Session interface.
type session struct {
	printCh    chan string
	stdinCh    chan []byte
	stdoutCh   chan []byte
	stderrCh   chan []byte
	shutdownCh chan struct{} // Graceful shutdown signal

	// File transfer tracking
	activeUploads   map[string]*uploadState   // transferID -> upload state
	activeDownloads map[string]*downloadState // transferID -> download state
	uploadMutex     sync.RWMutex
	downloadMutex   sync.RWMutex

	// Shutdown state tracking
	shutdownMutex       sync.Mutex
	isShuttingDown      bool          // True when module done, waiting for transfers
	allTransfersDoneCh  chan struct{} // Closed when all transfers complete during shutdown
	activeTransferCount int           // Number of active transfers

	// Round-robin scheduling for concurrent downloads.
	downloadOrder []string
	downloadIndex int
}

func (s *session) Print(a ...any) {
	s.printCh <- fmt.Sprint(a...)
}

func (s *session) Printf(format string, a ...any) {
	s.printCh <- fmt.Sprintf(format, a...)
}

func (s *session) Println(a ...any) {
	s.printCh <- fmt.Sprintln(a...)
}

// Console returns readers and writers for interactive console I/O.
//
//nolint:nonamedreturns
func (s *session) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	stdinReader, err = chanio.NewChanReader(s.stdinCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdinReader: %v", err)
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdoutCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stdoutWriter: %v", err)
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderrCh)
	if err != nil {
		log.Fatalf("session.Console() failed to create stderrWriter: %v", err)
	}

	return stdinReader, stdoutWriter, stderrWriter
}

// RequestFile requests a file from the client.
// It sends a FileTransferRequest and returns a reader that streams the file chunks.
func (s *session) RequestFile(name string) (io.Reader, error) {
	transferID := uuid.New().String()

	log.Printf("Module issued file request for: %q (transfer_id=%s)", name, transferID)

	// Try to stat the file to get size
	var fileSize int64

	fileInfo, err := os.Stat(name)
	if err == nil {
		fileSize = fileInfo.Size()
	}
	// If stat fails, Size defaults to 0 (client treats as unknown)

	// Create upload state with pipe for streaming
	reader, writer := io.Pipe()
	state := &uploadState{
		transferID: transferID,
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: fileSize,
		},
		file:      writer,
		reader:    reader,
		lastChunk: -1,
	}

	s.uploadMutex.Lock()
	s.activeUploads[transferID] = state
	s.uploadMutex.Unlock()

	// Track this transfer for graceful shutdown
	s.trackTransferStart()

	return state.reader, nil
}

// SendFile sends a file to the client.
// It chunks the file and manages the transfer state.
// The size parameter should be the total file size in bytes.
func (s *session) SendFile(name string, size int64, r io.Reader) error {
	transferID := uuid.New().String()

	log.Printf("Module issued file send for: %q, size: %d bytes (transfer_id=%s)", name, size, transferID)

	// Register download
	state := &downloadState{
		transferID: transferID,
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: size,
		},
		reader:      r,
		chunkNumber: 0,
	}

	s.downloadMutex.Lock()
	s.activeDownloads[transferID] = state
	s.downloadOrder = append(s.downloadOrder, transferID)
	s.downloadMutex.Unlock()

	// Track this transfer for graceful shutdown
	s.trackTransferStart()

	return nil
}

// getNextChunk returns the next chunk for a download in round-robin fashion.
// Returns the chunk, a flag indicating if this is the final chunk, and any error.
func (s *session) getNextChunk(transferID string) (*pb.FileChunk, bool, error) {
	s.downloadMutex.RLock()
	state, exists := s.activeDownloads[transferID]
	s.downloadMutex.RUnlock()

	if !exists {
		return nil, false, fmt.Errorf("download not found: %s", transferID)
	}

	// Read next chunk from reader
	chunkData := make([]byte, chunkSize)
	n, err := state.reader.Read(chunkData)

	if n > 0 {
		chunkData = chunkData[:n]
	}

	isFinal := err == io.EOF
	if err != nil && err != io.EOF {
		return nil, false, err
	}

	// Calculate offset and chunk number
	chunkOffset := int64(state.chunkNumber) * int64(chunkSize)

	chunk := &pb.FileChunk{
		TransferId:  transferID,
		ChunkNumber: state.chunkNumber,
		ChunkData:   chunkData,
		ChunkOffset: chunkOffset,
		IsFinal:     isFinal,
	}

	// Increment chunk number for next call
	state.chunkNumber++

	return chunk, isFinal, nil
}

// registerUploadChunk registers a received chunk for an upload transfer.
func (s *session) registerUploadChunk(transferID string, chunk *pb.FileChunk) error {
	s.uploadMutex.Lock()
	state, exists := s.activeUploads[transferID]
	s.uploadMutex.Unlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", transferID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Validate chunk sequence
	expectedChunk := state.lastChunk + 1
	if chunk.GetChunkNumber() != expectedChunk {
		return fmt.Errorf("chunk order violation: expected %d, got %d", expectedChunk, chunk.GetChunkNumber())
	}

	state.lastChunk = chunk.GetChunkNumber()

	// Write chunk to pipe (data is streamed directly, not stored in memory)
	_, err := state.file.Write(chunk.GetChunkData())
	if err != nil {
		return err
	}

	// If this is the final chunk, close the pipe
	if chunk.GetIsFinal() {
		state.complete = true
		state.file.Close()
	}

	return nil
}

// getActiveUploads returns a list of active upload transfer IDs.
func (s *session) getActiveUploads() []string {
	s.uploadMutex.RLock()
	defer s.uploadMutex.RUnlock()

	transferIDs := make([]string, 0, len(s.activeUploads))
	for id := range s.activeUploads {
		transferIDs = append(transferIDs, id)
	}

	return transferIDs
}

// getActiveDownloads returns a list of active download transfer IDs for round-robin scheduling.
func (s *session) getActiveDownloads() []string {
	s.downloadMutex.RLock()
	defer s.downloadMutex.RUnlock()

	return append([]string{}, s.downloadOrder...)
}

// removeDownload removes a completed download from tracking.
func (s *session) removeDownload(transferID string) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	delete(s.activeDownloads, transferID)

	// Remove from order list.
	for i, id := range s.downloadOrder {
		if id == transferID {
			s.downloadOrder = append(s.downloadOrder[:i], s.downloadOrder[i+1:]...)

			break
		}
	}

	// Reset index if needed.
	if s.downloadIndex >= len(s.downloadOrder) {
		s.downloadIndex = 0
	}

	// Decrement active transfer count
	s.trackTransferEnd()
}

// getNextDownloadID returns the next transfer ID for downloading in round-robin fashion.
func (s *session) getNextDownloadID() string {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if len(s.downloadOrder) == 0 {
		return ""
	}

	id := s.downloadOrder[s.downloadIndex]
	s.downloadIndex = (s.downloadIndex + 1) % len(s.downloadOrder)

	return id
}

// getUpload retrieves upload state for a given transfer ID.
func (s *session) getUpload(transferID string) *uploadState {
	s.uploadMutex.RLock()
	defer s.uploadMutex.RUnlock()

	return s.activeUploads[transferID]
}

// removeUpload removes a completed upload from tracking.
func (s *session) removeUpload(transferID string) {
	s.uploadMutex.Lock()
	defer s.uploadMutex.Unlock()

	delete(s.activeUploads, transferID)

	// Decrement active transfer count
	s.trackTransferEnd()
}

// getDownload retrieves download state for a given transfer ID.
func (s *session) getDownload(transferID string) *downloadState {
	s.downloadMutex.RLock()
	defer s.downloadMutex.RUnlock()

	return s.activeDownloads[transferID]
}

// markDownloadAwaitingAck marks a download as waiting for client TRANSFER_COMPLETE.
func (s *session) markDownloadAwaitingAck(transferID string) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if state, exists := s.activeDownloads[transferID]; exists {
		state.awaitingFinalAck = true
	}
}

// IsShuttingDown checks if the session is in graceful shutdown mode.
func (s *session) IsShuttingDown() bool {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	return s.isShuttingDown
}

// Shutdown initiates graceful shutdown - signals that module execution is complete
// and workers should finish any pending file transfers before exiting.
// Workers will stop accepting new module requests but continue processing file transfers.
func (s *session) Shutdown() {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	if s.isShuttingDown {
		return // Already shutting down
	}

	log.Print("Session: Initiating graceful shutdown")

	s.isShuttingDown = true
	close(s.shutdownCh) // Signal workers that module is done

	// If there are no active transfers, immediately signal completion
	if s.activeTransferCount == 0 {
		log.Print("Session: No active transfers, shutdown complete")
		close(s.allTransfersDoneCh)
	}
}

// trackTransferStart increments the active transfer counter.
// Must be called when a new transfer starts.
func (s *session) trackTransferStart() {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	s.activeTransferCount++
}

// trackTransferEnd decrements the active transfer counter.
// If shutdown is in progress and no transfers remain, signals completion.
func (s *session) trackTransferEnd() {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	if s.activeTransferCount > 0 {
		s.activeTransferCount--
	}

	// If shutting down and all transfers done, signal completion
	if s.isShuttingDown && s.activeTransferCount == 0 {
		log.Print("Session: All transfers completed during shutdown")
		close(s.allTransfersDoneCh)
	}
}

// GetAllTransfersDoneCh returns the channel that signals when all transfers complete during shutdown.
func (s *session) GetAllTransfersDoneCh() <-chan struct{} {
	return s.allTransfersDoneCh
}

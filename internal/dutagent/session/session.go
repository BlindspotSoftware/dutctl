// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/chanio"
	"github.com/BlindspotSoftware/dutctl/internal/log"
	"github.com/google/uuid"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// chunkSize is the maximum size of a single file chunk (1MB).
const chunkSize = 1024 * 1024

// uploadState represents an active upload from client to agent (a file the
// module requested via RequestFile). Chunks arriving from the client are written
// to file; the module reads them through reader.
type uploadState struct {
	transferID  string
	metadata    *pb.FileMetadata
	lastChunk   int32 // last received chunk number, for sequence validation
	complete    bool
	file        *io.PipeWriter
	reader      *io.PipeReader
	requestSent bool // whether the initial FileTransferRequest has been announced
	mu          sync.Mutex
}

// downloadState represents an active download from agent to client (a file the
// module handed to SendFile). Chunks are read from reader and streamed out.
type downloadState struct {
	transferID       string
	metadata         *pb.FileMetadata
	reader           io.Reader
	closer           io.Closer // optional closer for the reader (e.g. *os.File)
	chunkNumber      int32
	awaitingFinalAck bool // waiting for the client's TRANSFER_COMPLETE
}

// backend implements the module.Session interface.
type backend struct {
	printCh  chan string
	stdinCh  chan []byte
	stdoutCh chan []byte
	stderrCh chan []byte

	// log is the session-scoped logger, frozen in by the broker (see Broker.Start)
	// because the module.Session methods carry no context to derive it from.
	log *slog.Logger

	// done is closed when the broker's workers are torn down. The module-facing
	// Print/Console calls select on it so a call blocked on a session channel whose
	// worker peer has exited unblocks — dropping output — instead of wedging the
	// module goroutine for the process lifetime. A nil done (a backend built
	// directly in a test) leaves the calls uncancellable.
	done <-chan struct{}

	// File-transfer tracking. Uploads flow client → agent (RequestFile), downloads
	// flow agent → client (SendFile). Both are chunked; a transfer is identified by
	// a UUID and lives in the map until it completes or is aborted.
	activeUploads   map[string]*uploadState
	activeDownloads map[string]*downloadState
	uploadMutex     sync.RWMutex
	downloadMutex   sync.RWMutex

	// fileTransferNotifyCh wakes toClientWorker when a new transfer is registered
	// so it can announce it and start streaming. Buffered to one; a pending signal
	// coalesces with a fresh one.
	fileTransferNotifyCh chan struct{}

	// sendMu serializes all sends on the bidirectional stream. The connect
	// BidiStream forbids concurrent Send calls, and both workers send responses
	// (toClientWorker streams downloads; fromClientWorker acks uploads), so every
	// send goes through sendToClient under this lock.
	sendMu sync.Mutex

	// Graceful-shutdown state. Shutdown marks the session done accepting module
	// output while letting in-flight transfers finish; transferWg reaches zero
	// once every registered transfer has been removed.
	shutdownCh     chan struct{}
	shutdownMutex  sync.Mutex
	isShuttingDown bool
	transferWg     sync.WaitGroup
}

// logger returns the session's scoped logger, falling back to the default if
// the broker has not set one (e.g. a session built directly in a test).
func (s *backend) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}

	return slog.Default()
}

// Print, Printf and Println forward a message to the client. The send is
// abandoned if the session has been torn down (done closed), so a module that
// keeps printing after its workers are gone is not wedged — the output is
// dropped, matching the fact that there is no longer a client to receive it.
func (s *backend) Print(a ...any) {
	select {
	case s.printCh <- fmt.Sprint(a...):
	case <-s.done:
	}
}

func (s *backend) Printf(format string, a ...any) {
	select {
	case s.printCh <- fmt.Sprintf(format, a...):
	case <-s.done:
	}
}

func (s *backend) Println(a ...any) {
	select {
	case s.printCh <- fmt.Sprintln(a...):
	case <-s.done:
	}
}

// Console returns the module's stdin/stdout/stderr streams (see module.Session).
// It must be called only from the module's Run goroutine, and it has no error
// return: the backing channels are always allocated by Broker.init before a module
// runs, so a nil channel here is a broken invariant and Console panics. runModule
// recovers that panic into a clean run error — do not add a top-level recover
// expecting to catch it, as a panic on another goroutine would be uncatchable.
//
//nolint:nonamedreturns
func (s *backend) Console() (stdin io.Reader, stdout, stderr io.Writer) {
	var (
		stdinReader                io.Reader
		stdoutWriter, stderrWriter io.Writer
		err                        error
	)

	// The channels are always initialized by Broker.init before a module runs,
	// so a failure here is a broken invariant (a nil channel), not a runtime
	// condition. Console has no error return, so panic; the module-execution
	// goroutine recovers it into a clean run error (see runModule).
	stdinReader, err = chanio.NewChanReader(s.stdinCh, s.done, log.Scope(s.logger(), scopeSessionUpstream))
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdin reader: %v", err))
	}

	stdoutWriter, err = chanio.NewChanWriter(s.stdoutCh, s.done)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stdout writer: %v", err))
	}

	stderrWriter, err = chanio.NewChanWriter(s.stderrCh, s.done)
	if err != nil {
		panic(fmt.Sprintf("session.Console: stderr writer: %v", err))
	}

	return stdinReader, stdoutWriter, stderrWriter
}

// RequestFile asks the client for the named file and returns a reader that
// streams its contents as chunks arrive. It returns immediately; the returned
// reader blocks until chunks are delivered (or the transfer is aborted). The
// error is opaque and not meant to be matched.
func (s *backend) RequestFile(name string) (io.Reader, error) {
	transferID := uuid.New().String()

	// Requesting and reading a file is the upstream (client → agent) flow.
	log.Scope(s.logger(), scopeSessionUpstream).Debug("module requested file", "name", name, "transfer_id", transferID)

	reader, writer := io.Pipe()
	state := &uploadState{
		transferID: transferID,
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: 0, // unknown; the client has the file
		},
		file:      writer,
		reader:    reader,
		lastChunk: -1,
	}

	s.uploadMutex.Lock()
	s.activeUploads[transferID] = state
	s.uploadMutex.Unlock()

	s.transferWg.Add(1)
	s.notifyFileTransfer()

	return state.reader, nil
}

// SendFile streams r to the client under the given name. size is the total file
// size in bytes. It returns immediately; the download proceeds chunk by chunk in
// toClientWorker. If r implements io.Closer, the session takes ownership and
// closes it when the transfer completes. The error is opaque and not meant to be
// matched.
func (s *backend) SendFile(name string, size int64, r io.Reader) error {
	transferID := uuid.New().String()

	// Sending a file to the client is the downstream (agent → client) flow.
	log.Scope(s.logger(), scopeSessionDownstream).Debug("module sending file", "name", name, "size", size, "transfer_id", transferID)

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

	if c, ok := r.(io.Closer); ok {
		state.closer = c
	}

	s.downloadMutex.Lock()
	s.activeDownloads[transferID] = state
	s.downloadMutex.Unlock()

	s.transferWg.Add(1)
	s.notifyFileTransfer()

	return nil
}

// notifyFileTransfer signals toClientWorker that file-transfer work is available.
func (s *backend) notifyFileTransfer() {
	select {
	case s.fileTransferNotifyCh <- struct{}{}:
	default:
	}
}

// getNextChunk returns the next chunk for a download transfer, a flag for whether
// it is the final chunk, and any read error.
func (s *backend) getNextChunk(transferID string) (*pb.FileChunk, bool, error) {
	s.downloadMutex.RLock()
	state, exists := s.activeDownloads[transferID]
	s.downloadMutex.RUnlock()

	if !exists {
		return nil, false, fmt.Errorf("download not found: %s", transferID)
	}

	chunkData := make([]byte, chunkSize)
	n, err := state.reader.Read(chunkData)
	chunkData = chunkData[:n]

	isFinal := err == io.EOF
	if err != nil && err != io.EOF {
		return nil, false, err
	}

	chunkOffset := int64(state.chunkNumber) * int64(chunkSize)

	chunk := &pb.FileChunk{
		TransferId:  transferID,
		ChunkNumber: state.chunkNumber,
		ChunkData:   chunkData,
		ChunkOffset: chunkOffset,
		IsFinal:     isFinal,
	}

	state.chunkNumber++

	return chunk, isFinal, nil
}

// registerUploadChunk writes a chunk received from the client into the upload's
// pipe, validating chunk ordering. The final chunk closes the pipe.
func (s *backend) registerUploadChunk(transferID string, chunk *pb.FileChunk) error {
	s.uploadMutex.RLock()
	state, exists := s.activeUploads[transferID]
	s.uploadMutex.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", transferID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	expectedChunk := state.lastChunk + 1
	if chunk.GetChunkNumber() != expectedChunk {
		return fmt.Errorf("chunk order violation: expected %d, got %d", expectedChunk, chunk.GetChunkNumber())
	}

	state.lastChunk = chunk.GetChunkNumber()

	_, err := state.file.Write(chunk.GetChunkData())
	if err != nil {
		return err
	}

	if chunk.GetIsFinal() {
		state.complete = true
		state.file.Close()
	}

	return nil
}

// getActiveUploads returns the IDs of every active upload transfer.
func (s *backend) getActiveUploads() []string {
	s.uploadMutex.RLock()
	defer s.uploadMutex.RUnlock()

	transferIDs := make([]string, 0, len(s.activeUploads))
	for id := range s.activeUploads {
		transferIDs = append(transferIDs, id)
	}

	return transferIDs
}

// getActiveDownloads returns the IDs of every active download transfer.
func (s *backend) getActiveDownloads() []string {
	s.downloadMutex.RLock()
	defer s.downloadMutex.RUnlock()

	transferIDs := make([]string, 0, len(s.activeDownloads))
	for id := range s.activeDownloads {
		transferIDs = append(transferIDs, id)
	}

	return transferIDs
}

// removeDownload removes a completed download from tracking, closing its reader
// if it owns one. Idempotent.
func (s *backend) removeDownload(transferID string) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	state, exists := s.activeDownloads[transferID]
	if !exists {
		return
	}

	if state.closer != nil {
		state.closer.Close()
	}

	delete(s.activeDownloads, transferID)

	s.transferWg.Done()
}

// getUpload retrieves upload state for a transfer ID, or nil.
func (s *backend) getUpload(transferID string) *uploadState {
	s.uploadMutex.RLock()
	defer s.uploadMutex.RUnlock()

	return s.activeUploads[transferID]
}

// removeUpload removes an upload from tracking, closing its pipe. Idempotent.
func (s *backend) removeUpload(transferID string) {
	s.uploadMutex.Lock()
	defer s.uploadMutex.Unlock()

	state, exists := s.activeUploads[transferID]
	if !exists {
		return
	}

	if state.file != nil {
		state.file.CloseWithError(fmt.Errorf("transfer removed"))
	}

	delete(s.activeUploads, transferID)

	s.transferWg.Done()
}

// getDownload retrieves download state for a transfer ID, or nil.
func (s *backend) getDownload(transferID string) *downloadState {
	s.downloadMutex.RLock()
	defer s.downloadMutex.RUnlock()

	return s.activeDownloads[transferID]
}

// isDownloadAwaitingAck reports whether a download is waiting for the client's
// TRANSFER_COMPLETE.
func (s *backend) isDownloadAwaitingAck(transferID string) bool {
	s.downloadMutex.RLock()
	defer s.downloadMutex.RUnlock()

	if state, exists := s.activeDownloads[transferID]; exists {
		return state.awaitingFinalAck
	}

	return false
}

// markDownloadAwaitingAck marks a download as waiting for the client's
// TRANSFER_COMPLETE.
func (s *backend) markDownloadAwaitingAck(transferID string) {
	s.downloadMutex.Lock()
	defer s.downloadMutex.Unlock()

	if state, exists := s.activeDownloads[transferID]; exists {
		state.awaitingFinalAck = true
	}
}

// IsShuttingDown reports whether the session is in graceful shutdown.
func (s *backend) IsShuttingDown() bool {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	return s.isShuttingDown
}

// Shutdown begins graceful shutdown: module output stops being forwarded, while
// the workers keep processing in-flight file transfers until they complete.
func (s *backend) Shutdown() {
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	if s.isShuttingDown {
		return
	}

	s.logger().Debug("session initiating graceful shutdown")

	s.isShuttingDown = true
	close(s.shutdownCh)
}

// WaitForTransfers blocks until every active transfer has completed.
func (s *backend) WaitForTransfers() {
	s.transferWg.Wait()
}

// abortTransfers tears down every still-active transfer and balances the
// transfer wait group. It is called once the stream workers have exited (e.g.
// the client disconnected mid-transfer) so a half-finished transfer cannot wedge
// WaitForTransfers — and therefore graceful shutdown — forever.
func (s *backend) abortTransfers() {
	s.uploadMutex.Lock()

	for id, state := range s.activeUploads {
		if state.file != nil {
			state.file.CloseWithError(fmt.Errorf("transfer aborted: stream closed"))
		}

		delete(s.activeUploads, id)
		s.transferWg.Done()
	}

	s.uploadMutex.Unlock()

	s.downloadMutex.Lock()

	for id, state := range s.activeDownloads {
		if state.closer != nil {
			state.closer.Close()
		}

		delete(s.activeDownloads, id)
		s.transferWg.Done()
	}

	s.downloadMutex.Unlock()
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/BlindspotSoftware/dutctl/internal/log"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// chunkSize is the maximum size of a single file chunk (1MB).
const chunkSize = 1024 * 1024

// ErrSessionClosed is returned by RequestFile and SendFile once the broker's
// workers have been torn down, so nothing remains to carry a transfer. It is
// terminal for the module: there is no client left to serve the transfer, so a
// module should return it rather than retry.
var ErrSessionClosed = errors.New("session is closed")

// uploadState represents an active upload from client to agent (a file the
// module requested via RequestFile). Chunks arriving from the client are written
// to file; the module reads them through reader.
type uploadState struct {
	transferID  string
	metadata    *pb.FileMetadata
	lastChunk   int32 // last received chunk number, for sequence validation
	file        *io.PipeWriter
	reader      *io.PipeReader
	requestSent bool // whether the initial FileTransferRequest has been announced
	mu          sync.Mutex

	// delivering is set while deliverChunk writes a chunk into the pipe. The
	// client sends a chunk only after the last one was acknowledged, so one
	// that arrives meanwhile breaks the protocol.
	delivering bool
	// ackPending marks a chunk the module has read, for toClientWorker to
	// acknowledge: ackNext is the next chunk expected, and ackFinal says the
	// chunk was the last one, which completes the upload.
	ackPending bool
	ackNext    int32
	ackFinal   bool

	// done is closed once the transfer is released, freeing the context watcher.
	done      chan struct{}
	closeOnce sync.Once
}

// close releases the upload once, failing the module's reader and a
// deliverChunk blocked in the pipe write with err. CloseWithError is safe
// concurrently with Write.
func (u *uploadState) close(err error) {
	u.closeOnce.Do(func() {
		if u.file != nil {
			u.file.CloseWithError(err)
		}

		close(u.done)
	})
}

// downloadState represents an active download from agent to client (a file the
// module hands to SendFile). SendFile reads the file and hands it over a chunk
// at a time; toClientWorker sends each chunk.
type downloadState struct {
	transferID string
	metadata   *pb.FileMetadata

	// mu guards chunk and failErr.
	mu sync.Mutex
	// chunk is the chunk SendFile handed over that toClientWorker has not sent
	// yet, nil while there is none.
	chunk *pb.FileChunk
	// failErr is why SendFile gave the download up, for toClientWorker to tell
	// the client.
	failErr error

	// sent wakes SendFile once toClientWorker has sent its chunk. Buffered to
	// one, as SendFile hands over one chunk at a time.
	sent chan struct{}

	// done is closed once the download ended, and err is its outcome then: nil
	// once the client confirmed the file, or why the file did not arrive.
	done      chan struct{}
	err       error
	closeOnce sync.Once

	awaitingFinalAck bool // waiting for the client's TRANSFER_COMPLETE; guarded by downloadMutex
}

// finish ends the download with err, unless it ended already. Once it
// returns, err holds the outcome.
func (d *downloadState) finish(err error) {
	d.closeOnce.Do(func() {
		d.err = err
		close(d.done)
	})
}

// backend implements the module.Session interface.
type backend struct {
	printCh   chan string
	consoleCh chan consoleEvent

	// mu guards currentConsole and lastConsoleID. currentConsole is swapped on
	// the module goroutine and read by the upstream worker for every input it
	// delivers.
	mu sync.Mutex

	// currentConsole is the open console, nil while none is.
	currentConsole *console
	// lastConsoleID is the id of the console opened last. Each console of the
	// run gets an id of its own, so the client marks its input for exactly one.
	lastConsoleID uint32

	// log is the session-scoped logger, frozen in by the broker (see Broker.Start)
	// because the module.Session methods carry no context to derive it from.
	log *slog.Logger

	// done is closed when the broker's workers are torn down. The module-facing
	// methods select on it so a call blocked on a session channel whose
	// worker peer has exited unblocks (dropping output) instead of wedging the
	// module goroutine for the process lifetime. A nil done (a backend built
	// directly in a test) leaves the calls uncancellable.
	done <-chan struct{}

	// File-transfer tracking. Uploads flow client → agent (RequestFile), downloads
	// flow agent → client (SendFile). Both are chunked; a transfer is identified by
	// an ID and lives in the map until it completes or is aborted.
	activeUploads   map[string]*uploadState
	activeDownloads map[string]*downloadState
	uploadMutex     sync.RWMutex
	downloadMutex   sync.RWMutex

	// lastTransferID numbers the transfers. An ID only has to be unique within
	// the session, which is also the client's scope for it.
	lastTransferID atomic.Uint64

	// fileTransferNotifyCh wakes toClientWorker when a transfer has work for it:
	// one to announce, a chunk to send or acknowledge, or a failure to report.
	// Buffered to one; a pending signal coalesces with a fresh one.
	fileTransferNotifyCh chan struct{}

	// sendMu serializes all sends on the bidirectional stream. The connect
	// BidiStream forbids concurrent Send calls, and both workers send responses
	// (toClientWorker streams downloads; fromClientWorker answers transfer
	// requests), so every send goes through sendToClient under this lock.
	sendMu sync.Mutex
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
// keeps printing after its workers are gone is not wedged. The output is
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

// RequestFile asks the client for the named file and returns a reader that
// streams its contents as chunks arrive. It returns immediately; the returned
// reader blocks until chunks are delivered (or the transfer is aborted).
//
// ctx bounds the transfer: cancelling it aborts the transfer and fails the
// reader, as the end of the session does. The session imposes no limit of its
// own.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer.
func (s *backend) RequestFile(ctx context.Context, name string) (io.Reader, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	if s.isClosed() {
		return nil, ErrSessionClosed
	}

	transferID := s.newTransferID()

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
		done:      make(chan struct{}),
	}

	s.uploadMutex.Lock()
	s.activeUploads[transferID] = state
	s.uploadMutex.Unlock()

	go s.watchUpload(ctx, state)

	s.notifyFileTransfer()

	return state.reader, nil
}

// watchUpload drops the upload once ctx or the session ends, which fails the
// module's reader and ends a deliverChunk blocked in the pipe. It returns once
// the upload ended, so it never outlives it.
func (s *backend) watchUpload(ctx context.Context, state *uploadState) {
	select {
	case <-ctx.Done():
		s.logger().Debug("upload aborted by context", "transfer_id", state.transferID, "err", ctx.Err())
	case <-s.done:
	case <-state.done:
		return
	}

	s.removeUpload(state.transferID, "")
}

// isClosed reports whether the broker's workers are gone. The done of a
// backend built directly in a test is nil and never closes.
func (s *backend) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// newTransferID returns an ID no other transfer of this session has.
func (s *backend) newTransferID() string {
	return strconv.FormatUint(s.lastTransferID.Add(1), 10)
}

// SendFile streams r to the client under the given name. size is the total file
// size in bytes. It reads r on the calling goroutine and returns once the client
// has confirmed the file, or with the reason the file did not arrive. The
// caller keeps r. Several calls from goroutines of the module stream their
// files side by side.
//
// ctx bounds the transfer: cancelling it ends the transfer between two reads
// of r, and the client is told why.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer.
func (s *backend) SendFile(ctx context.Context, name string, size int64, r io.Reader) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	if s.isClosed() {
		return ErrSessionClosed
	}

	transferID := s.newTransferID()

	// Sending a file to the client is the downstream (agent → client) flow.
	log.Scope(s.logger(), scopeSessionDownstream).Debug("module sending file", "name", name, "size", size, "transfer_id", transferID)

	state := &downloadState{
		transferID: transferID,
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: size,
		},
		sent: make(chan struct{}, 1),
		done: make(chan struct{}),
	}

	s.downloadMutex.Lock()
	s.activeDownloads[transferID] = state
	s.downloadMutex.Unlock()

	s.notifyFileTransfer()

	return s.streamDownload(ctx, state, r)
}

// streamDownload reads r a chunk at a time and hands each chunk to
// toClientWorker, reading the next once the last one went out. After the final
// chunk it waits for the client's confirmation. It returns the outcome.
func (s *backend) streamDownload(ctx context.Context, state *downloadState, r io.Reader) error {
	var offset int64

	for number := int32(0); ; number++ {
		buf := make([]byte, chunkSize)
		read, readErr := r.Read(buf)

		// A reader may return data together with io.EOF, so check it before the error.
		// errors.Is, not ==: SendFile takes any io.Reader, and a wrapped EOF is a
		// clean end of file, not a read failure.
		final := errors.Is(readErr, io.EOF)
		if readErr != nil && !final {
			return s.failDownload(state, fmt.Errorf("error reading file: %w", readErr))
		}

		state.mu.Lock()
		state.chunk = &pb.FileChunk{
			TransferId:  state.transferID,
			ChunkNumber: number,
			ChunkData:   buf[:read],
			ChunkOffset: offset,
			IsFinal:     final,
		}
		state.mu.Unlock()

		s.notifyFileTransfer()

		// Track the real offset rather than number*chunkSize: a short read is
		// legal and leaves chunks smaller than the maximum.
		offset += int64(read)

		// After the final chunk only the end of the download counts.
		sent := state.sent
		if final {
			sent = nil
		}

		ended, err := s.awaitDownload(ctx, state, sent)
		if ended {
			return err
		}
	}
}

// awaitDownload waits until toClientWorker has sent the chunk handed over, if
// sent is not nil, or until the download ended. A cancelled ctx or the end of
// the session ends the download first. It reports whether the download ended,
// and the outcome then.
func (s *backend) awaitDownload(ctx context.Context, state *downloadState, sent <-chan struct{}) (bool, error) {
	select {
	case <-sent:
		return false, nil
	case <-state.done:
		return true, state.err
	case <-ctx.Done():
		return true, s.failDownload(state, fmt.Errorf("the module cancelled the transfer: %w", context.Cause(ctx)))
	case <-s.done:
		return true, s.endDownload(state, ErrSessionClosed)
	}
}

// failDownload gives the download up with err. toClientWorker tells the client,
// unless the client has not heard of the download yet, and ends it. failDownload
// waits for that, so that the client hears why before the run ends, and returns
// the outcome, which the client may have settled first.
func (s *backend) failDownload(state *downloadState, err error) error {
	state.mu.Lock()
	state.failErr = err
	state.mu.Unlock()

	s.notifyFileTransfer()

	select {
	case <-state.done:
		return state.err
	case <-s.done:
		return s.endDownload(state, err)
	}
}

// endDownload drops the download and ends it with err, unless it ended
// already. It returns the outcome.
func (s *backend) endDownload(state *downloadState, err error) error {
	s.downloadMutex.Lock()
	delete(s.activeDownloads, state.transferID)
	s.downloadMutex.Unlock()

	state.finish(err)

	return state.err
}

// notifyFileTransfer signals toClientWorker that file-transfer work is available.
func (s *backend) notifyFileTransfer() {
	select {
	case s.fileTransferNotifyCh <- struct{}{}:
	default:
	}
}

// registerUploadChunk validates a chunk received from the client and hands it
// to deliverChunk, without waiting for the module to read it.
func (s *backend) registerUploadChunk(transferID string, chunk *pb.FileChunk) error {
	s.uploadMutex.RLock()
	state, exists := s.activeUploads[transferID]
	s.uploadMutex.RUnlock()

	if !exists {
		return fmt.Errorf("upload not found: %s", transferID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// A gap, a repeat or a chunk ahead of the acknowledgment means the client
	// is not speaking the protocol; there is no resend path, so the transfer
	// cannot recover. Wrapping ErrBadFileTransfer lets the RPC layer report
	// CodeInvalidArgument instead of a vague internal fault (see brokerError in
	// cmds/dutagent).
	expectedChunk := state.lastChunk + 1
	if chunk.GetChunkNumber() != expectedChunk {
		return fmt.Errorf("%w: chunk order violation: expected %d, got %d",
			ErrBadFileTransfer, expectedChunk, chunk.GetChunkNumber())
	}

	if state.delivering || state.ackPending {
		return fmt.Errorf("%w: chunk %d sent before chunk %d was acknowledged",
			ErrBadFileTransfer, chunk.GetChunkNumber(), state.lastChunk)
	}

	state.lastChunk = chunk.GetChunkNumber()
	state.delivering = true

	go s.deliverChunk(state, chunk)

	return nil
}

// deliverChunk writes a chunk into the upload's pipe, which returns once the
// module has read all of it, and then has toClientWorker acknowledge it. It
// runs on a goroutine of its own, so that fromClientWorker never waits for a
// module: console input and the chunks of other uploads keep flowing. Every
// abort closes the pipe, which ends the write. The end of the file comes from
// ackUploadChunks, once the client has been told.
func (s *backend) deliverChunk(state *uploadState, chunk *pb.FileChunk) {
	_, err := state.file.Write(chunk.GetChunkData())

	state.mu.Lock()

	state.delivering = false

	if err == nil {
		state.ackPending = true
		state.ackNext = chunk.GetChunkNumber() + 1
		state.ackFinal = chunk.GetIsFinal()
	}

	state.mu.Unlock()

	if err == nil {
		s.notifyFileTransfer()
	}
}

// takeUploadAck returns the acknowledgment an upload is waiting for, if any,
// and clears it.
func (s *backend) takeUploadAck(state *uploadState) (bool, int32, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()

	pending := state.ackPending
	state.ackPending = false

	return pending, state.ackNext, state.ackFinal
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

// removeDownload drops a download and ends it with err, which SendFile then
// returns, unless it ended already. Idempotent.
func (s *backend) removeDownload(transferID string, err error) {
	state := s.getDownload(transferID)
	if state != nil {
		_ = s.endDownload(state, err)
	}
}

// getUpload retrieves upload state for a transfer ID, or nil.
func (s *backend) getUpload(transferID string) *uploadState {
	s.uploadMutex.RLock()
	defer s.uploadMutex.RUnlock()

	return s.activeUploads[transferID]
}

// removeUpload removes an upload from tracking, closing its pipe, so that the
// module's reader fails: with the client's reason when the client gave one.
// Idempotent.
func (s *backend) removeUpload(transferID, clientReason string) {
	s.uploadMutex.Lock()
	defer s.uploadMutex.Unlock()

	state, exists := s.activeUploads[transferID]
	if !exists {
		return
	}

	err := errors.New("transfer removed")
	if clientReason != "" {
		err = fmt.Errorf("client could not send %q: %s", state.metadata.GetPath(), clientReason)
	}

	state.close(err)

	delete(s.activeUploads, transferID)
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

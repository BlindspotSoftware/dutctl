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

// errTransferInProgress is returned by RequestFile and SendFile while another
// transfer of the session runs: a session carries one transfer at a time.
var errTransferInProgress = errors.New("another file transfer is in progress")

// errTransferRemoved fails the reader of an upload that the agent dropped.
var errTransferRemoved = errors.New("transfer removed")

// uploadState represents an upload from client to agent (a file the module
// requested via RequestFile). Chunks arriving from the client are written to
// file; the module reads them through reader.
type uploadState struct {
	transferID  string
	metadata    *pb.FileMetadata
	lastChunk   int32 // last received chunk number, for sequence validation
	file        *io.PipeWriter
	reader      *io.PipeReader
	requestSent bool // whether the initial FileTransferRequest has been announced
	mu          sync.Mutex

	// aborted is closed once the context the module passed to RequestFile
	// ended, which ends the upload.
	aborted <-chan struct{}

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

// close releases the upload once, ending the module's reader and a
// deliverChunk blocked in the pipe write with err, or with io.EOF for a nil
// err. CloseWithError is safe concurrently with Write.
func (u *uploadState) close(err error) {
	u.closeOnce.Do(func() {
		// done first: a module that sees the end of its reader may start the
		// next transfer at once.
		close(u.done)

		if u.file != nil {
			u.file.CloseWithError(err)
		}
	})
}

// downloadState represents a download from agent to client (a file the module
// hands to SendFile). SendFile reads the file and hands it over a chunk at a
// time; toClientWorker sends each chunk.
type downloadState struct {
	transferID string
	metadata   *pb.FileMetadata

	// announced says that the client was told of the download. Only
	// toClientWorker uses it.
	announced bool

	// mu guards chunk, failErr and awaitingFinalAck.
	mu sync.Mutex
	// chunk is the chunk SendFile handed over that toClientWorker has not sent
	// yet, nil while there is none.
	chunk *pb.FileChunk
	// failErr is why SendFile gave the download up, for toClientWorker to tell
	// the client.
	failErr error
	// awaitingFinalAck is set once the final chunk goes out: only then does
	// the client's TRANSFER_COMPLETE end the download.
	awaitingFinalAck bool

	// sent wakes SendFile once toClientWorker has sent its chunk. Buffered to
	// one, as SendFile hands over one chunk at a time.
	sent chan struct{}

	// done is closed once the download ended, and err is its outcome then: nil
	// once the client confirmed the file, or why the file did not arrive.
	done      chan struct{}
	err       error
	closeOnce sync.Once
}

// finish ends the download with err, unless it ended already, and returns the
// outcome.
func (d *downloadState) finish(err error) error {
	d.closeOnce.Do(func() {
		d.err = err
		close(d.done)
	})

	return d.err
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

	// transferMu guards upload and download. A session carries one file
	// transfer at a time (see claimTransfer): uploads flow client → agent
	// (RequestFile), downloads agent → client (SendFile). The last one started
	// stays here once it ended, its done closed.
	transferMu sync.Mutex
	upload     *uploadState
	download   *downloadState

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
// It returns ErrSessionClosed if the session can no longer carry a transfer,
// and errTransferInProgress while another transfer of the session runs.
func (s *backend) RequestFile(ctx context.Context, name string) (io.Reader, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	if s.isClosed() {
		return nil, ErrSessionClosed
	}

	reader, writer := io.Pipe()
	state := &uploadState{
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: 0, // unknown; the client has the file
		},
		file:      writer,
		reader:    reader,
		lastChunk: -1,
		aborted:   ctx.Done(),
		done:      make(chan struct{}),
	}

	err = s.claimTransfer(state, nil)
	if err != nil {
		return nil, err
	}

	// Requesting and reading a file is the upstream (client → agent) flow.
	log.Scope(s.logger(), scopeSessionUpstream).Debug("module requested file", "name", name, "transfer_id", state.transferID)

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

	state.close(errTransferRemoved)
}

// isClosed reports whether the broker's workers are gone. The done of a
// backend built directly in a test is nil and never closes.
func (s *backend) isClosed() bool {
	return isDone(s.done)
}

// isDone reports whether ch is closed. A nil ch never is.
func isDone(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// claimTransfer makes upload or download, whichever is not nil, the transfer
// the session carries and gives it an ID, or returns errTransferInProgress
// while another transfer runs. An upload whose context ended counts as ended:
// claimTransfer drops it, as watchUpload is about to.
func (s *backend) claimTransfer(upload *uploadState, download *downloadState) error {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.download != nil && !isDone(s.download.done) {
		return errTransferInProgress
	}

	if s.upload != nil && !isDone(s.upload.done) {
		if !isDone(s.upload.aborted) {
			return errTransferInProgress
		}

		s.upload.close(errTransferRemoved)
	}

	transferID := strconv.FormatUint(s.lastTransferID.Add(1), 10)
	if upload != nil {
		upload.transferID = transferID
	} else {
		download.transferID = transferID
	}

	s.upload, s.download = upload, download

	return nil
}

// currentUpload returns the upload the session carries, nil if none runs.
func (s *backend) currentUpload() *uploadState {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.upload == nil || isDone(s.upload.done) {
		return nil
	}

	return s.upload
}

// currentDownload returns the download the session carries, nil if none runs.
func (s *backend) currentDownload() *downloadState {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.download == nil || isDone(s.download.done) {
		return nil
	}

	return s.download
}

// uploadByID returns the running upload with the client's transferID, or nil.
func (s *backend) uploadByID(transferID string) *uploadState {
	upload := s.currentUpload()
	if upload == nil || upload.transferID != transferID {
		return nil
	}

	return upload
}

// downloadByID returns the running download with the client's transferID, or nil.
func (s *backend) downloadByID(transferID string) *downloadState {
	download := s.currentDownload()
	if download == nil || download.transferID != transferID {
		return nil
	}

	return download
}

// SendFile streams r to the client under the given name. size is the total file
// size in bytes. It reads r on the calling goroutine and returns once the client
// has confirmed the file, or with the reason the file did not arrive. The
// caller keeps r.
//
// ctx bounds the transfer: cancelling it ends the transfer between two reads
// of r, and the client is told why.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer,
// and errTransferInProgress while another transfer of the session runs.
func (s *backend) SendFile(ctx context.Context, name string, size int64, r io.Reader) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	if s.isClosed() {
		return ErrSessionClosed
	}

	state := &downloadState{
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: size,
		},
		sent: make(chan struct{}, 1),
		done: make(chan struct{}),
	}

	err = s.claimTransfer(nil, state)
	if err != nil {
		return err
	}

	// Sending a file to the client is the downstream (agent → client) flow.
	log.Scope(s.logger(), scopeSessionDownstream).Debug("module sending file", "name", name, "size", size, "transfer_id", state.transferID)

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
		return true, state.finish(ErrSessionClosed)
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
		return state.finish(err)
	}
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
	state := s.uploadByID(transferID)
	if state == nil {
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
// module: console input keeps flowing. Every abort closes the pipe, which ends
// the write. The end of the file comes from ackUploadChunk, once the client has
// been told.
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

// removeUpload ends the running upload with transferID, if there is one, so
// that the module's reader fails: with the client's reason when the client gave
// one.
func (s *backend) removeUpload(transferID, clientReason string) {
	state := s.uploadByID(transferID)
	if state == nil {
		return
	}

	err := errTransferRemoved
	if clientReason != "" {
		err = fmt.Errorf("client could not send %q: %s", state.metadata.GetPath(), clientReason)
	}

	state.close(err)
}

// removeDownload ends the running download with transferID, if there is one,
// with err, which SendFile then returns.
func (s *backend) removeDownload(transferID string, err error) {
	state := s.downloadByID(transferID)
	if state != nil {
		_ = state.finish(err)
	}
}

// confirmDownload ends the running download with transferID once the client
// confirmed it, if its final chunk went out.
func (s *backend) confirmDownload(transferID string) {
	state := s.downloadByID(transferID)
	if state == nil {
		return
	}

	state.mu.Lock()
	awaiting := state.awaitingFinalAck
	state.mu.Unlock()

	if awaiting {
		_ = state.finish(nil)
	}
}

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

// transferEnd is the end of a transfer. The module's side asks for it with
// endTransfer, toClientWorker tells the client, and finish settles it.
type transferEnd struct {
	// mu guards ending and endErr.
	mu sync.Mutex
	// ending is set once the module's side is done with the transfer, for
	// toClientWorker to tell the client: endErr is why, or nil for an upload
	// whose file is complete.
	ending bool
	endErr error

	// done is closed once the transfer ended, and err is its outcome then.
	done chan struct{}
	err  error
	once sync.Once
}

// pending returns whether the module's side asked to end the transfer, and why.
func (e *transferEnd) pending() (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.ending, e.endErr
}

// finish ends the transfer with err, unless it ended already, and returns the
// outcome.
func (e *transferEnd) finish(err error) error {
	e.once.Do(func() {
		e.err = err
		close(e.done)
	})

	return e.err
}

// uploadState represents an upload from client to agent (a file the module
// requested via RequestFile). fromClientWorker hands each chunk to RequestFile,
// which writes it to the module's writer.
type uploadState struct {
	transferID string
	metadata   *pb.FileMetadata

	// announced says that the client was asked for the file. Only
	// toClientWorker uses it.
	announced bool
	// lastChunk is the number of the last chunk handed over, for the order
	// check. Only fromClientWorker uses it.
	lastChunk int32

	// chunks hands a chunk from fromClientWorker to RequestFile. Unbuffered:
	// fromClientWorker reads past a chunk only once RequestFile has written the
	// one before.
	chunks chan *pb.FileChunk

	// mu guards ackPending and ackNext.
	mu sync.Mutex
	// ackPending marks a chunk RequestFile has written, for toClientWorker to
	// acknowledge; ackNext is the next chunk expected.
	ackPending bool
	ackNext    int32

	// end is how the upload ends: nil once the whole file is in the module's
	// writer, or why it is not.
	end transferEnd
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

	// mu guards chunk and awaitingFinalAck.
	mu sync.Mutex
	// chunk is the chunk SendFile handed over that toClientWorker has not sent
	// yet, nil while there is none.
	chunk *pb.FileChunk
	// awaitingFinalAck is set once the final chunk goes out: only then does
	// the client's TRANSFER_COMPLETE end the download.
	awaitingFinalAck bool

	// sent wakes SendFile once toClientWorker has sent its chunk. Buffered to
	// one, as SendFile hands over one chunk at a time.
	sent chan struct{}

	// end is how the download ends: nil once the client confirmed the file, or
	// why the file did not arrive.
	end transferEnd
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
	// stays here once it ended, its end.done closed.
	transferMu sync.Mutex
	upload     *uploadState
	download   *downloadState

	// lastTransferID numbers the transfers. An ID only has to be unique within
	// the session, which is also the client's scope for it.
	lastTransferID atomic.Uint64

	// fileTransferNotifyCh wakes toClientWorker when a transfer has work for it:
	// one to announce, a chunk to send or acknowledge, or an end to report.
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

// RequestFile asks the client for the named file and writes it to w as its
// chunks arrive, on the calling goroutine. It returns once the whole file is in
// w and the client was told, or with the reason the file did not arrive.
//
// ctx bounds the transfer: cancelling it ends the transfer between two chunks,
// as an error from w does, and the client is told why. The session imposes no
// limit of its own.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer,
// and errTransferInProgress while another transfer of the session runs.
func (s *backend) RequestFile(ctx context.Context, name string, w io.Writer) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	if s.isClosed() {
		return ErrSessionClosed
	}

	state := &uploadState{
		metadata: &pb.FileMetadata{
			Path: name,
			Name: name,
			Size: 0, // unknown; the client has the file
		},
		lastChunk: -1,
		chunks:    make(chan *pb.FileChunk),
		end:       transferEnd{done: make(chan struct{})},
	}

	err = s.claimTransfer(state, nil)
	if err != nil {
		return err
	}

	// Requesting and reading a file is the upstream (client → agent) flow.
	log.Scope(s.logger(), scopeSessionUpstream).Debug("module requested file", "name", name, "transfer_id", state.transferID)

	s.notifyFileTransfer()

	return s.receiveUpload(ctx, state, w)
}

// receiveUpload writes each chunk fromClientWorker hands over to w, up to the
// final one. It returns the outcome of the upload.
func (s *backend) receiveUpload(ctx context.Context, state *uploadState, w io.Writer) error {
	for {
		select {
		case chunk := <-state.chunks:
			_, err := w.Write(chunk.GetChunkData())
			if err != nil {
				return s.endTransfer(&state.end, fmt.Errorf("writing the file: %w", err))
			}

			if chunk.GetIsFinal() {
				return s.endTransfer(&state.end, nil)
			}

			state.mu.Lock()
			state.ackPending = true
			state.ackNext = chunk.GetChunkNumber() + 1
			state.mu.Unlock()

			s.notifyFileTransfer()
		case <-state.end.done:
			return state.end.err
		case <-ctx.Done():
			return s.endTransfer(&state.end, fmt.Errorf("the module cancelled the transfer: %w", context.Cause(ctx)))
		case <-s.done:
			return state.end.finish(ErrSessionClosed)
		}
	}
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
// while another transfer runs.
func (s *backend) claimTransfer(upload *uploadState, download *downloadState) error {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.download != nil && !isDone(s.download.end.done) || s.upload != nil && !isDone(s.upload.end.done) {
		return errTransferInProgress
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

	if s.upload == nil || isDone(s.upload.end.done) {
		return nil
	}

	return s.upload
}

// currentDownload returns the download the session carries, nil if none runs.
func (s *backend) currentDownload() *downloadState {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.download == nil || isDone(s.download.end.done) {
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
		end:  transferEnd{done: make(chan struct{})},
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
			return s.endTransfer(&state.end, fmt.Errorf("error reading file: %w", readErr))
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
	case <-state.end.done:
		return true, state.end.err
	case <-ctx.Done():
		return true, s.endTransfer(&state.end, fmt.Errorf("the module cancelled the transfer: %w", context.Cause(ctx)))
	case <-s.done:
		return true, state.end.finish(ErrSessionClosed)
	}
}

// endTransfer ends a transfer from the module's side with err, nil for an
// upload whose file is complete. toClientWorker tells the client, unless the
// client has not heard of the transfer yet, and ends it. endTransfer waits for
// that, so that the client hears of the end before the run ends, and returns
// the outcome, which the client may have settled first.
func (s *backend) endTransfer(end *transferEnd, err error) error {
	end.mu.Lock()
	end.ending = true
	end.endErr = err
	end.mu.Unlock()

	s.notifyFileTransfer()

	select {
	case <-end.done:
		return end.err
	case <-s.done:
		return end.finish(err)
	}
}

// notifyFileTransfer signals toClientWorker that file-transfer work is available.
func (s *backend) notifyFileTransfer() {
	select {
	case s.fileTransferNotifyCh <- struct{}{}:
	default:
	}
}

// deliverUploadChunk hands a chunk from the client to RequestFile, once
// RequestFile has written the one before. A chunk of an upload that does not
// run is dropped: the client may have sent it before it heard that the upload
// ended.
func (s *backend) deliverUploadChunk(ctx context.Context, chunk *pb.FileChunk) error {
	state := s.uploadByID(chunk.GetTransferId())
	if state == nil {
		s.logger().Debug("dropping chunk of an upload that does not run", "transfer_id", chunk.GetTransferId())

		return nil
	}

	// A gap or a repeat means the client is not speaking the protocol; there is
	// no resend path, so the transfer cannot recover. Wrapping
	// ErrBadFileTransfer lets the RPC layer report CodeInvalidArgument instead
	// of a vague internal fault (see brokerError in cmds/dutagent).
	expectedChunk := state.lastChunk + 1
	if chunk.GetChunkNumber() != expectedChunk {
		return fmt.Errorf("%w: chunk order violation: expected %d, got %d",
			ErrBadFileTransfer, expectedChunk, chunk.GetChunkNumber())
	}

	state.lastChunk = chunk.GetChunkNumber()

	select {
	case state.chunks <- chunk:
	case <-state.end.done:
	case <-ctx.Done():
	}

	return nil
}

// removeUpload ends the running upload with transferID, if there is one, with
// the client's reason, which RequestFile then returns.
func (s *backend) removeUpload(transferID, clientReason string) {
	state := s.uploadByID(transferID)
	if state != nil {
		_ = state.end.finish(fmt.Errorf("client could not send %q: %s", state.metadata.GetPath(), clientReason))
	}
}

// removeDownload ends the running download with transferID, if there is one,
// with err, which SendFile then returns.
func (s *backend) removeDownload(transferID string, err error) {
	state := s.downloadByID(transferID)
	if state != nil {
		_ = state.end.finish(err)
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
		_ = state.end.finish(nil)
	}
}

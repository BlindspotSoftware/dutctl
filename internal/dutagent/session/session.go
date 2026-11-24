// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/BlindspotSoftware/dutctl/internal/digest"
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

// transfer is a file transfer of the session: an upload of a file the module
// requested with RequestFile, from the client to the module, or a download of
// a file the module gave SendFile, from the module to the client.
type transfer struct {
	id        string
	path      string
	direction pb.FileTransferRequest_Direction

	// chunks hands an upload's chunk from fromClientWorker to RequestFile.
	// Unbuffered: fromClientWorker reads past a chunk only once RequestFile has
	// written the one before, so the stream's flow control holds the client
	// back.
	chunks chan *pb.FileChunk
	// lastChunk is the number of the upload's last chunk handed over, for the
	// order check, final says that it was the final one, and sum digests the
	// chunks handed over. Only fromClientWorker uses them.
	lastChunk int32
	final     bool
	sum       hash.Hash

	// done is closed once the transfer ended, and err is its outcome then: nil
	// once the receiver has the whole file, or why it does not.
	done chan struct{}
	err  error
	once sync.Once
}

// finish ends the transfer with err, unless it ended already, and returns the
// outcome.
func (xfer *transfer) finish(err error) error {
	xfer.once.Do(func() {
		xfer.err = err
		close(xfer.done)
	})

	return xfer.err
}

// backend implements the module.Session interface.
type backend struct {
	printCh   chan string
	consoleCh chan consoleEvent
	// fileOut hands the messages of a transfer to toClientWorker: the request
	// that starts it, its chunks and its end.
	fileOut chan *pb.RunResponse

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

	// transferMu guards transfer, the file transfer the session carries. A
	// session carries one at a time (see claimTransfer); the last one started
	// stays here once it ended, its done closed.
	transferMu sync.Mutex
	transfer   *transfer

	// lastTransferID numbers the transfers. An ID only has to be unique within
	// the session, which is also the client's scope for it.
	lastTransferID atomic.Uint64
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
// w and toClientWorker has the end of the upload for the client, or with the
// reason the file did not arrive.
//
// ctx bounds the transfer: cancelling it ends the transfer between two chunks,
// as an error from w does, and the client is told why. The session imposes no
// limit of its own.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer,
// and errTransferInProgress while another transfer of the session runs.
func (s *backend) RequestFile(ctx context.Context, name string, w io.Writer) error {
	xfer, err := s.startTransfer(ctx, name, pb.FileTransferRequest_DIRECTION_UPLOAD)
	if err != nil {
		return err
	}

	// Requesting and reading a file is the upstream (client → agent) flow.
	log.Scope(s.logger(), scopeSessionUpstream).Debug("module requested file", "name", name, "transfer_id", xfer.id)

	for {
		select {
		case chunk := <-xfer.chunks:
			_, err := w.Write(chunk.GetChunkData())
			if err != nil {
				return s.endTransfer(xfer, fmt.Errorf("writing the file: %w", err))
			}

			if chunk.GetIsFinal() {
				return s.endTransfer(xfer, nil)
			}
		case <-xfer.done:
			return xfer.err
		case <-ctx.Done():
			return s.endTransfer(xfer, cancelled(ctx))
		case <-s.done:
			return xfer.finish(ErrSessionClosed)
		}
	}
}

// SendFile streams r to the client under the given name. size is the file size
// in bytes, 0 if unknown; a given size is checked. It reads r on the calling
// goroutine and returns once the client has confirmed the file, or with the
// reason the file did not arrive. The caller keeps r.
//
// ctx bounds the transfer: cancelling it ends the transfer between two reads
// of r, and the client is told why.
//
// It returns ErrSessionClosed if the session can no longer carry a transfer,
// and errTransferInProgress while another transfer of the session runs.
func (s *backend) SendFile(ctx context.Context, name string, size int64, r io.Reader) error {
	xfer, err := s.startTransfer(ctx, name, pb.FileTransferRequest_DIRECTION_DOWNLOAD)
	if err != nil {
		return err
	}

	// Sending a file to the client is the downstream (agent → client) flow.
	log.Scope(s.logger(), scopeSessionDownstream).Debug("module sending file", "name", name, "size", size, "transfer_id", xfer.id)

	return s.streamDownload(ctx, xfer, size, r)
}

// streamDownload reads r a chunk at a time and hands each chunk to
// toClientWorker, reading the next once the last one went out. The final chunk
// carries the digest of the whole file. After it, streamDownload waits for the
// client's confirmation. It returns the outcome.
func (s *backend) streamDownload(ctx context.Context, xfer *transfer, size int64, r io.Reader) error {
	var offset int64

	sum := digest.New()

	for number := int32(0); ; number++ {
		buf := make([]byte, chunkSize)
		read, readErr := r.Read(buf)

		// A reader may return data together with io.EOF, so check it before the error.
		// errors.Is, not ==: SendFile takes any io.Reader, and a wrapped EOF is a
		// clean end of file, not a read failure.
		final := errors.Is(readErr, io.EOF)
		if readErr != nil && !final {
			return s.endTransfer(xfer, fmt.Errorf("error reading file: %w", readErr))
		}

		sizeErr := checkSize(size, offset+int64(read), final)
		if sizeErr != nil {
			return s.endTransfer(xfer, sizeErr)
		}

		chunk := &pb.FileChunk{
			TransferId:  xfer.id,
			ChunkNumber: number,
			ChunkData:   buf[:read],
			ChunkOffset: offset,
			IsFinal:     final,
		}
		describe(chunk, size, sum)

		// Track the real offset rather than number*chunkSize: a short read is
		// legal and leaves chunks smaller than the maximum.
		offset += int64(read)

		ended, err := s.handOver(ctx, xfer, &pb.RunResponse{Msg: &pb.RunResponse_FileChunk{FileChunk: chunk}})

		switch {
		case ended && err == nil:
			return fmt.Errorf("%w: the client confirmed %q before its final chunk", ErrBadFileTransfer, xfer.path)
		case ended:
			return err
		case final:
			// After the final chunk only the end of the download counts.
			_, err = s.handOver(ctx, xfer, nil)

			return err
		}
	}
}

// describe puts what the sender knows of its file on chunk: the size on chunk
// 0, and on the final chunk the digest of the whole file, which sum has taken
// in by then.
func describe(chunk *pb.FileChunk, size int64, sum hash.Hash) {
	sum.Write(chunk.GetChunkData())

	if chunk.GetChunkNumber() == 0 {
		chunk.FileSize = size
	}

	if chunk.GetIsFinal() {
		chunk.Sha256 = sum.Sum(nil)
	}
}

// checkSize returns an error once the bytes read so far, all of them at eof,
// contradict size, the size the module gave. A size of 0 is unknown and
// matches every file.
func checkSize(size, read int64, eof bool) error {
	switch {
	case size == 0:
		return nil
	case read > size:
		return fmt.Errorf("the file is longer than the %d bytes given", size)
	case eof && read < size:
		return fmt.Errorf("the file ends after %d of the %d bytes given", read, size)
	default:
		return nil
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

// cancelled is why a transfer ends when the module cancels ctx.
func cancelled(ctx context.Context) error {
	return fmt.Errorf("the module cancelled the transfer: %w", context.Cause(ctx))
}

// startTransfer makes a transfer of the file at path the one the session
// carries, and hands toClientWorker the request that starts it. Until the
// client has that request, it knows of no transfer to end, so a transfer that
// ends before is not reported. It returns ErrSessionClosed if the session can
// no longer carry a transfer, and errTransferInProgress while another transfer
// of the session runs.
func (s *backend) startTransfer(ctx context.Context, path string, direction pb.FileTransferRequest_Direction) (*transfer, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	if s.isClosed() {
		return nil, ErrSessionClosed
	}

	xfer := &transfer{
		path:      path,
		direction: direction,
		lastChunk: -1,
		sum:       digest.New(),
		chunks:    make(chan *pb.FileChunk),
		done:      make(chan struct{}),
	}

	err = s.claimTransfer(xfer)
	if err != nil {
		return nil, err
	}

	req := &pb.FileTransferRequest{TransferId: xfer.id, Direction: direction, Path: path}

	select {
	case s.fileOut <- &pb.RunResponse{Msg: &pb.RunResponse_FileTransferRequest{FileTransferRequest: req}}:
		return xfer, nil
	case <-ctx.Done():
		return nil, xfer.finish(cancelled(ctx))
	case <-s.done:
		return nil, xfer.finish(ErrSessionClosed)
	}
}

// claimTransfer makes xfer the transfer the session carries and gives it an ID,
// or returns errTransferInProgress while another transfer runs.
func (s *backend) claimTransfer(xfer *transfer) error {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.transfer != nil && !isDone(s.transfer.done) {
		return errTransferInProgress
	}

	xfer.id = strconv.FormatUint(s.lastTransferID.Add(1), 10)
	s.transfer = xfer

	return nil
}

// handOver gives res to toClientWorker, which sends it to the client; a nil res
// is never taken, so handOver only waits for the end then. It reports whether
// the transfer ended first, and the outcome then. A cancelled ctx or the end of
// the session ends the transfer.
func (s *backend) handOver(ctx context.Context, xfer *transfer, res *pb.RunResponse) (bool, error) {
	// An ended transfer takes precedence over a worker ready to take res:
	// checked first, since a select with both ready picks at random.
	if isDone(xfer.done) {
		return true, xfer.err
	}

	out := s.fileOut
	if res == nil {
		out = nil
	}

	select {
	case out <- res:
		return false, nil
	case <-xfer.done:
		return true, xfer.err
	case <-ctx.Done():
		return true, s.endTransfer(xfer, cancelled(ctx))
	case <-s.done:
		return true, xfer.finish(ErrSessionClosed)
	}
}

// endTransfer ends xfer from the module's side with err, nil for an upload whose
// file is complete, and hands toClientWorker the end for the client:
// TRANSFER_COMPLETE, or ERROR with err. toClientWorker sends what it has taken
// before it stops, so the client hears of the end before the run ends. It
// returns the outcome, which the client may have settled first.
func (s *backend) endTransfer(xfer *transfer, err error) error {
	res := &pb.FileTransferResponse{TransferId: xfer.id, Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE}

	if err != nil {
		s.logger().Warn("file transfer failed", "transfer_id", xfer.id, "err", err)

		res.Status = pb.FileTransferResponse_STATUS_ERROR
		res.ErrorMessage = err.Error()
	}

	select {
	case s.fileOut <- &pb.RunResponse{Msg: &pb.RunResponse_FileTransferResponse{FileTransferResponse: res}}:
	case <-xfer.done:
	case <-s.done:
	}

	return xfer.finish(err)
}

// runningTransfer returns the transfer with the client's transferID if it
// runs, or nil.
func (s *backend) runningTransfer(transferID string) *transfer {
	s.transferMu.Lock()
	xfer := s.transfer
	s.transferMu.Unlock()

	if xfer == nil || xfer.id != transferID || isDone(xfer.done) {
		return nil
	}

	return xfer
}

// notRunning handles a message from the client for a transfer that does not
// run. The message is dropped if the session issued the transfer's ID: the
// client may have sent it before it heard that the transfer ended. An ID the
// session never issued is a protocol violation.
func (s *backend) notRunning(transferID string) error {
	n, err := strconv.ParseUint(transferID, 10, 64)
	if err != nil || n == 0 || n > s.lastTransferID.Load() {
		return fmt.Errorf("%w: transfer %q was never started", ErrBadFileTransfer, transferID)
	}

	s.logger().Debug("dropping a message for a transfer that ended", "transfer_id", transferID)

	return nil
}

// deliverUploadChunk hands a chunk from the client to RequestFile, once
// RequestFile has written the one before. A chunk of an upload that does not
// run is handled by notRunning.
func (s *backend) deliverUploadChunk(ctx context.Context, chunk *pb.FileChunk) error {
	xfer := s.runningTransfer(chunk.GetTransferId())
	if xfer == nil {
		return s.notRunning(chunk.GetTransferId())
	}

	// A chunk of a download, a gap, a repeat or a chunk after the final one
	// means the client is not speaking the protocol; there is no resend path,
	// so the transfer cannot recover. Wrapping ErrBadFileTransfer lets the RPC
	// layer report CodeInvalidArgument instead of a vague internal fault (see
	// brokerError in cmds/dutagent).
	switch number := chunk.GetChunkNumber(); {
	case xfer.direction != pb.FileTransferRequest_DIRECTION_UPLOAD:
		return fmt.Errorf("%w: chunk %d of download %s from the client", ErrBadFileTransfer, number, xfer.id)
	case xfer.final:
		return fmt.Errorf("%w: chunk %d after the final one", ErrBadFileTransfer, number)
	case number != xfer.lastChunk+1:
		return fmt.Errorf("%w: chunk order violation: expected %d, got %d", ErrBadFileTransfer, xfer.lastChunk+1, number)
	}

	xfer.lastChunk = chunk.GetChunkNumber()
	xfer.final = chunk.GetIsFinal()
	xfer.sum.Write(chunk.GetChunkData())

	if xfer.final {
		err := s.verifyUpload(xfer, chunk.GetSha256())
		if err != nil {
			_ = xfer.finish(err)

			return err
		}
	}

	select {
	case xfer.chunks <- chunk:
	case <-xfer.done:
	case <-ctx.Done():
	}

	return nil
}

// verifyUpload checks the digest of a complete upload against want, the one
// the client sent with the final chunk. It runs before the final chunk reaches
// RequestFile, so RequestFile returns the error instead of nil, and a module
// that acts on the file only once RequestFile returned nil never acts on
// damaged content. A final chunk without a digest breaks the protocol.
func (s *backend) verifyUpload(xfer *transfer, want []byte) error {
	got := xfer.sum.Sum(nil)

	switch {
	case len(want) == 0:
		return fmt.Errorf("%w: the final chunk of %q carries no checksum", ErrBadFileTransfer, xfer.path)
	case !digest.Match(got, want):
		return fmt.Errorf("%w: %q: want %s, got %s", ErrFileCorrupt, xfer.path, digest.Hex(want), digest.Hex(got))
	}

	return nil
}

// endByClient ends the transfer the client's res names: TRANSFER_COMPLETE
// confirms a download, and ERROR ends either direction with the client's
// reason. A message for a transfer that does not run is handled by notRunning.
func (s *backend) endByClient(res *pb.FileTransferResponse) error {
	xfer := s.runningTransfer(res.GetTransferId())
	if xfer == nil {
		return s.notRunning(res.GetTransferId())
	}

	switch res.GetStatus() {
	case pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE:
		// SendFile checks that the final chunk went out before this.
		if xfer.direction != pb.FileTransferRequest_DIRECTION_DOWNLOAD {
			return fmt.Errorf("%w: the client confirmed upload %s", ErrBadFileTransfer, xfer.id)
		}

		_ = xfer.finish(nil)
	case pb.FileTransferResponse_STATUS_ERROR:
		reason := cmp.Or(res.GetErrorMessage(), "no reason given")
		s.logger().Warn("client failed transfer", "transfer_id", xfer.id, "reason", reason)

		err := fmt.Errorf("client could not receive the file: %s", reason)
		if xfer.direction == pb.FileTransferRequest_DIRECTION_UPLOAD {
			err = fmt.Errorf("client could not send %q: %s", xfer.path, reason)
		}

		_ = xfer.finish(err)
	case pb.FileTransferResponse_STATUS_UNSPECIFIED:
		// Not meaningful from the client; ignore.
	}

	return nil
}

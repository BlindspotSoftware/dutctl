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

	"github.com/BlindspotSoftware/dutctl/internal/log"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// ErrBadFileTransfer marks a malformed file transfer from the client (a protocol
// violation) so the RPC layer can map it to CodeInvalidArgument rather than
// treating it as an internal fault.
var ErrBadFileTransfer = errors.New("bad file transfer")

// ErrStreamClosed reports that the stream could not accept a message because it
// is already gone. It is not a run failure: the workers stop quietly on it,
// since there is no client left to tell. Any other send error is real and
// terminates the worker with that error.
var ErrStreamClosed = errors.New("stream closed")

// sendToClient serializes all sends on the bidirectional stream. The connect
// BidiStream is not safe for concurrent Send calls, and both workers send
// responses (toClientWorker streams downloads and module output;
// fromClientWorker acks uploads), so every send goes through this lock.
//
// A Send on an already-closed stream can panic. That is recovered into
// ErrStreamClosed rather than a nil error: reporting success for a message that
// was never sent leaves the caller believing a chunk landed, and leaves this
// worker looping against a dead stream until something else cancels it.
func (s *backend) sendToClient(stream Stream, res *pb.RunResponse) (err error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			s.logger().Warn("recovered from panic in stream.Send", "err", r)

			err = fmt.Errorf("%w: send panicked: %v", ErrStreamClosed, r)
		}
	}()

	return stream.Send(res)
}

// sendOrStop delivers res and classifies a failure for a worker loop: a closed
// stream stops the worker without failing the run (there is no client left to
// report to), any other error is terminal and surfaces on the error channel.
func (s *backend) sendOrStop(stream Stream, res *pb.RunResponse) (bool, error) {
	sendErr := s.sendToClient(stream, res)

	switch {
	case sendErr == nil:
		return false, nil
	case errors.Is(sendErr, ErrStreamClosed):
		return true, nil
	default:
		return true, sendErr
	}
}

// sendDownloadError reports a download read error to the client and drops the
// transfer. The read error itself is not fatal to the run — only a failure to
// deliver the report is, and that is returned.
func sendDownloadError(
	stream Stream,
	s *backend,
	transferID string,
	downloadMetadataSent map[string]bool,
	err error,
) error {
	s.logger().Warn("error getting chunk for download", "transfer_id", transferID, "err", err)

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:        transferID,
				Status:            pb.FileTransferResponse_STATUS_ERROR,
				ErrorMessage:      fmt.Sprintf("error reading file: %v", err),
				NextChunkExpected: 0,
			},
		},
	}

	sendErr := s.sendToClient(stream, res)

	s.removeDownload(transferID)
	delete(downloadMetadataSent, transferID)

	return sendErr
}

// sendDownloadMetadata announces a download to the client (its metadata and
// direction). It reports whether a message was sent, and returns a send error
// unchanged for the caller to act on.
func sendDownloadMetadata(
	stream Stream,
	s *backend,
	transferID string,
	downloadMetadataSent map[string]bool,
) (bool, error) {
	if downloadMetadataSent[transferID] {
		return false, nil
	}

	download := s.getDownload(transferID)
	if download == nil {
		return false, nil
	}

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileTransferRequest{
			FileTransferRequest: &pb.FileTransferRequest{
				TransferId: transferID,
				Metadata:   download.metadata,
				Direction:  pb.FileTransferRequest_DIRECTION_DOWNLOAD,
			},
		},
	}

	sendErr := s.sendToClient(stream, res)
	if sendErr != nil {
		return false, sendErr
	}

	downloadMetadataSent[transferID] = true

	return true, nil
}

// handleDownloadFileTransfer advances a single download: announce its metadata,
// then stream one chunk. It reports whether a message was sent.
//
// A send error is returned rather than logged and shrugged off. The chunk has
// already been consumed from the module's reader by then, so there is nothing to
// retry; carrying on would silently drop those bytes and leave the transfer
// stalled with no further wake-up and nothing on the error channel.
func handleDownloadFileTransfer(
	stream Stream,
	s *backend,
	transferID string,
	downloadMetadataSent map[string]bool,
) (bool, error) {
	// Skip while waiting for the client's acknowledgment of the final chunk.
	if s.isDownloadAwaitingAck(transferID) {
		return false, nil
	}

	// Announce the transfer before streaming any chunk.
	if !downloadMetadataSent[transferID] {
		return sendDownloadMetadata(stream, s, transferID, downloadMetadataSent)
	}

	chunk, isFinal, err := s.getNextChunk(transferID)
	if err != nil {
		return true, sendDownloadError(stream, s, transferID, downloadMetadataSent, err)
	}

	if chunk == nil {
		return false, nil
	}

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileChunk{FileChunk: chunk},
	}

	sendErr := s.sendToClient(stream, res)
	if sendErr != nil {
		return false, sendErr
	}

	if isFinal {
		s.markDownloadAwaitingAck(transferID)
	}

	return true, nil
}

// forgetFinishedDownloads drops announcement markers for transfers that are no
// longer active, so the map cannot grow for the life of a long session.
func forgetFinishedDownloads(s *backend, downloadMetadataSent map[string]bool) {
	if len(downloadMetadataSent) == 0 {
		return
	}

	active := make(map[string]struct{}, len(downloadMetadataSent))
	for _, id := range s.getActiveDownloads() {
		active[id] = struct{}{}
	}

	for id := range downloadMetadataSent {
		if _, ok := active[id]; !ok {
			delete(downloadMetadataSent, id)
		}
	}
}

// processFileTransfers announces one pending upload and advances one download per
// call (one at a time for fairness). It reports whether a message was sent, so
// the caller can re-signal for the remaining work, and returns any send error so
// the worker can terminate instead of leaving the transfer stalled.
func processFileTransfers(stream Stream, s *backend, downloadMetadataSent map[string]bool) (bool, error) {
	sent := false

	// Announce a FileTransferRequest for a new upload that has not been sent yet.
	if !s.IsShuttingDown() {
		for _, transferID := range s.getActiveUploads() {
			upload := s.getUpload(transferID)
			if upload == nil {
				continue
			}

			// metadata is written by fromClientWorker under upload.mu, so read it
			// (and requestSent) under the same lock.
			upload.mu.Lock()
			metadata := upload.metadata
			alreadySent := upload.requestSent
			upload.mu.Unlock()

			if metadata == nil || alreadySent {
				continue
			}

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_FileTransferRequest{
					FileTransferRequest: &pb.FileTransferRequest{
						TransferId: transferID,
						Metadata:   metadata,
						Direction:  pb.FileTransferRequest_DIRECTION_UPLOAD,
					},
				},
			}

			sendErr := s.sendToClient(stream, res)
			if sendErr != nil {
				return sent, sendErr
			}

			upload.mu.Lock()
			upload.requestSent = true
			upload.mu.Unlock()

			sent = true

			break // one at a time
		}
	}

	// Advance the first download that has work available.
	for _, transferID := range s.getActiveDownloads() {
		advanced, err := handleDownloadFileTransfer(stream, s, transferID, downloadMetadataSent)
		if err != nil {
			return sent, err
		}

		if advanced {
			sent = true

			break // one at a time for fairness
		}
	}

	return sent, nil
}

// advanceFileTransfers runs one file-transfer step and classifies the outcome
// for toClientWorker's loop, reporting whether the worker should stop.
func advanceFileTransfers(
	l *slog.Logger,
	stream Stream,
	s *backend,
	downloadMetadataSent map[string]bool,
) (bool, error) {
	forgetFinishedDownloads(s, downloadMetadataSent)

	sent, err := processFileTransfers(stream, s, downloadMetadataSent)
	if err != nil {
		// A dead stream is not a run failure — there is no client left to tell —
		// but this worker must stop either way.
		if errors.Is(err, ErrStreamClosed) {
			l.Debug("worker terminating", "reason", "stream closed")

			return true, nil
		}

		l.Warn("error advancing file transfer", "err", err)

		return true, err
	}

	if sent {
		// More work may be pending; re-signal.
		s.notifyFileTransfer()
	}

	return false, nil
}

// toClientWorker sends module output and download chunks to the client. It loops
// until ctx is cancelled (returning nil) or a stream send fails (returning that
// error). While the session is shutting down, module output is discarded but file
// transfers keep flowing until they complete.
//
//nolint:cyclop // main select loop inherently has multiple cases
func toClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

	// Track which downloads have had their metadata announced.
	downloadMetadataSent := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return nil
		case str := <-s.printCh:
			// No shutdown check: Print refuses new output once shutdown starts, so
			// anything already on the channel was accepted and must still be sent.
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				if err != nil {
					l.Warn("error sending print", "err", err)
				}

				return err
			}
		case bytes := <-s.stdoutCh:
			// As with printCh: the Console writers refuse output once shutdown
			// starts, so whatever reached this channel was accepted and is sent.
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: bytes}}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				return err
			}
		case bytes := <-s.stderrCh:
			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stderr{Stderr: bytes}}},
			}

			stop, err := s.sendOrStop(stream, res)
			if stop {
				return err
			}
		case <-s.fileTransferNotifyCh:
			stop, err := advanceFileTransfers(l, stream, s, downloadMetadataSent)
			if stop {
				return err
			}
		}
	}
}

// fromClientWorker reads messages from the client and routes them: console stdin
// to the module, and file-transfer messages (chunks, requests, acknowledgments)
// to the transfer state machine. It loops until ctx is cancelled or the client
// closes the stream with io.EOF (both returning nil), or a stream error occurs
// (returning that error).
//
//nolint:cyclop,funlen,gocognit,gocyclo,maintidx
func fromClientWorker(ctx context.Context, stream Stream, s *backend) error {
	l := log.FromContext(ctx)

	type recvResult struct {
		req *pb.RunRequest
		err error
	}

	// Single goroutine performing blocking Receive calls and forwarding results.
	resCh := make(chan recvResult)
	// Receive loop goroutine rationale:
	//
	// We offload blocking stream.Receive calls to this goroutine so the main select
	// can remain responsive to ctx cancellation. The goroutine keeps calling
	// Receive until an error (including io.EOF) occurs, then returns.
	//
	// Two blocking points, both bounded:
	//   - stream.Receive is transport I/O that ctx cannot interrupt; it unblocks
	//     when the client closes the stream (EOF) or it errors, which happens
	//     shortly after module completion / broker cancellation tears the RPC
	//     down. This is an accepted bounded wait.
	//   - the resCh send is guarded by ctx.Done. Once the main loop returns it no
	//     longer receives from resCh, so an unguarded send here would block
	//     forever on a receiverless channel — leaking this goroutine for the
	//     process lifetime. Selecting on ctx.Done lets it exit instead, so the
	//     goroutine always terminates once Receive returns.
	go func() {
		for {
			req, err := stream.Receive()

			select {
			case resCh <- recvResult{req: req, err: err}:
			case <-ctx.Done():
				return
			}

			if err != nil { // stop receiving after any error (including EOF)
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Cancellation path: opportunistically drain one pending receive.
			select {
			case r := <-resCh:
				if r.err != nil && !errors.Is(r.err, io.EOF) {
					return r.err
				}

				return nil
			default:
				return nil
			}
		case r := <-resCh:
			if r.err != nil {
				if errors.Is(r.err, io.EOF) {
					return nil
				}

				return r.err
			}

			if r.req == nil { // Defensive: shouldn't happen unless stream.Receive misbehaves
				l.Warn("ignoring nil request without error")

				continue
			}

			reqMsg := r.req.GetMsg()
			switch msg := reqMsg.(type) {
			case *pb.RunRequest_Console:
				msgConsoleData := msg.Console.GetData()
				switch consoleMsg := msgConsoleData.(type) {
				case *pb.Console_Stdin:
					stdin := consoleMsg.Stdin
					if stdin == nil {
						l.Warn("ignoring nil stdin message")

						continue
					}

					l.Debug("received stdin from client", "bytes", len(stdin))

					select {
					case <-ctx.Done():
						return nil
					case s.stdinCh <- stdin:
					}
				default:
					l.Warn("unexpected console message", "type", fmt.Sprintf("%T", consoleMsg))
				}

			case *pb.RunRequest_FileChunk:
				chunk := msg.FileChunk
				if chunk == nil {
					continue
				}

				transferID := chunk.GetTransferId()

				registerErr := s.registerUploadChunk(transferID, chunk)
				if registerErr != nil {
					l.Warn("error registering upload chunk", "transfer_id", transferID, "err", registerErr)

					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId:   transferID,
								Status:       pb.FileTransferResponse_STATUS_ERROR,
								ErrorMessage: fmt.Sprintf("error processing chunk: %v", registerErr),
							},
						},
					}

					stop, sendErr := s.sendOrStop(stream, res)
					if stop {
						return sendErr
					}

					s.removeUpload(transferID)

					// An unknown transfer ID is a benign race: the agent may have
					// dropped the upload while chunks were still in flight. A broken
					// chunk sequence is not — it is a protocol violation the client
					// cannot recover from, so it terminates the run.
					if errors.Is(registerErr, ErrBadFileTransfer) {
						return registerErr
					}

					continue
				}

				// Acknowledge the chunk.
				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferResponse{
						FileTransferResponse: &pb.FileTransferResponse{
							TransferId:        transferID,
							Status:            pb.FileTransferResponse_STATUS_CHUNK_RECEIVED,
							NextChunkExpected: chunk.GetChunkNumber() + 1,
						},
					},
				}

				stop, sendErr := s.sendOrStop(stream, res)
				if stop {
					if sendErr != nil {
						l.Warn("error sending chunk acknowledgment", "transfer_id", transferID, "err", sendErr)
					}

					return sendErr
				}

				// The final chunk completes the upload.
				if chunk.GetIsFinal() {
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId: transferID,
								Status:     pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
							},
						},
					}

					stop, sendErr := s.sendOrStop(stream, res)
					if stop {
						if sendErr != nil {
							l.Warn("error sending transfer complete", "transfer_id", transferID, "err", sendErr)
						}

						return sendErr
					}

					s.removeUpload(transferID)
				}

			case *pb.RunRequest_FileTransferRequest:
				ftReq := msg.FileTransferRequest
				if ftReq == nil {
					continue
				}

				transferID := ftReq.GetTransferId()
				metadata := ftReq.GetMetadata()

				// Only uploads the module has requested are known here.
				upload := s.getUpload(transferID)
				if upload == nil {
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId:   transferID,
								Status:       pb.FileTransferResponse_STATUS_TRANSFER_REJECTED,
								ErrorMessage: "no matching request from module",
							},
						},
					}

					stop, sendErr := s.sendOrStop(stream, res)
					if stop {
						return sendErr
					}

					continue
				}

				// Record the client's metadata (size/name), then accept.
				upload.mu.Lock()
				upload.metadata = metadata
				upload.mu.Unlock()

				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferResponse{
						FileTransferResponse: &pb.FileTransferResponse{
							TransferId: transferID,
							Status:     pb.FileTransferResponse_STATUS_ACCEPTED,
						},
					},
				}

				stop, sendErr := s.sendOrStop(stream, res)
				if stop {
					if sendErr != nil {
						l.Warn("error sending acceptance", "transfer_id", transferID, "err", sendErr)
					}

					return sendErr
				}

			case *pb.RunRequest_FileTransferResponse:
				ftRes := msg.FileTransferResponse
				if ftRes == nil {
					continue
				}

				transferID := ftRes.GetTransferId()

				switch ftRes.GetStatus() {
				case pb.FileTransferResponse_STATUS_ERROR:
					s.removeDownload(transferID)
					s.removeUpload(transferID)
				case pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE:
					if s.isDownloadAwaitingAck(transferID) {
						s.removeDownload(transferID)
					}
				case pb.FileTransferResponse_STATUS_TRANSFER_REJECTED:
					// A rejection can answer either direction: the client refuses to
					// read a file it was asked to upload, or to write a download whose
					// destination is not among the command arguments. Drop both, or the
					// agent keeps streaming a file the client has already refused.
					l.Warn("client rejected transfer",
						"transfer_id", transferID, "reason", ftRes.GetErrorMessage())

					s.removeUpload(transferID)
					s.removeDownload(transferID)
				case pb.FileTransferResponse_STATUS_UNSPECIFIED,
					pb.FileTransferResponse_STATUS_ACCEPTED,
					pb.FileTransferResponse_STATUS_CHUNK_RECEIVED:
					// Not meaningful from the client for these flows; ignore.
				}

			case *pb.RunRequest_Command:
				// Command starts a run and is handled by the RPC entrypoint, not
				// here; it should not arrive during an active session.

			default:
				l.Warn("unexpected message type", "type", fmt.Sprintf("%T", msg))
			}
		}
	}
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/BlindspotSoftware/dutctl/internal/log"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// ErrBadFileTransfer marks a malformed file transfer from the client (a protocol
// violation) so the RPC layer can map it to CodeInvalidArgument rather than
// treating it as an internal fault.
var ErrBadFileTransfer = errors.New("bad file transfer")

// sendToClient serializes all sends on the bidirectional stream and recovers
// from a panic that can occur when the stream is already closed during graceful
// shutdown. The connect BidiStream is not safe for concurrent Send calls, and
// both workers send responses (toClientWorker streams downloads and module
// output; fromClientWorker acks uploads), so every send goes through this lock.
func (s *backend) sendToClient(stream Stream, res *pb.RunResponse) (err error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			s.logger().Warn("recovered from panic in stream.Send", "err", r)

			err = nil
		}
	}()

	return stream.Send(res)
}

// sendDownloadError reports a download read error to the client and drops the
// transfer.
func sendDownloadError(stream Stream, s *backend, transferID string, downloadMetadataSent map[string]bool, err error) {
	s.logger().Warn("error getting chunk for download", "transfer_id", transferID, "err", err)

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId:        transferID,
				Status:            pb.FileTransferResponse_ERROR,
				ErrorMessage:      fmt.Sprintf("error reading file: %v", err),
				NextChunkExpected: 0,
			},
		},
	}

	sendErr := s.sendToClient(stream, res)
	if sendErr != nil {
		s.logger().Warn("error sending download error response", "transfer_id", transferID, "err", sendErr)
	}

	s.removeDownload(transferID)
	delete(downloadMetadataSent, transferID)
}

// sendDownloadMetadata announces a download to the client (its metadata and
// direction). It returns true when a message was sent.
func sendDownloadMetadata(stream Stream, s *backend, transferID string, downloadMetadataSent map[string]bool) bool {
	if downloadMetadataSent[transferID] {
		return false
	}

	download := s.getDownload(transferID)
	if download == nil {
		return false
	}

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileTransferRequest{
			FileTransferRequest: &pb.FileTransferRequest{
				TransferId: transferID,
				Metadata:   download.metadata,
				Direction:  pb.FileTransferRequest_DOWNLOAD,
			},
		},
	}

	sendErr := s.sendToClient(stream, res)
	if sendErr != nil {
		s.logger().Warn("error sending download metadata", "transfer_id", transferID, "err", sendErr)

		return false
	}

	downloadMetadataSent[transferID] = true

	return true
}

// handleDownloadFileTransfer advances a single download: announce its metadata,
// then stream one chunk. It returns true when a message was sent.
func handleDownloadFileTransfer(stream Stream, s *backend, transferID string, downloadMetadataSent map[string]bool) bool {
	// Skip while waiting for the client's acknowledgment of the final chunk.
	if s.isDownloadAwaitingAck(transferID) {
		return false
	}

	// Announce the transfer before streaming any chunk.
	if !downloadMetadataSent[transferID] {
		return sendDownloadMetadata(stream, s, transferID, downloadMetadataSent)
	}

	chunk, isFinal, err := s.getNextChunk(transferID)
	if err != nil {
		sendDownloadError(stream, s, transferID, downloadMetadataSent, err)

		return true
	}

	if chunk == nil {
		return false
	}

	res := &pb.RunResponse{
		Msg: &pb.RunResponse_FileChunk{FileChunk: chunk},
	}

	sendErr := s.sendToClient(stream, res)
	if sendErr != nil {
		s.logger().Warn("error sending download chunk", "transfer_id", transferID, "err", sendErr)

		return false
	}

	if isFinal {
		s.markDownloadAwaitingAck(transferID)
	}

	return true
}

// processFileTransfers announces one pending upload and advances one download per
// call (one at a time for fairness). It returns true when a message was sent, so
// the caller can re-signal for the remaining work.
func processFileTransfers(stream Stream, s *backend, downloadMetadataSent map[string]bool) bool {
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
						Direction:  pb.FileTransferRequest_UPLOAD,
					},
				},
			}

			sendErr := s.sendToClient(stream, res)
			if sendErr != nil {
				s.logger().Warn("error sending upload request", "transfer_id", transferID, "err", sendErr)

				return sent
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
		if handleDownloadFileTransfer(stream, s, transferID, downloadMetadataSent) {
			sent = true

			break // one at a time for fairness
		}
	}

	return sent
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
			if s.IsShuttingDown() {
				continue
			}

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			err := s.sendToClient(stream, res)
			if err != nil {
				l.Warn("error sending print", "err", err)

				return err
			}
		case bytes := <-s.stdoutCh:
			if s.IsShuttingDown() {
				continue
			}

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: bytes}}},
			}

			err := s.sendToClient(stream, res)
			if err != nil {
				return err
			}
		case bytes := <-s.stderrCh:
			if s.IsShuttingDown() {
				continue
			}

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Console{Console: &pb.Console{Data: &pb.Console_Stderr{Stderr: bytes}}},
			}

			err := s.sendToClient(stream, res)
			if err != nil {
				return err
			}
		case <-s.fileTransferNotifyCh:
			if processFileTransfers(stream, s, downloadMetadataSent) {
				// More work may be pending; re-signal.
				s.notifyFileTransfer()
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
								Status:       pb.FileTransferResponse_ERROR,
								ErrorMessage: fmt.Sprintf("error processing chunk: %v", registerErr),
							},
						},
					}

					sendErr := s.sendToClient(stream, res)
					if sendErr != nil {
						return sendErr
					}

					s.removeUpload(transferID)

					continue
				}

				// Acknowledge the chunk.
				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferResponse{
						FileTransferResponse: &pb.FileTransferResponse{
							TransferId:        transferID,
							Status:            pb.FileTransferResponse_CHUNK_RECEIVED,
							NextChunkExpected: chunk.GetChunkNumber() + 1,
						},
					},
				}

				sendErr := s.sendToClient(stream, res)
				if sendErr != nil {
					l.Warn("error sending chunk acknowledgment", "transfer_id", transferID, "err", sendErr)

					return sendErr
				}

				// The final chunk completes the upload.
				if chunk.GetIsFinal() {
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId: transferID,
								Status:     pb.FileTransferResponse_TRANSFER_COMPLETE,
							},
						},
					}

					sendErr := s.sendToClient(stream, res)
					if sendErr != nil {
						l.Warn("error sending transfer complete", "transfer_id", transferID, "err", sendErr)

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
								Status:       pb.FileTransferResponse_TRANSFER_REJECTED,
								ErrorMessage: "no matching request from module",
							},
						},
					}

					sendErr := s.sendToClient(stream, res)
					if sendErr != nil {
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
							Status:     pb.FileTransferResponse_ACCEPTED,
						},
					},
				}

				sendErr := s.sendToClient(stream, res)
				if sendErr != nil {
					l.Warn("error sending acceptance", "transfer_id", transferID, "err", sendErr)

					return sendErr
				}

			case *pb.RunRequest_FileTransferResponse:
				ftRes := msg.FileTransferResponse
				if ftRes == nil {
					continue
				}

				transferID := ftRes.GetTransferId()

				switch ftRes.GetStatus() {
				case pb.FileTransferResponse_ERROR:
					s.removeDownload(transferID)
					s.removeUpload(transferID)
				case pb.FileTransferResponse_TRANSFER_COMPLETE:
					if s.isDownloadAwaitingAck(transferID) {
						s.removeDownload(transferID)
					}
				case pb.FileTransferResponse_TRANSFER_REJECTED:
					s.removeUpload(transferID)
				case pb.FileTransferResponse_STATUS_UNSPECIFIED,
					pb.FileTransferResponse_ACCEPTED,
					pb.FileTransferResponse_CHUNK_RECEIVED:
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

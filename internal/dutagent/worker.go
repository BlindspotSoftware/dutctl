// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dutagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// safeSend wraps stream.Send with panic recovery to handle graceful shutdown.
// If the stream is closed or the handler finishes, Send may panic.
// We recover from that panic and return nil to allow the worker to exit cleanly.
// Normal errors from Send are returned as-is.
func safeSend(stream Stream, res *pb.RunResponse) error {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in stream.Send: %v", r)
		}
	}()

	return stream.Send(res) // Returns error (if any) or nil
}

// sendDownloadError sends an error response for a download transfer and cleans up.
func sendDownloadError(stream Stream, s *session, transferID string, downloadMetadataSent map[string]bool, err error) {
	log.Printf("Error getting chunk for transfer %s: %v", transferID, err)

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

	sendErr := safeSend(stream, res)
	if sendErr != nil {
		log.Printf("handleDownloadFileTransfer: error sending error response: %v", sendErr)
	}

	s.removeDownload(transferID)
	delete(downloadMetadataSent, transferID)
}

// sendDownloadMetadata sends the file metadata to the client.
// Returns true if metadata was sent, false otherwise.
func sendDownloadMetadata(stream Stream, s *session, transferID string, downloadMetadataSent map[string]bool) bool {
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

	sendErr := safeSend(stream, res)
	if sendErr != nil {
		log.Printf("handleDownloadFileTransfer: error sending metadata: %v", sendErr)

		return false
	}

	downloadMetadataSent[transferID] = true

	return true
}

// handleDownloadFileTransfer processes a single download transfer for sending to the client.
// Returns true if work was done (a message was sent), false if nothing to do.
func handleDownloadFileTransfer(stream Stream, s *session, transferID string, downloadMetadataSent map[string]bool) bool {
	// Skip if waiting for client acknowledgment
	if s.isDownloadAwaitingAck(transferID) {
		return false
	}

	// Send metadata first
	if !downloadMetadataSent[transferID] {
		return sendDownloadMetadata(stream, s, transferID, downloadMetadataSent)
	}

	// Get next chunk
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

	sendErr := safeSend(stream, res)
	if sendErr != nil {
		log.Printf("handleDownloadFileTransfer: error sending chunk: %v", sendErr)

		return false
	}

	if isFinal {
		s.markDownloadAwaitingAck(transferID)
	}

	return true
}

// processFileTransfers handles pending upload requests and download chunks.
// Returns true if any work was done.
func processFileTransfers(stream Stream, s *session, downloadMetadataSent map[string]bool) bool {
	sent := false

	// Send FileTransferRequest for new uploads that haven't been announced yet.
	if !s.IsShuttingDown() {
		for _, transferID := range s.getActiveUploads() {
			upload := s.getUpload(transferID)
			if upload != nil && upload.metadata != nil && !upload.requestSent {
				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferRequest{
						FileTransferRequest: &pb.FileTransferRequest{
							TransferId: transferID,
							Metadata:   upload.metadata,
							Direction:  pb.FileTransferRequest_UPLOAD,
						},
					},
				}

				sendErr := safeSend(stream, res)
				if sendErr != nil {
					log.Printf("toClientWorker: error sending upload request: %v", sendErr)

					return sent
				}

				upload.requestSent = true
				sent = true

				break // One at a time
			}
		}
	}

	// Send download metadata or chunks. Iterate active downloads and process
	// the first one that has work available.
	for _, transferID := range s.getActiveDownloads() {
		if handleDownloadFileTransfer(stream, s, transferID, downloadMetadataSent) {
			sent = true

			break // One at a time for fairness
		}
	}

	return sent
}

// handleConsoleMessage handles stdout/stderr console messages.
func handleConsoleMessage(stream Stream, s *session, bytes []byte, isStderr bool) error {
	// During shutdown, discard messages but don't send
	if s.IsShuttingDown() {
		return nil
	}

	var res *pb.RunResponse
	if isStderr {
		res = &pb.RunResponse{
			Msg: &pb.RunResponse_Console{
				Console: &pb.Console{
					Data: &pb.Console_Stderr{Stderr: bytes},
				},
			},
		}
	} else {
		res = &pb.RunResponse{
			Msg: &pb.RunResponse_Console{
				Console: &pb.Console{
					Data: &pb.Console_Stdout{Stdout: bytes},
				},
			},
		}
	}

	err := safeSend(stream, res)
	if err != nil {
		if isStderr {
			log.Printf("toClientWorker: error sending stderr: %v", err)
		} else {
			log.Printf("toClientWorker: error sending stdout: %v", err)
		}

		return err
	}

	return nil
}

// toClientWorker sends messages from the module session to the client.
// This function is an infinite loop. It terminates when the context is cancelled.
//
// It handles:
// - Print/Console messages from modules.
// - FileTransferRequest messages with metadata to initiate downloads.
// - FileChunk messages for downloads (agent -> client).
//
//nolint:cyclop // main select loop inherently has multiple cases
func toClientWorker(ctx context.Context, stream Stream, s *session) error {
	// Track which downloads have had their metadata sent
	downloadMetadataSent := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return nil

		case str := <-s.printCh:
			// During shutdown, discard messages but don't send
			if s.IsShuttingDown() {
				continue
			}

			res := &pb.RunResponse{
				Msg: &pb.RunResponse_Print{Print: &pb.Print{Text: []byte(str)}},
			}

			err := safeSend(stream, res)
			if err != nil {
				log.Printf("toClientWorker: error sending print: %v", err)

				return err
			}

		case bytes := <-s.stdoutCh:
			err := handleConsoleMessage(stream, s, bytes, false)
			if err != nil {
				return err
			}

		case bytes := <-s.stderrCh:
			err := handleConsoleMessage(stream, s, bytes, true)
			if err != nil {
				return err
			}

		case <-s.fileTransferNotifyCh:
			if processFileTransfers(stream, s, downloadMetadataSent) {
				// Re-signal: more work may be pending.
				s.notifyFileTransfer()
			}
		}
	}
}

// fromClientWorker reads messages from the client and routes them appropriately.
// This function is an infinite loop. It terminates when an error (including io.EOF) occurs.
//
// It handles:
// - Console messages for interactive input
// - FileChunk messages for uploads (client -> agent)
// - FileTransferRequest messages to initiate downloads
// - FileTransferResponse messages to acknowledge transfers
//
//nolint:cyclop,funlen,gocognit,gocyclo,maintidx
func fromClientWorker(ctx context.Context, stream Stream, s *session) error {
	type recvResult struct {
		req *pb.RunRequest
		err error
	}

	// Single goroutine performing blocking Receive calls and forwarding results.
	resCh := make(chan recvResult)

	// Receive loop goroutine rationale:
	//
	// We offload blocking stream.Receive calls to this goroutine so the main select
	// can remain responsive to ctx cancellation. The goroutine will keep calling
	// Receive until an error (including io.EOF) occurs, then return.
	//
	// Potential leak concern: If ctx is cancelled while Receive is blocked the
	// goroutine keeps waiting. This is acceptable because, by contract, the RPC
	// stream is closed by the client (EOF) or ends with an error shortly after
	// module completion / broker cancellation; that closure unblocks Receive and
	// the goroutine exits, so it does not leak for the lifetime of the process.
	go func() {
		for {
			req, err := stream.Receive()
			resCh <- recvResult{req: req, err: err}

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
						continue
					}

					select {
					case <-ctx.Done():
						return nil
					case s.stdinCh <- stdin:
					}

				default:
					log.Printf("Unexpected Console message %T", consoleMsg)
				}

			case *pb.RunRequest_FileChunk:
				chunk := msg.FileChunk
				if chunk == nil {
					continue
				}

				transferID := chunk.GetTransferId()

				// Register or update the upload with this chunk.
				registerErr := s.registerUploadChunk(transferID, chunk)
				if registerErr != nil {
					log.Printf("Error registering upload chunk: %v", registerErr)

					// Send error response
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId:   transferID,
								Status:       pb.FileTransferResponse_ERROR,
								ErrorMessage: fmt.Sprintf("error processing chunk: %v", registerErr),
							},
						},
					}

					sendErr := stream.Send(res)
					if sendErr != nil {
						return sendErr
					}

					// Cleanup upload state - close pipe and remove from tracking
					s.removeUpload(transferID)

					continue
				}

				// Send acknowledgment.
				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferResponse{
						FileTransferResponse: &pb.FileTransferResponse{
							TransferId:        transferID,
							Status:            pb.FileTransferResponse_CHUNK_RECEIVED,
							NextChunkExpected: chunk.GetChunkNumber() + 1,
						},
					},
				}

				sendErr := stream.Send(res)
				if sendErr != nil {
					log.Printf("fromClientWorker: error sending chunk acknowledgment: %v", sendErr)

					return sendErr
				}

				// If this was the final chunk, send completion response
				if chunk.GetIsFinal() {
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId: transferID,
								Status:     pb.FileTransferResponse_TRANSFER_COMPLETE,
							},
						},
					}

					sendErr := stream.Send(res)
					if sendErr != nil {
						log.Printf("fromClientWorker: error sending transfer complete: %v", sendErr)

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
				// Check if this is a known transfer (module called RequestFile)
				upload := s.getUpload(transferID)
				if upload == nil {
					// Send rejection
					res := &pb.RunResponse{
						Msg: &pb.RunResponse_FileTransferResponse{
							FileTransferResponse: &pb.FileTransferResponse{
								TransferId:   transferID,
								Status:       pb.FileTransferResponse_TRANSFER_REJECTED,
								ErrorMessage: "no matching request from module",
							},
						},
					}

					sendErr := stream.Send(res)
					if sendErr != nil {
						return sendErr
					}

					continue
				}

				// Update metadata with client's info (protected by mutex)
				upload.mu.Lock()
				upload.metadata = metadata
				upload.mu.Unlock()

				// Send acceptance
				res := &pb.RunResponse{
					Msg: &pb.RunResponse_FileTransferResponse{
						FileTransferResponse: &pb.FileTransferResponse{
							TransferId: transferID,
							Status:     pb.FileTransferResponse_ACCEPTED,
						},
					},
				}

				sendErr := stream.Send(res)
				if sendErr != nil {
					log.Printf("fromClientWorker: error sending acceptance: %v", sendErr)

					return sendErr
				}

			case *pb.RunRequest_FileTransferResponse:
				ftRes := msg.FileTransferResponse
				if ftRes == nil {
					continue
				}

				transferID := ftRes.GetTransferId()
				status := ftRes.GetStatus()

				switch status {
				case pb.FileTransferResponse_ERROR:
					s.removeDownload(transferID)
					s.removeUpload(transferID)
				case pb.FileTransferResponse_TRANSFER_COMPLETE:
					// Remove download on client confirmation
					if s.isDownloadAwaitingAck(transferID) {
						s.removeDownload(transferID)
					}
				case pb.FileTransferResponse_TRANSFER_REJECTED:
					s.removeUpload(transferID)
				case pb.FileTransferResponse_CHUNK_RECEIVED:
					// Used for upload flows, ignore for downloads
				}

			case *pb.RunRequest_Command:
				// Command is handled by the broker, not here
				// This shouldn't arrive during an active session

			default:
				// Unexpected message type
			}
		}
	}
}

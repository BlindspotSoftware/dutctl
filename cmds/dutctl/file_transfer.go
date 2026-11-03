// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/BlindspotSoftware/dutctl/internal/digest"
	"github.com/BlindspotSoftware/dutctl/internal/output"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

const clientChunkSize = 1024 * 1024 // 1MB chunks

// chunkStream is the send side of the run stream the file-transfer manager needs.
// sendStream satisfies it, and a test can substitute a recording fake.
type chunkStream interface {
	Send(req *pb.RunRequest) error
}

// sendStream serializes all sends on the run stream. Connect permits Send and
// Receive from separate goroutines but forbids concurrent Send calls, and the
// client sends from three: the receive loop (transfer responses), the send
// loop (console input), and the goroutine sendUploadInChunks spawns. Receive is not wrapped: it
// stays on the raw stream, which is what makes the split legal.
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
	direction        string // "upload" or "download"
	expectedChunkNum int32  // For validating chunk sequence on download
	mu               sync.Mutex

	// file is the upload's source, or the download's temporary file until it is
	// moved to path. Guarded by mu.
	file *os.File
	// bytes counts what has moved so far, for the transfer report. Guarded by mu.
	bytes int
	// sum digests the chunks as they move. Only one goroutine writes it: the
	// upload sender, or the receive loop for a download.
	sum hash.Hash
	// sentSum is an upload's digest, for the report. Guarded by mu.
	sentSum []byte

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

// clientFileTransferManager manages file transfers on the client side. Following
// ADR-0014, it reads and writes the paths the agent names.
type clientFileTransferManager struct {
	transfers map[string]*clientFileTransferState
	mu        sync.RWMutex

	// report is called on the receive loop for every file that has moved.
	report func(output.FileTransfer)
}

func newClientFileTransferManager(report func(output.FileTransfer)) *clientFileTransferManager {
	return &clientFileTransferManager{
		transfers: make(map[string]*clientFileTransferState),
		report:    report,
	}
}

func (m *clientFileTransferManager) registerTransfer(transferID, path, direction string) *clientFileTransferState {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := &clientFileTransferState{
		transferID: transferID,
		path:       path,
		direction:  direction,
		sum:        digest.New(),
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

// removeTransfer drops a transfer and closes its file. An unfinished download's
// temporary file is deleted, so the destination keeps what it held before.
func (m *clientFileTransferManager) removeTransfer(transferID string) {
	m.mu.Lock()
	state, exists := m.transfers[transferID]
	delete(m.transfers, transferID)
	m.mu.Unlock()

	if !exists {
		return
	}

	state.stop()

	state.mu.Lock()
	file := state.file
	state.file = nil
	state.mu.Unlock()

	if file == nil {
		return
	}

	file.Close()

	if state.direction == "download" {
		os.Remove(file.Name())
	}
}

// removeAll drops every transfer still in progress, once the run has ended.
func (m *clientFileTransferManager) removeAll() {
	m.mu.RLock()

	ids := make([]string, 0, len(m.transfers))
	for id := range m.transfers {
		ids = append(ids, id)
	}

	m.mu.RUnlock()

	for _, id := range ids {
		m.removeTransfer(id)
	}
}

// dropTransfer removes a transfer, tells the agent why, and returns err.
func (m *clientFileTransferManager) dropTransfer(transferID string, err error, stream chunkStream) error {
	m.removeTransfer(transferID)

	sendErr := m.sendTransferError(transferID, err.Error(), stream)
	if sendErr != nil {
		return errors.Join(err, fmt.Errorf("sending transfer rejection: %w", sendErr))
	}

	return err
}

// sendChunkToAgent sends a file chunk to the agent. sum is the digest of the
// whole file, sent with the final chunk only.
func (m *clientFileTransferManager) sendChunkToAgent(
	transferID string,
	chunkNum int32,
	data []byte,
	isFinal bool,
	sum []byte,
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
				Sha256:      sum,
			},
		},
	}

	return stream.Send(chunk)
}

// handleUploadRequest processes a request to upload a file to the agent.
func (m *clientFileTransferManager) handleUploadRequest(transferID, path string, stream chunkStream) error {
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
		m.removeTransfer(transferID)

		return fmt.Errorf("announcing upload: %w", acceptErr)
	}

	slog.Debug("uploading file to device", "file", filepath.Base(path))

	m.sendUploadInChunks(transferID, path, file, state, stream)

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

// sendUploadInChunks reads and sends a file in chunks to the agent, keeping one
// chunk outstanding at a time: each chunk waits for the agent's CHUNK_RECEIVED
// before the next is read and sent.
//
// The agent acknowledges a chunk only once the module has consumed it, so this
// is genuine end-to-end backpressure: the client cannot outrun the module, and
// the client's memory stays bounded at one chunk regardless of file size. The
// cost is one round trip per megabyte, which is far below the rate at which a
// module writes a chunk to hardware.
//
// The transfer stays registered after the final chunk, until the agent's
// TRANSFER_COMPLETE reports it, or the end of the run drops it.
func (m *clientFileTransferManager) sendUploadInChunks(
	transferID, path string,
	file *os.File,
	state *clientFileTransferState,
	stream chunkStream,
) {
	go func() {
		for chunkNum := int32(0); ; chunkNum++ {
			chunkData := make([]byte, clientChunkSize)
			bytesRead, readErr := file.Read(chunkData)

			isFinal := errors.Is(readErr, io.EOF)
			if readErr != nil && !isFinal {
				// Tell the agent, or its module waits for the next chunk forever.
				slog.Warn("upload failed", "err",
					m.dropTransfer(transferID, fmt.Errorf("reading %q: %w", path, readErr), stream))

				return
			}

			state.sum.Write(chunkData[:bytesRead])

			var sum []byte
			if isFinal {
				sum = state.sum.Sum(nil)
			}

			state.mu.Lock()
			state.bytes += bytesRead
			state.sentSum = sum
			state.mu.Unlock()

			chunkErr := m.sendChunkToAgent(transferID, chunkNum, chunkData[:bytesRead], isFinal, sum, stream)
			if chunkErr != nil {
				slog.Warn("error sending file chunk", "err", chunkErr)
				m.removeTransfer(transferID)

				return
			}

			// The final chunk is answered with TRANSFER_COMPLETE, not an ack.
			if isFinal || !waitForChunkAck(state, chunkNum+1) {
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

// handleFileTransferRequest handles a FileTransferRequest from the agent: a
// request for a file the client uploads, or the announcement of a download. An
// unknown direction is refused and fails the run, since the agent may be
// waiting to hand over a file the client would otherwise silently drop.
func (m *clientFileTransferManager) handleFileTransferRequest(ftReq *pb.FileTransferRequest, stream chunkStream) error {
	transferID := ftReq.GetTransferId()
	path := ftReq.GetMetadata().GetPath()

	switch direction := ftReq.GetDirection(); direction {
	case pb.FileTransferRequest_DIRECTION_UPLOAD:
		return m.handleUploadRequest(transferID, path, stream)

	case pb.FileTransferRequest_DIRECTION_DOWNLOAD:
		// The agent streams the chunks right away; they are written to path.
		m.registerTransfer(transferID, path, "download")
		slog.Debug("downloading file", "file", filepath.Base(path))

		return nil

	default:
		return m.dropTransfer(transferID,
			fmt.Errorf("unknown transfer direction %v (agent/client version mismatch?)", direction), stream)
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

// handleFileChunk writes a download chunk into the transfer's temporary file,
// next to the destination. The final chunk moves the file into place, so a
// broken download leaves the destination as it was.
//
// A failure drops the transfer, tells the agent, and is returned so that the
// run fails: the module's SendFile returned long before, so nothing on the
// agent's side would report it.
func (m *clientFileTransferManager) handleFileChunk(chunk *pb.FileChunk, stream chunkStream) error {
	transferID := chunk.GetTransferId()

	state := m.getTransfer(transferID)
	if state == nil {
		return m.dropTransfer(transferID, fmt.Errorf("chunk for unknown transfer %s", transferID), stream)
	}

	if num := chunk.GetChunkNumber(); num != state.expectedChunkNum {
		return m.dropTransfer(transferID,
			fmt.Errorf("download %q: got chunk %d, want %d", state.path, num, state.expectedChunkNum), stream)
	}

	if chunk.GetChunkNumber() == 0 {
		// CreateTemp makes the file with mode 0600, as ADR-0014 requires.
		file, err := os.CreateTemp(filepath.Dir(state.path), "."+filepath.Base(state.path)+".*.part")
		if err != nil {
			return m.dropTransfer(transferID, fmt.Errorf("creating %q: %w", state.path, err), stream)
		}

		state.mu.Lock()
		state.file = file
		state.mu.Unlock()
	}

	state.mu.Lock()
	file := state.file
	state.mu.Unlock()

	_, err := file.Write(chunk.GetChunkData())
	if err != nil {
		return m.dropTransfer(transferID, fmt.Errorf("writing %q: %w", state.path, err), stream)
	}

	state.sum.Write(chunk.GetChunkData())
	state.expectedChunkNum++
	state.bytes += len(chunk.GetChunkData())

	if !chunk.GetIsFinal() {
		return nil
	}

	return m.finishDownload(state, chunk.GetSha256(), stream)
}

// finishDownload verifies a complete download against want, the digest the
// agent sent, moves it to its destination, reports it, and confirms it to the
// agent.
func (m *clientFileTransferManager) finishDownload(state *clientFileTransferState, want []byte, stream chunkStream) error {
	sum := state.sum.Sum(nil)

	// Verified before the rename: damaged bytes must not replace the destination.
	switch {
	case digest.Missing(want):
		slog.Warn("agent sent no checksum, file not verified", "path", state.path)
	case !digest.Match(sum, want):
		return m.dropTransfer(state.transferID, fmt.Errorf("received file %q: %w: want %s, got %s (%d bytes)",
			state.path, digest.ErrMismatch, digest.Hex(want), digest.Hex(sum), state.bytes), stream)
	}

	state.mu.Lock()
	file := state.file
	state.file = nil // from here on, removeTransfer must not delete it
	state.mu.Unlock()

	// Close reports a failed write-back, for example over NFS, so it is checked.
	err := file.Close()
	if err == nil {
		err = os.Rename(file.Name(), state.path)
	}

	if err != nil {
		os.Remove(file.Name())

		return m.dropTransfer(state.transferID, fmt.Errorf("saving %q: %w", state.path, err), stream)
	}

	m.removeTransfer(state.transferID)
	m.report(output.FileTransfer{Direction: "received", Path: state.path, Bytes: state.bytes, SHA256: digest.Hex(sum)})

	err = m.sendTransferComplete(state.transferID, stream)
	if err != nil {
		return fmt.Errorf("confirming download %q: %w", state.path, err)
	}

	return nil
}

// handleFileTransferResponse handles a FileTransferResponse from the agent. It
// returns an error for a download that failed on the agent: the module's
// SendFile returned long before, so the run would otherwise succeed without the
// file. A failed upload fails the module that requested it, which reports why.
func (m *clientFileTransferManager) handleFileTransferResponse(ftRes *pb.FileTransferResponse) error {
	transferID := ftRes.GetTransferId()
	state := m.getTransfer(transferID)

	switch ftRes.GetStatus() {
	case pb.FileTransferResponse_STATUS_ERROR, pb.FileTransferResponse_STATUS_TRANSFER_REJECTED:
		return m.transferFailed(state, ftRes)
	case pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE:
		m.removeTransfer(transferID)
		m.reportUpload(state)
	case pb.FileTransferResponse_STATUS_CHUNK_RECEIVED:
		// Release the upload sender to read and send the next chunk. A
		// non-blocking send keeps the receive loop moving: the channel is buffered
		// to one and only ever holds the acknowledgment for the chunk in flight.
		if state != nil {
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

	return nil
}

// transferFailed drops a transfer the agent ended with an error. Dropping it
// also closes the file the upload path opened.
func (m *clientFileTransferManager) transferFailed(state *clientFileTransferState, ftRes *pb.FileTransferResponse) error {
	m.removeTransfer(ftRes.GetTransferId())

	if state != nil && state.direction == "download" {
		return fmt.Errorf("download %q failed on the agent: %s", state.path, ftRes.GetErrorMessage())
	}

	slog.Warn("file transfer failed on the agent",
		"transfer_id", ftRes.GetTransferId(), "status", ftRes.GetStatus(), "reason", ftRes.GetErrorMessage())

	return nil
}

// reportUpload reports an upload the agent has confirmed. state is nil for a
// transfer the client no longer knows.
func (m *clientFileTransferManager) reportUpload(state *clientFileTransferState) {
	if state == nil || state.direction != "upload" {
		return
	}

	state.mu.Lock()
	sent, sum := state.bytes, state.sentSum
	state.mu.Unlock()

	m.report(output.FileTransfer{Direction: "sent", Path: state.path, Bytes: sent, SHA256: digest.Hex(sum)})
}

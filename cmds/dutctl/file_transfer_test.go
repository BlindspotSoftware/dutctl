// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/digest"
	"github.com/BlindspotSoftware/dutctl/internal/output"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// reports records the transfers a manager reports.
type reports struct {
	mu  sync.Mutex
	got []output.FileTransfer
}

func (r *reports) add(transfer output.FileTransfer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.got = append(r.got, transfer)
}

func (r *reports) all() []output.FileTransfer {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]output.FileTransfer(nil), r.got...)
}

// newTestManager returns a manager and the record of what it reports.
func newTestManager() (*clientFileTransferManager, *reports) {
	r := &reports{}

	return newClientFileTransferManager(r.add), r
}

// announceDownload hands m the agent's announcement of a download to dest.
func announceDownload(t *testing.T, m *clientFileTransferManager, id, dest string, stream chunkStream) {
	t.Helper()

	err := m.handleFileTransferRequest(&pb.FileTransferRequest{
		TransferId: id,
		Direction:  pb.FileTransferRequest_DIRECTION_DOWNLOAD,
		Metadata:   &pb.FileMetadata{Path: dest},
	}, stream)
	if err != nil {
		t.Fatalf("announcing download: %v", err)
	}
}

// ackUploads stands in for the agent: it acknowledges each upload chunk so the
// sender proceeds, and confirms the upload after the final one.
func ackUploads(m *clientFileTransferManager) func(*pb.FileChunk) {
	return func(c *pb.FileChunk) {
		res := &pb.FileTransferResponse{
			TransferId:        c.GetTransferId(),
			Status:            pb.FileTransferResponse_STATUS_CHUNK_RECEIVED,
			NextChunkExpected: c.GetChunkNumber() + 1,
		}
		if c.GetIsFinal() {
			res = &pb.FileTransferResponse{TransferId: c.GetTransferId(), Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE}
		}

		_ = m.handleFileTransferResponse(res)
	}
}

// leftovers returns the names in dir other than keep, such as temporary files.
func leftovers(t *testing.T, dir, keep string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var out []string

	for _, e := range entries {
		if e.Name() != keep {
			out = append(out, e.Name())
		}
	}

	return out
}

// recordingStream is a chunkStream that captures every RunRequest sent through
// it, so a test can assert what the manager put on the wire. It is safe for the
// concurrent sends the upload goroutine performs.
type recordingStream struct {
	mu   sync.Mutex
	sent []*pb.RunRequest
	err  error // when non-nil, Send returns it instead of recording

	// onChunk, when set, is invoked after a FileChunk is recorded. Uploads keep
	// one chunk outstanding, so a test that wants the sender to progress has to
	// stand in for the agent and acknowledge.
	onChunk func(*pb.FileChunk)
}

func (r *recordingStream) Send(req *pb.RunRequest) error {
	r.mu.Lock()

	if r.err != nil {
		r.mu.Unlock()

		return r.err
	}

	r.sent = append(r.sent, req)
	onChunk := r.onChunk

	r.mu.Unlock()

	if onChunk != nil {
		if c, ok := req.GetMsg().(*pb.RunRequest_FileChunk); ok {
			onChunk(c.FileChunk)
		}
	}

	return nil
}

func (r *recordingStream) messages() []*pb.RunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]*pb.RunRequest(nil), r.sent...)
}

// responses returns every FileTransferResponse the manager sent.
func (r *recordingStream) responses() []*pb.FileTransferResponse {
	var out []*pb.FileTransferResponse

	for _, m := range r.messages() {
		if ftr, ok := m.GetMsg().(*pb.RunRequest_FileTransferResponse); ok {
			out = append(out, ftr.FileTransferResponse)
		}
	}

	return out
}

// chunks returns every FileChunk the manager sent (upload path).
func (r *recordingStream) chunks() []*pb.FileChunk {
	var out []*pb.FileChunk

	for _, m := range r.messages() {
		if fc, ok := m.GetMsg().(*pb.RunRequest_FileChunk); ok {
			out = append(out, fc.FileChunk)
		}
	}

	return out
}

func statuses(res []*pb.FileTransferResponse) []pb.FileTransferResponse_Status {
	out := make([]pb.FileTransferResponse_Status, len(res))
	for i, r := range res {
		out[i] = r.GetStatus()
	}

	return out
}

func downloadChunk(id string, num int32, data []byte, final bool) *pb.FileChunk {
	return &pb.FileChunk{TransferId: id, ChunkNumber: num, ChunkData: data, IsFinal: final}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("condition not met within timeout")
}

// Download happy path: chunks arrive in order, the file is written verbatim,
// completion is sent, the file is reported, and the transfer is cleaned up.
func TestHandleFileChunk_DownloadHappyPath(t *testing.T) {
	t.Parallel()

	content := []byte("hello chunked world")
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")

	m, reported := newTestManager()
	stream := &recordingStream{}

	announceDownload(t, m, "t1", dest, stream)

	if err := m.handleFileChunk(downloadChunk("t1", 0, content[:10], false), stream); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	sum := sha256.Sum256(content)
	final := downloadChunk("t1", 1, content[10:], true)
	final.Sha256 = sum[:]

	if err := m.handleFileChunk(final, stream); err != nil {
		t.Fatalf("chunk 1 (final): %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("downloaded content = %q, want %q", got, content)
	}

	// The agent waits only for TRANSFER_COMPLETE; the stream itself bounds how
	// far it can run ahead.
	want := []pb.FileTransferResponse_Status{pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE}
	if got := statuses(stream.responses()); !equalStatuses(got, want) {
		t.Errorf("response sequence = %v, want %v", got, want)
	}

	if m.getTransfer("t1") != nil {
		t.Errorf("transfer not removed after completion")
	}

	wantReport := output.FileTransfer{Direction: "received", Path: dest, Bytes: len(content), SHA256: digest.Hex(sum[:])}
	if got := reported.all(); len(got) != 1 || got[0] != wantReport {
		t.Errorf("reports = %+v, want [%+v]", got, wantReport)
	}

	if left := leftovers(t, dir, "out.bin"); len(left) != 0 {
		t.Errorf("files left next to the destination: %v", left)
	}
}

// A download that breaks off leaves the destination as it was, rather than
// truncated, removes its temporary file, and fails the run.
func TestHandleFileChunk_BrokenDownloadKeepsOldFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := filepath.Join(dir, "image.bin")

	if err := os.WriteFile(dest, []byte("last good image"), 0o600); err != nil {
		t.Fatalf("writing old file: %v", err)
	}

	m, reported := newTestManager()
	stream := &recordingStream{}

	announceDownload(t, m, "t1", dest, stream)

	if err := m.handleFileChunk(downloadChunk("t1", 0, []byte("half of a new"), false), stream); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	// The agent cannot read the rest of the file.
	err := m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "t1", Status: pb.FileTransferResponse_STATUS_ERROR, ErrorMessage: "read error",
	})
	if err == nil {
		t.Error("a download that failed on the agent did not fail the run")
	}

	if got, _ := os.ReadFile(dest); string(got) != "last good image" {
		t.Errorf("destination = %q, want the old content", got)
	}

	if left := leftovers(t, dir, "image.bin"); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	if got := reported.all(); len(got) != 0 {
		t.Errorf("a failed download was reported: %+v", got)
	}
}

// A download that does not match the agent's digest was damaged on the way: it
// fails the run and leaves the destination as it was.
func TestHandleFileChunk_CorruptDownloadKeepsOldFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dest := filepath.Join(dir, "image.bin")

	if err := os.WriteFile(dest, []byte("last good image"), 0o600); err != nil {
		t.Fatalf("writing old file: %v", err)
	}

	m, reported := newTestManager()
	stream := &recordingStream{}

	announceDownload(t, m, "t1", dest, stream)

	sent := sha256.Sum256([]byte("what the agent read"))
	final := downloadChunk("t1", 0, []byte("what arrived"), true)
	final.Sha256 = sent[:]

	if err := m.handleFileChunk(final, stream); !errors.Is(err, digest.ErrMismatch) {
		t.Errorf("handleFileChunk err = %v, want digest.ErrMismatch", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) {
		t.Errorf("expected a TRANSFER_REJECTED, got %v", statuses(stream.responses()))
	}

	if got, _ := os.ReadFile(dest); string(got) != "last good image" {
		t.Errorf("destination = %q, want the old content", got)
	}

	if left := leftovers(t, dir, "image.bin"); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	if got := reported.all(); len(got) != 0 {
		t.Errorf("a corrupted download was reported: %+v", got)
	}
}

// A download the client cannot save is refused to the agent and fails the run,
// since the module that sent it has already returned.
func TestHandleFileChunk_UnwritableDestinationFailsRun(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "missing-dir", "out.bin")

	m, _ := newTestManager()
	stream := &recordingStream{}

	announceDownload(t, m, "t1", dest, stream)

	if err := m.handleFileChunk(downloadChunk("t1", 0, []byte("x"), true), stream); err == nil {
		t.Error("handleFileChunk = nil, want an error that fails the run")
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) {
		t.Errorf("expected a TRANSFER_REJECTED, got %v", statuses(stream.responses()))
	}

	if m.getTransfer("t1") != nil {
		t.Error("transfer not removed after the failure")
	}
}

// A chunk arriving out of order is rejected and the transfer is torn down,
// rather than being written to the file.
func TestHandleFileChunk_OutOfOrderRejected(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.bin")

	m, _ := newTestManager()
	stream := &recordingStream{}

	announceDownload(t, m, "t1", dest, stream)

	// Expected chunk 0, send chunk 1.
	if err := m.handleFileChunk(downloadChunk("t1", 1, []byte("x"), false), stream); err == nil {
		t.Error("handleFileChunk = nil, want an error that fails the run")
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) {
		t.Errorf("expected a TRANSFER_REJECTED, got %v", statuses(stream.responses()))
	}

	if m.getTransfer("t1") != nil {
		t.Errorf("transfer not removed after sequence error")
	}

	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("destination file should not exist after a rejected chunk")
	}
}

// A chunk for a transfer that was never announced is rejected.
func TestHandleFileChunk_UnknownTransfer(t *testing.T) {
	t.Parallel()

	m, _ := newTestManager()
	stream := &recordingStream{}

	if err := m.handleFileChunk(downloadChunk("ghost", 0, []byte("x"), true), stream); err == nil {
		t.Error("handleFileChunk = nil, want an error that fails the run")
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) {
		t.Errorf("expected rejection for unknown transfer, got %v", statuses(stream.responses()))
	}
}

// Upload happy path: the manager accepts, then streams the file back in chunks
// whose concatenation equals the file, ending with a final chunk, and reports
// the file once the agent confirms it.
func TestHandleUploadRequest_StreamsFile(t *testing.T) {
	t.Parallel()

	content := []byte("upload me please")
	src := filepath.Join(t.TempDir(), "src.bin")

	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	m, reported := newTestManager()
	stream := &recordingStream{}
	stream.onChunk = ackUploads(m)

	if err := m.handleUploadRequest("u1", src, stream); err != nil {
		t.Fatalf("handleUploadRequest: %v", err)
	}

	// The upload streams from a goroutine; wait for the final chunk.
	waitFor(t, func() bool {
		cs := stream.chunks()

		return len(cs) > 0 && cs[len(cs)-1].GetIsFinal()
	})

	var got []byte
	for _, c := range stream.chunks() {
		got = append(got, c.GetChunkData()...)
	}

	if string(got) != string(content) {
		t.Errorf("uploaded bytes = %q, want %q", got, content)
	}

	// The client answers the agent's request with the file's measured size, which
	// is the only point at which the agent learns how big the upload is.
	var announced *pb.FileTransferRequest

	for _, msg := range stream.messages() {
		if ftr, ok := msg.GetMsg().(*pb.RunRequest_FileTransferRequest); ok {
			announced = ftr.FileTransferRequest

			break
		}
	}

	if announced == nil {
		t.Fatalf("upload was not announced; messages = %d", len(stream.messages()))
	}

	if announced.GetDirection() != pb.FileTransferRequest_DIRECTION_UPLOAD {
		t.Errorf("announced direction = %v, want UPLOAD", announced.GetDirection())
	}

	if got, want := announced.GetMetadata().GetSize(), int64(len(content)); got != want {
		t.Errorf("announced size = %d, want %d", got, want)
	}

	// Chunks are numbered from zero, in order.
	for i, c := range stream.chunks() {
		if c.GetChunkNumber() != int32(i) {
			t.Errorf("chunk %d has number %d", i, c.GetChunkNumber())
		}
	}

	waitFor(t, func() bool { return m.getTransfer("u1") == nil })

	sum := sha256.Sum256(content)
	if got := stream.chunks()[len(stream.chunks())-1].GetSha256(); !digest.Match(got, sum[:]) {
		t.Errorf("final chunk digest = %s, want %s", digest.Hex(got), digest.Hex(sum[:]))
	}

	wantReport := output.FileTransfer{Direction: "sent", Path: src, Bytes: len(content), SHA256: digest.Hex(sum[:])}
	if got := reported.all(); len(got) != 1 || got[0] != wantReport {
		t.Errorf("reports = %+v, want [%+v]", got, wantReport)
	}
}

// A file the client cannot read is refused to the agent. Otherwise the module
// waits for the next chunk, and the run, forever. A directory opens fine but
// fails on the first read.
func TestHandleUploadRequest_ReadErrorTellsAgent(t *testing.T) {
	t.Parallel()

	m, _ := newTestManager()
	stream := &recordingStream{}

	if err := m.handleUploadRequest("u1", t.TempDir(), stream); err != nil {
		t.Fatalf("handleUploadRequest: %v", err)
	}

	waitFor(t, func() bool { return hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) })

	if m.getTransfer("u1") != nil {
		t.Error("transfer not removed after the read error")
	}
}

// An unknown transfer direction is refused and fails the run rather than being
// acted on.
func TestHandleFileTransferRequest_UnknownDirection(t *testing.T) {
	t.Parallel()

	m, _ := newTestManager()
	stream := &recordingStream{}

	req := &pb.FileTransferRequest{
		TransferId: "t1",
		Metadata:   &pb.FileMetadata{Path: "/x"},
		Direction:  pb.FileTransferRequest_DIRECTION_UNSPECIFIED,
	}

	if err := m.handleFileTransferRequest(req, stream); err == nil {
		t.Error("handleFileTransferRequest = nil, want an error that fails the run")
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_STATUS_TRANSFER_REJECTED) {
		t.Errorf("expected an error response for unknown direction, got %v", statuses(stream.responses()))
	}
}

// A terminal FileTransferResponse from the agent clears the local transfer
// state. Only a failed download fails the run from here: a failed upload fails
// the module that requested it.
func TestHandleFileTransferResponse_TerminalStatusesClearState(t *testing.T) {
	t.Parallel()

	m, _ := newTestManager()
	m.registerTransfer("done", "/a", "upload")
	m.registerTransfer("failed", "/b", "upload")
	m.registerTransfer("broken", "/c", "download")

	if err := m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "done", Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
	}); err != nil {
		t.Errorf("completed upload: %v", err)
	}

	if err := m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "failed", Status: pb.FileTransferResponse_STATUS_ERROR, ErrorMessage: "boom",
	}); err != nil {
		t.Errorf("failed upload: %v, want nil", err)
	}

	if err := m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "broken", Status: pb.FileTransferResponse_STATUS_ERROR, ErrorMessage: "boom",
	}); err == nil {
		t.Error("failed download: nil, want an error that fails the run")
	}

	for _, id := range []string{"done", "failed", "broken"} {
		if m.getTransfer(id) != nil {
			t.Errorf("transfer %q not cleared", id)
		}
	}
}

func hasStatus(res []*pb.FileTransferResponse, want pb.FileTransferResponse_Status) bool {
	for _, r := range res {
		if r.GetStatus() == want {
			return true
		}
	}

	return false
}

func equalStatuses(a, b []pb.FileTransferResponse_Status) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// unguardedStream models connect's BidiStream, whose Send may not be called
// concurrently with itself. sends is deliberately unsynchronised so the race
// detector reports any overlap; the atomics assert the same thing without it.
type unguardedStream struct {
	sends   int
	inSend  atomic.Int32
	maxSeen atomic.Int32

	// onChunk acknowledges a chunk so the ack-gated upload keeps sending, which
	// is what sustains the overlap this test needs.
	onChunk func(*pb.FileChunk)
}

func (s *unguardedStream) Send(req *pb.RunRequest) error {
	now := s.inSend.Add(1)

	for {
		prev := s.maxSeen.Load()
		if now <= prev || s.maxSeen.CompareAndSwap(prev, now) {
			break
		}
	}

	s.sends++

	time.Sleep(time.Millisecond) // widen the window so overlaps are observable

	s.inSend.Add(-1)

	if s.onChunk != nil {
		if c, ok := req.GetMsg().(*pb.RunRequest_FileChunk); ok {
			s.onChunk(c.FileChunk)
		}
	}

	return nil
}

// TestSendStreamSerializesUploadAndAcks guards the client's send serialisation.
// runRPC sends from three goroutines (the receive routine, the send routine and
// the goroutine sendUploadInChunks spawns), and connect forbids concurrent Send
// calls. Routing them all through sendStream is what keeps that legal; without
// it an upload racing any other message corrupts the stream.
func TestSendStreamSerializesUploadAndAcks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.bin")
	if err := os.WriteFile(path, make([]byte, 4*clientChunkSize), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	raw := &unguardedStream{}
	sender := newSendStream(raw)
	mgr, _ := newTestManager()
	raw.onChunk = ackUploads(mgr)

	// The agent asks for the file: this spawns the upload goroutine.
	if err := mgr.handleUploadRequest("up-1", path, sender); err != nil {
		t.Fatalf("handleUploadRequest: %v", err)
	}

	// Meanwhile the receive loop keeps sending, as it does for any message that
	// arrives while an upload is running.
	for range 50 {
		if err := mgr.sendTransferComplete("dl-1", sender); err != nil {
			t.Fatalf("sendTransferComplete: %v", err)
		}
	}

	waitFor(t, func() bool { return mgr.getTransfer("up-1") == nil })

	if got := raw.maxSeen.Load(); got > 1 {
		t.Errorf("observed %d concurrent Send calls, want at most 1", got)
	}
}

// An upload keeps a single chunk outstanding: until the agent acknowledges,
// nothing further is read or sent. Without that the client streams the whole
// file as fast as it can and next_chunk_expected means nothing.
func TestUploadWaitsForChunkAck(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "image.bin")
	if err := os.WriteFile(path, make([]byte, 4*clientChunkSize), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	m, _ := newTestManager()
	stream := &recordingStream{} // no onChunk: nothing is ever acknowledged

	if err := m.handleUploadRequest("u1", path, stream); err != nil {
		t.Fatalf("handleUploadRequest: %v", err)
	}

	waitFor(t, func() bool { return len(stream.chunks()) >= 1 })

	// Give a would-be runaway sender room to send the rest of the file.
	time.Sleep(200 * time.Millisecond)

	if got := len(stream.chunks()); got != 1 {
		t.Errorf("sent %d chunks without an acknowledgment, want 1", got)
	}

	// Acknowledging releases exactly one more chunk.
	_ = m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId:        "u1",
		Status:            pb.FileTransferResponse_STATUS_CHUNK_RECEIVED,
		NextChunkExpected: 1,
	})

	waitFor(t, func() bool { return len(stream.chunks()) == 2 })

	// Dropping the transfer must release the sender rather than leak it.
	m.removeTransfer("u1")

	time.Sleep(100 * time.Millisecond)

	if got := len(stream.chunks()); got != 2 {
		t.Errorf("sender kept going after the transfer was dropped: %d chunks", got)
	}
}

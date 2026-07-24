// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// recordingStream is a chunkStream that captures every RunRequest sent through
// it, so a test can assert what the manager put on the wire. It is safe for the
// concurrent sends the upload goroutine performs.
type recordingStream struct {
	mu   sync.Mutex
	sent []*pb.RunRequest
	err  error // when non-nil, Send returns it instead of recording
}

func (r *recordingStream) Send(req *pb.RunRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return r.err
	}

	r.sent = append(r.sent, req)

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

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	abs := filepath.Join(t.TempDir(), "f.bin")

	if got := normalizePath(abs); got != abs {
		t.Errorf("already-absolute path changed: got %q want %q", got, abs)
	}

	// A relative path resolves to an absolute one.
	if got := normalizePath("f.bin"); !filepath.IsAbs(got) {
		t.Errorf("relative path not made absolute: %q", got)
	}

	// Two spellings of the same path normalize equal.
	if normalizePath(abs) != normalizePath(filepath.Join(filepath.Dir(abs), ".", "f.bin")) {
		t.Errorf("equivalent paths did not normalize equal")
	}
}

func TestIsValidPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authorized := filepath.Join(dir, "ok.bin")

	m := newClientFileTransferManager([]string{authorized, "unrelated.txt"})

	if !m.isValidPath(authorized) {
		t.Errorf("authorized path rejected")
	}
	// Same target via a relative spelling from a different cwd still won't match
	// unless it resolves to the same absolute path; a clearly different path must
	// be rejected.
	if m.isValidPath(filepath.Join(dir, "other.bin")) {
		t.Errorf("unauthorized path accepted")
	}
}

// Download happy path: chunks arrive in order, the file is written verbatim,
// each chunk is acknowledged, completion is sent, and the transfer is cleaned up.
func TestHandleFileChunk_DownloadHappyPath(t *testing.T) {
	t.Parallel()

	content := []byte("hello chunked world")
	dest := filepath.Join(t.TempDir(), "out.bin")

	m := newClientFileTransferManager([]string{dest})
	stream := &recordingStream{}

	if err := m.handleDownloadRequest("t1", dest, stream); err != nil {
		t.Fatalf("handleDownloadRequest: %v", err)
	}

	if err := m.handleFileChunk(downloadChunk("t1", 0, content[:10], false), stream); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}

	if err := m.handleFileChunk(downloadChunk("t1", 1, content[10:], true), stream); err != nil {
		t.Fatalf("chunk 1 (final): %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}

	if string(got) != string(content) {
		t.Errorf("downloaded content = %q, want %q", got, content)
	}

	// ACCEPTED, then a CHUNK_RECEIVED per chunk, then TRANSFER_COMPLETE.
	want := []pb.FileTransferResponse_Status{
		pb.FileTransferResponse_ACCEPTED,
		pb.FileTransferResponse_CHUNK_RECEIVED,
		pb.FileTransferResponse_CHUNK_RECEIVED,
		pb.FileTransferResponse_TRANSFER_COMPLETE,
	}
	if got := statuses(stream.responses()); !equalStatuses(got, want) {
		t.Errorf("response sequence = %v, want %v", got, want)
	}

	if m.getTransfer("t1") != nil {
		t.Errorf("transfer not removed after completion")
	}
}

// A chunk arriving out of order is rejected and the transfer is torn down,
// rather than being written to the file.
func TestHandleFileChunk_OutOfOrderRejected(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.bin")

	m := newClientFileTransferManager([]string{dest})
	stream := &recordingStream{}

	if err := m.handleDownloadRequest("t1", dest, stream); err != nil {
		t.Fatalf("handleDownloadRequest: %v", err)
	}

	// Expected chunk 0, send chunk 1.
	if err := m.handleFileChunk(downloadChunk("t1", 1, []byte("x"), false), stream); err != nil {
		t.Fatalf("handleFileChunk returned error (should be nil, error sent on stream): %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
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

	m := newClientFileTransferManager(nil)
	stream := &recordingStream{}

	if err := m.handleFileChunk(downloadChunk("ghost", 0, []byte("x"), true), stream); err != nil {
		t.Fatalf("handleFileChunk: %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
		t.Errorf("expected rejection for unknown transfer, got %v", statuses(stream.responses()))
	}
}

// A registered transfer whose path is not among the command arguments is
// refused when its chunks arrive — the path allow-list is enforced per chunk.
func TestHandleFileChunk_PathNotAuthorized(t *testing.T) {
	t.Parallel()

	m := newClientFileTransferManager(nil) // no authorized paths
	stream := &recordingStream{}

	m.registerTransfer("t1", "/etc/shadow", "download")

	if err := m.handleFileChunk(downloadChunk("t1", 0, []byte("x"), true), stream); err != nil {
		t.Fatalf("handleFileChunk: %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
		t.Errorf("expected rejection for unauthorized path, got %v", statuses(stream.responses()))
	}

	if m.getTransfer("t1") != nil {
		t.Errorf("transfer not removed after authorization failure")
	}
}

// A download to a path outside the command arguments is refused up front.
func TestHandleDownloadRequest_Unauthorized(t *testing.T) {
	t.Parallel()

	m := newClientFileTransferManager([]string{"/only/this"})
	stream := &recordingStream{}

	if err := m.handleDownloadRequest("t1", "/somewhere/else", stream); err != nil {
		t.Fatalf("handleDownloadRequest: %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
		t.Errorf("expected rejection, got %v", statuses(stream.responses()))
	}

	if m.getTransfer("t1") != nil {
		t.Errorf("no transfer should be registered for a rejected download")
	}
}

// Upload happy path: the manager accepts, then streams the file back in chunks
// whose concatenation equals the file, ending with a final chunk.
func TestHandleUploadRequest_StreamsFile(t *testing.T) {
	t.Parallel()

	content := []byte("upload me please")
	src := filepath.Join(t.TempDir(), "src.bin")

	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	m := newClientFileTransferManager([]string{src})
	stream := &recordingStream{}

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

	if !hasStatus(stream.responses(), pb.FileTransferResponse_ACCEPTED) {
		t.Errorf("upload was not accepted; responses = %v", statuses(stream.responses()))
	}

	// Chunks are numbered from zero, in order.
	for i, c := range stream.chunks() {
		if c.GetChunkNumber() != int32(i) {
			t.Errorf("chunk %d has number %d", i, c.GetChunkNumber())
		}
	}

	waitFor(t, func() bool { return m.getTransfer("u1") == nil })
}

// An upload of a file not named in the command arguments is refused without
// opening anything.
func TestHandleUploadRequest_Unauthorized(t *testing.T) {
	t.Parallel()

	m := newClientFileTransferManager(nil)
	stream := &recordingStream{}

	if err := m.handleUploadRequest("u1", "/etc/passwd", stream); err != nil {
		t.Fatalf("handleUploadRequest: %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
		t.Errorf("expected rejection, got %v", statuses(stream.responses()))
	}

	if m.getTransfer("u1") != nil {
		t.Errorf("no transfer should be registered for a rejected upload")
	}
}

// An unknown transfer direction is reported as an error rather than acted on.
func TestHandleFileTransferRequest_UnknownDirection(t *testing.T) {
	t.Parallel()

	m := newClientFileTransferManager(nil)
	stream := &recordingStream{}

	req := &pb.FileTransferRequest{
		TransferId: "t1",
		Metadata:   &pb.FileMetadata{Path: "/x"},
		Direction:  pb.FileTransferRequest_DIRECTION_UNSPECIFIED,
	}

	if err := m.handleFileTransferRequest(req, stream); err != nil {
		t.Fatalf("handleFileTransferRequest: %v", err)
	}

	if !hasStatus(stream.responses(), pb.FileTransferResponse_TRANSFER_REJECTED) {
		t.Errorf("expected an error response for unknown direction, got %v", statuses(stream.responses()))
	}
}

// A terminal FileTransferResponse from the agent clears the local transfer state.
func TestHandleFileTransferResponse_TerminalStatusesClearState(t *testing.T) {
	t.Parallel()

	m := newClientFileTransferManager(nil)
	m.registerTransfer("done", "/a", "download")
	m.registerTransfer("failed", "/b", "download")

	m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "done", Status: pb.FileTransferResponse_TRANSFER_COMPLETE,
	})
	m.handleFileTransferResponse(&pb.FileTransferResponse{
		TransferId: "failed", Status: pb.FileTransferResponse_ERROR, ErrorMessage: "boom",
	})

	if m.getTransfer("done") != nil {
		t.Errorf("completed transfer not cleared")
	}

	if m.getTransfer("failed") != nil {
		t.Errorf("errored transfer not cleared")
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

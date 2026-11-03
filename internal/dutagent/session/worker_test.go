// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/digest"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// newTransferBackend returns a backend that can register transfers without a
// broker around it.
func newTransferBackend() *backend {
	return &backend{
		activeUploads:   make(map[string]*uploadState),
		activeDownloads: make(map[string]*downloadState),
	}
}

// TestUploadDigest covers the three digest outcomes on an upload: verified,
// unverifiable (the client predates the field), and corrupted. A corrupted
// upload fails the module's reader at its end instead of reporting io.EOF.
func TestUploadDigest(t *testing.T) {
	content := []byte("firmware payload")
	good := sha256.Sum256(content)
	corrupt := sha256.Sum256([]byte("something else entirely"))

	tests := []struct {
		name    string
		sum     []byte
		wantErr error
	}{
		{name: "verified", sum: good[:]},
		{name: "no digest is accepted", sum: nil},
		{name: "mismatch is rejected", sum: corrupt[:], wantErr: ErrFileCorrupt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTransferBackend()

			reader, err := s.RequestFile(context.Background(), "fw.bin")
			if err != nil {
				t.Fatalf("RequestFile: %v", err)
			}

			readErr := make(chan error, 1)

			go func() {
				_, err := io.ReadAll(reader)
				readErr <- err
			}()

			id := s.getActiveUploads()[0]

			err = s.registerUploadChunk(id, &pb.FileChunk{
				TransferId: id, ChunkNumber: 0, ChunkData: content, IsFinal: true, Sha256: tt.sum,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("registerUploadChunk err = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil && !strings.Contains(err.Error(), digest.Hex(tt.sum)) {
				t.Errorf("err = %q, want it to name the expected digest", err)
			}

			if got := <-readErr; !errors.Is(got, tt.wantErr) {
				t.Errorf("module's reader err = %v, want %v", got, tt.wantErr)
			}
		})
	}
}

// A damaged upload fails the run, so the client learns of it even when the
// module ignores its reader's error.
func TestUploadCorruptFailsRun(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, runCtx := start(t, b, stream)

	reader, err := sess.RequestFile(context.Background(), "fw.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	go io.Copy(io.Discard, reader) //nolint:errcheck // drains the pipe so the write proceeds

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)
	wrong := sha256.Sum256([]byte("not the content"))

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id, ChunkNumber: 0, ChunkData: []byte("content"), IsFinal: true, Sha256: wrong[:],
	}}}

	select {
	case <-runCtx.Done():
		if cause := context.Cause(runCtx); !errors.Is(cause, ErrFileCorrupt) {
			t.Errorf("worker failure = %v, want ErrFileCorrupt", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a corrupted upload did not fail the run")
	}
}

// A download carries the digest of the whole file on its final chunk, so the
// client can verify what the agent sent.
func TestDownloadCarriesDigest(t *testing.T) {
	content := bytes.Repeat([]byte("result log "), chunkSize/5) // two chunks
	want := sha256.Sum256(content)

	s := newTransferBackend()

	err := s.SendFile(context.Background(), "result.log", int64(len(content)), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	id := s.getActiveDownloads()[0]

	for {
		chunk, isFinal, err := s.getNextChunk(id)
		if err != nil {
			t.Fatalf("getNextChunk: %v", err)
		}

		if !isFinal {
			if len(chunk.GetSha256()) != 0 {
				t.Errorf("chunk %d carries a digest; only the final one should", chunk.GetChunkNumber())
			}

			continue
		}

		if got := chunk.GetSha256(); !digest.Match(got, want[:]) {
			t.Errorf("sent digest = %s, want %s", digest.Hex(got), digest.Hex(want[:]))
		}

		return
	}
}

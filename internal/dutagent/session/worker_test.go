// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/digest"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

func fileRequest(path string, content, sum []byte) *pb.RunRequest {
	return &pb.RunRequest{
		Msg: &pb.RunRequest_File{
			File: &pb.File{Path: path, Content: content, Sha256: sum},
		},
	}
}

// TestFromClientWorker_Digest covers the three digest outcomes on an incoming
// file: verified, unverifiable (peer predates the field), and corrupted.
func TestFromClientWorker_Digest(t *testing.T) {
	const path = "fw.bin"

	content := []byte("firmware payload")
	corruptSum := digest.Sum([]byte("something else entirely"))

	tests := []struct {
		name    string
		sum     []byte
		wantErr error
	}{
		{name: "verified", sum: digest.Sum(content)},
		{name: "no digest is accepted", sum: nil},
		{name: "mismatch is rejected", sum: corruptSum, wantErr: ErrFileCorrupt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &backend{fileCh: make(chan chan []byte, 1)}
			s.setCurrentFile(path)

			stream := &testStream{recvReqs: []*pb.RunRequest{fileRequest(path, content, tt.sum)}}

			err := fromClientWorker(context.Background(), stream, s)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}

				if !strings.Contains(err.Error(), digest.Hex(tt.sum)) {
					t.Errorf("err = %q, want it to name the expected digest", err)
				}

				if len(s.fileCh) != 0 {
					t.Error("corrupted file was handed to the module")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected err = %v", err)
			}

			select {
			case file := <-s.fileCh:
				if got := string(<-file); got != string(content) {
					t.Errorf("module received %q, want %q", got, content)
				}
			default:
				t.Fatal("no file handed to the module")
			}
		})
	}
}

// TestToClientWorker_SendsDigest asserts an outgoing file carries its digest,
// so the client can verify what the agent sent.
func TestToClientWorker_SendsDigest(t *testing.T) {
	content := []byte("result log")

	s := &backend{fileCh: make(chan chan []byte, 1)}
	s.setCurrentFile("result.log")

	file := make(chan []byte, 1)
	file <- content
	close(file)

	s.fileCh <- file

	stream := newCaptureStream()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- toClientWorker(ctx, stream, s) }()

	sent := stream.await(t)

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("toClientWorker err = %v", err)
	}

	got := sent.GetFile().GetSha256()
	if !digest.Match(got, digest.Sum(content)) {
		t.Errorf("sent digest = %s, want %s", digest.Hex(got), digest.Hex(digest.Sum(content)))
	}
}

// captureStream records what the agent sends to the client.
type captureStream struct {
	mu   sync.Mutex
	msgs []*pb.RunResponse
	sent chan struct{}
}

func newCaptureStream() *captureStream {
	return &captureStream{sent: make(chan struct{}, 1)}
}

func (s *captureStream) Send(res *pb.RunResponse) error {
	s.mu.Lock()
	s.msgs = append(s.msgs, res)
	s.mu.Unlock()

	select {
	case s.sent <- struct{}{}:
	default:
	}

	return nil
}

func (s *captureStream) Receive() (*pb.RunRequest, error) {
	return nil, io.EOF
}

// await blocks until a message has been sent and returns the first one.
func (s *captureStream) await(t *testing.T) *pb.RunResponse {
	t.Helper()

	select {
	case <-s.sent:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for a sent message")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.msgs[0]
}

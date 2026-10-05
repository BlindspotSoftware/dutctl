// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// fileStream is a Stream for the file transfer tests: it records every
// response the broker sends and hands the broker the requests a test pushes,
// blocking in between as a client holding the stream open does, until the
// test ends it.
type fileStream struct {
	mu   sync.Mutex
	sent []*pb.RunResponse

	reqs     chan *pb.RunRequest
	gone     chan struct{}
	goneOnce sync.Once
}

func newFileStream(t *testing.T) *fileStream {
	t.Helper()

	s := &fileStream{reqs: make(chan *pb.RunRequest), gone: make(chan struct{})}
	t.Cleanup(func() { s.goneOnce.Do(func() { close(s.gone) }) })

	return s
}

func (s *fileStream) Receive() (*pb.RunRequest, error) {
	select {
	case req := <-s.reqs:
		return req, nil
	case <-s.gone:
		return nil, io.EOF
	}
}

func (s *fileStream) Send(res *pb.RunResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sent = append(s.sent, res)

	return nil
}

// awaitFileRequest waits until the broker sent a FileRequest for path.
func (s *fileStream) awaitFileRequest(t *testing.T, path string) {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		s.mu.Lock()

		for _, res := range s.sent {
			if res.GetFileRequest().GetPath() == path {
				s.mu.Unlock()

				return
			}
		}

		s.mu.Unlock()

		select {
		case <-deadline:
			t.Fatalf("no FileRequest for %q was sent", path)
		case <-time.After(time.Millisecond):
		}
	}
}

// A file the client uploads reaches the module's RequestFile and nothing else:
// the downstream worker, which waits for files to send at the same time, must
// never take it. The race was narrow, so the round trip is repeated.
func TestBrokerUploadReachesRequestFile(t *testing.T) {
	const rounds = 100

	for range rounds {
		b := &Broker{}
		stream := newFileStream(t)
		sess, _ := b.Start(t.Context(), stream)

		type result struct {
			data []byte
			err  error
		}

		got := make(chan result, 1)

		go func() {
			r, err := sess.RequestFile("f.txt")
			if err != nil {
				got <- result{err: err}

				return
			}

			data, err := io.ReadAll(r)
			got <- result{data: data, err: err}
		}()

		stream.awaitFileRequest(t, "f.txt")

		select {
		case stream.reqs <- &pb.RunRequest{Msg: &pb.RunRequest_File{File: &pb.File{Path: "f.txt", Content: []byte("data")}}}:
		case <-time.After(2 * time.Second):
			t.Fatal("the broker did not take the uploaded file")
		}

		select {
		case res := <-got:
			if res.err != nil || string(res.data) != "data" {
				t.Fatalf("RequestFile = %q, %v; want \"data\", nil", res.data, res.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("RequestFile did not receive the upload: the downstream worker took it")
		}

		if err := b.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
}

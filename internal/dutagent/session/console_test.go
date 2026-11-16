// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// consoleStream is a Stream for the console tests: it records every response
// the broker sends and hands the broker the requests a test pushes, blocking in
// between as a client holding the stream open does, until the test ends it.
type consoleStream struct {
	mu   sync.Mutex
	sent []*pb.RunResponse

	reqs     chan *pb.RunRequest
	gone     chan struct{}
	goneOnce sync.Once
}

func newConsoleStream(t *testing.T) *consoleStream {
	t.Helper()

	s := &consoleStream{reqs: make(chan *pb.RunRequest), gone: make(chan struct{})}
	t.Cleanup(s.hangUp)

	return s
}

// push sends req from the client; it returns once the broker took it.
func (s *consoleStream) push(t *testing.T, req *pb.RunRequest) {
	t.Helper()

	select {
	case s.reqs <- req:
	case <-time.After(time.Second):
		t.Fatal("the broker did not take the pushed request")
	}
}

// hangUp ends the stream from the client side.
func (s *consoleStream) hangUp() {
	s.goneOnce.Do(func() { close(s.gone) })
}

func (s *consoleStream) Receive() (*pb.RunRequest, error) {
	select {
	case req := <-s.reqs:
		return req, nil
	case <-s.gone:
		return nil, io.EOF
	}
}

func (s *consoleStream) Send(res *pb.RunResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sent = append(s.sent, res)

	return nil
}

// responses returns the responses sent so far.
func (s *consoleStream) responses() []*pb.RunResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*pb.RunResponse(nil), s.sent...)
}

// await waits until n responses were sent and returns them.
func (s *consoleStream) await(t *testing.T, n int) []*pb.RunResponse {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		if res := s.responses(); len(res) >= n {
			return res
		}

		select {
		case <-deadline:
			t.Fatalf("got %d responses, want %d", len(s.responses()), n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// kind names a response for assertions on the order the client sees.
func kind(res *pb.RunResponse) string {
	switch msg := res.GetMsg().(type) {
	case *pb.RunResponse_ConsoleOpen:
		return "open"
	case *pb.RunResponse_ConsoleClose:
		return "close"
	case *pb.RunResponse_ConsoleOutput:
		if msg.ConsoleOutput.GetStderr() != nil {
			return "stderr:" + string(msg.ConsoleOutput.GetStderr())
		}

		return "stdout:" + string(msg.ConsoleOutput.GetStdout())
	case *pb.RunResponse_Print:
		return "print:" + string(msg.Print.GetText())
	case *pb.RunResponse_FileTransferRequest:
		return "file-request:" + msg.FileTransferRequest.GetMetadata().GetPath()
	default:
		return "other"
	}
}

func kinds(res []*pb.RunResponse) []string {
	out := make([]string, 0, len(res))
	for _, r := range res {
		out = append(out, kind(r))
	}

	return out
}

func assertKinds(t *testing.T, got []*pb.RunResponse, want ...string) {
	t.Helper()

	gotKinds := kinds(got)
	if len(gotKinds) != len(want) {
		t.Fatalf("responses = %q, want %q", gotKinds, want)
	}

	for i := range want {
		if gotKinds[i] != want[i] {
			t.Fatalf("responses = %q, want %q", gotKinds, want)
		}
	}
}

func inputReq(id uint32, data string) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_ConsoleInput{ConsoleInput: &pb.ConsoleInput{Id: id, Data: []byte(data)}}}
}

func eofReq(id uint32) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_ConsoleControl{ConsoleControl: &pb.ConsoleControl{
		Id: id, Control: &pb.ConsoleControl_Eof{Eof: &pb.ConsoleEof{}},
	}}}
}

// readChunk reads one chunk from r on a goroutine and returns it, or fails the
// test if nothing arrives.
func readChunk(t *testing.T, r io.Reader) (string, error) {
	t.Helper()

	type result struct {
		data string
		err  error
	}

	done := make(chan result, 1)

	go func() {
		buf := make([]byte, 64)
		n, err := r.Read(buf)
		done <- result{data: string(buf[:n]), err: err}
	}()

	select {
	case res := <-done:
		return res.data, res.err
	case <-time.After(2 * time.Second):
		t.Fatal("no read result")

		return "", nil
	}
}

// The open event precedes the console's first output and the close event
// follows its last; the mode and the id travel with the open event.
func TestConsoleFramesOutput(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	con := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	if _, err := con.Stdout.Write([]byte("hello")); err != nil {
		t.Fatalf("stdout.Write: %v", err)
	}

	if _, err := con.Stderr.Write([]byte("warn")); err != nil {
		t.Fatalf("stderr.Write: %v", err)
	}

	b.CloseConsole()

	res := stream.await(t, 4)
	assertKinds(t, res, "open", "stdout:hello", "stderr:warn", "close")

	open := res[0].GetConsoleOpen()
	if open.GetId() != 1 || open.GetMode() != pb.ConsoleMode_CONSOLE_MODE_RAW {
		t.Errorf("ConsoleOpen = id %d mode %v, want id 1 mode RAW", open.GetId(), open.GetMode())
	}

	// After the close, writes fail and nothing more reaches the client.
	if _, err := con.Stdout.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("stdout.Write after close = %v, want io.ErrClosedPipe", err)
	}

	if _, err := readChunk(t, con.Stdin); !errors.Is(err, io.EOF) {
		t.Errorf("stdin.Read after close = %v, want io.EOF", err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	assertKinds(t, stream.responses(), "open", "stdout:hello", "stderr:warn", "close")
}

// Input marked with the open console's id reaches the module; input for any
// other console is dropped and never reaches a console opened later.
func TestConsoleInputDelivery(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	con := sess.OpenConsole(module.ConsoleOptions{})
	stream.await(t, 1)

	stream.push(t, inputReq(1, "abc"))

	if data, err := readChunk(t, con.Stdin); err != nil || data != "abc" {
		t.Fatalf("stdin.Read = %q, %v; want \"abc\", nil", data, err)
	}

	// The worker handles requests in order: the one for another console is
	// dropped before the next one is delivered.
	stream.push(t, inputReq(7, "other"))
	stream.push(t, inputReq(1, "ok"))

	if data, err := readChunk(t, con.Stdin); err != nil || data != "ok" {
		t.Fatalf("stdin.Read = %q, %v; want \"ok\", nil", data, err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// Input sent while no console is open is dropped at once, so a file transfer
// behind it is still served: before, the upstream worker parked on the input
// until the run ended, and the module's RequestFile waited behind it.
func TestConsoleInputWithoutConsoleKeepsFilesFlowing(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	// A client without an open console marks its input with id 0.
	stream.push(t, inputReq(0, "typed too early"))

	type result struct {
		data []byte
		err  error
	}

	got := make(chan result, 1)

	go func() {
		r, err := sess.RequestFile(t.Context(), "f.txt")
		if err != nil {
			got <- result{err: err}

			return
		}

		data, err := io.ReadAll(r)
		got <- result{data: data, err: err}
	}()

	announced := stream.await(t, 1)
	assertKinds(t, announced, "file-request:f.txt")

	id := announced[0].GetFileTransferRequest().GetTransferId()
	stream.push(t, &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id, ChunkData: []byte("data"), IsFinal: true,
	}}})

	select {
	case res := <-got:
		if res.err != nil || string(res.data) != "data" {
			t.Fatalf("RequestFile = %q, %v; want \"data\", nil", res.data, res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestFile did not complete: the file reply was stuck behind the dropped input")
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// The client's end of input ends the console's input only: Stdin reports EOF
// while the console stays open for output.
func TestConsoleEofFromClient(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	con := sess.OpenConsole(module.ConsoleOptions{})
	stream.await(t, 1)

	stream.push(t, eofReq(1))

	if _, err := readChunk(t, con.Stdin); !errors.Is(err, io.EOF) {
		t.Fatalf("stdin.Read after eof = %v, want io.EOF", err)
	}

	if _, err := con.Stdout.Write([]byte("still here")); err != nil {
		t.Fatalf("stdout.Write after eof: %v", err)
	}

	// Input after the end of input is dropped.
	stream.push(t, inputReq(1, "late"))

	if _, err := readChunk(t, con.Stdin); !errors.Is(err, io.EOF) {
		t.Fatalf("stdin.Read after late input = %v, want io.EOF", err)
	}

	assertKinds(t, stream.await(t, 2), "open", "stdout:still here")

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// The module's Stdin.Close ends the input from another goroutine, also while a
// Read blocks, and leaves the console open for output.
func TestConsoleStdinCloseUnblocksRead(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	con := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	readErr := make(chan error, 1)

	go func() {
		_, err := con.Stdin.Read(make([]byte, 8))
		readErr <- err
	}()

	// Give the Read time to park on the channel before closing.
	time.Sleep(20 * time.Millisecond)

	if err := con.Stdin.Close(); err != nil {
		t.Fatalf("stdin.Close: %v", err)
	}

	select {
	case err := <-readErr:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stdin.Read after Close = %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdin.Read did not return after Close")
	}

	if _, err := con.Stdout.Write([]byte("out")); err != nil {
		t.Fatalf("stdout.Write after stdin.Close: %v", err)
	}

	if err := con.Stdin.Close(); err != nil {
		t.Fatalf("second stdin.Close: %v", err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// Opening a second console ends the first: the client sees close then open,
// the first console's streams are over, and input for it is dropped while
// input for the second is delivered.
func TestConsoleReopenEndsPrevious(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	first := sess.OpenConsole(module.ConsoleOptions{})
	second := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	res := stream.await(t, 3)
	assertKinds(t, res, "open", "close", "open")

	if id := res[2].GetConsoleOpen().GetId(); id != 2 {
		t.Errorf("second ConsoleOpen id = %d, want 2", id)
	}

	if _, err := readChunk(t, first.Stdin); !errors.Is(err, io.EOF) {
		t.Errorf("first stdin.Read = %v, want io.EOF", err)
	}

	if _, err := first.Stdout.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("first stdout.Write = %v, want io.ErrClosedPipe", err)
	}

	stream.push(t, inputReq(1, "stale"))
	stream.push(t, inputReq(2, "fresh"))

	if data, err := readChunk(t, second.Stdin); err != nil || data != "fresh" {
		t.Fatalf("second stdin.Read = %q, %v; want \"fresh\", nil", data, err)
	}

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A console opened after the session was torn down is over at once: its input
// is at its end and its writers fail, so the module is never wedged.
func TestConsoleAfterTeardown(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	con := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})

	if _, err := con.Stdout.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("stdout.Write = %v, want io.ErrClosedPipe", err)
	}

	if _, err := con.Stdin.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Errorf("stdin.Read = %v, want io.EOF", err)
	}

	// Closing a console nobody can hear about returns at once.
	b.CloseConsole()
}

// Writes from several goroutines and a reader on another are safe; the race
// detector checks the console's channels and its end signals.
func TestConsoleConcurrentUse(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	con := sess.OpenConsole(module.ConsoleOptions{Mode: module.ConsoleRaw})
	stream.await(t, 1)

	var wg sync.WaitGroup

	for i := range 3 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 10 {
				_, _ = con.Stdout.Write([]byte{byte('a' + i)})
			}
		}()
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		_, _ = io.ReadAll(con.Stdin)
	}()

	for range 5 {
		stream.push(t, inputReq(1, "in"))
	}

	stream.push(t, eofReq(1))
	wg.Wait()
	b.CloseConsole()

	assertKinds(t, stream.await(t, 32)[31:], "close")

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// awaitUploads waits until the agent asked the client for n files and returns
// their transfer IDs by name.
func (s *consoleStream) awaitUploads(t *testing.T, n int) map[string]string {
	t.Helper()

	deadline := time.After(2 * time.Second)

	for {
		ids := map[string]string{}

		for _, res := range s.responses() {
			if r := res.GetFileTransferRequest(); r.GetDirection() == pb.FileTransferRequest_DIRECTION_UPLOAD {
				ids[r.GetMetadata().GetPath()] = r.GetTransferId()
			}
		}

		if len(ids) >= n {
			return ids
		}

		select {
		case <-deadline:
			t.Fatalf("the agent asked for %d files, want %d", len(ids), n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// pushFile sends name as the client does for a small file: its size, then
// its content in one final chunk.
func (s *consoleStream) pushFile(t *testing.T, id, name string) {
	t.Helper()

	s.push(t, &pb.RunRequest{Msg: &pb.RunRequest_FileTransferRequest{FileTransferRequest: &pb.FileTransferRequest{
		TransferId: id,
		Metadata:   &pb.FileMetadata{Path: name, Name: name, Size: int64(len(name))},
		Direction:  pb.FileTransferRequest_DIRECTION_UPLOAD,
	}}})
	s.push(t, &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id,
		ChunkData:  []byte(name),
		IsFinal:    true,
	}}})
}

// A chunk waits until the module reads it, but not in the upstream worker:
// a module may read its files in any order, the client's chunk of the other
// one arriving first.
func TestUploadsReachTheModuleInAnyOrder(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	first, err := sess.RequestFile(t.Context(), "first.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	second, err := sess.RequestFile(t.Context(), "second.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	ids := stream.awaitUploads(t, 2)
	stream.pushFile(t, ids["first.bin"], "first.bin")
	stream.pushFile(t, ids["second.bin"], "second.bin")

	for _, f := range []struct {
		r    io.Reader
		want string
	}{{second, "second.bin"}, {first, "first.bin"}} {
		if data, err := readChunk(t, f.r); err != nil || data != f.want {
			t.Fatalf("read = %q, %v; want %q", data, err, f.want)
		}
	}
}

// Console input reaches the module while a chunk of a file waits for the
// module to read it.
func TestConsoleInputPassesAWaitingChunk(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	file, err := sess.RequestFile(t.Context(), "image.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	con := sess.OpenConsole(module.ConsoleOptions{})

	stream.pushFile(t, stream.awaitUploads(t, 1)["image.bin"], "image.bin")
	stream.push(t, inputReq(1, "abc"))

	if data, err := readChunk(t, con.Stdin); err != nil || data != "abc" {
		t.Fatalf("stdin.Read = %q, %v; want \"abc\", nil", data, err)
	}

	if data, err := readChunk(t, file); err != nil || data != "image.bin" {
		t.Fatalf("file read = %q, %v; want \"image.bin\"", data, err)
	}
}

// The client learns that its upload is complete before the module sees the
// end of the file. A module that returns at once ends the run, which drops the
// upload, and the client must not miss the end of it then.
func TestUploadCompleteBeforeTheModuleSeesItsEnd(t *testing.T) {
	b := &Broker{}
	stream := newConsoleStream(t)
	sess, _ := start(t, b, stream)

	file, err := sess.RequestFile(t.Context(), "image.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	stream.pushFile(t, stream.awaitUploads(t, 1)["image.bin"], "image.bin")

	data, err := io.ReadAll(file)
	if err != nil || string(data) != "image.bin" {
		t.Fatalf("read = %q, %v; want \"image.bin\"", data, err)
	}

	for _, res := range stream.responses() {
		if res.GetFileTransferResponse().GetStatus() == pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE {
			return
		}
	}

	t.Fatal("the module saw the end of the file before the client was told the upload is complete")
}

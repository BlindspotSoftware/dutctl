// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// uploadStream learns the upload transfer ID from the FileTransferRequest the
// agent announces, then feeds chunks for it and stays connected.
type uploadStream struct {
	idCh chan string
	id   string
	n    int
	stop chan struct{}
}

func (s *uploadStream) Send(res *pb.RunResponse) error {
	if r := res.GetFileTransferRequest(); r != nil && r.GetDirection() == pb.FileTransferRequest_DIRECTION_UPLOAD {
		select {
		case s.idCh <- r.GetTransferId():
		default:
		}
	}

	return nil
}

func (s *uploadStream) Receive() (*pb.RunRequest, error) {
	if s.id == "" {
		s.id = <-s.idCh
	}

	if s.n >= 3 {
		<-s.stop // the client stays connected but sends nothing further

		return nil, context.Canceled
	}

	num := int32(s.n)
	s.n++

	return &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId:  s.id,
		ChunkNumber: num,
		ChunkData:   make([]byte, 1024),
	}}}, nil
}

// TestUploadWriteUnblocksOnCancel guards the cancellation watchdog in
// Broker.Start. An upload chunk is written into an io.Pipe that only the module
// drains, so a module that abandons its reader leaves fromClientWorker blocked
// in a write no context can interrupt. Only abortTransfers releases it — and
// aborting from wg.Wait alone cannot, because the blocked worker is one of the
// two being waited on. Without the watchdog the broker never tears down, the
// worker leaks for the process lifetime, and the RPC hangs.
func TestUploadWriteUnblocksOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &uploadStream{idCh: make(chan string, 1), stop: make(chan struct{})}

	defer close(stream.stop)

	b := &Broker{}
	sess, errCh := b.Start(ctx, stream)

	if _, err := sess.RequestFile(ctx, "image.bin"); err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	// Give the announced transfer time to reach registerUploadChunk and block
	// there; the module deliberately never reads the returned reader.
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not tear down after cancel: fromClientWorker wedged in pipe.Write")
	}
}

// TestRequestFileRefusedDuringShutdown guards the transferWg accounting.
// processFileTransfers stops announcing uploads once shutdown begins, so a
// transfer registered after that point could never be announced, completed or
// released — and executeModules waits on transferWg before cancelling the
// workers, leaving nothing to break the wait.
func TestRequestFileRefusedDuringShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &testStream{recvBlock: true, unblockCh: make(chan struct{})}

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	b.Shutdown()

	_, err := sess.RequestFile(ctx, "image.bin")
	if !errors.Is(err, ErrSessionShuttingDown) {
		t.Errorf("RequestFile err = %v, want ErrSessionShuttingDown", err)
	}

	sendErr := sess.SendFile(ctx, "out.bin", 4, strings.NewReader("data"))
	if !errors.Is(sendErr, ErrSessionShuttingDown) {
		t.Errorf("SendFile err = %v, want ErrSessionShuttingDown", sendErr)
	}

	done := make(chan struct{})

	go func() { b.WaitForTransfersToComplete(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForTransfers blocked on a transfer registered after shutdown")
	}
}

// rejectStream accepts the download announcement, answers TRANSFER_REJECTED,
// then stays connected and counts the chunks that keep arriving.
type rejectStream struct {
	mu       sync.Mutex
	chunks   int
	rejected bool
	idCh     chan string
	stop     chan struct{}
}

func (s *rejectStream) Send(res *pb.RunResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r := res.GetFileTransferRequest(); r != nil && r.GetDirection() == pb.FileTransferRequest_DIRECTION_DOWNLOAD {
		select {
		case s.idCh <- r.GetTransferId():
		default:
		}
	}

	if res.GetFileChunk() != nil && s.rejected {
		s.chunks++
	}

	return nil
}

func (s *rejectStream) Receive() (*pb.RunRequest, error) {
	id, ok := <-s.idCh
	if !ok {
		<-s.stop

		return nil, context.Canceled
	}

	close(s.idCh) // any later Receive parks until the test finishes

	s.mu.Lock()
	s.rejected = true
	s.mu.Unlock()

	return &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{FileTransferResponse: &pb.FileTransferResponse{
		TransferId:   id,
		Status:       pb.FileTransferResponse_STATUS_TRANSFER_REJECTED,
		ErrorMessage: "destination not in command arguments",
	}}}, nil
}

// TestDownloadRejectionStopsStreaming guards the client's only defence against
// an agent writing to a path the user never named: the client answers
// TRANSFER_REJECTED, and the agent must drop the download. Handling the status
// for uploads alone lets the agent stream the whole file to a client that has
// already refused it.
func TestDownloadRejectionStopsStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const size = 32 * 1024 * 1024 // 32 chunks, enough to catch a full stream-out

	stream := &rejectStream{idCh: make(chan string, 1), stop: make(chan struct{})}

	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	if err := sess.SendFile(ctx, "/etc/shadow", size, bytes.NewReader(make([]byte, size))); err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if left := b.session.getActiveDownloads(); len(left) != 0 {
		t.Errorf("download still active after rejection: %v", left)
	}

	stream.mu.Lock()
	settled := stream.chunks
	stream.mu.Unlock()

	// A chunk already in flight when the rejection is processed is acceptable;
	// continuing to stream is not.
	time.Sleep(300 * time.Millisecond)

	stream.mu.Lock()
	after := stream.chunks
	stream.mu.Unlock()

	if after != settled {
		t.Errorf("agent kept streaming after rejection: %d chunks, then %d", settled, after)
	}

	if after > 4 {
		t.Errorf("agent sent %d chunks after the client rejected the download", after)
	}
}

// scriptedStream drives a full transfer from the client's side: it records what
// the agent sends and answers each message the way a well-behaved client would.
type scriptedStream struct {
	mu   sync.Mutex
	sent []*pb.RunResponse

	reply chan *pb.RunRequest
	stop  chan struct{}
}

func newScriptedStream() *scriptedStream {
	return &scriptedStream{
		reply: make(chan *pb.RunRequest, 64),
		stop:  make(chan struct{}),
	}
}

func (s *scriptedStream) Send(res *pb.RunResponse) error {
	s.mu.Lock()
	s.sent = append(s.sent, res)
	s.mu.Unlock()

	return nil
}

func (s *scriptedStream) Receive() (*pb.RunRequest, error) {
	select {
	case req := <-s.reply:
		return req, nil
	case <-s.stop:
		return nil, io.EOF
	}
}

func (s *scriptedStream) responses() []*pb.RunResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*pb.RunResponse(nil), s.sent...)
}

// downloadChunks returns the chunks the agent has streamed so far.
func (s *scriptedStream) downloadChunks() []*pb.FileChunk {
	var out []*pb.FileChunk

	for _, res := range s.responses() {
		if c := res.GetFileChunk(); c != nil {
			out = append(out, c)
		}
	}

	return out
}

// awaitAnnouncement waits for the agent to announce a transfer in dir and
// returns its ID.
func awaitAnnouncement(t *testing.T, s *scriptedStream, dir pb.FileTransferRequest_Direction) string {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		for _, res := range s.responses() {
			if r := res.GetFileTransferRequest(); r != nil && r.GetDirection() == dir {
				return r.GetTransferId()
			}
		}

		select {
		case <-deadline:
			t.Fatalf("no %v announcement within timeout", dir)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func awaitStatus(t *testing.T, s *scriptedStream, want pb.FileTransferResponse_Status) {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		for _, res := range s.responses() {
			if r := res.GetFileTransferResponse(); r != nil && r.GetStatus() == want {
				return
			}
		}

		select {
		case <-deadline:
			t.Fatalf("agent never reported %v", want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestDownloadHappyPath covers the agent's download state machine end to end:
// announce, stream every chunk in order, mark the last one final, and release
// the transfer only once the client confirms completion.
func TestDownloadHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const size = 3*chunkSize + 512 // four chunks, last one short

	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	// A short reader: legal per io.Reader, and the case that breaks a chunk_offset
	// computed as chunkNumber*chunkSize rather than from the bytes actually sent.
	src := io.NopCloser(&shortReader{src: bytes.NewReader(content), max: chunkSize / 3})

	if err := sess.SendFile(ctx, "out.bin", size, src); err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_DOWNLOAD)

	// Accept, then acknowledge chunks until the agent marks one final.
	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
		FileTransferResponse: &pb.FileTransferResponse{
			TransferId: id, Status: pb.FileTransferResponse_STATUS_ACCEPTED,
		},
	}}

	deadline := time.After(10 * time.Second)

	for {
		cs := stream.downloadChunks()
		if len(cs) > 0 && cs[len(cs)-1].GetIsFinal() {
			break
		}

		select {
		case <-deadline:
			t.Fatalf("download did not finish; %d chunks sent", len(cs))
		case <-time.After(5 * time.Millisecond):
		}
	}

	chunks := stream.downloadChunks()

	var (
		got    []byte
		offset int64
	)

	for i, c := range chunks {
		if c.GetChunkNumber() != int32(i) {
			t.Errorf("chunk %d numbered %d", i, c.GetChunkNumber())
		}

		if c.GetChunkOffset() != offset {
			t.Errorf("chunk %d offset = %d, want %d", i, c.GetChunkOffset(), offset)
		}

		offset += int64(len(c.GetChunkData()))
		got = append(got, c.GetChunkData()...)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("streamed %d bytes, want %d (content mismatch)", len(got), len(content))
	}

	if final := chunks[len(chunks)-1]; !final.GetIsFinal() {
		t.Error("last chunk not marked final")
	}

	// The transfer stays registered until the client confirms.
	if left := b.session.getActiveDownloads(); len(left) != 1 {
		t.Errorf("download released before the client confirmed: %v", left)
	}

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
		FileTransferResponse: &pb.FileTransferResponse{
			TransferId: id, Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
		},
	}}

	done := make(chan struct{})

	go func() { b.WaitForTransfersToComplete(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("transfer never released after TRANSFER_COMPLETE")
	}
}

// TestUploadHappyPath covers the agent's upload state machine: announce the
// request, record the size the client reports, reassemble the chunks through the
// module's reader, and complete.
func TestUploadHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	content := []byte("firmware image contents")

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	reader, err := sess.RequestFile(ctx, "image.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	read := make(chan []byte, 1)

	go func() {
		got, _ := io.ReadAll(reader)
		read <- got
	}()

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	// The client answers with the file's real size, then sends it.
	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferRequest{
		FileTransferRequest: &pb.FileTransferRequest{
			TransferId: id,
			Direction:  pb.FileTransferRequest_DIRECTION_UPLOAD,
			Metadata:   &pb.FileMetadata{Path: "image.bin", Size: int64(len(content))},
		},
	}}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ACCEPTED)

	if got := b.session.getUpload(id); got == nil {
		t.Fatal("upload disappeared after acceptance")
	} else if size := got.metadata.GetSize(); size != int64(len(content)) {
		t.Errorf("recorded upload size = %d, want %d", size, len(content))
	}

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id, ChunkNumber: 0, ChunkData: content, IsFinal: true,
	}}}

	select {
	case got := <-read:
		if !bytes.Equal(got, content) {
			t.Errorf("module read %q, want %q", got, content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("module never received the uploaded file")
	}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE)
}

// TestUploadChunkOutOfOrderFailsRun guards the sequence check. There is no
// resend path, so a gap in the chunk numbering is a protocol violation the run
// cannot recover from — it must surface as ErrBadFileTransfer rather than be
// answered in-band and forgotten.
func TestUploadChunkOutOfOrderFailsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, errCh := b.Start(ctx, stream)

	reader, err := sess.RequestFile(ctx, "image.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	go io.Copy(io.Discard, reader) //nolint:errcheck // drains the pipe so writes proceed

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	// Chunk 1 without chunk 0.
	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id, ChunkNumber: 1, ChunkData: []byte("out of order"),
	}}}

	select {
	case workerErr := <-errCh:
		if !errors.Is(workerErr, ErrBadFileTransfer) {
			t.Errorf("worker err = %v, want ErrBadFileTransfer", workerErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("out-of-order chunk did not fail the run")
	}
}

// shortReader delivers at most max bytes per Read, which io.Reader explicitly
// permits and which real pipes and sockets do routinely.
type shortReader struct {
	src *bytes.Reader
	max int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(p) > r.max {
		p = p[:r.max]
	}

	return r.src.Read(p)
}

// TestRequestFileContextBoundsTransfer: a module that gives RequestFile a
// deadline gets one. The reader fails with the context's cause and the transfer
// is released, so the run is not held open by a client that stopped sending.
func TestRequestFileContextBoundsTransfer(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	// The module bounds its own transfer, exactly as it would bound a subprocess.
	transferCtx, cancelTransfer := context.WithCancel(context.Background())

	reader, err := sess.RequestFile(transferCtx, "image.bin")
	if err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	readErr := make(chan error, 1)

	go func() {
		_, err := io.ReadAll(reader)
		readErr <- err
	}()

	// The client goes quiet. Only the module's own context can end this.
	cancelTransfer()

	select {
	case err := <-readErr:
		if err == nil {
			t.Error("reader returned success after the transfer was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not unblock the module's reader")
	}

	if left := b.session.getActiveUploads(); len(left) != 0 {
		t.Errorf("upload still registered after cancellation: %v", left)
	}

	done := make(chan struct{})

	go func() { b.WaitForTransfersToComplete(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled transfer was never released from the wait group")
	}
}

// closeSpyReader reports whether the session closed it, which is the contract
// SendFile documents for a reader it has taken ownership of.
type closeSpyReader struct {
	*bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func (r *closeSpyReader) Close() error {
	r.once.Do(func() { close(r.closed) })

	return nil
}

// TestSendFileContextBoundsTransfer: cancelling after SendFile has returned
// still aborts the in-flight download and closes the reader. SendFile is
// fire-and-forget, so the context is the only handle the module keeps on it.
func TestSendFileContextBoundsTransfer(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	const size = 32 * 1024 * 1024

	src := &closeSpyReader{
		Reader: bytes.NewReader(make([]byte, size)),
		closed: make(chan struct{}),
	}

	transferCtx, cancelTransfer := context.WithCancel(context.Background())

	if err := sess.SendFile(transferCtx, "out.bin", size, src); err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_DOWNLOAD)

	// SendFile has already returned; the context is still in charge.
	cancelTransfer()

	select {
	case <-src.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not close the reader the session owned")
	}

	deadline := time.After(5 * time.Second)

	for {
		if len(b.session.getActiveDownloads()) == 0 {
			break
		}

		select {
		case <-deadline:
			t.Fatal("download still registered after cancellation")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// A context already cancelled when the module calls in is refused outright,
// rather than registering a transfer that is torn down a moment later.
func TestTransferRefusedOnCancelledContext(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := sess.RequestFile(dead, "image.bin"); !errors.Is(err, context.Canceled) {
		t.Errorf("RequestFile err = %v, want context.Canceled", err)
	}

	if err := sess.SendFile(dead, "out.bin", 4, strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Errorf("SendFile err = %v, want context.Canceled", err)
	}

	if n := len(b.session.getActiveUploads()) + len(b.session.getActiveDownloads()); n != 0 {
		t.Errorf("%d transfers registered despite a cancelled context", n)
	}
}

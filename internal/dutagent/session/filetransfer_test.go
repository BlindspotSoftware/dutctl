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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/digest"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// uploadStream learns the upload transfer ID from the FileTransferRequest the
// agent announces, then sends one chunk for it and stays connected, but sends
// nothing more.
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

	if s.n >= 1 {
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

// TestUploadEndsWithTheSession: RequestFile waits for the chunks of a client
// that went quiet. The end of the session must end the upload, also when the
// module gave RequestFile a context that never ends.
func TestUploadEndsWithTheSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &uploadStream{idCh: make(chan string, 1), stop: make(chan struct{})}

	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	result := requestFileAsync(context.Background(), sess, "image.bin", io.Discard)

	// Give the first chunk time to arrive; the client sends no more.
	time.Sleep(200 * time.Millisecond)

	cancel()

	_ = b.Stop()

	if err := awaitResult(t, result); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("RequestFile err = %v, want %v", err, ErrSessionClosed)
	}

	if carried(&b.session) != nil {
		t.Error("upload still registered after the session ended")
	}
}

// carried returns the transfer the session carries, nil if none runs.
func carried(s *backend) *transfer {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()

	if s.transfer == nil || isDone(s.transfer.done) {
		return nil
	}

	return s.transfer
}

// sendFileAsync calls SendFile on a goroutine, as a module waiting for its
// download, and delivers what it returns.
func sendFileAsync(ctx context.Context, sess module.Session, name string, size int64, r io.Reader) <-chan error {
	result := make(chan error, 1)

	go func() { result <- sess.SendFile(ctx, name, size, r) }()

	return result
}

// requestFileAsync calls RequestFile on a goroutine, as a module waiting for
// its upload, and delivers what it returns.
func requestFileAsync(ctx context.Context, sess module.Session, name string, w io.Writer) <-chan error {
	result := make(chan error, 1)

	go func() { result <- sess.RequestFile(ctx, name, w) }()

	return result
}

// awaitResult returns what SendFile or RequestFile returned, failing the test
// if it does not return in time.
func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the transfer call did not return; the transfer never ended")

		return nil
	}
}

// uploadChunk is the client's chunk number num of the upload id.
func uploadChunk(id string, num int32, data string, final bool) *pb.RunRequest {
	return &pb.RunRequest{Msg: &pb.RunRequest_FileChunk{FileChunk: &pb.FileChunk{
		TransferId: id, ChunkNumber: num, ChunkData: []byte(data), IsFinal: final,
	}}}
}

// rejectStream takes the download announcement, answers ERROR, then stays
// connected and counts the chunks that keep arriving.
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
		Status:       pb.FileTransferResponse_STATUS_ERROR,
		ErrorMessage: "writing out.bin: no space left on device",
	}}}, nil
}

// TestDownloadRejectionStopsStreaming: a client that cannot save a download
// answers ERROR, and the agent must drop the download and return the client's
// reason from SendFile. Handling the status for uploads alone lets the agent
// stream the whole file to a client that has already refused it.
func TestDownloadRejectionStopsStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const size = 32 * 1024 * 1024 // 32 chunks, enough to catch a full stream-out

	stream := &rejectStream{idCh: make(chan string, 1), stop: make(chan struct{})}

	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	err := awaitResult(t, sendFileAsync(ctx, sess, "out.bin", size, bytes.NewReader(make([]byte, size))))
	if err == nil || !strings.Contains(err.Error(), "no space left on device") {
		t.Errorf("SendFile err = %v, want the client's reason", err)
	}

	if carried(&b.session) != nil {
		t.Error("download still active after rejection")
	}

	stream.mu.Lock()
	after := stream.chunks
	stream.mu.Unlock()

	// A chunk already in flight when the rejection is processed is acceptable;
	// continuing to stream is not.
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

	return awaitAnnouncements(t, s, dir, 1)[0]
}

// awaitAnnouncements waits until the agent has announced n transfers in dir and
// returns their IDs.
func awaitAnnouncements(t *testing.T, s *scriptedStream, dir pb.FileTransferRequest_Direction, n int) []string {
	t.Helper()

	deadline := time.After(5 * time.Second)

	for {
		var ids []string

		for _, res := range s.responses() {
			if r := res.GetFileTransferRequest(); r != nil && r.GetDirection() == dir {
				ids = append(ids, r.GetTransferId())
			}
		}

		if len(ids) >= n {
			return ids
		}

		select {
		case <-deadline:
			t.Fatalf("%d of %d %v announcements within timeout", len(ids), n, dir)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// awaitFinalChunk waits until the agent has sent a download's final chunk, and
// returns every chunk it sent.
func awaitFinalChunk(t *testing.T, s *scriptedStream) []*pb.FileChunk {
	t.Helper()

	deadline := time.After(10 * time.Second)

	for {
		cs := s.downloadChunks()
		if len(cs) > 0 && cs[len(cs)-1].GetIsFinal() {
			return cs
		}

		select {
		case <-deadline:
			t.Fatalf("download did not finish; %d chunks sent", len(cs))
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
// announce, stream every chunk in order, mark the last one final with the
// file's digest, and return from SendFile only once the client confirms
// completion.
func TestDownloadHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const size = 3*chunkSize + 512 // four chunks, last one short

	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}

	sum := sha256.Sum256(content)

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	// A short reader: legal per io.Reader, and the case that breaks a chunk_offset
	// computed as chunkNumber*chunkSize rather than from the bytes actually sent.
	src := &shortReader{src: bytes.NewReader(content), max: chunkSize / 3}

	result := sendFileAsync(ctx, sess, "out.bin", size, src)

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_DOWNLOAD)

	// The agent streams without waiting for the client, until it marks a chunk
	// final.
	chunks := awaitFinalChunk(t, stream)

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

		// The size the module gave goes on chunk 0 only.
		var wantSize int64
		if i == 0 {
			wantSize = size
		}

		if c.GetFileSize() != wantSize {
			t.Errorf("chunk %d file size = %d, want %d", i, c.GetFileSize(), wantSize)
		}

		// The digest of the whole file goes on the final chunk only.
		var wantSum []byte
		if c.GetIsFinal() {
			wantSum = sum[:]
		}

		if !bytes.Equal(c.GetSha256(), wantSum) {
			t.Errorf("chunk %d digest = %s, want %s", i, digest.Hex(c.GetSha256()), digest.Hex(wantSum))
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

	// SendFile waits, and the transfer stays registered, until the client
	// confirms.
	select {
	case err := <-result:
		t.Fatalf("SendFile returned %v before the client confirmed", err)
	default:
	}

	if carried(&b.session) == nil {
		t.Error("download released before the client confirmed")
	}

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
		FileTransferResponse: &pb.FileTransferResponse{
			TransferId: id, Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
		},
	}}

	if err := awaitResult(t, result); err != nil {
		t.Errorf("SendFile: %v", err)
	}

	if carried(&b.session) != nil {
		t.Error("download still registered after TRANSFER_COMPLETE")
	}
}

// fastAckStream confirms a download from inside the Send of its final chunk,
// as a client on a fast connection can: Send returns only after the agent's
// receive worker has handled the confirmation and asked for the next message.
type fastAckStream struct {
	reply   chan *pb.RunRequest
	handled chan struct{}
	acked   atomic.Bool
	stop    chan struct{}
}

func (s *fastAckStream) Send(res *pb.RunResponse) error {
	if c := res.GetFileChunk(); c.GetIsFinal() {
		s.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
			FileTransferResponse: &pb.FileTransferResponse{
				TransferId: c.GetTransferId(), Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
			},
		}}
		<-s.handled
	}

	return nil
}

func (s *fastAckStream) Receive() (*pb.RunRequest, error) {
	if s.acked.Load() {
		close(s.handled)
		<-s.stop

		return nil, context.Canceled
	}

	select {
	case req := <-s.reply:
		s.acked.Store(true)

		return req, nil
	case <-s.stop:
		return nil, context.Canceled
	}
}

// TestDownloadConfirmedDuringFinalSend: the client may confirm a download
// before the agent's Send of the final chunk returns. The agent must already
// wait for the confirmation then, or it drops it and SendFile never returns.
func TestDownloadConfirmedDuringFinalSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fastAckStream{
		reply:   make(chan *pb.RunRequest),
		handled: make(chan struct{}),
		stop:    make(chan struct{}),
	}
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	if err := awaitResult(t, sendFileAsync(ctx, sess, "out.bin", 3, strings.NewReader("abc"))); err != nil {
		t.Errorf("SendFile: %v", err)
	}
}

// TestUploadHappyPath covers the agent's upload state machine: ask the client
// for the file, write the chunks the client then sends to the module's writer,
// and tell the client that the upload is complete. A module that returns at
// once ends the run, and the client must not miss the end of its upload then.
func TestUploadHappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(ctx, stream)

	var got bytes.Buffer

	result := requestFileAsync(ctx, sess, "image.bin", &got)

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	stream.reply <- uploadChunk(id, 0, "firmware image", false)
	stream.reply <- uploadChunk(id, 1, " contents", true)

	if err := awaitResult(t, result); err != nil {
		t.Fatalf("RequestFile: %v", err)
	}

	if got.String() != "firmware image contents" {
		t.Errorf("module got %q, want %q", got.String(), "firmware image contents")
	}

	// The module returns, and the run ends: Stop returns once the workers did.
	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	complete := slices.ContainsFunc(stream.responses(), func(res *pb.RunResponse) bool {
		return res.GetFileTransferResponse().GetStatus() == pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE
	})
	if !complete {
		t.Error("the run ended before the client was told that the upload is complete")
	}

	// The upload ended with RequestFile, so the module can move the next file
	// at once.
	if carried(&b.session) != nil {
		t.Error("upload still registered after RequestFile returned")
	}
}

// TestUploadDigest covers the three digest outcomes of an upload: verified,
// unverified (the client predates the field), and damaged. A damaged upload
// makes RequestFile fail before its final chunk reaches the writer, and fails
// the run, so the client learns of it even when the module ignores the error.
func TestUploadDigest(t *testing.T) {
	const content = "firmware payload"

	good := sha256.Sum256([]byte(content))
	wrong := sha256.Sum256([]byte("something else entirely"))

	for _, tc := range []struct {
		name    string
		sum     []byte
		wantErr error
	}{
		{name: "verified", sum: good[:]},
		{name: "no digest is accepted", sum: nil},
		{name: "mismatch is rejected", sum: wrong[:], wantErr: ErrFileCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := newScriptedStream()
			defer close(stream.stop)

			b := &Broker{}
			sess, runCtx := b.Start(context.Background(), stream)

			var got bytes.Buffer

			result := requestFileAsync(context.Background(), sess, "fw.bin", &got)
			id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

			chunk := uploadChunk(id, 0, content, true)
			chunk.GetFileChunk().Sha256 = tc.sum
			stream.reply <- chunk

			err := awaitResult(t, result)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RequestFile err = %v, want %v", err, tc.wantErr)
			}

			if tc.wantErr == nil {
				if got.String() != content {
					t.Errorf("module got %q, want %q", got.String(), content)
				}

				return
			}

			if !strings.Contains(err.Error(), digest.Hex(tc.sum)) {
				t.Errorf("err = %q, want it to name the expected digest", err)
			}

			if got.Len() != 0 {
				t.Errorf("the module got %q of a damaged file", got.String())
			}

			select {
			case <-runCtx.Done():
				if cause := context.Cause(runCtx); !errors.Is(cause, ErrFileCorrupt) {
					t.Errorf("worker failure = %v, want ErrFileCorrupt", cause)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a damaged upload did not fail the run")
			}
		})
	}
}

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// TestRequestFileWriteErrorFailsTheUpload: a writer that fails ends the upload
// on both ends. RequestFile returns the error to the module, and the client is
// told, so that it stops sending. A chunk the client sent before it heard is
// dropped and does not fail the run, also once the module requests the next
// file.
func TestRequestFileWriteErrorFailsTheUpload(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, runCtx := b.Start(context.Background(), stream)

	full := errors.New("no space left on device")
	result := requestFileAsync(context.Background(), sess, "image.bin", failingWriter{err: full})

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	stream.reply <- uploadChunk(id, 0, "firm", false)
	stream.reply <- uploadChunk(id, 1, "ware", false)

	if err := awaitResult(t, result); !errors.Is(err, full) {
		t.Errorf("RequestFile err = %v, want %v", err, full)
	}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ERROR)

	var next bytes.Buffer

	second := requestFileAsync(context.Background(), sess, "next.bin", &next)
	nextID := awaitAnnouncements(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD, 2)[1]

	// A chunk of the first upload still arrives, then the second upload's file:
	// the agent went past the late chunk once the second upload has its file.
	stream.reply <- uploadChunk(id, 2, "late", false)
	stream.reply <- uploadChunk(nextID, 0, "next", true)

	if err := awaitResult(t, second); err != nil || next.String() != "next" {
		t.Errorf("second RequestFile = %q, %v; want \"next\", nil", next.String(), err)
	}

	if err := context.Cause(runCtx); err != nil {
		t.Errorf("the run failed: %v", err)
	}
}

// TestUploadChunkOutOfOrderFailsRun guards the sequence check. There is no
// resend path, so a gap in the chunk numbering is a protocol violation the run
// cannot recover from. It must surface as ErrBadFileTransfer rather than be
// answered in-band and forgotten.
func TestUploadChunkOutOfOrderFailsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, runCtx := b.Start(ctx, stream)

	requestFileAsync(ctx, sess, "image.bin", io.Discard)

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	// Chunk 1 without chunk 0.
	stream.reply <- uploadChunk(id, 1, "out of order", false)

	awaitBadTransfer(t, runCtx)
}

// awaitBadTransfer waits until the run failed because the client broke the
// file transfer protocol.
func awaitBadTransfer(t *testing.T, runCtx context.Context) {
	t.Helper()

	select {
	case <-runCtx.Done():
		if cause := context.Cause(runCtx); !errors.Is(cause, ErrBadFileTransfer) {
			t.Errorf("worker failure = %v, want ErrBadFileTransfer", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the broken transfer message did not fail the run")
	}
}

// TestMessageForATransferNeverStartedFailsRun: a chunk or an end for an ID the
// agent never issued comes from a client that does not speak the protocol. It
// fails the run, so that the client does not wait for a transfer that never
// runs.
func TestMessageForATransferNeverStartedFailsRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  *pb.RunRequest
	}{
		{"chunk", uploadChunk("7", 0, "data", true)},
		{"error", &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{FileTransferResponse: &pb.FileTransferResponse{
			TransferId: "x", Status: pb.FileTransferResponse_STATUS_ERROR,
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := newScriptedStream()
			defer close(stream.stop)

			b := &Broker{}
			_, runCtx := b.Start(context.Background(), stream)

			stream.reply <- tc.msg

			awaitBadTransfer(t, runCtx)
		})
	}
}

// gateWriter blocks every write until release is closed.
type gateWriter chan struct{}

func (w gateWriter) Write(p []byte) (int, error) {
	<-w

	return len(p), nil
}

// TestUploadChunkAfterTheFinalFailsRun: a chunk that follows the final one,
// while the module still writes the file, fails the run.
func TestUploadChunkAfterTheFinalFailsRun(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, runCtx := b.Start(context.Background(), stream)

	release := make(gateWriter)
	defer close(release)

	requestFileAsync(context.Background(), sess, "image.bin", release)

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	stream.reply <- uploadChunk(id, 0, "image", true)
	stream.reply <- uploadChunk(id, 1, "more", false)

	awaitBadTransfer(t, runCtx)
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
// deadline gets one. RequestFile returns the context's cause, the client is
// told, and the transfer is released, so the run is not held open by a client
// that stopped sending.
func TestRequestFileContextBoundsTransfer(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	// The module bounds its own transfer, exactly as it would bound a subprocess.
	transferCtx, cancelTransfer := context.WithCancel(context.Background())

	result := requestFileAsync(transferCtx, sess, "image.bin", io.Discard)

	awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)

	// The client goes quiet. Only the module's own context can end this.
	cancelTransfer()

	if err := awaitResult(t, result); !errors.Is(err, context.Canceled) {
		t.Errorf("RequestFile err = %v, want context.Canceled", err)
	}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ERROR)

	if carried(&b.session) != nil {
		t.Error("upload still registered after cancellation")
	}
}

// failingReader returns data, then fails with err.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}

	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// TestSendFileReadErrorFailsTheDownload: a reader that fails ends the download
// on both ends. SendFile returns the error to the module, and the client is
// told, so its run fails instead of waiting for a file that will not come.
func TestSendFileReadErrorFailsTheDownload(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	broken := errors.New("input/output error")
	src := &failingReader{data: []byte("firm"), err: broken}

	if err := awaitResult(t, sendFileAsync(context.Background(), sess, "out.bin", 8, src)); !errors.Is(err, broken) {
		t.Errorf("SendFile err = %v, want %v", err, broken)
	}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ERROR)

	if carried(&b.session) != nil {
		t.Error("download still registered after it failed")
	}
}

// TestSendFileContextBoundsTransfer: cancelling the context ends a download
// that the client has not confirmed. SendFile returns the context's cause, and
// the client is told, so that neither side waits for the other.
func TestSendFileContextBoundsTransfer(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	transferCtx, cancelTransfer := context.WithCancel(context.Background())

	result := sendFileAsync(transferCtx, sess, "out.bin", 4, strings.NewReader("data"))

	// The client got the whole file but never confirms it.
	awaitFinalChunk(t, stream)
	cancelTransfer()

	if err := awaitResult(t, result); !errors.Is(err, context.Canceled) {
		t.Errorf("SendFile err = %v, want context.Canceled", err)
	}

	awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ERROR)

	if carried(&b.session) != nil {
		t.Error("download still registered after cancellation")
	}
}

// TestSendFileChecksTheSize: a reader that delivers another number of bytes
// than the module gave fails the download on both ends, before the client gets
// a final chunk.
func TestSendFileChecksTheSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		data string
	}{
		{"shorter", 8, "data"},
		{"longer", chunkSize, strings.Repeat("x", chunkSize+1)},
		{"longer on the first read", 2, "data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := newScriptedStream()
			defer close(stream.stop)

			b := &Broker{}
			sess, _ := b.Start(context.Background(), stream)

			err := awaitResult(t, sendFileAsync(context.Background(), sess, "out.bin", tc.size, strings.NewReader(tc.data)))
			if err == nil || !strings.Contains(err.Error(), "bytes given") {
				t.Errorf("SendFile err = %v, want the size mismatch", err)
			}

			awaitStatus(t, stream, pb.FileTransferResponse_STATUS_ERROR)

			if slices.ContainsFunc(stream.downloadChunks(), (*pb.FileChunk).GetIsFinal) {
				t.Error("the client got a final chunk of a file with the wrong size")
			}
		})
	}
}

// eventually waits until cond holds, failing the test if it does not in time.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within timeout")
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// gatedReader returns data, then blocks until gate is closed and reports the
// end of the file.
type gatedReader struct {
	data []byte
	gate chan struct{}
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]

		return n, nil
	}

	<-r.gate

	return 0, io.EOF
}

// TestDownloadConfirmedEarlyFails: a client that confirms a download before
// its final chunk has not received the whole file. SendFile must fail rather
// than report the file as sent.
func TestDownloadConfirmedEarlyFails(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	src := &gatedReader{data: []byte("firm"), gate: make(chan struct{})}
	result := sendFileAsync(context.Background(), sess, "out.bin", 0, src)

	id := awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_DOWNLOAD)

	eventually(t, func() bool { return len(stream.downloadChunks()) > 0 })

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
		FileTransferResponse: &pb.FileTransferResponse{TransferId: id, Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE},
	}}

	eventually(t, func() bool { return carried(&b.session) == nil })

	close(src.gate)

	if err := awaitResult(t, result); !errors.Is(err, ErrBadFileTransfer) {
		t.Errorf("SendFile err = %v, want ErrBadFileTransfer", err)
	}
}

// TestSessionCarriesOneTransferAtATime: while a download waits for the client,
// RequestFile and SendFile refuse a second transfer. Once the download ended,
// the next transfer starts.
func TestSessionCarriesOneTransferAtATime(t *testing.T) {
	stream := newScriptedStream()
	defer close(stream.stop)

	b := &Broker{}
	sess, _ := b.Start(context.Background(), stream)

	ctx := context.Background()
	result := sendFileAsync(ctx, sess, "out.bin", 4, strings.NewReader("data"))
	final := awaitFinalChunk(t, stream)

	if err := sess.RequestFile(ctx, "image.bin", io.Discard); !errors.Is(err, errTransferInProgress) {
		t.Errorf("RequestFile during a download: err = %v, want %v", err, errTransferInProgress)
	}

	if err := sess.SendFile(ctx, "other.bin", 4, strings.NewReader("data")); !errors.Is(err, errTransferInProgress) {
		t.Errorf("SendFile during a download: err = %v, want %v", err, errTransferInProgress)
	}

	stream.reply <- &pb.RunRequest{Msg: &pb.RunRequest_FileTransferResponse{
		FileTransferResponse: &pb.FileTransferResponse{
			TransferId: final[0].GetTransferId(), Status: pb.FileTransferResponse_STATUS_TRANSFER_COMPLETE,
		},
	}}

	if err := awaitResult(t, result); err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	requestFileAsync(ctx, sess, "image.bin", io.Discard)
	awaitAnnouncement(t, stream, pb.FileTransferRequest_DIRECTION_UPLOAD)
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

	if err := sess.RequestFile(dead, "image.bin", io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("RequestFile err = %v, want context.Canceled", err)
	}

	if err := sess.SendFile(dead, "out.bin", 4, strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Errorf("SendFile err = %v, want context.Canceled", err)
	}

	if carried(&b.session) != nil {
		t.Error("a transfer registered despite a cancelled context")
	}
}

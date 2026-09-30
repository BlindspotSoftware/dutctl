// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fastKeepalive detects a dead connection within a fraction of a second.
var fastKeepalive = keepalive{idle: 100 * time.Millisecond, timeout: 200 * time.Millisecond} //nolint:gochecknoglobals // test fixture

// blackholeProxy forwards TCP between clients and target until blackhole is
// called. From then on it swallows everything in both directions without
// closing either side, like a VPN or NAT mapping that silently went away. The
// proxy's own sockets stay healthy, so TCP keepalive cannot see the loss.
type blackholeProxy struct {
	ln     net.Listener
	target string
	dead   atomic.Bool

	mu    sync.Mutex
	conns []net.Conn
}

func newBlackholeProxy(t *testing.T, target string) *blackholeProxy {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	p := &blackholeProxy{ln: ln, target: target}

	go p.serve()

	t.Cleanup(func() {
		_ = ln.Close()

		p.mu.Lock()
		defer p.mu.Unlock()

		for _, c := range p.conns {
			_ = c.Close()
		}
	})

	return p
}

func (p *blackholeProxy) blackhole() { p.dead.Store(true) }

func (p *blackholeProxy) serve() {
	for {
		down, err := p.ln.Accept()
		if err != nil {
			return
		}

		up, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = down.Close()

			continue
		}

		p.mu.Lock()
		p.conns = append(p.conns, down, up)
		p.mu.Unlock()

		go p.pipe(down, up)
		go p.pipe(up, down)
	}
}

func (p *blackholeProxy) pipe(src, dst net.Conn) {
	buf := make([]byte, 32*1024)

	for {
		n, err := src.Read(buf)
		if n > 0 && !p.dead.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}

		if err != nil {
			return
		}
	}
}

// startServer serves a handler that sends one line, like a session's first
// output, then stays open until the request ends. ended is closed when it does.
func startServer(t *testing.T, ka keepalive) (addr string, ended <-chan struct{}) {
	t.Helper()

	done := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "connected\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(done)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := newH2CServer(ln.Addr().String(), handler, ka)

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	return ln.Addr().String(), done
}

// openSession starts a streaming request through the proxy and reads the first
// line, so the session is established before the connection goes dead.
func openSession(t *testing.T, client *http.Client, addr string) *bufio.Reader {
	t.Helper()

	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	body := bufio.NewReader(resp.Body)
	if _, err := body.ReadString('\n'); err != nil {
		t.Fatalf("first line: %v", err)
	}

	return body
}

func TestClientKeepaliveEndsSilentlyDeadConnection(t *testing.T) {
	addr, _ := startServer(t, keepalive{}) // server side off: only the client may notice
	proxy := newBlackholeProxy(t, addr)

	body := openSession(t, newH2CClient(fastKeepalive), proxy.ln.Addr().String())
	proxy.blackhole()

	readErr := make(chan error, 1)

	go func() {
		_, err := io.ReadAll(body)
		readErr <- err
	}()

	select {
	case err := <-readErr:
		if err == nil {
			t.Error("read ended without an error, want the dead connection reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client still waiting on a dead connection")
	}
}

func TestServerKeepaliveEndsSessionOfVanishedClient(t *testing.T) {
	addr, ended := startServer(t, fastKeepalive)
	proxy := newBlackholeProxy(t, addr)

	openSession(t, newH2CClient(keepalive{}), proxy.ln.Addr().String()) // client side off
	proxy.blackhole()

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("server still holds the session of a vanished client")
	}
}

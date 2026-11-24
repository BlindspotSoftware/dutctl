// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

// The tests of the modules that need no hardware, as a user runs them: they
// act on the agent's own machine.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// hostAgent starts an agent with a device whose commands use the modules
// that need no hardware, and returns it with the directory on the agent's
// side that its file commands use.
func hostAgent(t *testing.T) (*agent, string) {
	t.Helper()

	files := t.TempDir()

	agt := startAgentYAML(t, fmt.Sprintf(`
devices:
  host:
    desc: The agent's own machine
    cmds:
      run:
        uses:
          - module: shell
            passthrough: true
      run-quietly:
        uses:
          - module: shell
            passthrough: true
            with:
              quiet: true
      upload:
        uses:
          - module: file
            passthrough: true
            with:
              operation: upload
              destination: %[1]s/uploaded.txt
      download:
        uses:
          - module: file
            passthrough: true
            with:
              operation: download
              source: %[1]s/source.txt
      wait:
        uses:
          - module: time-wait
            passthrough: true
            with:
              duration: 10ms
      info:
        uses:
          - module: agent-status
`, files))

	return agt, files
}

func TestModulesShell(t *testing.T) {
	t.Parallel()

	agt, _ := hostAgent(t)

	res := agt.dutctl(t, "host", "run", "echo one two | tr ' ' '-'")
	mustSucceed(t, res)

	if res.stdout != "one-two\n" {
		t.Errorf("the shell printed %q", res.stdout)
	}

	res = agt.dutctl(t, "host", "run", "echo out; echo err >&2; exit 3")
	mustFail(t, res, 1, "shell command exited with code 3")

	if !strings.Contains(res.stdout, "out") || !strings.Contains(res.stdout, "err") {
		t.Errorf("the failed command's output %q lacks its stdout or stderr", res.stdout)
	}

	// Quiet: only a failed command's stderr is shown.
	res = agt.dutctl(t, "host", "run-quietly", "echo out")
	mustSucceed(t, res)

	if res.stdout != "" {
		t.Errorf("a quiet command printed %q", res.stdout)
	}

	res = agt.dutctl(t, "host", "run-quietly", "echo out; echo err >&2; exit 1")
	mustFail(t, res, 1, "shell command exited with code 1")

	if strings.Contains(res.stdout, "out") || !strings.Contains(res.stdout, "err") {
		t.Errorf("a quiet failed command printed %q, want its stderr only", res.stdout)
	}

	mustFail(t, agt.dutctl(t, "host", "run", "a", "b"), 1, "too many arguments")
}

func TestModulesFile(t *testing.T) {
	t.Parallel()

	agt, files := hostAgent(t)

	err := os.WriteFile(filepath.Join(agt.dir, "local.txt"), []byte("from the client\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	mustSucceed(t, agt.dutctl(t, "host", "upload", "local.txt"))

	got, err := os.ReadFile(filepath.Join(files, "uploaded.txt"))
	if err != nil || string(got) != "from the client\n" {
		t.Errorf("the uploaded file holds %q (%v)", got, err)
	}

	err = os.WriteFile(filepath.Join(files, "source.txt"), []byte("from the agent\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	mustSucceed(t, agt.dutctl(t, "host", "download", "fetched.txt"))

	got, err = os.ReadFile(filepath.Join(agt.dir, "fetched.txt"))
	if err != nil || string(got) != "from the agent\n" {
		t.Errorf("the downloaded file holds %q (%v)", got, err)
	}

	// A file the client cannot send leaves the destination as it was.
	mustFail(t, agt.dutctl(t, "host", "upload", "missing.txt"), 1, `"missing.txt"`)

	got, err = os.ReadFile(filepath.Join(files, "uploaded.txt"))
	if err != nil || string(got) != "from the client\n" {
		t.Errorf("after the failed upload, the destination holds %q (%v)", got, err)
	}
}

// Every transfer carries the SHA-256 of its file, and the client shows it. An
// upload damaged in transit fails the check on the agent, and the file is not
// written.
func TestModulesFileChecksum(t *testing.T) {
	t.Parallel()

	agt, files := hostAgent(t)

	const upload = "checksummed by the file module\n"

	err := os.WriteFile(filepath.Join(agt.dir, "local.txt"), []byte(upload), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	wantUp := sha256Hex(upload)

	// The proxy damages the file's content but leaves the stream intact, as a
	// faulty router does: nothing below the checksum notices.
	damaged := *agt
	damaged.addr = corruptingProxy(t, agt.addr, []byte("checksummed"))

	res := damaged.dutctl(t, "host", "upload", "local.txt")
	mustFail(t, res, 1, "file corrupted in transit")

	if !strings.Contains(res.stderr, "want "+wantUp) {
		t.Errorf("the error names no expected digest %s:\n%s", wantUp, res.stderr)
	}

	_, err = os.Stat(filepath.Join(files, "uploaded.txt"))
	if !os.IsNotExist(err) {
		t.Errorf("the damaged upload was written (%v)", err)
	}

	res = agt.dutctl(t, "host", "upload", "local.txt")
	mustSucceed(t, res)
	inOrder(t, res.stdout, `sent "local.txt" (`, "sha256 "+wantUp[:12])

	const download = "checksummed by the agent\n"

	err = os.WriteFile(filepath.Join(files, "source.txt"), []byte(download), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	res = agt.dutctl(t, "host", "download", "fetched.txt")
	mustSucceed(t, res)
	inOrder(t, res.stdout, `received "fetched.txt" (`, "sha256 "+sha256Hex(download)[:12])
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// corruptingProxy forwards connections to target and flips the last byte of
// the first marker a client sends, once across all connections. Every byte
// keeps its place, so the stream stays well-formed and only the payload is
// wrong.
func corruptingProxy(t *testing.T, target string, marker []byte) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	var done atomic.Bool

	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}

			go proxyConn(client, target, marker, &done)
		}
	}()

	return ln.Addr().String()
}

func proxyConn(client net.Conn, target string, marker []byte, done *atomic.Bool) {
	defer client.Close()

	agent, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer agent.Close()

	go func() {
		_, _ = io.Copy(client, agent)
		_ = client.Close()
	}()

	buf := make([]byte, 64*1024)

	// tail is the end of the reads before, so a marker that TCP splits across
	// two reads is found too. The marker's last byte is always in the newest
	// read, which is not forwarded yet, so that is the byte to flip.
	var tail []byte

	for {
		n, err := client.Read(buf)

		seen := append(append([]byte(nil), tail...), buf[:n]...)
		if i := bytes.Index(seen, marker); i >= 0 && done.CompareAndSwap(false, true) {
			buf[i+len(marker)-1-len(tail)] ^= 0x20
		}

		tail = seen[max(0, len(seen)-len(marker)+1):]

		if _, werr := agent.Write(buf[:n]); werr != nil || err != nil {
			return
		}
	}
}

func TestModulesTimeWait(t *testing.T) {
	t.Parallel()

	agt, _ := hostAgent(t)

	res := agt.dutctl(t, "host", "wait")
	mustSucceed(t, res)

	if res.stdout != "Waiting for 10ms\n" {
		t.Errorf("wait printed %q", res.stdout)
	}

	res = agt.dutctl(t, "host", "wait", "20ms")
	mustSucceed(t, res)

	if res.stdout != "Waiting for 20ms\n" {
		t.Errorf("wait 20ms printed %q", res.stdout)
	}

	mustFail(t, agt.dutctl(t, "host", "wait", "soon"), 1, "invalid duration")

	// A wait ends as soon as its command is cancelled.
	wait := agt.start(t, "host", "wait", "1h")
	wait.stdout.waitFor(t, "Waiting for 1h0m0s")
	wait.signal(t, os.Interrupt)
	mustFail(t, wait.wait(t), 130, "interrupted")
	agt.waitRecord(t, "module stopped after the command was cancelled", "module", "time-wait")
}

func TestModulesAgentStatus(t *testing.T) {
	t.Parallel()

	agt, _ := hostAgent(t)

	res := agt.dutctl(t, "host", "info")
	mustSucceed(t, res)

	if !strings.HasPrefix(res.stdout, "Linux ") {
		t.Errorf("info printed %q, want the agent's uname -a", res.stdout)
	}
}

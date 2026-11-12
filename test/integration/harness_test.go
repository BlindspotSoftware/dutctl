// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

// Package integration runs the dutctl and dutagent binaries of this checkout
// against each other on this machine: the RPCs, the module.Session contract
// through the demo modules, the configuration grammar, the client's flags and
// the modules that need no hardware. It is the middle of three test layers,
// between the unit tests and the tests on real hardware in FirmwareCI.
//
// The tests skip unless DUTCTL_INTEGRATION=1, and with -short. The first test
// builds both binaries, with the race detector when the tests run with it, and
// every test starts an agent of its own on a free port. The serial tests also
// need socat, and skip without it.
//
// With DUTCTL_INTEGRATION_COVERDIR set to an absolute directory, the binaries
// are built with coverage of all packages of the module and write their
// counters there. To see it together with the unit tests' coverage:
//
//	mkdir -p /tmp/cover/unit /tmp/cover/integration
//	go test -cover -coverpkg=./... ./... -args -test.gocoverdir=/tmp/cover/unit
//	DUTCTL_INTEGRATION=1 DUTCTL_INTEGRATION_COVERDIR=/tmp/cover/integration go test ./test/integration/
//	go tool covdata percent -i=/tmp/cover/unit,/tmp/cover/integration
package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/pty"
)

const (
	// enableEnv enables the integration tests when set to 1.
	enableEnv = "DUTCTL_INTEGRATION"
	// coverEnv names a directory for the binaries' coverage counters; see the
	// package doc.
	coverEnv = "DUTCTL_INTEGRATION_COVERDIR"
	// waitTimeout bounds every wait for something a process does. It is
	// generous, for binaries built with the race detector on a busy runner;
	// a test that passes never waits that long.
	waitTimeout = 20 * time.Second
	// testUser is $USER for the client, so that its default identity is
	// testUser@<host>.
	testUser = "integration"
)

// binaries are dutagent and dutctl, built once for all tests.
//
//nolint:gochecknoglobals // built once per test binary, shared by the tests
var binaries struct {
	once   sync.Once
	dir    string
	agent  string
	client string
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()

	if binaries.dir != "" {
		_ = os.RemoveAll(binaries.dir)
	}

	os.Exit(code)
}

// build skips t unless the integration tests are enabled, and builds the
// binaries on its first call.
func build(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test, skipped with -short")
	}

	if os.Getenv(enableEnv) != "1" {
		t.Skipf("integration test, set %s=1 to run it", enableEnv)
	}

	// The binaries run in other directories than the test.
	if dir := os.Getenv(coverEnv); dir != "" && !filepath.IsAbs(dir) {
		t.Fatalf("%s must be an absolute path, not %q", coverEnv, dir)
	}

	binaries.once.Do(func() {
		binaries.dir, binaries.err = os.MkdirTemp("", "dutctl-integration-")
		if binaries.err != nil {
			return
		}

		args := []string{"build", "-o", binaries.dir + "/"}
		if raceEnabled {
			args = append(args, "-race")
		}

		if os.Getenv(coverEnv) != "" {
			args = append(args, "-cover", "-coverpkg=github.com/BlindspotSoftware/dutctl/...")
		}

		args = append(args, "github.com/BlindspotSoftware/dutctl/cmds/dutagent", "github.com/BlindspotSoftware/dutctl/cmds/dutctl")

		out, err := exec.Command("go", args...).CombinedOutput()
		if err != nil {
			binaries.err = fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, out)

			return
		}

		binaries.agent = filepath.Join(binaries.dir, "dutagent")
		binaries.client = filepath.Join(binaries.dir, "dutctl")
	})

	if binaries.err != nil {
		t.Fatalf("building the binaries: %v", binaries.err)
	}
}

// stream collects what a process writes to one of its outputs and lets a test
// wait for something to arrive in it, without polling.
type stream struct {
	mu  sync.Mutex
	buf bytes.Buffer
	// changed is closed and renewed on every write and when the stream ends.
	changed chan struct{}
	// ended tells that nothing more will arrive: the writer has gone.
	ended bool
}

func newStream() *stream {
	return &stream{changed: make(chan struct{})}
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf.Write(p)
	s.notify()

	return len(p), nil
}

// end marks that nothing more arrives, so a wait for it fails at once.
func (s *stream) end() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ended = true
	s.notify()
}

// notify wakes the waiters. s.mu must be held.
func (s *stream) notify() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *stream) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// waitFor waits until the stream holds want and returns what it holds then.
func (s *stream) waitFor(t *testing.T, want string) string {
	t.Helper()

	return s.waitMatch(t, fmt.Sprintf("%q", want), func(got string) bool { return strings.Contains(got, want) })
}

// waitMatch waits until match accepts what the stream holds, and returns it.
// what describes what the test waits for.
func (s *stream) waitMatch(t *testing.T, what string, match func(string) bool) string {
	t.Helper()

	deadline := time.After(waitTimeout)

	for {
		s.mu.Lock()
		got, changed, ended := s.buf.String(), s.changed, s.ended
		s.mu.Unlock()

		if match(got) {
			return got
		}

		if ended {
			t.Fatalf("waited for %s, but the stream ended with:\n%s", what, got)
		}

		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("waited %s for %s, got:\n%s", waitTimeout, what, got)
		}
	}
}

// process is a started dutagent or dutctl.
type process struct {
	cmd    *exec.Cmd
	stdout *stream
	stderr *stream
	// exited is closed once the process exited and its outputs ended.
	exited chan struct{}
	// err is what Wait returned, set before exited is closed.
	err error
}

// start starts cmd with its outputs on fresh streams, unless they are set,
// and stops it, if it still runs, when the test ends.
func start(t *testing.T, cmd *exec.Cmd) *process {
	t.Helper()

	proc := &process{cmd: cmd, stdout: newStream(), stderr: newStream(), exited: make(chan struct{})}

	if cmd.Stdout == nil {
		cmd.Stdout = proc.stdout
	}

	if cmd.Stderr == nil {
		cmd.Stderr = proc.stderr
	}

	err := cmd.Start()
	if err != nil {
		t.Fatalf("starting %s: %v", cmd.Path, err)
	}

	go func() {
		proc.err = cmd.Wait()
		proc.stdout.end()
		proc.stderr.end()
		close(proc.exited)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-proc.exited
	})

	return proc
}

// wait waits for the process to exit and returns its exit code.
func (p *process) wait(t *testing.T) int {
	t.Helper()

	select {
	case <-p.exited:
	case <-time.After(waitTimeout):
		t.Fatalf("%s did not exit within %s\nstdout:\n%s\nstderr:\n%s", p.cmd.Path, waitTimeout, p.stdout, p.stderr)
	}

	var exitErr *exec.ExitError
	if errors.As(p.err, &exitErr) {
		return exitErr.ExitCode()
	}

	if p.err != nil {
		t.Fatalf("%s: %v", p.cmd.Path, p.err)
	}

	return 0
}

// env returns the environment for a process: nothing of the user's but PATH,
// and a home of its own, so that neither binary reads or writes the files of
// the user who runs the tests.
func env(home string) []string {
	return []string{
		"GOCOVERDIR=" + os.Getenv(coverEnv),
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"USER=" + testUser,
		"LANG=C",
		// A binary built with the race detector sleeps a second at its exit
		// by default, waiting for reports from other goroutines; dutctl exits
		// many times per test.
		"GORACE=atexit_sleep_ms=20",
	}
}

// agent is a running dutagent.
type agent struct {
	*process

	// addr is the address the agent listens on.
	addr string
	// dir is the working directory of the clients, where their files go.
	dir string
	// home is the home directory of the clients.
	home string
}

// startAgent starts dutagent with the configuration file config on a free
// port, waits until it listens, and stops it when the test ends: with SIGTERM,
// as a service manager does, and the test fails if the agent does not exit
// cleanly or the race detector reported a race in it.
func startAgent(t *testing.T, config string) *agent {
	t.Helper()
	build(t)

	cmd := exec.Command(binaries.agent, "-a", "127.0.0.1:0", "-c", config, "-log-json", "-log", "debug")
	cmd.Dir = t.TempDir()
	cmd.Env = env(t.TempDir())

	agt := &agent{dir: t.TempDir(), home: t.TempDir()}
	agt.process = start(t, cmd)

	// Cleanups run last first: the stop runs before start's kill, and after
	// the cleanups of the clients started later, so no run holds it up.
	t.Cleanup(func() { agt.stop(t) })

	listening := agt.waitRecord(t, "rpc service listening")

	addr, ok := listening["addr"].(string)
	if !ok {
		t.Fatalf("the agent logged no address: %v", listening)
	}

	agt.addr = addr

	return agt
}

// startAgentYAML starts dutagent with the configuration config.
func startAgentYAML(t *testing.T, config string) *agent {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dutagent.yaml")

	err := os.WriteFile(path, []byte(config), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return startAgent(t, path)
}

// repoFile returns the absolute path of a file of the repository, given
// relative to its root.
func repoFile(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}

	return abs
}

// stop stops the agent and checks how it ended. A reservation that the test
// left keeps the agent in its first stop stage, so it gets a second signal
// then, which ends the reservations.
func (a *agent) stop(t *testing.T) {
	t.Helper()

	_ = a.cmd.Process.Signal(syscall.SIGTERM)

	if a.waitHeld() {
		_ = a.cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-a.exited:
	case <-time.After(waitTimeout):
		_ = a.cmd.Process.Kill()
		<-a.exited
		t.Errorf("the agent did not stop within %s", waitTimeout)
	}

	log := a.stderr.String()

	if strings.Contains(log, "DATA RACE") {
		t.Errorf("the race detector reported a race in the agent")
	}

	if a.err != nil {
		t.Errorf("the agent exited with %v", a.err)
	}

	if t.Failed() {
		t.Logf("agent log:\n%s", log)
	}
}

// waitHeld waits, after the first stop signal, until the agent exits or says
// that something holds it in the first stop stage, and tells whether it did.
func (a *agent) waitHeld() bool {
	deadline := time.After(waitTimeout)

	for {
		a.stderr.mu.Lock()
		log, changed := a.stderr.buf.String(), a.stderr.changed
		a.stderr.mu.Unlock()

		if _, held := findRecord(log, "waiting for the running commands and reservations to end; "+
			"send the stop signal again to abort the commands"); held {
			return true
		}

		select {
		case <-a.exited:
			return false
		case <-changed:
		case <-deadline:
			return false
		}
	}
}

// records parses the complete JSON lines of the agent's log.
func records(log string) []map[string]any {
	var recs []map[string]any

	scanner := bufio.NewScanner(strings.NewReader(log))
	for scanner.Scan() {
		var rec map[string]any
		if json.Unmarshal(scanner.Bytes(), &rec) == nil {
			recs = append(recs, rec)
		}
	}

	return recs
}

// findRecord returns the first record of log with message msg and the
// attributes attrs, given as key, value, key, value and so on.
func findRecord(log, msg string, attrs ...string) (map[string]any, bool) {
	for _, rec := range records(log) {
		if rec["msg"] != msg {
			continue
		}

		match := true

		for i := 0; i+1 < len(attrs); i += 2 {
			if fmt.Sprint(rec[attrs[i]]) != attrs[i+1] {
				match = false
			}
		}

		if match {
			return rec, true
		}
	}

	return nil, false
}

// waitRecord waits until the agent logged a record with message msg and the
// attributes attrs, given as key, value, key, value and so on.
func (a *agent) waitRecord(t *testing.T, msg string, attrs ...string) map[string]any {
	t.Helper()

	var rec map[string]any

	a.stderr.waitMatch(t, fmt.Sprintf("a log record %q %v", msg, attrs), func(log string) bool {
		var ok bool
		rec, ok = findRecord(log, msg, attrs...)

		return ok
	})

	return rec
}

// result is how a dutctl run ended.
type result struct {
	stdout string
	stderr string
	code   int
}

// client is a running dutctl.
type client struct {
	*process

	// stdin is the pipe on the client's standard input.
	stdin io.WriteCloser
}

// newClientCmd returns the command for dutctl with hidden and args, run in dir
// with home as its home directory, and logs it as a user would type it: with
// args, but without hidden, the arguments the harness adds for every call,
// such as the agent's address.
func newClientCmd(t *testing.T, dir, home string, hidden []string, args ...string) *exec.Cmd {
	t.Helper()
	t.Logf("$ %s", shellQuote(append([]string{"dutctl"}, args...)))

	cmd := exec.Command(binaries.client, append(slices.Clone(hidden), args...)...)
	cmd.Dir = dir
	cmd.Env = env(home)

	return cmd
}

// shellQuote returns args as a shell command line: an argument the shell would
// split or expand is in single quotes.
func shellQuote(args []string) string {
	plain := regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)
	quoted := make([]string, len(args))

	for i, arg := range args {
		if plain.MatchString(arg) {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
	}

	return strings.Join(quoted, " ")
}

// start starts dutctl against the agent with args and a pipe on its standard
// input.
func (a *agent) start(t *testing.T, args ...string) *client {
	t.Helper()

	cmd := newClientCmd(t, a.dir, a.home, []string{"-s", a.addr}, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	return &client{process: start(t, cmd), stdin: stdin}
}

// dutctl runs dutctl against the agent with args, and no input, to its end.
func (a *agent) dutctl(t *testing.T, args ...string) result {
	t.Helper()

	return a.dutctlInput(t, "", args...)
}

// dutctlInput runs dutctl against the agent with args and input on its
// standard input, to its end.
func (a *agent) dutctlInput(t *testing.T, input string, args ...string) result {
	t.Helper()

	c := a.start(t, args...)
	c.write(t, input)
	c.closeInput(t)

	return c.wait(t)
}

// write types s on the client's standard input.
func (c *client) write(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		return
	}

	_, err := io.WriteString(c.stdin, s)
	if err != nil {
		t.Fatalf("writing to dutctl: %v", err)
	}
}

// closeInput ends the client's standard input.
func (c *client) closeInput(t *testing.T) {
	t.Helper()

	err := c.stdin.Close()
	if err != nil {
		t.Fatalf("closing the input of dutctl: %v", err)
	}
}

// signal sends sig to the client.
func (c *client) signal(t *testing.T, sig os.Signal) {
	t.Helper()

	err := c.cmd.Process.Signal(sig)
	if err != nil {
		t.Fatalf("signalling dutctl: %v", err)
	}
}

// wait waits for the client to exit and returns how it ended. The test fails
// if the race detector reported a race in the client.
func (c *client) wait(t *testing.T) result {
	t.Helper()

	code := c.process.wait(t)
	res := result{stdout: c.stdout.String(), stderr: c.stderr.String(), code: code}

	if strings.Contains(res.stderr, "DATA RACE") {
		t.Errorf("the race detector reported a race in dutctl:\n%s", res.stderr)
	}

	return res
}

// terminal is a dutctl whose standard input and output are a pseudo-terminal,
// its controlling one, as in an interactive shell.
type terminal struct {
	*process

	// master is the terminal's master end: what is written to it, the client
	// reads as typed keys.
	master *os.File
	// slave is the terminal's slave end, the client's standard input and
	// output.
	slave *os.File
	// screen collects what the client writes to the terminal.
	screen *stream
}

// startOnTerminal starts dutctl against the agent with args, on a fresh
// pseudo-terminal. Its standard error is a pipe, apart from the terminal.
func (a *agent) startOnTerminal(t *testing.T, args ...string) *terminal {
	t.Helper()

	master, slave := pty.Open(t)

	cmd := newClientCmd(t, a.dir, a.home, []string{"-s", a.addr}, args...)
	cmd.Stdin, cmd.Stdout = slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	term := &terminal{master: master, slave: slave, screen: newStream()}

	// The copy ends when the test closes the terminal.
	go func() { _, _ = io.Copy(term.screen, master) }()

	term.process = start(t, cmd)

	return term
}

// write types s on the terminal.
func (term *terminal) write(t *testing.T, s string) {
	t.Helper()

	_, err := term.master.WriteString(s)
	if err != nil {
		t.Fatalf("typing on the terminal: %v", err)
	}
}

// wait waits for the client to exit and returns how it ended; its stdout is
// what reached the terminal.
func (term *terminal) wait(t *testing.T) result {
	t.Helper()

	code := term.process.wait(t)

	return result{stdout: term.screen.String(), stderr: term.stderr.String(), code: code}
}

// ordered tells whether each of wants appears in got after the one before it.
func ordered(got string, wants ...string) bool {
	for _, want := range wants {
		i := strings.Index(got, want)
		if i < 0 {
			return false
		}

		got = got[i+len(want):]
	}

	return true
}

// inOrder fails the test unless each of wants appears in got after the one
// before it.
func inOrder(t *testing.T, got string, wants ...string) {
	t.Helper()

	if !ordered(got, wants...) {
		t.Fatalf("%q missing, or not in this order, in:\n%s", wants, got)
	}
}

// mustSucceed fails the test unless res is a successful run.
func mustSucceed(t *testing.T, res result) {
	t.Helper()

	if res.code != 0 {
		t.Fatalf("dutctl exited with %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
}

// mustFail fails the test unless res is a run that exited with code and
// said want on standard error.
func mustFail(t *testing.T, res result, code int, want string) {
	t.Helper()

	if res.code != code || !strings.Contains(res.stderr, want) {
		t.Fatalf("dutctl exited with %d, want %d with %q on stderr\nstdout:\n%s\nstderr:\n%s",
			res.code, code, want, res.stdout, res.stderr)
	}
}

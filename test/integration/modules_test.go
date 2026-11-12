// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

// The tests of the modules that need no hardware, as a user runs them: they
// act on the agent's own machine.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	mustFail(t, agt.dutctl(t, "host", "upload", "missing.txt"), 1, `"missing.txt"`)
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

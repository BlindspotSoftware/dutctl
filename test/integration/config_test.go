// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

// The tests of the configuration grammar, through the commands of the
// example configuration: what a command hands to its modules, in which order
// they run, and what the agent does with a configuration it cannot use.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A command with templated arguments hands each module the value of the
// argument it names, also inside a longer argument; the modules run in order.
func TestConfigTemplatedArguments(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	err := os.WriteFile(filepath.Join(agt.dir, "fw.bin"), []byte("firmware 2.0\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	res := agt.dutctl(t, "demo-board", "update", "fw.bin")
	mustSucceed(t, res)
	inOrder(t, res.stdout,
		"Updating the board with fw.bin.\n",
		"The power line is already off.\n",
		"Wrote fw.bin, 13 bytes",
		"The power line is now on.\n")

	// The board boots what the update wrote.
	res = agt.dutctlInput(t, "version\r", "demo-board", "console")
	mustSucceed(t, res)
	inOrder(t, res.stdout, "Booting fw.bin, 13 bytes", "version\r\nfw.bin, 13 bytes")

	mustFail(t, agt.dutctl(t, "demo-board", "update"), 1, "expected 1 argument(s) but got 0")
	mustFail(t, agt.dutctl(t, "demo-board", "update", "a", "b"), 1, "expected 1 argument(s) but got 2")

	// An argument with a space stays one, also inside a longer one.
	res = agt.dutctl(t, "agent", "hello", "Ada Lovelace")
	mustSucceed(t, res)

	if res.stdout != "Hello, Ada Lovelace!\n" {
		t.Errorf("hello printed %q", res.stdout)
	}
}

// A command of modules with fixed arguments takes none at run time; the
// options of a module's use reach that use's instance.
func TestConfigFixedArgumentsAndOptions(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	mustFail(t, agt.dutctl(t, "demo-board", "protect", "now"), 1, "neither a passthrough module nor declared arguments")

	// protect and unprotect run the switch that power runs, wired by its
	// option to the write-protect line, with a fixed argument each.
	res := agt.dutctl(t, "demo-board", "protect")
	mustSucceed(t, res)

	if res.stdout != "The write-protect line is now on.\n" {
		t.Errorf("protect printed %q", res.stdout)
	}

	mustFail(t, agt.dutctl(t, "demo-board", "flash", "write", "fw.bin"), 1, "write-protected")

	res = agt.dutctl(t, "demo-board", "unprotect")
	mustSucceed(t, res)

	if res.stdout != "The write-protect line is now off.\n" {
		t.Errorf("unprotect printed %q", res.stdout)
	}

	res = agt.dutctl(t, "demo-board", "power")
	mustSucceed(t, res)

	if res.stdout != "The power line is off.\n" {
		t.Errorf("after protect and unprotect, power printed %q", res.stdout)
	}

	// The switch's cycle is 1s unless configured, and the console runs until
	// the user ends it unless it expects a text: reboot sets 500ms and the
	// board's welcome line.
	res = agt.dutctl(t, "demo-board", "reboot")
	mustSucceed(t, res)
	inOrder(t, res.stdout, "Switching it on again in 500ms.", "Welcome to the demo board.")
	agt.waitRecord(t, "console ended: the board printed the expected text", "module", "demo-console")
}

// A passthrough module gets the run's arguments as they are: a file name with
// a space arrives as one argument.
func TestConfigPassthrough(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	err := os.WriteFile(filepath.Join(agt.dir, "my fw.bin"), []byte("firmware 2.1\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	res := agt.dutctl(t, "demo-board", "flash", "write", "my fw.bin")
	mustSucceed(t, res)
	inOrder(t, res.stdout, "Wrote my fw.bin, 13 bytes")

	res = agt.dutctl(t, "agent", "echo", "one", "two words")
	mustSucceed(t, res)

	if res.stdout != "one two words\n" {
		t.Errorf("echo printed %q", res.stdout)
	}
}

// An agent refuses to start with a configuration it cannot use, and tells why.
func TestConfigTheAgentRefuses(t *testing.T) {
	t.Parallel()
	build(t)

	tests := []struct {
		name   string
		config string
		want   string
	}{
		{"unknown module", "devices:\n  d:\n    cmds:\n      c:\n        uses:\n          - module: nothing\n", "loading config failed"},
		{"a module's Init fails", "devices:\n  d:\n    cmds:\n      c:\n        uses:\n          - module: demo-switch\n" +
			"            with:\n              line: reset\n", "module initialization failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dutagent.yaml")

			err := os.WriteFile(path, []byte(tt.config), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(binaries.agent, "-a", "127.0.0.1:0", "-c", path)
			cmd.Env = env(t.TempDir())

			proc := start(t, cmd)
			if code := proc.wait(t); code != 1 {
				t.Errorf("the agent exited with %d, want 1", code)
			}

			inOrder(t, proc.stderr.String(), tt.want)
		})
	}
}

// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package integration

import (
	"encoding/csv"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// tour is the example configuration of the demo modules, which the README's
// Getting Started uses: the device agent with the wrap modules, and the
// simulated demo board.
const tour = "pkg/module/demo/demo-example-cfg.yml"

func TestRPCListsDevicesAndCommands(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	for _, args := range [][]string{nil, {"list"}} {
		res := agt.dutctl(t, args...)
		mustSucceed(t, res)

		if want := "- agent\n- demo-board\n"; res.stdout != want {
			t.Errorf("dutctl %v listed %q, want %q", args, res.stdout, want)
		}
	}

	res := agt.dutctl(t, "demo-board")
	mustSucceed(t, res)

	if want := "- console\n- flash\n- power\n- protect\n- reboot\n- unprotect\n- update\n"; res.stdout != want {
		t.Errorf("the commands of demo-board are %q, want %q", res.stdout, want)
	}

	mustFail(t, agt.dutctl(t, "nowhere"), 1, `"nowhere"`)
}

func TestRPCHelp(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	// A passthrough module's help is the command's help.
	res := agt.dutctl(t, "demo-board", "flash", "help")
	mustSucceed(t, res)

	if !strings.Contains(res.stdout, "Write a firmware image to the flash of the simulated demo board") {
		t.Errorf("the help of flash lacks the module's help:\n%s", res.stdout)
	}

	// A command with templated arguments lists them with their descriptions.
	res = agt.dutctl(t, "demo-board", "update", "help")
	mustSucceed(t, res)

	if !strings.Contains(res.stdout, "image: The firmware image on your machine") {
		t.Errorf("the help of update lacks its argument:\n%s", res.stdout)
	}

	mustFail(t, agt.dutctl(t, "demo-board", "nothing", "help"), 1, `"nothing"`)
	mustFail(t, agt.dutctl(t, "demo-board", "flash", "help", "more"), 1, "invalid command line")
}

func TestRPCRunsACommand(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctl(t, "demo-board", "power", "status")
	mustSucceed(t, res)

	if res.stdout != "The power line is off.\n" {
		t.Errorf("status printed %q", res.stdout)
	}

	mustFail(t, agt.dutctl(t, "nowhere", "status"), 1, `"nowhere"`)
	mustFail(t, agt.dutctl(t, "demo-board", "nothing"), 1, `"nothing"`)
	mustFail(t, agt.dutctl(t, "demo-board", "flash", "erase", "fw.bin"), 1, `unknown operation "erase"`)
}

func TestRPCLocksADevice(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))
	alice := []string{"-u", "alice"}
	bob := []string{"-u", "bob"}

	mustSucceed(t, agt.dutctl(t, append(alice, "demo-board", "lock", "30m")...))

	res := agt.dutctl(t, "list")
	if !strings.Contains(res.stdout, "demo-board") || !strings.Contains(res.stdout, "alice") {
		t.Errorf("list does not show alice's lock:\n%s", res.stdout)
	}

	// The owner runs commands, another user does not.
	mustSucceed(t, agt.dutctl(t, append(alice, "demo-board", "power", "status")...))
	mustFail(t, agt.dutctl(t, append(bob, "demo-board", "power", "status")...), 1, `locked by "alice"`)
	mustFail(t, agt.dutctl(t, append(bob, "demo-board", "lock")...), 1, `locked by "alice"`)
	mustFail(t, agt.dutctl(t, append(bob, "demo-board", "unlock")...), 1, `locked by "alice"`)

	// A forced unlock frees the device for everyone.
	mustSucceed(t, agt.dutctl(t, append(bob, "demo-board", "unlock", "force")...))
	mustSucceed(t, agt.dutctl(t, append(bob, "demo-board", "power", "status")...))

	mustFail(t, agt.dutctl(t, append(alice, "demo-board", "unlock")...), 1, "not locked")
	mustFail(t, agt.dutctl(t, append(alice, "demo-board", "lock", "0s")...), 1, "positive")
	mustFail(t, agt.dutctl(t, append(alice, "demo-board", "unlock", "please")...), 1, "invalid command line")
}

// A user's different commands run side by side; the same command, or another
// user's, does not run beside one that runs.
func TestRPCRunsAUsersCommandsSideBySide(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	console := agt.start(t, "demo-board", "console")
	console.stderr.waitFor(t, "Connected to the demo board")

	mustSucceed(t, agt.dutctl(t, "demo-board", "power", "on"))
	console.stdout.waitFor(t, "Welcome to the demo board")

	mustFail(t, agt.dutctl(t, "demo-board", "console"), 1, "already running")
	mustFail(t, agt.dutctl(t, "-u", "someone-else", "demo-board", "power", "status"), 1, `in use by "integration@`)

	console.write(t, "exit\r")
	mustSucceed(t, console.wait(t))

	// The console's run has ended, and with it its hold on the device.
	mustSucceed(t, agt.dutctlInput(t, "exit\r", "demo-board", "console"))
	mustSucceed(t, agt.dutctl(t, "-u", "someone-else", "demo-board", "power", "status"))
}

func TestRPCReportsAnUnreachableAgent(t *testing.T) {
	t.Parallel()
	build(t)

	// A port that was free a moment ago has no agent behind it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := listener.Addr().String()
	_ = listener.Close()

	cmd := newClientCmd(t, t.TempDir(), t.TempDir(), nil, "-s", addr, "list")
	res := result{}
	proc := start(t, cmd)
	res.code = proc.wait(t)
	res.stderr = proc.stderr.String()

	mustFail(t, res, 1, "cannot reach dutagent at "+addr)
}

func TestRPCReportsAVersion(t *testing.T) {
	t.Parallel()
	build(t)

	proc := start(t, newClientCmd(t, t.TempDir(), t.TempDir(), nil, "version"))
	if code := proc.wait(t); code != 0 || !strings.Contains(proc.stdout.String(), "DUT Control Client") {
		t.Errorf("dutctl version exited with %d and printed %q", code, proc.stdout)
	}

	proc = start(t, newClientCmd(t, t.TempDir(), t.TempDir(), nil, "-bogus"))
	if code := proc.wait(t); code != 2 || !strings.Contains(proc.stderr.String(), "flag provided but not defined") {
		t.Errorf("dutctl -bogus exited with %d and said %q", code, proc.stderr)
	}
}

// The output formats carry the same content in their own shape.
func TestRPCOutputFormats(t *testing.T) {
	t.Parallel()

	agt := startAgent(t, repoFile(t, tour))

	res := agt.dutctl(t, "-f", "json", "list")
	mustSucceed(t, res)

	var list struct {
		ContentType string `json:"contentType"`
		Data        []struct {
			Name string `json:"name"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(res.stdout), &list); err != nil || len(list.Data) != 2 || list.Data[0].Name != "agent" {
		t.Errorf("json list %q does not parse into the two devices: %v", res.stdout, err)
	}

	res = agt.dutctl(t, "-f", "yaml", "list")
	mustSucceed(t, res)

	var yamlList map[string]any
	if err := yaml.Unmarshal([]byte(res.stdout), &yamlList); err != nil || !strings.Contains(res.stdout, "demo-board") {
		t.Errorf("yaml list %q does not parse or lacks demo-board: %v", res.stdout, err)
	}

	// A oneline record is a CSV record on one line: time, content type, level,
	// data. Output of several lines stays on that line.
	res = agt.dutctl(t, "-f", "oneline", "demo-board", "power", "status")
	mustSucceed(t, res)

	fields, err := csv.NewReader(strings.NewReader(res.stdout)).Read()
	if err != nil || strings.Count(res.stdout, "\n") != 1 || len(fields) != 4 || fields[1] != "module-output" ||
		fields[3] != "The power line is off." {
		t.Errorf("oneline output %q is not a one-line CSV record of the module's output: %v", res.stdout, err)
	}

	res = agt.dutctl(t, "-f", "oneline", "demo-board", "flash", "help")
	mustSucceed(t, res)

	if strings.Count(res.stdout, "\n") != 1 || !strings.Contains(res.stdout, `ARGUMENTS:\n`) {
		t.Errorf("oneline help %q is not one record on one line", res.stdout)
	}

	// -v adds the context of the response.
	res = agt.dutctl(t, "-v", "demo-board", "power", "status")
	mustSucceed(t, res)

	if !strings.Contains(res.stdout, agt.addr) || !strings.Contains(res.stdout, "status") {
		t.Errorf("-v output lacks the agent's address and the command:\n%s", res.stdout)
	}

	// -log debug tells about the client's work on stderr, the output stays.
	res = agt.dutctl(t, "-log", "debug", "demo-board", "power", "status")
	mustSucceed(t, res)

	if res.stdout != "The power line is off.\n" || !strings.Contains(res.stderr, "DEBUG") {
		t.Errorf("-log debug: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

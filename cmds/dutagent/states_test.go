// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/BlindspotSoftware/dutctl/internal/fsm"
	"github.com/BlindspotSoftware/dutctl/internal/test/fakes"
	"github.com/BlindspotSoftware/dutctl/pkg/dut"
	"github.com/BlindspotSoftware/dutctl/pkg/module"

	pb "github.com/BlindspotSoftware/dutctl/protobuf/gen/dutctl/v1"
)

// stateEqual reports whether two state function pointers are the same.
func stateEqual(a, b fsm.State[runCmdArgs]) bool { return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b) }

func TestReceiveCommandRPC(t *testing.T) {
	tests := []struct {
		name        string
		recv        []*pb.RunRequest
		recvErr     error
		wantErrCode connect.Code
		wantNext    fsm.State[runCmdArgs]
		wantCmd     *pb.Command
	}{
		{
			name:     "success_first_command",
			recv:     []*pb.RunRequest{{Msg: &pb.RunRequest_Command{Command: &pb.Command{Device: "devA", Command: "cmdX", Args: []string{"a", "b"}}}}},
			wantNext: findDUTCmd,
			wantCmd:  &pb.Command{Device: "devA", Command: "cmdX", Args: []string{"a", "b"}},
		},
		{
			name:        "receive_error_translated",
			recvErr:     errors.New("network issue"),
			wantErrCode: connect.CodeAborted,
		},
		{
			name:        "first_message_not_command",
			recv:        []*pb.RunRequest{{Msg: &pb.RunRequest_Console{Console: &pb.Console{Data: &pb.Console_Stdout{Stdout: []byte("hi")}}}}},
			wantErrCode: connect.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakes.FakeStream{RecvQueue: tt.recv, RecvErr: tt.recvErr}
			args := runCmdArgs{
				stream:      fake,
				moduleErrCh: make(chan error, 1),
			}

			gotArgs, next, err := receiveCommandRPC(context.Background(), args)

			if tt.wantErrCode != 0 {
				if err == nil {
					t.Fatalf("expected connect error code %v, got nil", tt.wantErrCode)
				}
				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.wantErrCode {
					t.Fatalf("expected connect error code %v, got %v (err=%v)", tt.wantErrCode, cErr.Code(), err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !stateEqual(next, tt.wantNext) {
				t.Fatalf("unexpected next state, want %p got %p", tt.wantNext, next)
			}
			if gotArgs.cmdMsg == nil {
				t.Fatalf("cmdMsg not set")
			}
			if gotArgs.cmdMsg.GetDevice() != tt.wantCmd.GetDevice() || gotArgs.cmdMsg.GetCommand() != tt.wantCmd.GetCommand() {
				t.Fatalf("unexpected command captured: want %v got %v", tt.wantCmd, gotArgs.cmdMsg)
			}
			if len(gotArgs.cmdMsg.GetArgs()) != len(tt.wantCmd.GetArgs()) {
				t.Fatalf("unexpected args length: want %d got %d", len(tt.wantCmd.GetArgs()), len(gotArgs.cmdMsg.GetArgs()))
			}
		})
	}
}

func TestFindDUTCmd(t *testing.T) {
	validCmd := pb.Command{Device: "dev1", Command: "echo"}

	// Helper to build a dut.Devlist with optional command configuration.
	makeDevlist := func(includeCmd bool, cmdModules int, mainCount int) dut.Devlist {
		devs := make(dut.Devlist)
		if includeCmd {
			modules := make([]dut.Module, 0, cmdModules)
			for i := 0; i < cmdModules; i++ {
				m := dut.Module{}
				m.Config.Name = fmt.Sprintf("mod%d", i)
				if i < mainCount { // mark first mainCount modules as main (can create invalid config)
					m.Config.Main = true
				}
				modules = append(modules, m)
			}
			devs[validCmd.GetDevice()] = dut.Device{Cmds: map[string]dut.Command{
				validCmd.GetCommand(): {Modules: modules},
			}}
		} else {
			devs[validCmd.GetDevice()] = dut.Device{Cmds: map[string]dut.Command{}}
		}
		return devs
	}

	tests := []struct {
		name        string
		cmdMsg      *pb.Command
		devs        dut.Devlist
		wantErrCode connect.Code
		wantNext    fsm.State[runCmdArgs]
	}{
		{
			name:        "nil_cmdMsg",
			cmdMsg:      nil,
			devs:        dut.Devlist{},
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:     "success_valid_command",
			cmdMsg:   &validCmd,
			devs:     makeDevlist(true, 1, 1),
			wantNext: executeModules,
		},
		{
			name:        "device_not_found",
			cmdMsg:      &validCmd,
			devs:        dut.Devlist{},
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:        "command_not_found",
			cmdMsg:      &validCmd,
			devs:        makeDevlist(false, 0, 0),
			wantErrCode: connect.CodeInvalidArgument,
		},
		{
			name:        "invalid_command_no_modules",
			cmdMsg:      &validCmd,
			devs:        makeDevlist(true, 0, 0),
			wantErrCode: connect.CodeInternal,
		},
		{
			name:        "invalid_command_multiple_mains",
			cmdMsg:      &validCmd,
			devs:        makeDevlist(true, 2, 2),
			wantErrCode: connect.CodeInternal,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			args := runCmdArgs{
				cmdMsg:      tt.cmdMsg,
				deviceList:  tt.devs,
				moduleErrCh: make(chan error, 1),
			}

			gotArgs, next, err := findDUTCmd(context.Background(), args)

			if tt.wantErrCode != 0 {
				if err == nil {
					t.Fatalf("expected error with code %v, got nil", tt.wantErrCode)
				}
				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.wantErrCode {
					t.Fatalf("expected connect code %v, got %v (err=%v)", tt.wantErrCode, cErr.Code(), err)
				}
				if next != nil {
					t.Fatalf("expected next state nil on error, got %p", next)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !stateEqual(next, executeModules) {
				t.Fatalf("expected next state executeModules, got %p", next)
			}
			if gotArgs.dev.Desc == "" && len(gotArgs.cmd.Modules) == 0 { // simple sanity check device/command captured
				t.Fatalf("expected device and command to be set")
			}
		})
	}
}

// dummyModule is a lightweight test double implementing module.Module behavior needed for executeModules tests.
type dummyModule struct {
	err      error
	runArgs  []string
	runCalls int
}

func (m *dummyModule) Help() string  { return "dummy" }
func (m *dummyModule) Init() error   { return nil }
func (m *dummyModule) Deinit() error { return nil }
func (m *dummyModule) Run(_ context.Context, _ module.Session, args ...string) error { // session unused in these tests
	m.runCalls++
	m.runArgs = append([]string{}, args...) // copy for safety
	return m.err
}

func TestExecuteModules(t *testing.T) {
	type expect struct {
		wantErrCode    connect.Code // error returned by executeModules state itself
		wantNext       fsm.State[runCmdArgs]
		wantModuleErr  bool     // whether we expect a value to arrive on moduleErr channel
		moduleErrIsNil bool     // whether expected moduleErr channel value is nil
		mainArgs       []string // expected args passed to main module
		nonMainArgs    []string // expected args passed to non-main module (if present)
		mainRuns       int
		nonMainRuns    int
	}

	tests := []struct {
		name      string
		preCancel bool
		modules   []dut.Module
		cmdMsg    *pb.Command
		expect    expect
	}{
		{
			name: "success_single_main_module",
			modules: func() []dut.Module {
				m := &dummyModule{}
				wrap := dut.Module{}
				wrap.Config.Name = "mainMod"
				wrap.Config.Main = true
				wrap.Module = m
				return []dut.Module{wrap}
			}(),
			cmdMsg: &pb.Command{Device: "devX", Command: "cmdY", Args: []string{"a", "b"}},
			expect: expect{wantNext: waitModules, wantModuleErr: true, moduleErrIsNil: true, mainArgs: []string{"a", "b"}, mainRuns: 1},
		},
		{
			name: "success_two_modules_main_and_helper",
			modules: func() []dut.Module {
				main := &dummyModule{}
				helper := &dummyModule{}
				w1 := dut.Module{}
				w1.Config.Name = "mainMod"
				w1.Config.Main = true
				w1.Module = main
				w2 := dut.Module{}
				w2.Config.Name = "helperMod"
				w2.Config.Main = false
				w2.Config.Args = []string{"conf1"}
				w2.Module = helper
				return []dut.Module{w1, w2}
			}(),
			cmdMsg: &pb.Command{Device: "devX", Command: "cmdY", Args: []string{"x", "y"}},
			expect: expect{wantNext: waitModules, wantModuleErr: true, moduleErrIsNil: true, mainArgs: []string{"x", "y"}, nonMainArgs: []string{"conf1"}, mainRuns: 1, nonMainRuns: 1},
		},
		{
			name: "module_error_stops_execution",
			modules: func() []dut.Module {
				bad := &dummyModule{err: errors.New("boom")}
				wrap := dut.Module{}
				wrap.Config.Name = "badMain"
				wrap.Config.Main = true
				wrap.Module = bad
				return []dut.Module{wrap}
			}(),
			cmdMsg: &pb.Command{Device: "devX", Command: "cmdY"},
			expect: expect{wantNext: waitModules, wantModuleErr: true, moduleErrIsNil: false, mainRuns: 1},
		},
		{
			name: "second_module_error_stops_execution",
			modules: func() []dut.Module {
				main := &dummyModule{} // succeeds
				failing := &dummyModule{err: errors.New("helper failed")}
				w1 := dut.Module{}
				w1.Config.Name = "mainMod"
				w1.Config.Main = true
				w1.Module = main
				w2 := dut.Module{}
				w2.Config.Name = "helperMod"
				w2.Config.Main = false
				w2.Config.Args = []string{"harg"}
				w2.Module = failing
				return []dut.Module{w1, w2}
			}(),
			cmdMsg: &pb.Command{Device: "devX", Command: "cmdY", Args: []string{"m1", "m2"}},
			expect: expect{wantNext: waitModules, wantModuleErr: true, moduleErrIsNil: false, mainArgs: []string{"m1", "m2"}, nonMainArgs: []string{"harg"}, mainRuns: 1, nonMainRuns: 1},
		},
		{
			name:      "pre_canceled_context_no_module_run",
			preCancel: true,
			modules: func() []dut.Module {
				m := &dummyModule{}
				wrap := dut.Module{}
				wrap.Config.Name = "mainMod"
				wrap.Config.Main = true
				wrap.Module = m
				return []dut.Module{wrap}
			}(),
			cmdMsg: &pb.Command{Device: "devX", Command: "cmdY"},
			expect: expect{wantNext: waitModules, wantModuleErr: false, mainRuns: 0},
		},
	}

	for _, tt := range tests {
		tt := tt
		// Extract underlying dummy modules for later inspection
		var mainDummy, helperDummy *dummyModule
		if len(tt.modules) > 0 {
			if dm, ok := tt.modules[0].Module.(*dummyModule); ok {
				mainDummy = dm
			}
		}
		if len(tt.modules) > 1 {
			if dm, ok := tt.modules[1].Module.(*dummyModule); ok {
				helperDummy = dm
			}
		}

		t.Run(tt.name, func(t *testing.T) {
			// Context setup
			ctx := context.Background()
			if tt.preCancel {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}

			moduleErrCh := make(chan error, 1)

			args := runCmdArgs{
				stream:      &fakes.FakeStream{}, // no actual traffic needed for these tests
				cmdMsg:      tt.cmdMsg,
				cmd:         dut.Command{Modules: tt.modules},
				moduleErrCh: moduleErrCh,
				deviceList:  nil,
			}

			gotArgs, next, err := executeModules(ctx, args)

			if tt.expect.wantErrCode != 0 {
				if err == nil {
					t.Fatalf("expected error code %v, got nil", tt.expect.wantErrCode)
				}
				cErr, ok := err.(*connect.Error)
				if !ok || cErr.Code() != tt.expect.wantErrCode {
					t.Fatalf("expected connect error code %v, got %v (err=%v)", tt.expect.wantErrCode, cErr.Code(), err)
				}
				if next != nil {
					t.Fatalf("expected no next state on error, got %p", next)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error from executeModules: %v", err)
			}

			if !stateEqual(next, tt.expect.wantNext) {
				t.Fatalf("unexpected next state: want %p got %p", tt.expect.wantNext, next)
			}

			// Observe async results (moduleErr) if expected.
			select {
			case me := <-moduleErrCh:
				if !tt.expect.wantModuleErr {
					t.Fatalf("did not expect moduleErr signal, got %v", me)
				}
				if tt.expect.moduleErrIsNil && me != nil {
					t.Fatalf("expected nil moduleErr signal (success), got %v", me)
				}
				if !tt.expect.moduleErrIsNil && me == nil {
					t.Fatalf("expected error moduleErr signal, got nil")
				}
			case <-time.After(150 * time.Millisecond):
				if tt.expect.wantModuleErr {
					t.Fatalf("expected moduleErr signal, timed out")
				}
			}

			// Validate module runs if we have dummy modules
			if mainDummy != nil && mainDummy.runCalls != tt.expect.mainRuns {
				t.Fatalf("main module runCalls mismatch: want %d got %d", tt.expect.mainRuns, mainDummy.runCalls)
			}
			if helperDummy != nil && helperDummy.runCalls != tt.expect.nonMainRuns {
				t.Fatalf("non-main module runCalls mismatch: want %d got %d", tt.expect.nonMainRuns, helperDummy.runCalls)
			}

			if len(tt.expect.mainArgs) > 0 && mainDummy != nil {
				if fmt.Sprint(mainDummy.runArgs) != fmt.Sprint(tt.expect.mainArgs) {
					t.Fatalf("main module args mismatch: want %v got %v", tt.expect.mainArgs, mainDummy.runArgs)
				}
			}
			if len(tt.expect.nonMainArgs) > 0 && helperDummy != nil {
				if fmt.Sprint(helperDummy.runArgs) != fmt.Sprint(tt.expect.nonMainArgs) {
					t.Fatalf("non-main module args mismatch: want %v got %v", tt.expect.nonMainArgs, helperDummy.runArgs)
				}
			}

			// Ensure returned args unchanged for cmd reference
			// Basic sanity: number of modules should remain consistent.
			if len(gotArgs.cmd.Modules) != len(args.cmd.Modules) {
				t.Fatalf("command modules mutated unexpectedly: want %d got %d", len(args.cmd.Modules), len(gotArgs.cmd.Modules))
			}
		})
	}
}

func TestWaitModules(t *testing.T) {
	// Design notes:
	//  - We inject pre-buffered channels (size 1) for moduleErr and brokerErrCh directly into runCmdArgs.
	//    This avoids spinning real broker workers and eliminates timing flakiness.
	//  - Where a second signal is sent "later", we use a goroutine purely to mimic asynchronous arrival;
	//    ordering is NOT strictly enforced and is intentionally irrelevant to assertions:
	//        * Success requires BOTH channels to produce a nil value; order does not matter.
	//        * Any non-nil error (moduleErr => CodeAborted, brokerErr => CodeInternal) causes immediate exit
	//          regardless of whether the other channel has produced a value yet.
	//  - Late failure scenarios (one channel nil then the other non-nil) prove we don't falsely
	//    succeed after a single nil; the loop only returns success when both brokerDone && moduleDone.
	//
	// If stricter ordering were ever required, we could replace the goroutine sends with synchronization
	// primitives (e.g., unbuffered channel + latch) but current semantics render that unnecessary.
	type expect struct {
		wantSuccess bool
		wantErrCode connect.Code
	}

	tests := []struct {
		name  string
		setup func() (context.Context, runCmdArgs)
		exp   expect
	}{
		{
			name: "success_module_then_broker",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				close(moduleErrCh)
				go func() { close(brokerErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantSuccess: true},
		},
		{
			name: "success_broker_then_module",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				close(brokerErrCh)
				go func() { close(moduleErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantSuccess: true},
		},
		{
			name: "module_failure_first",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				moduleErrCh <- errors.New("module exploded")
				close(moduleErrCh)
				// broker later (would be ignored)
				go func() { close(brokerErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantErrCode: connect.CodeAborted},
		},
		{
			name: "broker_failure_first",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				brokerErrCh <- errors.New("broker I/O failed")
				close(brokerErrCh)
				go func() { close(moduleErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantErrCode: connect.CodeInternal},
		},
		{
			name: "module_success_then_broker_failure",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				close(moduleErrCh)
				go func() { brokerErrCh <- errors.New("late broker fail"); close(brokerErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantErrCode: connect.CodeInternal},
		},
		{
			name: "broker_success_then_module_failure",
			setup: func() (context.Context, runCmdArgs) {
				ctx := context.Background()
				moduleErrCh := make(chan error, 1)
				brokerErrCh := make(chan error, 1)
				close(brokerErrCh) // Broker success = channel closure only
				go func() { moduleErrCh <- errors.New("late module fail"); close(moduleErrCh) }()
				args := runCmdArgs{moduleErrCh: moduleErrCh, brokerErrCh: brokerErrCh}
				return ctx, args
			},
			exp: expect{wantErrCode: connect.CodeAborted},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, args := tt.setup()

			// Run waitModules; we ignore returned args for these tests.
			_, next, err := waitModules(ctx, args)

			if tt.exp.wantSuccess {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				if next != nil {
					t.Fatalf("expected no next state, got %p", next)
				}
				return
			}

			if tt.exp.wantErrCode == 0 {
				t.Fatalf("test misconfigured: missing expected error code")
			}
			if err == nil {
				t.Fatalf("expected error code %v, got nil", tt.exp.wantErrCode)
			}
			cErr, ok := err.(*connect.Error)
			if !ok || cErr.Code() != tt.exp.wantErrCode {
				t.Fatalf("expected connect code %v, got %v (err=%v)", tt.exp.wantErrCode, cErr.Code(), err)
			}
			if next != nil {
				t.Fatalf("expected nil next state on error, got %p", next)
			}
		})
	}
}

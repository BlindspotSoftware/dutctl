// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

func newSwitch(t *testing.T, line board.Line, cycle string) *Switch {
	t.Helper()

	sw := &Switch{Line: string(line), Cycle: cycle}
	if err := sw.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return sw
}

func TestSwitchInitRejectsABadOption(t *testing.T) {
	bad := []*Switch{
		{},
		{Line: "reset"},
		{Line: "power", Cycle: "soon"},
		{Line: "power", Cycle: "0s"},
		{Line: "power", Cycle: "-1s"},
	}

	for _, sw := range bad {
		if err := sw.Init(context.Background()); err == nil {
			t.Errorf("Init accepted line %q, cycle %q", sw.Line, sw.Cycle)
		}
	}

	if sw := newSwitch(t, board.Power, ""); sw.cycle != defaultCycle {
		t.Errorf("an unset cycle is %s, want %s", sw.cycle, defaultCycle)
	}
}

func TestSwitchActions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		on      bool // the line's state before
		wantOn  bool
		wantOut string
	}{
		{"status is the default", nil, true, true, "The power line is on.\n"},
		{"status", []string{"status"}, false, false, "The power line is off.\n"},
		{"on", []string{"on"}, false, true, "The power line is now on.\n"},
		{"on again", []string{"on"}, true, true, "The power line is already on.\n"},
		{"off", []string{"off"}, true, false, "The power line is now off.\n"},
		{"toggle on", []string{"toggle"}, false, true, "The power line is now on.\n"},
		{"toggle off", []string{"toggle"}, true, false, "The power line is now off.\n"},
		{"cycle", []string{"cycle"}, true, true, "The power line is now on.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := freshBoard(t)
			b.Switch(board.Power, tt.on)

			s := &mock.Session{}

			err := newSwitch(t, board.Power, "1ms").Run(context.Background(), s, tt.args...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if b.On(board.Power) != tt.wantOn {
				t.Errorf("the line is on: %v, want %v", b.On(board.Power), tt.wantOn)
			}

			if s.PrintText != tt.wantOut {
				t.Errorf("last output %q, want %q", s.PrintText, tt.wantOut)
			}
		})
	}
}

// A switch drives the line it is wired to and no other.
func TestSwitchDrivesItsLine(t *testing.T) {
	b := freshBoard(t)
	s := &mock.Session{}

	if err := newSwitch(t, board.WriteProtect, "").Run(context.Background(), s, "on"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !b.On(board.WriteProtect) || b.On(board.Power) {
		t.Error("the switch did not drive the write-protect line alone")
	}

	if s.PrintText != "The write-protect line is now on.\n" {
		t.Errorf("output %q", s.PrintText)
	}
}

func TestSwitchRejectsBadArguments(t *testing.T) {
	freshBoard(t)

	for _, args := range [][]string{{"sideways"}, {"on", "off"}} {
		if err := newSwitch(t, board.Power, "").Run(context.Background(), &mock.Session{}, args...); err == nil {
			t.Errorf("Run accepted %q", args)
		}
	}
}

// A cancelled cycle returns at once and leaves the line off.
func TestSwitchCycleEndsWithItsContext(t *testing.T) {
	b := freshBoard(t)
	b.Switch(board.Power, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	err := newSwitch(t, board.Power, "1h").Run(ctx, &mock.Session{}, "cycle")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}

	if time.Since(start) > time.Minute {
		t.Error("the cycle waited although its context was done")
	}

	if b.On(board.Power) {
		t.Error("an aborted cycle switched the line on")
	}
}

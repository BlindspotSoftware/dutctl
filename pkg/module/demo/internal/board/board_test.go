// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"errors"
	"strings"
	"testing"
)

// output takes what b sent as a string.
func output(b *Board) string {
	out, _ := b.TakeOutput()

	return string(out)
}

func TestBoardBootsTheImageInItsFlash(t *testing.T) {
	b := New()

	img := Image{Name: "fw.bin", Data: []byte("new firmware")}
	if err := b.Flash(img); err != nil {
		t.Fatalf("flashing an off board: %v", err)
	}

	if !b.Switch(Power, true) {
		t.Fatal("switching an off board on reported no change")
	}

	if b.Switch(Power, true) {
		t.Error("switching an on board on reported a change")
	}

	got := output(b)
	if !strings.Contains(got, "Booting "+img.Summary()) || !strings.HasSuffix(got, Prompt) {
		t.Errorf("boot output %q, want the image's summary and the prompt", got)
	}

	if err := b.Flash(Factory()); !errors.Is(err, ErrPowered) {
		t.Errorf("flashing an on board: %v, want ErrPowered", err)
	}
}

// The write-protect line keeps the flash as it is, and says nothing on the
// serial line.
func TestBoardWriteProtectsItsFlash(t *testing.T) {
	b := New()

	if !b.Switch(WriteProtect, true) || !b.On(WriteProtect) {
		t.Fatal("the write-protect line did not switch on")
	}

	img := Image{Name: "fw.bin", Data: []byte("new firmware")}
	if err := b.Flash(img); !errors.Is(err, ErrWriteProtected) || b.Firmware().Name != Factory().Name {
		t.Errorf("flashing a protected flash: %v, want ErrWriteProtected and the flash unchanged", err)
	}

	b.Switch(WriteProtect, false)

	if err := b.Writable(); err != nil {
		t.Errorf("an off board with an unprotected flash is not writable: %v", err)
	}

	if out := output(b); out != "" || b.On(Power) {
		t.Errorf("the write-protect line sent %q or powered the board", out)
	}
}

func TestBoardWakesAConsoleWhenItSends(t *testing.T) {
	b := New()

	_, changed := b.TakeOutput()

	select {
	case <-changed:
		t.Fatal("changed was closed before the board sent anything")
	default:
	}

	b.Switch(Power, true)

	select {
	case <-changed:
	default:
		t.Fatal("changed was not closed when the board booted")
	}
}

func TestBoardShell(t *testing.T) {
	tests := []struct {
		name   string
		keys   string
		want   string
		logout bool
	}{
		{"echo and run", "echo hi\r", "echo hi\r\nhi\r\n" + Prompt, false},
		{"LF ends a line", "echo hi\n", "echo hi\r\nhi\r\n" + Prompt, false},
		{"CR LF ends one line", "echo hi\r\n", "echo hi\r\nhi\r\n" + Prompt, false},
		{"empty line", "\r", "\r\n" + Prompt, false},
		{"backspace edits", "ecx\x7fho a\r", "ecx\b \bho a\r\na\r\n" + Prompt, false},
		{"backspace on an empty line", "\x08", "", false},
		{"Ctrl-C drops the line", "abc\x03", "abc^C\r\n" + Prompt, false},
		{"other control keys are ignored", "\x1b[A\r", "[A\r\n[A: command not found\r\n" + Prompt, false},
		{"unknown command", "nope\r", "nope\r\nnope: command not found\r\n" + Prompt, false},
		{"version", "version\r", "version\r\n" + Factory().Summary() + "\r\n" + Prompt, false},
		{"exit logs out and drops the rest", "exit\rhelp\r", "exit\r\nlogout\r\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			b.Switch(Power, true)
			output(b)

			if logout := b.TypeIn([]byte(tt.keys)); logout != tt.logout {
				t.Errorf("TypeIn reported logout %v, want %v", logout, tt.logout)
			}

			if got := output(b); got != tt.want {
				t.Errorf("the board sent %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBoardIgnoresKeysWhileOff(t *testing.T) {
	b := New()

	if b.TypeIn([]byte("exit\r")) {
		t.Error("an off board logged out")
	}

	if got := output(b); got != "" {
		t.Errorf("an off board sent %q", got)
	}
}

func TestBoardKeepsTheEndOfALongBacklog(t *testing.T) {
	b := New()
	b.Switch(Power, true)

	line := []byte("echo " + strings.Repeat("x", 100) + "\r")
	for range uartBacklog / len(line) {
		b.TypeIn(line)
	}

	b.TypeIn([]byte("\x03"))

	got := output(b)
	if len(got) != uartBacklog || !strings.HasSuffix(got, "^C\r\n"+Prompt) {
		t.Errorf("backlog of %d bytes ending in %q, want %d bytes ending in the prompt", len(got), got[len(got)-10:], uartBacklog)
	}
}

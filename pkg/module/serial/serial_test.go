// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
)

// recordingSession is a module.Session that accumulates the client-facing text,
// so tests can assert exactly what was forwarded.
type recordingSession struct {
	mock.Session
	out strings.Builder
}

func (r *recordingSession) Print(a ...any)            { r.out.WriteString(fmt.Sprint(a...)) }
func (r *recordingSession) Printf(f string, a ...any) { r.out.WriteString(fmt.Sprintf(f, a...)) }
func (r *recordingSession) Println(a ...any)          { r.out.WriteString(fmt.Sprintln(a...)) }

// newSerialWithPort builds a Serial whose port opener returns fp, so tests run
// against a fake port instead of real hardware. The post-send drain is
// shortened so send-ending sequences don't make the suite wait a full second.
func newSerialWithPort(fp *fakePort) *Serial {
	return &Serial{
		Port:         "/dev/fake",
		Baud:         115200,
		drainTimeout: 10 * time.Millisecond,
		open:         func(_ string, _ int) (port, error) { return fp, nil },
	}
}

func TestSerialRunScenarios(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		reads       [][]byte
		wantErr     bool
		wantWritten string
	}{
		{
			name:        "send wait send",
			args:        []string{"-t", "1s", "--", "send", "foo", "expect", "bar", "send", "foobar"},
			reads:       [][]byte{[]byte("xx bar yy")},
			wantWritten: "foo\rfoobar\r",
		},
		{
			name:  "expect only",
			args:  []string{"--", "expect", "foo"},
			reads: [][]byte{[]byte("...foo...")},
		},
		{
			name:        "expect send expect",
			args:        []string{"--", "expect", "foo", "send", "bar", "expect", "1"},
			reads:       [][]byte{[]byte("foo"), []byte("1")},
			wantWritten: "bar\r",
		},
		{
			name:        "send send",
			args:        []string{"--", "send", "foo", "send", "bar"},
			reads:       nil,
			wantWritten: "foo\rbar\r",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &fakePort{reads: tt.reads} // readErr nil => quiet port after exhaustion
			s := newSerialWithPort(fp)

			err := s.Run(context.Background(), &mock.Session{}, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Run err = %v, wantErr = %v", err, tt.wantErr)
			}

			if got := string(fp.written); got != tt.wantWritten {
				t.Errorf("written = %q, want %q", got, tt.wantWritten)
			}

			if !fp.closed {
				t.Error("port was not closed")
			}

			if fp.resetCount != 1 {
				t.Errorf("ResetInputBuffer called %d times, want 1", fp.resetCount)
			}
		})
	}
}

func TestSerialRunTimeoutNamesStep(t *testing.T) {
	fp := &fakePort{reads: [][]byte{[]byte("boot log without the marker")}} // then quiet
	s := newSerialWithPort(fp)

	err := s.Run(context.Background(), &mock.Session{},
		"-t", "30ms", "--", "send", "go", "expect", "PROMPT")
	if err == nil {
		t.Fatal("Run = nil error, want timeout error")
	}

	// The failing step is the 2nd one (expect PROMPT) and the error must name it.
	if !strings.Contains(err.Error(), "step 2") || !strings.Contains(err.Error(), "PROMPT") {
		t.Errorf("error %q should identify step 2 and the pattern PROMPT", err)
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error %q should mention timeout", err)
	}
}

func TestSerialRunBadArgs(t *testing.T) {
	s := newSerialWithPort(&fakePort{})

	if err := s.Run(context.Background(), &mock.Session{}, "--", "bogus", "x"); err == nil {
		t.Error("Run with unknown verb = nil error, want error")
	}
}

func TestSerialInit(t *testing.T) {
	t.Run("missing port", func(t *testing.T) {
		s := &Serial{}
		if err := s.Init(); err == nil {
			t.Error("Init with empty Port = nil error, want error")
		}
	})

	t.Run("defaults baud", func(t *testing.T) {
		s := &Serial{Port: "/dev/fake"}
		if err := s.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}

		if s.Baud != DefaultBaudRate {
			t.Errorf("Baud = %d, want default %d", s.Baud, DefaultBaudRate)
		}
	})
}

func TestSerialInitDelay(t *testing.T) {
	tests := []struct {
		name    string
		delay   string
		want    time.Duration
		wantErr bool
	}{
		{"default when unset", "", defaultDelay, false},
		{"explicit", "200ms", 200 * time.Millisecond, false},
		{"zero disables", "0s", 0, false},
		{"invalid", "fast", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Serial{Port: "/dev/fake", Delay: tt.delay}

			err := s.Init()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Init err = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && s.delay != tt.want {
				t.Errorf("delay = %v, want %v", s.delay, tt.want)
			}
		})
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), 0); err != nil {
		t.Errorf("sleepCtx(0) = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepCtx(cancelled) = %v, want context.Canceled", err)
	}
}

func TestSerialRunMonitor(t *testing.T) {
	fp := &fakePort{reads: [][]byte{[]byte("watching...\n")}} // then quiet
	s := newSerialWithPort(fp)
	sess := &mock.Session{}

	// No step args => monitor mode; a short -t ends the stream with success.
	err := s.Run(context.Background(), sess, "-t", "30ms")
	if err != nil {
		t.Fatalf("monitor Run = %v, want nil", err)
	}

	if !fp.closed {
		t.Error("port was not closed")
	}

	if sess.PrintText != "--- Connection closed ---\n" {
		t.Errorf("last client message = %q, want the connection-closed banner", sess.PrintText)
	}
}

func TestSerialRunTrailingSendDrain(t *testing.T) {
	// Sequence ends on a send; the DUT's reply arrives during the drain.
	fp := &fakePort{reads: [][]byte{[]byte("Rebooting now...\n")}}
	s := newSerialWithPort(fp) // 10ms drain

	err := s.Run(context.Background(), &mock.Session{}, "--", "send", "reboot")
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if got, want := string(fp.written), "reboot\r"; got != want {
		t.Errorf("written = %q, want %q", got, want)
	}

	if len(fp.reads) != 0 {
		t.Errorf("drain did not consume the queued reply; %d reads left", len(fp.reads))
	}
}

func TestSerialRunFiltersEscapes(t *testing.T) {
	// A colour reset splits the prompt ("#\x1b[0m "); filtering (the default)
	// strips it so "# " is contiguous and the expect matches.
	fp := &fakePort{reads: [][]byte{[]byte("#\x1b[0m ")}}
	s := newSerialWithPort(fp)

	err := s.Run(context.Background(), &mock.Session{}, "-t", "1s", "--", "expect", "# ")
	if err != nil {
		t.Fatalf("Run = %v, want nil (escapes stripped so '# ' matches)", err)
	}
}

func TestSerialRunKeepEscapes(t *testing.T) {
	// With -keep-escapes the colour reset is kept, so "# " is not contiguous and
	// the expect cannot match before the timeout.
	fp := &fakePort{reads: [][]byte{[]byte("#\x1b[0m ")}} // then quiet
	s := newSerialWithPort(fp)

	err := s.Run(context.Background(), &mock.Session{}, "-keep-escapes", "-t", "30ms", "--", "expect", "# ")
	if err == nil {
		t.Fatal("Run = nil, want timeout (-keep-escapes keeps the escape so '# ' is not contiguous)")
	}
}

func TestClientWriterMarkersOnOwnLine(t *testing.T) {
	rec := &recordingSession{}
	cw := newClientWriter(rec)

	_, _ = cw.Write([]byte("login: ")) // prompt, no trailing newline
	cw.markerf("--- [1/2] matched %q ---\n", "login:")
	cw.markerf("--- [2/2] sent %q ---\n", "root") // already at line start: no blank line
	_, _ = cw.Write([]byte("output\n"))           // ends with a newline
	cw.markerf("--- done ---\n")

	want := "login: \n" +
		"--- [1/2] matched \"login:\" ---\n" +
		"--- [2/2] sent \"root\" ---\n" +
		"output\n" +
		"--- done ---\n"

	if got := rec.out.String(); got != want {
		t.Errorf("client output:\n got: %q\nwant: %q", got, want)
	}
}

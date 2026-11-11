// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BlindspotSoftware/dutctl/internal/test/mock"
	"github.com/BlindspotSoftware/dutctl/pkg/module/demo/internal/board"
)

func TestFlashWritesAndReadsBack(t *testing.T) {
	b := freshBoard(t)

	s := &mock.Session{RequestedFileResponse: strings.NewReader("new firmware")}

	if err := (&Flash{}).Run(context.Background(), s, "write", "images/fw.bin"); err != nil {
		t.Fatalf("write: %v", err)
	}

	if s.RequestedFileName != "images/fw.bin" {
		t.Errorf("requested %q, want the name as given", s.RequestedFileName)
	}

	if got := b.Firmware(); got.Name != "fw.bin" || string(got.Data) != "new firmware" {
		t.Errorf("the flash holds %q: %q", got.Name, got.Data)
	}

	if err := (&Flash{}).Run(context.Background(), s, "read", "copy.bin"); err != nil {
		t.Fatalf("read: %v", err)
	}

	if s.SentFileName != "copy.bin" || string(s.SentFileContent) != "new firmware" {
		t.Errorf("sent %q: %q, want the flash as copy.bin", s.SentFileName, s.SentFileContent)
	}
}

func TestFlashRefusesToWrite(t *testing.T) {
	declined := errors.New("no such file")

	tests := []struct {
		name    string
		on      board.Line // a line switched on before, if any
		session *mock.Session
		want    string
	}{
		{"while the board is on", board.Power, &mock.Session{}, "power it off"},
		{"while the flash is write-protected", board.WriteProtect, &mock.Session{}, "write-protected"},
		{"a declined file", "", &mock.Session{RequestFileErr: declined}, "no such file"},
		{
			"an image too big",
			"",
			&mock.Session{RequestedFileResponse: bytes.NewReader(make([]byte, board.FlashSize+1))},
			"does not fit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := freshBoard(t)
			if tt.on != "" {
				b.Switch(tt.on, true)
			}

			err := (&Flash{}).Run(context.Background(), tt.session, "write", "fw.bin")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("write returned %v, want an error about %q", err, tt.want)
			}

			if got := b.Firmware(); got.Name != board.Factory().Name {
				t.Errorf("the flash holds %q after a refused write", got.Name)
			}
		})
	}
}

func TestFlashRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"write"}, {"erase", "x"}, {"read", "a", "b"}} {
		if err := (&Flash{}).Run(context.Background(), &mock.Session{}, args...); err == nil {
			t.Errorf("Run accepted %q", args)
		}
	}
}

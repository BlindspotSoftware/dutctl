// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestParseLockDuration(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{
			name: "no args uses default",
			args: nil,
			want: defaultLockDuration,
		},
		{
			name: "empty arg uses default",
			args: []string{""},
			want: defaultLockDuration,
		},
		{
			name: "explicit minutes",
			args: []string{"5m"},
			want: 5 * time.Minute,
		},
		{
			name: "explicit compound duration",
			args: []string{"1h30m"},
			want: 90 * time.Minute,
		},
		{
			name:    "unparseable duration",
			args:    []string{"banana"},
			wantErr: true,
		},
		{
			name:    "zero duration rejected",
			args:    []string{"0s"},
			wantErr: true,
		},
		{
			name:    "negative duration rejected",
			args:    []string{"-5m"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLockDuration(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got duration %v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}

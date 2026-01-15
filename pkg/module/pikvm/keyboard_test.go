package pikvm

import (
	"reflect"
	"testing"
	"time"
)

func TestParseKeyboardFlags(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		args          []string
		wantDelay     time.Duration
		wantRemaining []string
		wantErr       bool
	}{
		{
			name:          "default",
			args:          []string{"key", "F12"},
			wantDelay:     defaultKeyDelay,
			wantRemaining: []string{"key", "F12"},
		},
		{
			name:          "delay equals",
			args:          []string{"--delay=25ms", "key-combo", "Ctrl+Alt+Delete"},
			wantDelay:     25 * time.Millisecond,
			wantRemaining: []string{"key-combo", "Ctrl+Alt+Delete"},
		},
		{
			name:          "delay separate",
			args:          []string{"--delay", "30ms", "key", "Enter"},
			wantDelay:     30 * time.Millisecond,
			wantRemaining: []string{"key", "Enter"},
		},
		{
			name:    "invalid delay",
			args:    []string{"--delay", "nope", "key", "Enter"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"--wat", "key", "Enter"},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			delay, remaining, err := parseKeyboardFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if delay != tc.wantDelay {
				t.Fatalf("delay=%v, want %v", delay, tc.wantDelay)
			}
			if !reflect.DeepEqual(remaining, tc.wantRemaining) {
				t.Fatalf("remaining=%#v, want %#v", remaining, tc.wantRemaining)
			}
		})
	}
}

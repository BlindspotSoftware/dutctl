// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serial

import (
	"testing"
)

func TestParseCharMap(t *testing.T) {
	tests := []struct {
		name    string
		names   []string
		want    charMap
		wantErr bool
	}{
		{"empty", nil, charMap{}, false},
		{"one", []string{"lfcrlf"}, charMap{lfcrlf: true}, false},
		{"all", []string{"crlf", "crcrlf", "igncr", "lfcr", "lfcrlf", "ignlf", "bsdel", "delbs"},
			charMap{crlf: true, crcrlf: true, igncr: true, lfcr: true, lfcrlf: true, ignlf: true, bsdel: true, delbs: true}, false},
		{"any case", []string{"LfCrLf"}, charMap{lfcrlf: true}, false},
		{"unknown", []string{"inlcrnl"}, charMap{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCharMap(tt.names)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCharMap(%q) err = %v, wantErr %v", tt.names, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("parseCharMap(%q) = %+v, want %+v", tt.names, got, tt.want)
			}
		})
	}
}

// Every map on one read, and the two that add a byte on reads that split a
// CR LF pair, so the state carried across reads is exercised. A trailing
// flush stands for a pause in the input.
func TestInboundMapChars(t *testing.T) {
	tests := []struct {
		name  string
		maps  charMap
		reads []string
		want  []string // what each read yields; the last entry is the flush
	}{
		{"no map passes through", charMap{}, []string{"a\r\nb\n\rc"}, []string{"a\r\nb\n\rc", ""}},
		{"lfcrlf adds a CR to a bare LF", charMap{lfcrlf: true}, []string{"a\nb\n"}, []string{"a\r\nb\r\n", ""}},
		{"lfcrlf keeps a CR LF pair", charMap{lfcrlf: true}, []string{"a\r\nb"}, []string{"a\r\nb", ""}},
		{"lfcrlf keeps a pair split across reads", charMap{lfcrlf: true}, []string{"a\r", "\nb"}, []string{"a\r", "\nb", ""}},
		{"lfcrlf after a CR with text between", charMap{lfcrlf: true}, []string{"a\rb\n"}, []string{"a\rb\r\n", ""}},
		{"crcrlf adds a LF to a bare CR", charMap{crcrlf: true}, []string{"a\rb"}, []string{"a\r\nb", ""}},
		{"crcrlf keeps a CR LF pair", charMap{crcrlf: true}, []string{"a\r\nb"}, []string{"a\r\nb", ""}},
		{"crcrlf holds a trailing CR for the LF", charMap{crcrlf: true}, []string{"a\r", "\nb"}, []string{"a", "\r\nb", ""}},
		{"crcrlf holds a trailing CR for a byte", charMap{crcrlf: true}, []string{"a\r", "b"}, []string{"a", "\r\nb", ""}},
		{"crcrlf releases a held CR on a pause", charMap{crcrlf: true}, []string{"a\r"}, []string{"a", "\r\n"}},
		{"crcrlf holds a CR for the next CR", charMap{crcrlf: true}, []string{"a\r", "\r"}, []string{"a", "\r\n", "\r\n"}},
		{"both adders on mixed line ends", charMap{crcrlf: true, lfcrlf: true}, []string{"a\rb\nc\r\nd"}, []string{"a\r\nb\r\nc\r\nd", ""}},
		{"crlf", charMap{crlf: true}, []string{"a\r\nb"}, []string{"a\n\nb", ""}},
		{"lfcr", charMap{lfcr: true}, []string{"a\r\nb"}, []string{"a\r\rb", ""}},
		{"igncr", charMap{igncr: true}, []string{"a\r\nb"}, []string{"a\nb", ""}},
		{"ignlf", charMap{ignlf: true}, []string{"a\r\nb"}, []string{"a\rb", ""}},
		{"igncr with lfcrlf", charMap{igncr: true, lfcrlf: true}, []string{"a\r\nb"}, []string{"a\r\nb", ""}},
		{"bsdel", charMap{bsdel: true}, []string{"a\bb\x7f"}, []string{"a\x7fb\x7f", ""}},
		{"delbs", charMap{delbs: true}, []string{"a\bb\x7f"}, []string{"a\bb\b", ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInbound(tt.maps, false)

			for idx, read := range tt.reads {
				if got := string(in.apply([]byte(read))); got != tt.want[idx] {
					t.Errorf("read %d %q -> %q, want %q", idx, read, got, tt.want[idx])
				}
			}

			if got := string(in.flush()); got != tt.want[len(tt.reads)] {
				t.Errorf("flush -> %q, want %q", got, tt.want[len(tt.reads)])
			}
		})
	}
}

// The map runs before the escape filter, and neither disturbs the other.
func TestInboundMapsThenStrips(t *testing.T) {
	in := newInbound(charMap{lfcrlf: true}, true)

	got := string(in.apply([]byte("\x1b[32mok\x1b[0m\nnext\x1b[")))
	got += string(in.apply([]byte("1m!\n")))

	if want := "ok\r\nnext!\r\n"; got != want {
		t.Errorf("mapped and stripped = %q, want %q", got, want)
	}
}

// What a pause releases takes the same way as read bytes: through the escape
// filter, so a sequence pending there ends on the released CR LF as a
// terminal would end it, and a pending control string swallows it.
func TestInboundFlushGoesThroughTheFilter(t *testing.T) {
	in := newInbound(charMap{crcrlf: true}, true)

	steps := []struct {
		read string // "" stands for a pause
		want string
	}{
		{"\x1b[\r", ""},      // a CSI pending, the CR held back
		{"", "\r\n"},         // the pause: the CSI ends on the CR, which comes out as CR LF
		{"login:", "login:"}, // nothing of it is taken for the CSI's final byte
		{"\x1b]0;t\r", ""},   // an OSC pending, the CR held back
		{"", ""},             // the pause: CR LF inside the control string is swallowed
		{"itle\x07x", "x"},   // the OSC ends at its BEL
	}

	for idx, st := range steps {
		var got []byte
		if st.read == "" {
			got = in.flush()
		} else {
			got = in.apply([]byte(st.read))
		}

		if string(got) != st.want {
			t.Errorf("step %d (%q) -> %q, want %q", idx, st.read, got, st.want)
		}
	}
}

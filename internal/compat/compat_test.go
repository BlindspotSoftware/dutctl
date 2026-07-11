// Copyright 2025 Blindspot Software
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package compat

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		name        string
		self, peer  string
		wantVerdict Verdict
		wantField   Field
		wantValid   bool
	}{
		{"identical", "1.0.0-alpha.1", "1.0.0-alpha.1", Compatible, Equal, true},
		{"identical with v", "v1.2.3", "v1.2.3", Compatible, Equal, true},
		{"patch differs", "1.0.0", "1.0.1", Compatible, Patch, true},
		{"minor differs", "1.1.0", "1.0.0", Tolerated, Minor, true},
		{"major differs", "2.0.0", "1.9.9", Incompatible, Major, true},
		{"prerelease differs", "1.0.0-alpha.1", "1.0.0-alpha.5", Tolerated, Prerelease, true},
		{"self dev", "devel", "1.0.0", Tolerated, Equal, false},
		{"peer empty", "1.0.0", "", Tolerated, Equal, false},
		{"both garbage", "nope", "also-nope", Tolerated, Equal, false},
		{"git describe ahead of tag", "v1.0.0-alpha.1-5-gabc123", "v1.0.0-alpha.1", Tolerated, Prerelease, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Compare(tc.self, tc.peer)
			if got.Verdict != tc.wantVerdict {
				t.Errorf("Compare(%q, %q).Verdict = %v, want %v", tc.self, tc.peer, got.Verdict, tc.wantVerdict)
			}

			if got.Field != tc.wantField {
				t.Errorf("Compare(%q, %q).Field = %v, want %v", tc.self, tc.peer, got.Field, tc.wantField)
			}

			if got.Valid != tc.wantValid {
				t.Errorf("Compare(%q, %q).Valid = %v, want %v", tc.self, tc.peer, got.Valid, tc.wantValid)
			}
		})
	}
}

// TestCompareInvalidNeverMajor guards the dutserver invariant: unparsable input
// must never surface Field==Major (dutserver panics on a major gap and ignores Valid).
func TestCompareInvalidNeverMajor(t *testing.T) {
	for _, tc := range []struct{ self, peer string }{
		{"", "1.0.0"},
		{"1.0.0", ""},
		{"garbage", "also-garbage"},
	} {
		got := Compare(tc.self, tc.peer)
		if got.Valid {
			t.Fatalf("Compare(%q, %q).Valid = true, want false", tc.self, tc.peer)
		}

		if got.Field == Major {
			t.Errorf("Compare(%q, %q).Field = Major on invalid input; must stay Equal", tc.self, tc.peer)
		}
	}
}

func TestCompareCmp(t *testing.T) {
	if c := Compare("1.2.0", "1.5.0").Cmp; c >= 0 {
		t.Errorf("Cmp(self older) = %d, want < 0", c)
	}

	if c := Compare("1.5.0", "1.2.0").Cmp; c <= 0 {
		t.Errorf("Cmp(self newer) = %d, want > 0", c)
	}
}

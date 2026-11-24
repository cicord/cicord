package storeutil

import (
	"math"
	"testing"
	"time"
)

func TestPrefixEnd(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		ok      bool
	}{
		{"", "", false},
		{"a", "b", true},
		{"ab", "ac", true},
		{"a\xff", "b", true},
		{"\xff", "", false},
		{"\xff\xff", "", false},
		{"a\xff\xff", "b", true},
		{"\x00", "\x01", true},
		{"a\x00", "a\x01", true},
	} {
		out, ok := PrefixEnd(tc.in)
		if out != tc.out || ok != tc.ok {
			t.Errorf("PrefixEnd(%q) = %q, %v; want %q, %v", tc.in, out, ok, tc.out, tc.ok)
		}
	}
}

func TestDueCutoff(t *testing.T) {
	if _, ok := DueCutoff(time.Time{}); ok {
		t.Error("zero time is before the range but ok")
	}
	if c, ok := DueCutoff(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)); !ok || c != math.MaxInt64 {
		t.Errorf("year 3000 cutoff = %d, %v", c, ok)
	}
	now := time.Now()
	if c, ok := DueCutoff(now); !ok || c != now.UnixNano() {
		t.Errorf("now cutoff = %d, %v", c, ok)
	}
}

func TestAddNumber(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		missing bool
		by      float64
		want    string
		err     bool
	}{
		{missing: true, by: 1, want: "1"},
		{raw: "41", by: 1, want: "42"},
		{raw: "1.5", by: -2, want: "-0.5"},
		{raw: `"5"`, by: 1, err: true},
		{raw: `null`, by: 1, err: true},
		{raw: `1.7e308`, by: 1.7e308, err: true},
	} {
		var raw []byte
		if !tc.missing {
			raw = []byte(tc.raw)
		}
		_, enc, err := AddNumber(raw, tc.by)
		if (err != nil) != tc.err || (err == nil && string(enc) != tc.want) {
			t.Errorf("AddNumber(%q, %v) = %s, %v", tc.raw, tc.by, enc, err)
		}
	}
}

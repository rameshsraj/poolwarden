package model

import "testing"

func TestParseLSN(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"0/0", 0, false},
		{"3/A1B2C3D4", 3<<32 | 0xA1B2C3D4, false},
		{"FF/FFFFFFFF", 0xFF<<32 | 0xFFFFFFFF, false},
		{"1/10", 1<<32 | 0x10, false},
		{"", 0, true},
		{"nope", 0, true},
		{"3/", 0, true},
		{"/ABC", 0, true},
		{"3/ZZZ", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseLSN(tt.in)
		if tt.err {
			if err == nil {
				t.Errorf("%q: want error", tt.in)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%q = %d,%v want %d", tt.in, got, err, tt.want)
		}
	}
}

func TestLSNDiff(t *testing.T) {
	if d := LSNDiff("0/2000", "0/1000"); d != 0x1000 {
		t.Errorf("diff %d", d)
	}
	if d := LSNDiff("1/0", "0/FFFFFFFF"); d != 1 {
		t.Errorf("diff %d", d)
	}
	if d := LSNDiff("bad", "0/10"); d != -1 {
		t.Errorf("want -1, got %d", d)
	}
}

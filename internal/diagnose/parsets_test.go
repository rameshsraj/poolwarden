package diagnose

import (
	"testing"
	"time"
)

func TestParseTS(t *testing.T) {
	t0 := parseTS("2026-11-05 12:03:01")
	if t0.IsZero() {
		t.Fatal("naive")
	}
	want := time.Date(2026, 11, 5, 12, 3, 1, 0, time.Local)
	if !t0.Equal(want) {
		t.Errorf("naive should be local: %v want %v", t0, want)
	}
	z := parseTS("2026-11-05 12:03:01.123 +01")
	if z.IsZero() || z.UTC().Hour() != 11 {
		t.Errorf("numeric zone: %v", z)
	}
	z2 := parseTS("2026-11-05T12:03:01+02:00")
	if z2.IsZero() || z2.UTC().Hour() != 10 {
		t.Errorf("rfc3339: %v", z2)
	}
	if parseTS("garbage").IsZero() == false {
		t.Error("garbage")
	}
}

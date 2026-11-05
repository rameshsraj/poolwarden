package render

import (
	"strings"
	"testing"
	"time"
)

func TestMapKey(t *testing.T) {
	cases := []struct {
		k    byte
		kind string
	}{
		{'q', "quit"}, {'p', "pause"}, {'+', "interval"}, {'-', "interval"},
		{'1', "view"}, {'5', "view"}, {'x', ""},
	}
	for _, c := range cases {
		a := MapKey(c.k, 1)
		if c.kind == "" {
			if a != nil {
				t.Errorf("%c → %+v", c.k, a)
			}
			continue
		}
		if a == nil || a.Kind != c.kind {
			t.Errorf("%c → %+v", c.k, a)
		}
	}
	if MapKey('+', 1).Arg != 0.5 || MapKey('-', 1).Arg != 2 {
		t.Error("factors")
	}
}

func TestClampInterval(t *testing.T) {
	if ClampInterval(time.Second, 0.5) != time.Second {
		t.Error("min")
	}
	if ClampInterval(60*time.Second, 2) != 60*time.Second {
		t.Error("max")
	}
	if ClampInterval(4*time.Second, 2) != 8*time.Second {
		t.Error("double")
	}
}

func TestFrame(t *testing.T) {
	f := Frame("status", 2*time.Second, true,
		time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "BODY\n")
	if !strings.Contains(f, "PAUSED") || !strings.Contains(f, "watch status") ||
		!strings.Contains(f, "every 2s") || !strings.HasSuffix(f, "BODY\n") {
		t.Fatalf("%q", f)
	}
}

package forecast

import "testing"

func TestScore(t *testing.T) {
	tests := []struct {
		down       bool
		crit, warn int
		rising     bool
		want       int
	}{
		{false, 0, 0, false, 100},
		{true, 0, 0, false, 50},
		{false, 1, 0, false, 70},
		{false, 0, 2, false, 90},
		{false, 0, 0, true, 90},
		{true, 1, 1, true, 0},
		{true, 1, 0, false, 20},
	}
	for i, tt := range tests {
		if got := Score(tt.down, tt.crit, tt.warn, tt.rising); got != tt.want {
			t.Errorf("case %d: %d, want %d", i, got, tt.want)
		}
	}
}

package render

import "testing"

func TestSparkline(t *testing.T) {
	if got := Sparkline(nil, 10); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := Sparkline([]float64{5, 5, 5}, 10); got != "▁▁▁" {
		t.Errorf("constant: %q", got)
	}
	if got := Sparkline([]float64{0, 1, 2, 3, 4, 5, 6, 7}, 8); got != "▁▂▃▄▅▆▇█" {
		t.Errorf("ramp: %q", got)
	}
	got := Sparkline([]float64{0, 0, 10, 10, 5, 5, 1, 1}, 4)
	if len([]rune(got)) != 4 {
		t.Errorf("downsample width: %q", got)
	}
}

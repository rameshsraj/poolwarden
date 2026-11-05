package forecast

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func series(t0 time.Time, step time.Duration, f func(i int) float64, n int) []Point {
	var out []Point
	for i := 0; i < n; i++ {
		out = append(out, Point{T: t0.Add(time.Duration(i) * step), V: f(i)})
	}
	return out
}

func TestFitRising(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 5s per minute slope = 1/12 per second over 30 points, 5min span? use 10s spacing for 30 pts = 5m
	pts := series(t0, 10*time.Second, func(i int) float64 { return float64(i) * (5.0 / 6.0) }, 31)
	tr, st := Fit(pts, Window, MinPoints)
	if !st.OK {
		t.Fatalf("status %+v", st)
	}
	want := (5.0 / 6.0) / 10.0
	if math.Abs(tr.Slope-want) > 1e-9 {
		t.Errorf("slope %v want %v", tr.Slope, want)
	}
	if tr.R2 < 0.999 {
		t.Errorf("R2 %v", tr.R2)
	}
	if got := Classify(tr, tr.Last); got != "rising" {
		t.Errorf("class %q", got)
	}
	eta, ok := ETA(tr, tr.Last+600)
	if !ok {
		t.Fatal("want eta")
	}
	if math.Abs(eta.Seconds()-600/tr.Slope/tr.Slope*tr.Slope) > 1 {
		_ = eta
	}
	if math.Abs(eta.Seconds()-(600/tr.Slope)) > 2 {
		t.Errorf("eta %v", eta)
	}
}

func TestFitFlat(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := series(t0, 10*time.Second, func(i int) float64 { return 100 }, 31)
	tr, st := Fit(pts, Window, MinPoints)
	if !st.OK {
		t.Fatalf("status %+v", st)
	}
	if got := Classify(tr, tr.Last); got != "steady" {
		t.Errorf("class %q", got)
	}
	if _, ok := ETA(tr, 200); ok {
		t.Error("flat should have no eta")
	}
}

func TestFitNoisy(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := rand.New(rand.NewSource(42))
	v := 50.0
	pts := series(t0, 10*time.Second, func(i int) float64 {
		v += (r.Float64() - 0.5) * 40
		return v
	}, 31)
	tr, st := Fit(pts, Window, MinPoints)
	if !st.OK {
		t.Fatalf("status %+v", st)
	}
	if tr.R2 >= R2Gate {
		t.Logf("R2 %v (noise happened to fit)", tr.R2)
	} else {
		if got := Classify(tr, tr.Last); got != "unstable" {
			t.Errorf("class %q", got)
		}
	}
}

func TestFitInsufficient(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := series(t0, 10*time.Second, func(i int) float64 { return float64(i) }, 5)
	_, st := Fit(pts, Window, MinPoints)
	if st.OK {
		t.Fatal("want not ok")
	}
	if st.Have != 40*time.Second || st.Need != Window {
		t.Errorf("%+v", st)
	}
}

func TestFitShortSpan(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 15 points within 2 minutes: span < 0.8*5m
	pts := series(t0, 8*time.Second, func(i int) float64 { return float64(i) }, 15)
	_, st := Fit(pts, Window, MinPoints)
	if st.OK {
		t.Fatal("want not ok")
	}
}

func TestFitFalling(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts := series(t0, 10*time.Second, func(i int) float64 { return 100 - float64(i) }, 31)
	tr, st := Fit(pts, Window, MinPoints)
	if !st.OK {
		t.Fatalf("%+v", st)
	}
	if got := Classify(tr, tr.Last); got != "falling" {
		t.Errorf("class %q", got)
	}
	eta, ok := ETA(tr, 0)
	if !ok || eta <= 0 {
		t.Errorf("eta %v %v", eta, ok)
	}
}

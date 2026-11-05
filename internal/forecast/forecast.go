// Package forecast holds the trend/ETA and health-score math (design 01 §13).
package forecast

import "time"

// Score is a node's health score: starts at 100, minus 50 when the node is
// down or unreachable, minus 30 for any crit finding, minus 10 for any warn
// finding, minus 10 when the lag trend is rising. Floor is 0.
func Score(down bool, crit, warn int, lagRising bool) int {
	s := 100
	if down {
		s -= 50
	}
	if crit > 0 {
		s -= 30
	}
	if warn > 0 {
		s -= 10
	}
	if lagRising {
		s -= 10
	}
	if s < 0 {
		return 0
	}
	return s
}

// Window is the regression window for trends and ETAs (01 §13).
const Window = 5 * time.Minute

// MinPoints is the minimum samples needed inside the window.
const MinPoints = 10

// R2Gate is the goodness-of-fit gate for classes and ETAs.
const R2Gate = 0.6

// Point is one (time, value) observation.
type Point struct {
	T time.Time
	V float64
}

// Trend is an OLS fit over the window.
type Trend struct {
	N         int
	Span      time.Duration
	Slope     float64 // units per second
	Intercept float64
	R2        float64
	Last      float64
	Class     string
}

// Status reports why a Fit failed.
type Status struct {
	Have time.Duration
	Need time.Duration
	OK   bool
}

// Fit runs least-squares over points within `window` ending at the last
// point. ok=false when fewer than minPoints fall inside or the covered span
// is under 80% of the window; Status carries have/need for messages.
func Fit(points []Point, window time.Duration, minPoints int) (Trend, Status) {
	st := Status{Need: window}
	if len(points) == 0 {
		return Trend{}, st
	}
	last := points[len(points)-1].T
	cutoff := last.Add(-window)
	var xs, ys []float64
	for _, p := range points {
		if !p.T.Before(cutoff) && !p.T.After(last) {
			xs = append(xs, p.T.Sub(last).Seconds())
			ys = append(ys, p.V)
		}
	}
	n := len(xs)
	st.Have = last.Sub(points[0].T)
	if n < minPoints {
		return Trend{}, st
	}
	first := last.Add(time.Duration(xs[0]) * time.Second)
	span := last.Sub(first)
	st.Have = span
	if span < time.Duration(float64(window)*0.8) {
		return Trend{}, st
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	nf := float64(n)
	den := nf*sxx - sx*sx
	var slope, intercept float64
	if den != 0 {
		slope = (nf*sxy - sx*sy) / den
		intercept = (sy - slope*sx) / nf
	}
	var ssRes, ssTot, mean float64
	mean = sy / nf
	for i := range xs {
		d := ys[i] - (slope*xs[i] + intercept)
		ssRes += d * d
		dt := ys[i] - mean
		ssTot += dt * dt
	}
	r2 := 1.0
	if ssTot != 0 {
		r2 = 1 - ssRes/ssTot
	}
	tr := Trend{N: n, Span: span, Slope: slope, Intercept: intercept, R2: r2, Last: ys[n-1]}
	st.OK = true
	return tr, st
}

// Classify describes the trend: unstable below the R² gate; steady when the
// predicted change over the window is under 2% of scale; else rising/falling.
func Classify(t Trend, scale float64) string {
	if t.R2 < R2Gate {
		return "unstable"
	}
	move := t.Slope * t.Span.Seconds()
	if scale < 1 {
		scale = 1
	}
	if move < 0 {
		move = -move
	}
	if move < scale*0.02 {
		return "steady"
	}
	if t.Slope > 0 {
		return "rising"
	}
	return "falling"
}

// ETA returns the time until the trend crosses threshold, only when the fit
// is good and the slope moves toward it.
func ETA(t Trend, threshold float64) (time.Duration, bool) {
	if t.R2 < R2Gate || t.Slope == 0 {
		return 0, false
	}
	d := (threshold - t.Last) / t.Slope
	if d <= 0 {
		return 0, false
	}
	return time.Duration(d * float64(time.Second)), true
}

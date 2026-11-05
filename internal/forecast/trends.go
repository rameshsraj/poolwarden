package forecast

import (
	"time"

	"github.com/smarsh/poolwarden/internal/history"
)

// Trends bundles the per-metric regression results shared by views and the
// diagnosis engine.
type Trends struct {
	Span         time.Duration
	OK           bool
	Have, Need   time.Duration
	ReplayDelay  map[string]Trend // by node host
	LagBytes     map[string]Trend
	RetainedWAL  Trend
	BusyChildren Trend
	Conns        map[string]Trend
}

// BuildTrends computes all trends from history samples. span is the store's
// total coverage (History.Span()); pass 0 when unavailable.
func BuildTrends(samples []history.Sample, span time.Duration) *Trends {
	tr := &Trends{
		Span:        span,
		ReplayDelay: map[string]Trend{},
		LagBytes:    map[string]Trend{},
		Conns:       map[string]Trend{},
	}
	tr.OK = span >= time.Duration(float64(Window)*0.8)
	if span < Window {
		tr.Have, tr.Need = span, Window
	}
	if len(samples) == 0 {
		return tr
	}
	byHost := func(f func(history.NodeSample) float64) map[string][]Point {
		out := map[string][]Point{}
		for _, sm := range samples {
			for _, nd := range sm.Nodes {
				if v := f(nd); v >= 0 {
					out[nd.Host] = append(out[nd.Host], Point{T: sm.TS, V: v})
				}
			}
		}
		return out
	}
	fitMap := func(series map[string][]Point) map[string]Trend {
		out := map[string]Trend{}
		for k, pts := range series {
			if t, st := Fit(pts, Window, MinPoints); st.OK {
				out[k] = t
			}
		}
		return out
	}
	tr.ReplayDelay = fitMap(byHost(func(n history.NodeSample) float64 { return n.ReplayDelaySec }))
	tr.LagBytes = fitMap(byHost(func(n history.NodeSample) float64 { return float64(n.LagBytes) }))
	tr.Conns = fitMap(byHost(func(n history.NodeSample) float64 { return float64(n.Conns) }))
	var walPts []Point
	for _, sm := range samples {
		var mx int64 = -1
		for _, nd := range sm.Nodes {
			if nd.RetainedWALBytes > mx {
				mx = nd.RetainedWALBytes
			}
		}
		if mx >= 0 {
			walPts = append(walPts, Point{T: sm.TS, V: float64(mx)})
		}
	}
	if t, st := Fit(walPts, Window, MinPoints); st.OK {
		tr.RetainedWAL = t
	}
	var busyPts []Point
	for _, sm := range samples {
		busyPts = append(busyPts, Point{T: sm.TS, V: float64(sm.Children.Busy)})
	}
	if t, st := Fit(busyPts, Window, MinPoints); st.OK {
		tr.BusyChildren = t
	}
	return tr
}

package cli

import (
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/history"
)

// fakeHist returns a fixed series: db2 replay delay rising linearly over 5m.
type fakeHist struct {
	samples []history.Sample
	span    time.Duration
}

func (f *fakeHist) Since(d time.Duration) ([]history.Sample, error) {
	var out []history.Sample
	if len(f.samples) == 0 {
		return nil, nil
	}
	cutoff := f.samples[len(f.samples)-1].TS.Add(-d)
	for _, s := range f.samples {
		if !s.TS.Before(cutoff) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeHist) Span() (time.Duration, error) { return f.span, nil }

func risingHist(t0 time.Time) *fakeHist {
	var samples []history.Sample
	for i := 0; i < 31; i++ {
		samples = append(samples, history.Sample{
			TS: t0.Add(time.Duration(i) * 10 * time.Second),
			Nodes: []history.NodeSample{
				{Host: "db1", NodeID: 0, Reachable: true, Conns: 80, MaxConns: 500,
					RetainedWALBytes: int64(i) * 1024},
				{Host: "db2", NodeID: 1, Reachable: true, InRecovery: true,
					ReplayDelaySec: float64(i) * 2, LagBytes: int64(i) * 1024,
					Conns: 40, MaxConns: 500},
			},
			Children: history.ChildrenSample{Busy: i, Total: 100, Clients: 60},
		})
	}
	return &fakeHist{samples: samples, span: 5 * time.Minute}
}

func shortHist() *fakeHist {
	return &fakeHist{samples: nil, span: 2 * time.Minute}
}

func TestForecastGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.History = risingHist(time.Date(2026, 11, 5, 12, 3, 1, 0, time.UTC).Add(-5 * time.Minute))
	if code := Execute([]string{"forecast"}, d); code != 0 {
		t.Fatalf("code %d stderr %s", code, errb)
	}
	goldenView(t, "forecast.table.golden", out.String())
}

func TestForecastInsufficient(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.History = shortHist()
	if code := Execute([]string{"forecast"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "forecast_insufficient.golden", out.String())
}

func TestLagTrendGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.History = risingHist(time.Date(2026, 11, 5, 12, 3, 1, 0, time.UTC).Add(-5 * time.Minute))
	if code := Execute([]string{"lag"}, d); code != 0 {
		t.Fatalf("code %d stderr %s", code, errb)
	}
	goldenView(t, "lag_trend.table.golden", out.String())
}

func TestLagHistorySparkline(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.History = risingHist(time.Date(2026, 11, 5, 12, 3, 1, 0, time.UTC).Add(-5 * time.Minute))
	if code := Execute([]string{"lag", "--history", "1h"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "lag_history.table.golden", out.String())
}

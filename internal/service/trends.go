package service

import (
	"context"
	"math"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/model"
)

// HistoryReader is the history surface views need.
type HistoryReader interface {
	Since(time.Duration) ([]history.Sample, error)
	Span() (time.Duration, error)
}

// Trends is an alias for the shared forecast trends.
type Trends = forecast.Trends

func (s *Service) trends(ctx context.Context, snap *model.Snapshot) *Trends {
	if s.History == nil {
		return &Trends{}
	}
	span, _ := s.History.Span()
	samples, err := s.History.Since(forecast.Window + s.Cfg.Interval)
	if err != nil {
		return &Trends{Span: span}
	}
	tr := forecast.BuildTrends(samples, span)
	return tr
}

// lagTrend returns class and eta for one standby.
func lagTrend(t *Trends, host string, critSec float64) (class string, eta float64) {
	tr, ok := t.ReplayDelay[host]
	if !ok {
		return "-", -1
	}
	scale := math.Max(math.Abs(tr.Last), critSec)
	class = forecast.Classify(tr, scale)
	if e, ok := forecast.ETA(tr, critSec); ok {
		eta = e.Seconds()
	}
	return class, eta
}

// risingHosts marks standbys whose replay delay is classified rising.
func risingHosts(t *Trends, cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	crit := float64(cfg.Lag.CritSeconds)
	for host, tr := range t.ReplayDelay {
		scale := math.Max(math.Abs(tr.Last), crit)
		if forecast.Classify(tr, scale) == "rising" {
			out[host] = true
		}
	}
	return out
}

// prevSample returns the sample just before the latest within interval*3,
// for "change since the last sample" columns.
func (s *Service) prevSample(snap *model.Snapshot) *history.Sample {
	if s.History == nil {
		return nil
	}
	win := s.Cfg.Interval * 3
	if win <= 0 {
		win = 30 * time.Second
	}
	samples, err := s.History.Since(win)
	if err != nil || len(samples) < 2 {
		return nil
	}
	return &samples[len(samples)-2]
}

func forecastETA(t forecast.Trend, threshold float64) (float64, bool) {
	if e, ok := forecast.ETA(t, threshold); ok {
		return e.Seconds(), true
	}
	return 0, false
}

// DelaySeries returns the replay-delay series per standby over window for
// the --history sparkline.
func (s *Service) DelaySeries(ctx context.Context, window time.Duration) map[string][]float64 {
	if s.History == nil {
		return nil
	}
	samples, err := s.History.Since(window)
	if err != nil {
		return nil
	}
	out := map[string][]float64{}
	for _, sm := range samples {
		for _, nd := range sm.Nodes {
			out[nd.Host] = append(out[nd.Host], nd.ReplayDelaySec)
		}
	}
	return out
}

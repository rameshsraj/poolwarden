package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/model"
)

// ForecastRow is one line of `forecast` (02 §3.3).
type ForecastRow struct {
	Metric    string  `json:"metric"`
	Subject   string  `json:"subject"`
	Now       float64 `json:"now"`
	Slope     float64 `json:"slope_per_sec"`
	R2        float64 `json:"r2"`
	Class     string  `json:"class"`
	ETASec    float64 `json:"eta_sec"`   // -1 = "-"
	Threshold float64 `json:"threshold"` // -1 = "-"
	Unit      string  `json:"unit"`
}

type Insufficient struct {
	Have time.Duration `json:"have"`
	Need time.Duration `json:"need"`
}

type ForecastResult struct {
	Header       Header          `json:"header"`
	Insufficient *Insufficient   `json:"insufficient,omitempty"`
	Rows         []ForecastRow   `json:"rows"`
	Trends       *Trends         `json:"-"`
	Findings     []model.Finding `json:"findings"`
}

func (s *Service) Forecast(ctx context.Context) (*ForecastResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	res := &ForecastResult{Header: r.header, Findings: r.findings}
	tr := s.trends(ctx, r.snap)
	res.Trends = tr
	if s.History == nil || tr.Span < time.Duration(float64(forecast.Window)*0.8) {
		res.Insufficient = &Insufficient{Have: tr.Span, Need: forecast.Window}
		return res, nil
	}
	crit := float64(s.Cfg.Lag.CritSeconds)
	add := func(m ForecastRow, t forecast.Trend, threshold float64, unit string) {
		m.Unit = unit
		m.Now = t.Last
		m.Slope = t.Slope
		m.R2 = t.R2
		m.Threshold = threshold
		m.Class = forecast.Classify(t, math.Max(math.Abs(t.Last), math.Abs(threshold)))
		m.ETASec = -1
		if threshold >= 0 {
			if e, ok := forecast.ETA(t, threshold); ok {
				m.ETASec = e.Seconds()
			}
		}
		res.Rows = append(res.Rows, m)
	}
	for _, b := range r.snap.Backends {
		name := b.Name
		if name == "" {
			name = b.Host
		}
		if b.InRecovery {
			if t, ok := tr.ReplayDelay[name]; ok {
				add(ForecastRow{Metric: "replay_delay", Subject: name}, t, crit, "s")
			}
			if t, ok := tr.LagBytes[name]; ok {
				add(ForecastRow{Metric: "lag_bytes", Subject: name}, t, -1, "B")
			}
		}
		if t, ok := tr.Conns[name]; ok && b.MaxConns > 0 {
			add(ForecastRow{Metric: "conns", Subject: name}, t, float64(b.MaxConns), "")
		}
	}
	if vol := int64(s.Cfg.WALVolumeBytes); vol > 0 {
		if t := tr.RetainedWAL; t.N > 0 {
			add(ForecastRow{Metric: "retained_wal", Subject: "primary"}, t, float64(vol), "B")
		}
	}
	if nc := atoi(r.snap.Settings["num_init_children"]); nc > 0 && tr.BusyChildren.N > 0 {
		add(ForecastRow{Metric: "busy_children", Subject: "pgpool"}, tr.BusyChildren, float64(nc), "")
	}
	sort.Slice(res.Rows, func(i, j int) bool {
		if res.Rows[i].Metric != res.Rows[j].Metric {
			return res.Rows[i].Metric < res.Rows[j].Metric
		}
		return res.Rows[i].Subject < res.Rows[j].Subject
	})
	_ = fmt.Sprint
	return res, nil
}

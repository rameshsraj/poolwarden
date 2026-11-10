package service

import (
	"context"
	"fmt"

	"github.com/smarsh/poolwarden/internal/model"
)

// MonitorBundle is every dashboard view derived from ONE collection cycle.
// `poolwarden monitor` emits it per tick; the console gateway streams it to
// browsers, so many clients share a single collect.
type MonitorBundle struct {
	Header   Header          `json:"header"`
	Status   *StatusResult   `json:"status"`
	Nodes    *NodesResult    `json:"nodes"`
	Lag      *LagResult      `json:"lag,omitempty"`
	LagErr   string          `json:"lag_err,omitempty"`
	Pools    *PoolsResult    `json:"pools"`
	Conns    *ConnsResult    `json:"conns"`
	Slots    *SlotsResult    `json:"slots"`
	Watchdog *WatchdogResult `json:"watchdog"`
	Health   *HealthResult   `json:"health"`
	Topology *TopologyResult `json:"topology"`
	WAL      *WALInfo        `json:"wal,omitempty"`
	Rates    *MonitorRates   `json:"rates,omitempty"`
	Findings []model.Finding `json:"findings"`
}

// WALInfo is the WAL inventory of the observed primary (pg_ls_waldir).
type WALInfo struct {
	Primary      string `json:"primary"`
	PrimaryLSN   string `json:"primary_lsn"`
	WalDirBytes  int64  `json:"wal_dir_bytes"` // -1 unknown
	WalFiles     int    `json:"wal_files"`     // -1 unknown
	SlotRetained int64  `json:"slot_retained"` // sum of replication-slot retained WAL, -1 unknown
}

// MonitorRates are per-tick deltas computed by the caller (the monitor loop
// keeps the previous bundle); values are -1 when not computable (first tick,
// timeline switch, counter reset).
type MonitorRates struct {
	IntervalMS  int64              `json:"interval_ms"`
	WalWriteBps float64            `json:"wal_write_bps"`      // primary WAL generation rate
	Nodes       map[int]float64    `json:"nodes,omitempty"`    // node id -> selects/sec
	Standbys    map[string]float64 `json:"standbys,omitempty"` // standby -> replay bytes/sec
}

// Monitor runs one collect and derives all views from the same snapshot, so
// the bundle is internally consistent (topology and lag agree on roles).
func (s *Service) Monitor(ctx context.Context) (*MonitorBundle, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	b := &MonitorBundle{
		Header:   r.header,
		Status:   s.statusFrom(ctx, r),
		Nodes:    s.nodesFrom(ctx, r),
		Pools:    s.poolsFrom(r),
		Conns:    s.connsFrom(r),
		Slots:    s.slotsFrom(ctx, r),
		Watchdog: s.watchdogFrom(r),
		Health:   s.healthFrom(r),
		Topology: s.topologyFrom(r),
		Findings: r.findings,
	}
	if lag, lerr := s.lagFrom(ctx, r); lerr != nil {
		b.LagErr = lerr.Error()
	} else {
		b.Lag = lag
	}
	b.WAL = walInfoFrom(r)
	return b, nil
}

// walInfoFrom extracts the primary's WAL inventory from the snapshot.
func walInfoFrom(r *result) *WALInfo {
	for _, b := range r.snap.Backends {
		if b.InRecovery || !b.Reachable {
			continue
		}
		name := b.Name
		if name == "" {
			name = fmt.Sprintf("%s:%d", b.Host, b.Port)
		}
		w := &WALInfo{
			Primary: name, PrimaryLSN: b.CurrentLSN,
			WalDirBytes: b.WalDirBytes, WalFiles: b.WalFiles, SlotRetained: -1,
		}
		var tot int64
		seen := false
		for _, sl := range b.Slots {
			if sl.RetainedBytes >= 0 {
				tot += sl.RetainedBytes
				seen = true
			}
		}
		if seen {
			w.SlotRetained = tot
		}
		return w
	}
	return nil
}

// ProgressEvent is one real stage/step observation of a control action,
// emitted as it happens (never fabricated). The console turns these into
// operation timeline entries.
type ProgressEvent struct {
	Op     string         `json:"op"`               // action name: detach, promote, ...
	Stage  string         `json:"stage"`            // preflight|plan|confirm|step_start|step_end|verify_state|outcome
	Step   *Step          `json:"step,omitempty"`   // set for step_start/step_end
	Detail string         `json:"detail,omitempty"` // verdict, poll state, outcome, error
	Code   int            `json:"code,omitempty"`   // exit code on outcome
	At     model.JSONTime `json:"at"`
}

func (s *Service) progress(ev ProgressEvent) {
	if s.Progress != nil {
		ev.At = model.JSONTime(s.now())
		s.Progress(ev)
	}
}

// Package service holds one method per user action. It never prints and
// never reads stdin: it returns typed results the cli layer renders.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/forecast"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/toollog"
)

// SnapshotSource builds a cluster snapshot (*collector.Collector).
type SnapshotSource interface {
	Collect(ctx context.Context) *model.Snapshot
}

// Diagnoser runs the diagnosis rules over a prepared Input.
type Diagnoser interface {
	Run(ctx context.Context, in diagnose.Input) []model.Finding
}

// NoopDiagnoser returns no findings.
type NoopDiagnoser struct{}

func (NoopDiagnoser) Run(context.Context, diagnose.Input) []model.Finding { return nil }

// ProcInfoer fetches verbose pcp_proc_info output (*pcp.PCP).
type ProcInfoer interface {
	ProcInfo(ctx context.Context, pid int) (pcp.Result, error)
}

type Service struct {
	Cfg          *config.Config
	Collector    SnapshotSource
	Diagnoser    Diagnoser
	PCPProc      ProcInfoer
	History      HistoryReader
	ErrSrc       ErrorSource
	Tool         *toollog.Logger
	ErrorsColl   *errevents.Collector
	Recorder     *audit.Recorder
	PCP          PCPRunner
	SQLRunner    SQLRunner
	InitProbes   InitProbes
	CfgPath      string
	ConfigRemote RemoteOps
	ShowParam    ShowParamFn
	// FileSettingsFn reads each pgpool host's parsed conf (best-effort).
	FileSettingsFn func(ctx context.Context) map[string]map[string]string
	Lock           *remote.ClusterLock
	ScriptExists   func(ctx context.Context, host config.Host, path string) (bool, error)
	Confirmer      Confirmer
	// Progress, when set, receives real stage/step events during control
	// actions (cli renders NDJSON for the console gateway).
	Progress     func(ProgressEvent)
	FS           pwfs.FS
	Clock        clock.Clock
	BuildVersion string // poolwarden build version
	Local        string // local inventory host name, "" when not matched
}

// Header is the one-line context every view starts with (02 §0).
type Header struct {
	Cluster       string         `json:"cluster"`
	PgpoolVersion string         `json:"pgpool_version"`
	LocalHost     string         `json:"local_host"`
	LocalWDRole   string         `json:"local_wd_role"`
	VIPHost       string         `json:"vip_host"`
	Collected     model.JSONTime `json:"collected"`
	DurationMS    int64          `json:"duration_ms"`
	Degraded      bool           `json:"degraded"`
}

// result bundles one collect+diagnose pass.
type result struct {
	snap     *model.Snapshot
	findings []model.Finding
	header   Header
}

func (s *Service) collect(ctx context.Context) (*result, error) {
	snap := s.Collector.Collect(ctx)
	// best-effort history append so one-shot/cron use builds history (I25)
	if st, ok := s.History.(interface {
		Append(history.Sample) error
	}); ok && st != nil {
		if err := st.Append(history.FromSnapshot(snap)); err != nil && s.Tool != nil {
			s.Tool.Warn("history append failed", "err", err)
		}
	}
	findings := []model.Finding(nil)
	if s.Diagnoser != nil {
		findings = s.Diagnoser.Run(ctx, s.buildInput(ctx, snap))
	}
	if !snap.Pgpool.Reachable && !anyBackendReachable(snap) {
		ep := s.Cfg.Pgpool
		return nil, exitcode.New(exitcode.Connection,
			fmt.Sprintf("cannot reach pgpool at %s:%d (%s)", ep.Host, ep.Port, snap.Pgpool.Err)).
			WithCause("pgpool-II service not running on " + orLocal(s, ep.Host) + ", or wrong port in " + config.DefaultPath).
			WithHint("systemctl status " + pgpoolService(s) + "   |   poolwarden config self-check")
	}
	return &result{snap: snap, findings: findings, header: s.header(snap)}, nil
}

func orLocal(s *Service, host string) string {
	if s.Local != "" {
		return s.Local
	}
	return host
}

func pgpoolService(s *Service) string {
	for _, h := range s.Cfg.PgpoolHosts() {
		if h.Pgpool != nil && h.Pgpool.Service != "" {
			return h.Pgpool.Service
		}
	}
	return "pgpool-II"
}

// buildInput assembles the diagnose.Input for one snapshot.
func (s *Service) buildInput(ctx context.Context, snap *model.Snapshot) diagnose.Input {
	in := diagnose.Input{
		Snap:  snap,
		Cfg:   s.Cfg,
		Local: s.Local,
		Now:   s.now(),
	}
	if s.FileSettingsFn != nil {
		in.FileSettings = s.FileSettingsFn(ctx)
	}
	if s.History != nil {
		span, _ := s.History.Span()
		if samples, err := s.History.Since(forecast.Window + s.Cfg.Interval); err == nil && len(samples) > 0 {
			in.Trends = forecast.BuildTrends(samples, span)
		}
		in.Prev = s.prevSample(snap)
	}
	return in
}

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

func anyBackendReachable(snap *model.Snapshot) bool {
	for _, b := range snap.Backends {
		if b.Reachable {
			return true
		}
	}
	return false
}

func (s *Service) header(snap *model.Snapshot) Header {
	h := Header{
		Cluster:       s.Cfg.Cluster,
		PgpoolVersion: orDash(snap.Pgpool.Version),
		LocalHost:     s.Local,
		Collected:     model.JSONTime(snap.Taken),
		DurationMS:    snap.Duration.Milliseconds(),
		Degraded:      snap.Degraded,
	}
	for _, n := range snap.Watchdog.Nodes {
		if s.Local != "" && n.Host == s.Local {
			h.LocalWDRole = n.Status
		}
	}
	// VIP segment: "" when no delegate_ip is configured, "none" when it is
	// configured but not present on any host, else the holder list.
	if vipAddress(snap) != "" {
		var vips []string
		for _, v := range snap.VIP {
			if v.Present {
				vips = append(vips, v.Host)
			}
		}
		if len(vips) == 0 {
			h.VIPHost = "none"
		} else {
			h.VIPHost = joinComma(vips)
		}
	}
	return h
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// backendFor matches a pool node to a probed backend: NodeID first, then
// host:port via the inventory name or address.
func (s *Service) backendFor(snap *model.Snapshot, n model.PoolNode) *model.Backend {
	for i := range snap.Backends {
		if snap.Backends[i].NodeID == n.ID && n.ID >= 0 {
			return &snap.Backends[i]
		}
	}
	addr := n.Hostname
	if h, ok := s.Cfg.HostByName(n.Hostname); ok {
		addr = h.Address
	}
	for i := range snap.Backends {
		b := &snap.Backends[i]
		if b.Port == n.Port && (b.Host == addr || b.Host == n.Hostname) {
			return b
		}
	}
	return nil
}

func (s *Service) backendByHost(snap *model.Snapshot, host string) *model.Backend {
	for i := range snap.Backends {
		if snap.Backends[i].Name == host || snap.Backends[i].Host == host {
			return &snap.Backends[i]
		}
	}
	if h, ok := s.Cfg.HostByName(host); ok {
		for i := range snap.Backends {
			if snap.Backends[i].Host == h.Address {
				return &snap.Backends[i]
			}
		}
	}
	return nil
}

// findingsFor returns the node-scoped findings (Subject is the inventory
// host name) plus matching "node<id>" and host:port subjects.
func findingsFor(fs []model.Finding, name string, id int, hostport string) []model.Finding {
	var out []model.Finding
	for _, f := range fs {
		if f.Subject == name || f.Subject == fmt.Sprintf("node%d", id) || f.Subject == hostport {
			out = append(out, f)
		}
	}
	return out
}

// realRole is what the backend itself reports.
func realRole(b *model.Backend) string {
	switch {
	case b == nil:
		return "?"
	case !b.Reachable:
		return "UNREACHABLE"
	case b.InRecovery:
		return "standby"
	default:
		return "primary"
	}
}

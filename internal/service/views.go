package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
)

// NodeRow is one line of `nodes` (02 §2.2).
type NodeRow struct {
	ID            int            `json:"id"`
	HostPort      string         `json:"host_port"`
	Status        string         `json:"status"`
	PgpoolRole    string         `json:"pgpool_role"`
	RealRole      string         `json:"real_role"`
	LBWeight      float64        `json:"lb_weight"`
	Selects       int64          `json:"selects"`
	DelaySec      float64        `json:"delay_sec"` // -1 unknown
	ReplState     string         `json:"repl_state"`
	Sync          string         `json:"sync"`
	LastChange    model.JSONTime `json:"last_change"`
	Score         int            `json:"score"`
	Note          string         `json:"note"`
	Timeline      int            `json:"timeline,omitempty"`
	CurrentLSN    string         `json:"current_lsn,omitempty"`
	ReceiveLSN    string         `json:"receive_lsn,omitempty"`
	Conns         int            `json:"conns"`
	MaxConns      int            `json:"max_conns"`
	OldestXactSec float64        `json:"oldest_xact_sec"`
	Conflicts     int64          `json:"conflicts"`
}

type NodesResult struct {
	Header   Header          `json:"header"`
	Nodes    []NodeRow       `json:"nodes"`
	Findings []model.Finding `json:"findings"`
}

type NodeDetail struct {
	Header   Header          `json:"header"`
	Node     NodeRow         `json:"node"`
	Findings []model.Finding `json:"findings"`
}

func (s *Service) nodeRow(snap *model.Snapshot, findings []model.Finding, n model.PoolNode, rising map[string]bool) NodeRow {
	b := s.backendFor(snap, n)
	rr := realRole(b)
	host := n.Hostname
	if h, ok := s.Cfg.HostByName(n.Hostname); ok {
		host = h.Name
	}
	hostport := fmt.Sprintf("%s:%d", host, n.Port)
	nf := findingsFor(findings, host, n.ID, hostport)
	var crit, warn int
	for _, f := range nf {
		switch f.Severity {
		case model.SevCrit:
			crit++
		case model.SevWarn:
			warn++
		}
	}
	delay := -1.0
	if b != nil && b.Reachable && b.InRecovery {
		delay = b.ReplayDelaySec
	}
	note := ""
	switch {
	case b == nil:
	case !b.Reachable && n.Status == "up":
		note = "pgpool up / direct down"
	case b.Reachable && n.Status == "down":
		note = "pgpool down / direct up"
	case !b.Reachable:
		note = "UNREACHABLE"
	case rr != "?" && rr != n.Role && n.Role != "":
		note = "ROLE MISMATCH"
	}
	row := NodeRow{
		ID: n.ID, HostPort: hostport, Status: n.Status, PgpoolRole: n.Role,
		RealRole: rr, LBWeight: n.LBWeight, Selects: n.SelectCnt, DelaySec: delay,
		ReplState: orDash(n.ReplicationState), Sync: orDash(n.ReplicationSyncState),
		LastChange: model.JSONTime(n.LastStatusChange),
		Score:      forecast.Score(n.Status == "down" || n.Status == "quarantine" || (b != nil && !b.Reachable), crit, warn, rising[host]),
		Note:       note,
		Conns:      -1, MaxConns: -1, OldestXactSec: -1, Conflicts: -1,
	}
	if b != nil {
		row.Timeline = b.Timeline
		row.CurrentLSN = b.CurrentLSN
		row.ReceiveLSN = b.ReceiveLSN
		row.Conns = b.Conns
		row.MaxConns = b.MaxConns
		row.OldestXactSec = b.OldestXactSec
		row.Conflicts = b.Conflicts
	}
	return row
}

func (s *Service) Nodes(ctx context.Context) (*NodesResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.nodesFrom(ctx, r), nil
}

func (s *Service) nodesFrom(ctx context.Context, r *result) *NodesResult {
	res := &NodesResult{Header: r.header, Nodes: []NodeRow{}, Findings: r.findings}
	tr := s.trends(ctx, r.snap)
	for _, n := range r.snap.Pgpool.Nodes {
		res.Nodes = append(res.Nodes, s.nodeRow(r.snap, r.findings, n, risingHosts(tr, s.Cfg)))
	}
	return res
}

func (s *Service) Node(ctx context.Context, id int) (*NodeDetail, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	tr := s.trends(ctx, r.snap)
	for _, n := range r.snap.Pgpool.Nodes {
		if n.ID == id {
			return &NodeDetail{Header: r.header,
				Node:     s.nodeRow(r.snap, r.findings, n, risingHosts(tr, s.Cfg)),
				Findings: r.findings}, nil
		}
	}
	return nil, exitcode.New(exitcode.Usage, fmt.Sprintf("node %d not found", id)).
		WithHint("poolwarden nodes")
}

// LagRow is one standby line of `lag` (02 §2.3).
type LagRow struct {
	Standby       string  `json:"standby"`
	State         string  `json:"state"`
	Sync          string  `json:"sync"`
	LagBytes      int64   `json:"lag_bytes"`
	WriteLagSec   float64 `json:"write_lag_sec"`
	FlushLagSec   float64 `json:"flush_lag_sec"`
	ReplayLagSec  float64 `json:"replay_lag_sec"`
	DelaySec      float64 `json:"delay_sec"` // -1 unknown
	DelaySeverity string  `json:"delay_severity"`
	Trend         string  `json:"trend"`   // "-" until M4
	ETASec        float64 `json:"eta_sec"` // -1 = "-"
	SentLSN       string  `json:"sent_lsn,omitempty"`
	WriteLSN      string  `json:"write_lsn,omitempty"`
	FlushLSN      string  `json:"flush_lsn,omitempty"`
	ReplayLSN     string  `json:"replay_lsn,omitempty"`
}

type LagResult struct {
	Header     Header               `json:"header"`
	Primary    string               `json:"primary"`
	PrimaryLSN string               `json:"primary_lsn"`
	Standbys   []LagRow             `json:"standbys"`
	Series     map[string][]float64 `json:"-"` // delay sparkline data (--history)
	Findings   []model.Finding      `json:"findings"`
}

func (s *Service) primaryBackend(snap *model.Snapshot) *model.Backend {
	for i := range snap.Backends {
		if snap.Backends[i].Reachable && !snap.Backends[i].InRecovery {
			return &snap.Backends[i]
		}
	}
	return nil
}

func (s *Service) Lag(ctx context.Context) (*LagResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.lagFrom(ctx, r)
}

func (s *Service) lagFrom(ctx context.Context, r *result) (*LagResult, error) {
	p := s.primaryBackend(r.snap)
	if p == nil {
		return nil, exitcode.New(exitcode.Connection, "no reachable primary found").
			WithHint("poolwarden nodes")
	}
	res := &LagResult{
		Header:     r.header,
		Primary:    fmt.Sprintf("%s:%d", p.Host, p.Port),
		PrimaryLSN: p.CurrentLSN,
		Standbys:   []LagRow{},
		Findings:   r.findings,
	}
	tr := s.trends(ctx, r.snap)
	for _, sr := range p.Standbys {
		name := sr.AppName
		var b *model.Backend
		for i := range r.snap.Backends {
			if model.StandbyMatches(sr, r.snap.Backends[i]) {
				b = &r.snap.Backends[i]
				if b.Name != "" {
					name = b.Name
				}
				break
			}
		}
		delay := -1.0
		if b != nil && b.Reachable {
			delay = b.ReplayDelaySec
		}
		host := name
		if b != nil && b.Name != "" {
			host = b.Name
		}
		class, eta := lagTrend(tr, host, float64(s.Cfg.Lag.CritSeconds))
		row := LagRow{
			Standby: orDash(name), State: sr.State, Sync: sr.SyncState,
			LagBytes: sr.LagBytes, WriteLagSec: sr.WriteLag, FlushLagSec: sr.FlushLag,
			ReplayLagSec: sr.ReplayLag, DelaySec: delay,
			DelaySeverity: s.delaySeverity(delay),
			Trend:         class, ETASec: eta,
			SentLSN: sr.SentLSN, WriteLSN: sr.WriteLSN, FlushLSN: sr.FlushLSN,
			ReplayLSN: sr.ReplayLSN,
		}
		res.Standbys = append(res.Standbys, row)
	}
	res.Series = s.DelaySeries(ctx, time.Hour)
	// standbys missing from pg_stat_replication still appear, marked
	for i := range r.snap.Backends {
		b := &r.snap.Backends[i]
		if !b.Reachable || !b.InRecovery {
			continue
		}
		found := false
		for _, row := range res.Standbys {
			if row.Standby == b.Name || row.Standby == b.Host {
				found = true
			}
		}
		if !found {
			class, eta := lagTrend(tr, orDash(b.Name), float64(s.Cfg.Lag.CritSeconds))
			res.Standbys = append(res.Standbys, LagRow{
				Standby: orDash(b.Name), State: "not streaming", Sync: "-",
				LagBytes: -1, WriteLagSec: -1, FlushLagSec: -1, ReplayLagSec: -1,
				DelaySec: b.ReplayDelaySec, DelaySeverity: s.delaySeverity(b.ReplayDelaySec),
				Trend: class, ETASec: eta,
			})
		}
	}
	return res, nil
}

func (s *Service) delaySeverity(delay float64) string {
	switch {
	case delay < 0:
		return model.SevInfo
	case delay >= float64(s.Cfg.Lag.CritSeconds):
		return model.SevCrit
	case delay >= float64(s.Cfg.Lag.WarnSeconds):
		return model.SevWarn
	}
	return model.SevInfo
}

// SlotRow is one line of `slots` (02 §2.4).
type SlotRow struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Active        bool    `json:"active"`
	WALStatus     string  `json:"wal_status"`
	RetainedBytes int64   `json:"retained_bytes"`
	ETASec        float64 `json:"eta_sec"` // -1 = "-"
}

type SlotsResult struct {
	Header   Header          `json:"header"`
	Slots    []SlotRow       `json:"slots"`
	Findings []model.Finding `json:"findings"`
}

func (s *Service) Slots(ctx context.Context) (*SlotsResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.slotsFrom(ctx, r), nil
}

func (s *Service) slotsFrom(ctx context.Context, r *result) *SlotsResult {
	res := &SlotsResult{Header: r.header, Slots: []SlotRow{}, Findings: r.findings}
	eta := -1.0
	if s.History != nil && int64(s.Cfg.WALVolumeBytes) > 0 {
		tr := s.trends(ctx, r.snap)
		if tr.RetainedWAL.N > 0 {
			if e, ok := forecastETA(tr.RetainedWAL, float64(s.Cfg.WALVolumeBytes)); ok {
				eta = e
			}
		}
	}
	if p := s.primaryBackend(r.snap); p != nil {
		for _, sl := range p.Slots {
			res.Slots = append(res.Slots, SlotRow{
				Name: sl.Name, Type: sl.Type, Active: sl.Active,
				WALStatus: orDash(sl.WALStatus), RetainedBytes: sl.RetainedBytes,
				ETASec: eta,
			})
		}
	}
	return res
}

// PoolGroup is one line of `pools` (02 §2.5).
type PoolGroup struct {
	Database         string `json:"database"`
	User             string `json:"user"`
	Backends         int    `json:"backends"`
	Pools            int    `json:"pools"`
	Connected        int    `json:"connected"`
	LoadBalanceNodes int    `json:"load_balance_nodes"`
}

type PoolsResult struct {
	Header       Header          `json:"header"`
	ChildrenBusy int             `json:"children_busy"`
	ChildrenMax  int             `json:"children_max"` // num_init_children
	PoolMax      int             `json:"pool_max"`     // max_pool: backend conns pooled per child
	Clients      int             `json:"clients"`      // cumulative client conns served (lifetime)
	Groups       []PoolGroup     `json:"groups"`
	Findings     []model.Finding `json:"findings"`
}

func (s *Service) Pools(ctx context.Context) (*PoolsResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.poolsFrom(r), nil
}

func (s *Service) poolsFrom(r *result) *PoolsResult {
	res := &PoolsResult{Header: r.header, Groups: []PoolGroup{}, Findings: r.findings}
	busy := 0
	for _, p := range r.snap.Pgpool.Processes {
		res.Clients += p.ClientConnectionCount
		if p.Status != "" && p.Status != "Wait for connection" && p.Status != "Idle" {
			busy++
		}
	}
	res.ChildrenBusy = busy
	res.ChildrenMax = atoi(r.snap.Settings["num_init_children"])
	if res.ChildrenMax == 0 {
		res.ChildrenMax = len(r.snap.Pgpool.Processes)
	}
	res.PoolMax = atoi(r.snap.Settings["max_pool"])
	groups := map[string]*PoolGroup{}
	for _, pl := range r.snap.Pgpool.Pools {
		key := pl.Database + "|" + pl.Username
		g, ok := groups[key]
		if !ok {
			g = &PoolGroup{Database: pl.Database, User: pl.Username}
			groups[key] = g
		}
		g.Pools++
		g.Backends++
		if pl.PoolConnected == 1 {
			g.Connected++
		}
		if pl.LoadBalanceNode == 1 {
			g.LoadBalanceNodes++
		}
	}
	for _, g := range groups {
		res.Groups = append(res.Groups, *g)
	}
	sort.Slice(res.Groups, func(i, j int) bool {
		return res.Groups[i].Database+res.Groups[i].User < res.Groups[j].Database+res.Groups[j].User
	})
	return res
}

func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

// ProcRow is one SHOW POOL_PROCESSES line, verbatim columns.
type ProcRow struct {
	PID             int    `json:"pid"`
	StartTime       string `json:"start_time"`
	ClientConns     int    `json:"client_conns"`
	Database        string `json:"database"`
	User            string `json:"user"`
	BackendConnTime string `json:"backend_conn_time"`
	PoolCounter     int    `json:"pool_counter"`
	Status          string `json:"status"`
}

type ProcsResult struct {
	Header   Header          `json:"header"`
	Procs    []ProcRow       `json:"procs"`
	Findings []model.Finding `json:"findings"`
}

type ProcDetailResult struct {
	Header   Header            `json:"header"`
	Rows     []pcp.ProcInfoRow `json:"rows"`
	Findings []model.Finding   `json:"findings"`
}

func (s *Service) Procs(ctx context.Context) (*ProcsResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	res := &ProcsResult{Header: r.header, Procs: []ProcRow{}, Findings: r.findings}
	for _, p := range r.snap.Pgpool.Processes {
		res.Procs = append(res.Procs, ProcRow{
			PID: p.PID, StartTime: p.StartTime, ClientConns: p.ClientConnectionCount,
			Database: orDash(p.Database), User: orDash(p.Username),
			BackendConnTime: orDash(p.BackendConnectionTime), PoolCounter: p.PoolCounter,
			Status: orDash(p.Status),
		})
	}
	return res, nil
}

func (s *Service) Proc(ctx context.Context, pid int) (*ProcDetailResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	res := &ProcDetailResult{Header: r.header, Rows: []pcp.ProcInfoRow{}, Findings: r.findings}
	if s.PCPProc == nil {
		return res, nil
	}
	pr, err := s.PCPProc.ProcInfo(ctx, pid)
	if err != nil {
		return nil, exitcode.New(exitcode.Connection, "pcp_proc_info failed").
			WithCause(err.Error()).Wrap(err)
	}
	rows, err := pcp.ParseProcInfo(pr.Stdout)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, "cannot parse pcp_proc_info output").
			WithCause(err.Error()).Wrap(err)
	}
	res.Rows = rows
	return res, nil
}

// ConnRow is one line of `conns` (02 §2.5).
type ConnRow struct {
	Where       string `json:"where"`
	Database    string `json:"database"`
	User        string `json:"user"`
	Application string `json:"application"`
	State       string `json:"state"` // pg_stat_activity.state; "-" for pgpool-side rows
	Conns       int    `json:"conns"`
	Limit       int    `json:"limit"`
}

type ConnsResult struct {
	Header   Header          `json:"header"`
	Conns    []ConnRow       `json:"conns"`
	Findings []model.Finding `json:"findings"`
}

func (s *Service) Conns(ctx context.Context) (*ConnsResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.connsFrom(r), nil
}

func (s *Service) connsFrom(r *result) *ConnsResult {
	res := &ConnsResult{Header: r.header, Conns: []ConnRow{}, Findings: r.findings}
	pgLimit := atoi(r.snap.Settings["num_init_children"]) * atoi(r.snap.Settings["max_pool"])
	pgGroups := map[string]*ConnRow{}
	for _, pl := range r.snap.Pgpool.Pools {
		if pl.PoolConnected != 1 {
			continue
		}
		key := pl.Database + "|" + pl.Username
		g, ok := pgGroups[key]
		if !ok {
			g = &ConnRow{Where: "pgpool", Database: orDash(pl.Database),
				User: orDash(pl.Username), Application: "-", State: "-", Limit: pgLimit}
			pgGroups[key] = g
		}
		g.Conns++
	}
	for _, g := range pgGroups {
		res.Conns = append(res.Conns, *g)
	}
	for _, b := range r.snap.Backends {
		if !b.Reachable {
			continue
		}
		where := orDash(b.Name)
		for _, g := range b.Clients {
			res.Conns = append(res.Conns, ConnRow{
				Where: where, Database: orDash(g.Database), User: orDash(g.User),
				Application: orDash(g.Application), State: orDash(g.State),
				Conns: g.Count, Limit: b.MaxConns,
			})
		}
	}
	return res
}

// WatchdogNodeRow is one line of `watchdog`'s node table (02 §2.6).
type WatchdogNodeRow struct {
	Node       int    `json:"node"`
	Host       string `json:"host"`
	Status     string `json:"status"`
	Priority   int    `json:"priority"`
	VIPPresent bool   `json:"vip_present"`
}

type WatchdogResult struct {
	Header    Header            `json:"header"`
	Available bool              `json:"available"`
	Quorum    string            `json:"quorum"`
	Leader    string            `json:"leader"`
	Alive     int               `json:"alive"`
	Total     int               `json:"total"`
	Params    map[string]string `json:"params"`
	Nodes     []WatchdogNodeRow `json:"nodes"`
	VIPBanner string            `json:"vip_banner"`
	Findings  []model.Finding   `json:"findings"`
}

var watchdogParams = []string{
	"enable_consensus_with_half_votes", "failover_when_quorum_exists",
	"failover_require_consensus", "use_watchdog", "delegate_ip",
}

func (s *Service) Watchdog(ctx context.Context) (*WatchdogResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.watchdogFrom(r), nil
}

func (s *Service) watchdogFrom(r *result) *WatchdogResult {
	w := r.snap.Watchdog
	res := &WatchdogResult{
		Header:    r.header,
		Available: w.Available,
		Quorum:    orDash(w.QuorumState),
		Leader:    orDash(w.LeaderHost),
		Alive:     w.AliveRemote + 1,
		Total:     w.TotalNodes,
		Params:    map[string]string{},
		Nodes:     []WatchdogNodeRow{},
		Findings:  r.findings,
	}
	for _, k := range watchdogParams {
		res.Params[k] = orDash(r.snap.Settings[k])
	}
	vipByHost := map[string]bool{}
	for _, v := range r.snap.VIP {
		vipByHost[v.Host] = v.Present
	}
	for _, n := range w.Nodes {
		res.Nodes = append(res.Nodes, WatchdogNodeRow{
			Node: n.ID, Host: n.Host, Status: orDash(n.Status),
			Priority: n.Priority, VIPPresent: vipByHost[n.Host],
		})
	}
	var vipHosts []string
	for _, v := range r.snap.VIP {
		if v.Present {
			vipHosts = append(vipHosts, v.Host)
		}
	}
	if len(vipHosts) > 1 {
		res.VIPBanner = fmt.Sprintf("VIP %s is up on %s", vipAddress(r.snap), strings.Join(vipHosts, " and "))
	}
	return res
}

func vipAddress(snap *model.Snapshot) string {
	for _, n := range snap.Watchdog.Nodes {
		if n.DelegateIP != "" {
			return n.DelegateIP
		}
	}
	return snap.Settings["delegate_ip"]
}

// HealthRow is one line of `health` (02 §2.7); Delta* are nil until M4.
type HealthRow struct {
	Node      int     `json:"node"`
	Host      string  `json:"host"`
	Status    string  `json:"status"`
	Total     int64   `json:"total"`
	Success   int64   `json:"success"`
	Fail      int64   `json:"fail"`
	Skip      int64   `json:"skip"`
	Retry     int64   `json:"retry"`
	AvgMS     float64 `json:"avg_ms"`
	MaxMS     int64   `json:"max_ms"`
	LastFail  string  `json:"last_fail"`
	DeltaFail *int64  `json:"delta_fail"`
}

type HealthResult struct {
	Header   Header          `json:"header"`
	Nodes    []HealthRow     `json:"nodes"`
	Findings []model.Finding `json:"findings"`
}

func (s *Service) Health(ctx context.Context) (*HealthResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.healthFrom(r), nil
}

func (s *Service) healthFrom(r *result) *HealthResult {
	res := &HealthResult{Header: r.header, Nodes: []HealthRow{}, Findings: r.findings}
	prev := s.prevSample(r.snap)
	for _, h := range r.snap.Pgpool.HealthStats {
		row := HealthRow{
			Node: h.NodeID, Host: h.Hostname, Status: h.Status,
			Total: h.TotalCount, Success: h.SuccessCount, Fail: h.FailCount,
			Skip: h.SkipCount, Retry: h.RetryCount,
			AvgMS: h.AverageDurationMS, MaxMS: h.MaxDurationMS,
			LastFail: orDash(h.LastFailedHealthCheck),
		}
		if prev != nil {
			for _, nd := range prev.Nodes {
				if nd.NodeID == h.NodeID && nd.HealthFail >= 0 {
					d := h.FailCount - nd.HealthFail
					row.DeltaFail = &d
				}
			}
		}
		res.Nodes = append(res.Nodes, row)
	}
	return res
}

// TopologyRow is one line of `topology show` (02 §2.8).
type TopologyRow struct {
	Host        string   `json:"host"`
	Address     string   `json:"address"`
	Roles       []string `json:"roles"`
	SSHUser     string   `json:"ssh_user"`
	Watchdog    string   `json:"watchdog"`
	VIP         bool     `json:"vip"`
	BackendRole string   `json:"backend_role"`
}

type TopologyResult struct {
	Header   Header          `json:"header"`
	Hosts    []TopologyRow   `json:"hosts"`
	Findings []model.Finding `json:"findings"`
}

func (s *Service) TopologyShow(ctx context.Context) (*TopologyResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.topologyFrom(r), nil
}

func (s *Service) topologyFrom(r *result) *TopologyResult {
	res := &TopologyResult{Header: r.header, Hosts: []TopologyRow{}, Findings: r.findings}
	wdByHost := map[string]string{}
	for _, n := range r.snap.Watchdog.Nodes {
		wdByHost[n.Host] = n.Status
	}
	vipByHost := map[string]bool{}
	for _, v := range r.snap.VIP {
		vipByHost[v.Host] = v.Present
	}
	for _, h := range s.Cfg.Hosts {
		row := TopologyRow{
			Host: h.Name, Address: h.Address, SSHUser: h.SSHUser,
			Watchdog: orDash(wdByHost[h.Name]), VIP: vipByHost[h.Name],
			BackendRole: "-",
		}
		for _, ro := range h.Roles {
			row.Roles = append(row.Roles, string(ro))
		}
		if b := s.backendByHost(r.snap, h.Name); b != nil && b.Reachable {
			if b.InRecovery {
				row.BackendRole = "standby"
			} else {
				row.BackendRole = "primary"
			}
		} else if b != nil {
			row.BackendRole = "UNREACHABLE"
		}
		res.Hosts = append(res.Hosts, row)
	}
	return res
}

// StatusResult is the one-screen summary (02 §2.1).
type StatusResult struct {
	Header       Header          `json:"header"`
	Nodes        []NodeRow       `json:"nodes"`
	WatchdogOK   bool            `json:"watchdog_available"`
	Quorum       string          `json:"quorum"`
	Leader       string          `json:"leader"`
	Alive        int             `json:"alive"`
	Total        int             `json:"total"`
	HalfVotes    bool            `json:"half_votes"`
	ChildrenBusy int             `json:"children_busy"`
	ChildrenMax  int             `json:"children_max"`
	Clients      int             `json:"clients"`
	CountCrit    int             `json:"crit"`
	CountWarn    int             `json:"warn"`
	CountInfo    int             `json:"info"`
	Findings     []model.Finding `json:"findings"`
}

func (s *Service) Status(ctx context.Context) (*StatusResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	return s.statusFrom(ctx, r), nil
}

func (s *Service) statusFrom(ctx context.Context, r *result) *StatusResult {
	res := &StatusResult{Header: r.header, Nodes: []NodeRow{}, Findings: r.findings}
	tr := s.trends(ctx, r.snap)
	rising := risingHosts(tr, s.Cfg)
	for _, n := range r.snap.Pgpool.Nodes {
		res.Nodes = append(res.Nodes, s.nodeRow(r.snap, r.findings, n, rising))
	}
	w := r.snap.Watchdog
	res.WatchdogOK = w.Available
	res.Quorum = orDash(w.QuorumState)
	res.Leader = orDash(w.LeaderHost)
	res.Alive = w.AliveRemote + 1
	res.Total = w.TotalNodes
	res.HalfVotes = r.snap.Settings["enable_consensus_with_half_votes"] == "on"
	pools := s.poolsFrom(r)
	res.ChildrenBusy = pools.ChildrenBusy
	res.ChildrenMax = pools.ChildrenMax
	res.Clients = pools.Clients
	for _, f := range r.findings {
		switch f.Severity {
		case model.SevCrit:
			res.CountCrit++
		case model.SevWarn:
			res.CountWarn++
		case model.SevInfo:
			res.CountInfo++
		}
	}
	return res
}

// VersionResult is `version`'s best-effort data.
type VersionResult struct {
	Poolwarden string            `json:"poolwarden"`
	Pgpool     string            `json:"pgpool"`
	Backends   map[string]string `json:"backends"`
}

func (s *Service) Version(ctx context.Context) (*VersionResult, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	res := &VersionResult{Poolwarden: s.BuildVersion, Pgpool: "-", Backends: map[string]string{}}
	snap := s.Collector.Collect(cctx)
	if snap.Pgpool.Version != "" {
		res.Pgpool = snap.Pgpool.Version
	}
	for _, b := range snap.Backends {
		if b.Reachable && b.Version > 0 {
			name := orDash(b.Name)
			res.Backends[name] = serverVersion(b.Version)
		}
	}
	return res, nil
}

// serverVersion renders 160003 as "16.3".
func serverVersion(num int) string {
	major := num / 10000
	if major >= 10 {
		return fmt.Sprintf("%d.%d", major, (num%10000)/100)
	}
	return fmt.Sprintf("%d.%d.%d", num/10000, (num%10000)/100, num%100)
}

var _ = strings.TrimSpace
var _ = time.Now

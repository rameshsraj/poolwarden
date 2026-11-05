// Package collector builds one model.Snapshot by querying every data source
// in parallel, each under its own deadline (design 01 §16). A failed source
// is marked and never blocks the others; when pgpool's SQL interface is down
// the snapshot is produced in degraded mode from pcp and direct probes.
package collector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/smarsh/poolwarden/internal/backendsql"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/pgpoolsql"
	"github.com/smarsh/poolwarden/internal/remote"
)

// Sessions provides a remote.Session per inventory host (*remote.Pool).
type Sessions interface {
	Session(ctx context.Context, h config.Host) (remote.Session, error)
}

type Collector struct {
	Cfg      *config.Config
	Pgpool   pgpoolsql.Dialer
	Backends backendsql.Prober
	PCP      *pcp.PCP
	Remote   Sessions
	Clock    clock.Clock
}

func (c *Collector) now() time.Time {
	if c.Clock != nil {
		return c.Clock.Now()
	}
	return time.Now()
}

// settingsFallback is the short param list tried when SHOW ALL fails.
var settingsFallback = []string{
	"delegate_ip", "failover_command", "follow_primary_command",
	"recovery_1st_stage_command", "num_init_children", "max_pool",
	"enable_consensus_with_half_votes", "failover_when_quorum_exists",
	"failover_require_consensus", "use_watchdog", "wd_escalation_command",
	"wd_de_escalation_command", "health_check_period", "log_line_prefix",
}

// Collect gathers the whole snapshot. It returns within Cfg.Timeout+1s even
// if an adapter ignores context cancellation — the abandoned goroutine still
// exits once its context fires (fakes must honour ctx).
func (c *Collector) Collect(ctx context.Context) *model.Snapshot {
	start := c.now()
	s := &model.Snapshot{Taken: start, Settings: map[string]string{}}
	to := c.Cfg.Timeout
	if to <= 0 {
		to = 3 * time.Second
	}
	srcCtx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	hardDeadline := start.Add(to + time.Second)

	var wg sync.WaitGroup
	var mu sync.Mutex
	addErr := func(src, host, msg string) {
		mu.Lock()
		s.Errors = append(s.Errors, model.SourceError{Source: src, Host: host, Msg: msg})
		mu.Unlock()
	}

	// Wave 1: pgpool SQL and watchdog in parallel.
	var pgErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		pgErr = c.collectPgpool(srcCtx, s, &mu, addErr)
	}()
	go func() {
		defer wg.Done()
		s.Watchdog = c.collectWatchdog(srcCtx, addErr)
	}()
	waitOrDeadline(&wg, hardDeadline)

	// Wave 2: node list (pcp when degraded), backend probes, VIP checks.
	if pgErr != nil || !s.Pgpool.Reachable {
		s.Degraded = true
		if errors.Is(pgErr, context.DeadlineExceeded) {
			s.DegradedReason = fmt.Sprintf("pgpool SQL timeout after %s: %v", to, pgErr)
		} else {
			s.DegradedReason = fmt.Sprintf("pgpool SQL unreachable: %v", pgErr)
		}
		addErr("pgpool", c.Cfg.Pgpool.Host, s.DegradedReason)
		nodes := c.collectNodesPCP(srcCtx, addErr)
		s.Pgpool.Nodes = nodes
	}
	targets := backendTargets(c.Cfg, s.Pgpool.Nodes)
	var bmu sync.Mutex
	wg2 := sync.WaitGroup{}
	for _, t := range targets {
		wg2.Add(1)
		go func(t backendTarget) {
			defer wg2.Done()
			b := c.Backends.Probe(srcCtx, backendsql.Addr{
				Host: t.host, Port: t.port, User: c.cfgUser(), DB: c.cfgDB(),
			})
			b.Name = t.name
			b.NodeID = t.nodeID
			b.Addresses = []string{t.host, t.name, b.Host}
			if h, ok := c.Cfg.HostByName(t.name); ok {
				b.Addresses = append(b.Addresses, h.Address, h.Name)
			}
			if ips, err := net.DefaultResolver.LookupHost(ctx, b.Host); err == nil {
				b.Addresses = append(b.Addresses, ips...)
			}
			bmu.Lock()
			s.Backends = append(s.Backends, b)
			bmu.Unlock()
		}(t)
	}
	for _, h := range c.Cfg.PgpoolHosts() {
		wg2.Add(1)
		go func(h config.Host) {
			defer wg2.Done()
			vc := c.checkVIP(srcCtx, h, vipAddress(s))
			mu.Lock()
			s.VIP = append(s.VIP, vc)
			mu.Unlock()
		}(h)
	}
	waitOrDeadline(&wg2, hardDeadline)

	s.Duration = c.now().Sub(start)
	return s
}

func (c *Collector) cfgUser() string { return c.Cfg.Pgpool.User }
func (c *Collector) cfgDB() string   { return c.Cfg.Pgpool.DBName }

// waitOrDeadline waits for wg or the hard deadline, whichever is first.
func waitOrDeadline(wg *sync.WaitGroup, deadline time.Time) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-done:
		case <-time.After(10 * time.Millisecond):
		}
		return
	}
	t := time.NewTimer(remaining)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

func (c *Collector) collectPgpool(ctx context.Context, s *model.Snapshot, mu *sync.Mutex, addErr func(string, string, string)) error {
	cli, err := c.Pgpool.Dial(ctx, c.Cfg.Pgpool)
	if err != nil {
		mu.Lock()
		s.Pgpool.Err = err.Error()
		mu.Unlock()
		return err
	}
	defer cli.Close(ctx)
	s.Pgpool.Reachable = true
	var ver string
	if v, err := cli.Version(ctx); err == nil {
		ver = v
	}
	// each SHOW query is best-effort; failures are recorded.
	if n, err := cli.PoolNodes(ctx); err != nil {
		addErr("pool_nodes", c.Cfg.Pgpool.Host, err.Error())
	} else {
		s.Pgpool.Nodes = n
	}
	if p, err := cli.PoolProcesses(ctx); err != nil {
		addErr("pool_processes", c.Cfg.Pgpool.Host, err.Error())
	} else {
		s.Pgpool.Processes = p
	}
	if p, err := cli.PoolPools(ctx); err != nil {
		addErr("pool_pools", c.Cfg.Pgpool.Host, err.Error())
	} else {
		s.Pgpool.Pools = p
	}
	if h, err := cli.HealthCheckStats(ctx); err != nil {
		addErr("health_check_stats", c.Cfg.Pgpool.Host, err.Error())
	} else {
		s.Pgpool.HealthStats = h
	}
	if b, err := cli.BackendStats(ctx); err != nil {
		addErr("backend_stats", c.Cfg.Pgpool.Host, err.Error())
	} else {
		s.Pgpool.BackendStats = b
	}
	if m, err := cli.ShowAll(ctx); err == nil {
		s.Settings = m
	} else {
		for _, k := range settingsFallback {
			if v, err := cli.Show(ctx, k); err == nil {
				s.Settings[k] = v
			}
		}
	}
	s.Pgpool.Version = ver
	return nil
}

func (c *Collector) collectWatchdog(ctx context.Context, addErr func(string, string, string)) model.Watchdog {
	if c.PCP == nil {
		return model.Watchdog{Err: "no pcp configured"}
	}
	w, _, err := c.PCP.WatchdogInfo(ctx)
	if err != nil {
		addErr("watchdog", "", err.Error())
		return model.Watchdog{Err: err.Error()}
	}
	w.Available = true
	return w
}

func (c *Collector) collectNodesPCP(ctx context.Context, addErr func(string, string, string)) []model.PoolNode {
	if c.PCP == nil {
		return nil
	}
	nodes, _, err := c.PCP.NodeInfoAll(ctx)
	if err != nil {
		addErr("pcp_node_info", "", err.Error())
		return nil
	}
	for i := range nodes {
		nodes[i].ID = i
	}
	return nodes
}

type backendTarget struct {
	name   string
	host   string
	port   int
	nodeID int
}

// backendTargets resolves probe targets: pool nodes first, else the PG
// inventory. Node hostnames may be names or addresses from the inventory.
func backendTargets(cfg *config.Config, nodes []model.PoolNode) []backendTarget {
	var out []backendTarget
	for _, n := range nodes {
		t := backendTarget{host: n.Hostname, port: n.Port, nodeID: n.ID}
		if h, ok := cfg.HostByName(n.Hostname); ok {
			t.name = h.Name
			t.host = h.Address
		} else {
			for _, h := range cfg.Hosts {
				if h.Address == n.Hostname {
					t.name = h.Name
				}
			}
		}
		if t.port == 0 {
			t.port = 5432
		}
		out = append(out, t)
	}
	if len(out) > 0 {
		return out
	}
	for _, h := range cfg.PGHosts() {
		port := 5432
		if h.PG != nil && h.PG.Port != 0 {
			port = h.PG.Port
		}
		out = append(out, backendTarget{name: h.Name, host: h.Address, port: port, nodeID: -1})
	}
	return out
}

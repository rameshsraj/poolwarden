// Package preflight holds the pure GO/WARN/NO-GO checks run before every
// mutating command (design 01 §14, 02 §4.1).
package preflight

import (
	"fmt"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
)

// Check is one preflight check result.
type Check = model.Check

// Result is the model's PreflightResult (Action, Verdict, Checks).
type Result = model.PreflightResult

// Input is everything the checks need.
type Input struct {
	Snap     *model.Snapshot
	Cfg      *config.Config
	Findings []model.Finding
	Now      time.Time
	// RecentFailover is true when a FAILOVER-tagged event appeared in the
	// last 5 minutes (the caller derives it from the error collector).
	RecentFailover bool
	// ScriptExists checks a path on a host (recover only; nil = uncheckable).
	ScriptExists func(host config.Host, path string) (bool, error)
}

func on(s string) bool { return s == "on" || s == "true" || s == "yes" || s == "1" }

func set(s *model.Snapshot, k string) string { return s.Settings[k] }

func nodeByID(s *model.Snapshot, id int) *model.PoolNode {
	for i := range s.Pgpool.Nodes {
		if s.Pgpool.Nodes[i].ID == id {
			return &s.Pgpool.Nodes[i]
		}
	}
	return nil
}

func backendFor(s *model.Snapshot, cfg *config.Config, n *model.PoolNode) *model.Backend {
	for i := range s.Backends {
		b := &s.Backends[i]
		if b.NodeID == n.ID {
			return b
		}
		if b.Port == n.Port && (b.Host == n.Hostname || b.Name == n.Hostname) {
			return b
		}
		if h, ok := cfg.HostByName(n.Hostname); ok && b.Host == h.Address && b.Port == n.Port {
			return b
		}
	}
	return nil
}

func primaryOf(s *model.Snapshot) *model.Backend {
	for i := range s.Backends {
		if s.Backends[i].Reachable && !s.Backends[i].InRecovery {
			return &s.Backends[i]
		}
	}
	return nil
}

func ok(name, detail string) Check   { return Check{Name: name, Result: "OK", Detail: detail} }
func warn(name, detail string) Check { return Check{Name: name, Result: "WARN", Detail: detail} }
func nogo(name, detail string) Check { return Check{Name: name, Result: "NO-GO", Detail: detail} }

func verdict(checks []Check) string {
	v := "GO"
	for _, c := range checks {
		if c.Result == "NO-GO" {
			return "NO-GO"
		}
		if c.Result == "WARN" {
			v = "WARN"
		}
	}
	return v
}

// Promote is the switchover preflight (01 §14 row 1) or the --pgpool-only
// variant (row 2, T1 UNVERIFIED).
func Promote(in Input, targetID int, pgpoolOnly bool) *Result {
	if pgpoolOnly {
		var checks []Check
		target := backendByNodeID(in, targetID)
		if target == nil {
			checks = append(checks, nogo("target", fmt.Sprintf("node %d not found", targetID)))
		} else {
			if !target.InRecovery {
				checks = append(checks, ok("target", "target reports not-in-recovery"))
			} else {
				checks = append(checks, nogo("target", "target still in recovery"))
			}
			others := 0
			for _, b := range in.Snap.Backends {
				if b.Reachable && !b.InRecovery && b.NodeID != targetID {
					others++
				}
			}
			if others == 0 {
				checks = append(checks, ok("no other primary", "no other node reports not-in-recovery"))
			} else {
				checks = append(checks, nogo("no other primary",
					fmt.Sprintf("%d other node(s) report not-in-recovery", others)))
			}
		}
		return &Result{Action: "promote --pgpool-only (UNVERIFIED: T1)",
			Verdict: verdict(checks), Checks: checks}
	}
	var checks []Check
	n := nodeByID(in.Snap, targetID)
	var b *model.Backend
	if n != nil {
		b = backendFor(in.Snap, in.Cfg, n)
	}
	if n == nil {
		checks = append(checks, nogo("target up & standby", fmt.Sprintf("node %d not found", targetID)))
	} else {
		up := n.Status == "up" && (b == nil || b.Reachable)
		standby := n.Role == "standby" || (b != nil && b.InRecovery)
		p := primaryOf(in.Snap)
		if p != nil && b != nil && b.Host == p.Host {
			checks = append(checks, nogo("target up & standby", "target is the current primary"))
		} else if !up || !standby {
			checks = append(checks, nogo("target up & standby",
				fmt.Sprintf("status=%s role=%s", n.Status, n.Role)))
		} else {
			checks = append(checks, ok("target up & standby", "target up & standby"))
		}
	}
	if b != nil && b.ReplayDelaySec >= 0 {
		limit := in.Cfg.Lag.WarnSeconds
		if b.ReplayDelaySec < float64(limit) {
			checks = append(checks, ok("replay delay",
				fmt.Sprintf("replay delay %ds < %s", int(b.ReplayDelaySec), fmtSecs(limit))))
		} else {
			checks = append(checks, nogo("replay delay",
				fmt.Sprintf("replay delay %ds ≥ %s", int(b.ReplayDelaySec), fmtSecs(limit))))
		}
	}
	if b != nil {
		best := true
		for _, o := range in.Snap.Backends {
			if o.Reachable && o.InRecovery && o.NodeID != targetID {
				if lsnLess(b.CurrentLSN, o.CurrentLSN) {
					best = false
				}
			}
		}
		if best {
			checks = append(checks, ok("most advanced LSN among standbys",
				fmt.Sprintf("target replay %s", orDash(b.CurrentLSN))))
		} else {
			checks = append(checks, nogo("most advanced LSN among standbys",
				fmt.Sprintf("target replay %s behind another standby", orDash(b.CurrentLSN))))
		}
	}
	if on(set(in.Snap, "use_watchdog")) {
		if in.Snap.Watchdog.Available &&
			(in.Snap.Watchdog.QuorumState == "QUORUM EXIST" ||
				in.Snap.Watchdog.QuorumState == "IS ON THE EDGE") {
			checks = append(checks, ok("watchdog quorum", in.Snap.Watchdog.QuorumState))
		} else {
			checks = append(checks, nogo("watchdog quorum",
				fmt.Sprintf("quorum=%s", orDash(in.Snap.Watchdog.QuorumState))))
		}
	} else {
		checks = append(checks, ok("watchdog quorum", "watchdog off"))
	}
	if set(in.Snap, "failover_command") != "" && set(in.Snap, "follow_primary_command") != "" {
		checks = append(checks, ok("failover_command / follow_primary_command set", "both set"))
	} else {
		checks = append(checks, nogo("failover_command / follow_primary_command set", "missing"))
	}
	prims := 0
	for _, bb := range in.Snap.Backends {
		if bb.Reachable && !bb.InRecovery {
			prims++
		}
	}
	if prims > 1 {
		checks = append(checks, nogo("no split-brain", fmt.Sprintf("%d primaries", prims)))
	} else {
		checks = append(checks, ok("no split-brain", "one primary"))
	}
	if in.RecentFailover {
		checks = append(checks, nogo("no other failover in progress", "failover event < 5m ago"))
	} else {
		quar := false
		for _, nd := range in.Snap.Pgpool.Nodes {
			if nd.Status == "quarantine" {
				quar = true
			}
		}
		if quar {
			checks = append(checks, nogo("no other failover in progress", "a node is quarantined"))
		} else {
			checks = append(checks, ok("no other failover in progress", "none"))
		}
	}
	return &Result{Action: "promote", Verdict: verdict(checks), Checks: checks}
}

func backendByNodeID(in Input, id int) *model.Backend {
	for i := range in.Snap.Backends {
		if in.Snap.Backends[i].NodeID == id {
			return &in.Snap.Backends[i]
		}
	}
	return nil
}

// Detach checks per 01 §14.
func Detach(in Input, targetID int, gracefully bool) *Result {
	var checks []Check
	n := nodeByID(in.Snap, targetID)
	if n == nil {
		checks = append(checks, nogo("target", fmt.Sprintf("node %d not found", targetID)))
	} else if n.Status == "down" {
		checks = append(checks, nogo("target", "already down"))
	} else {
		others := 0
		var totalW float64
		for _, nd := range in.Snap.Pgpool.Nodes {
			if nd.ID != targetID && nd.Status == "up" {
				b := backendFor(in.Snap, in.Cfg, &nd)
				if b == nil || b.Reachable {
					others++
					totalW += nd.LBWeight
				}
			}
		}
		if others == 0 {
			checks = append(checks, nogo("another healthy node remains", "no other up node"))
		} else {
			checks = append(checks, ok("another healthy node remains",
				fmt.Sprintf("%d healthy node(s) remain", others)))
			if totalW > 0 {
				pct := int(n.LBWeight / (totalW + n.LBWeight) * 100)
				checks = append(checks, ok("LB capacity lost",
					fmt.Sprintf("LB capacity lost %d%%", pct)))
			}
		}
		if n.Role == "primary" {
			checks = append(checks, warn("failover", "THIS TRIGGERS FAILOVER"))
		}
	}
	if on(set(in.Snap, "use_watchdog")) &&
		!(in.Snap.Watchdog.Available &&
			(in.Snap.Watchdog.QuorumState == "QUORUM EXIST" || in.Snap.Watchdog.QuorumState == "IS ON THE EDGE")) {
		checks = append(checks, nogo("watchdog quorum", orDash(in.Snap.Watchdog.QuorumState)))
	}
	return &Result{Action: "detach", Verdict: verdict(checks), Checks: checks}
}

// Attach checks per 01 §14.
func Attach(in Input, targetID int) *Result {
	var checks []Check
	n := nodeByID(in.Snap, targetID)
	b := backendByNodeID(in, targetID)
	if n == nil {
		checks = append(checks, nogo("target", fmt.Sprintf("node %d not found", targetID)))
	} else if n.Status == "up" {
		checks = append(checks, nogo("target", "already attached"))
	} else {
		if b == nil || !b.Reachable {
			checks = append(checks, nogo("node reachable", "direct connect fails"))
		} else {
			checks = append(checks, ok("node reachable", "connects"))
			if !b.InRecovery {
				checks = append(checks, nogo("in recovery", "not a standby"))
			} else {
				checks = append(checks, ok("in recovery", "in recovery"))
			}
			p := primaryOf(in.Snap)
			// streaming implies timeline agreement — check it first.
			streaming := false
			if p != nil {
				for _, st := range p.Standbys {
					if model.StandbyMatches(st, *b) {
						streaming = true
					}
				}
			}
			switch {
			case streaming:
				checks = append(checks, ok("same timeline",
					fmt.Sprintf("streaming from primary (tl %d)", p.Timeline)))
			case p != nil && b.Timeline != p.Timeline:
				checks = append(checks, nogo("same timeline",
					fmt.Sprintf("t%d vs primary t%d", b.Timeline, p.Timeline)))
			default:
				checks = append(checks, ok("same timeline", fmt.Sprintf("timeline %d", b.Timeline)))
			}
			if !b.WALReceiver {
				checks = append(checks, nogo("streaming", "no WAL receiver"))
			} else {
				found := streaming
				if found {
					checks = append(checks, ok("streaming", "in pg_stat_replication"))
				} else {
					checks = append(checks, nogo("streaming", "not in pg_stat_replication"))
				}
			}
		}
	}
	return &Result{Action: "attach", Verdict: verdict(checks), Checks: checks}
}

// Recover checks per 01 §14.
func Recover(in Input, targetID int) *Result {
	var checks []Check
	cmd := set(in.Snap, "recovery_1st_stage_command")
	if cmd == "" {
		checks = append(checks, nogo("recovery_1st_stage_command set", "empty"))
	} else {
		checks = append(checks, ok("recovery_1st_stage_command set", cmd))
		var host *config.Host
		if p := primaryOf(in.Snap); p != nil {
			for i := range in.Cfg.Hosts {
				if in.Cfg.Hosts[i].Address == p.Host || in.Cfg.Hosts[i].Name == p.Name {
					host = &in.Cfg.Hosts[i]
				}
			}
		}
		if host != nil && host.PG != nil && in.ScriptExists != nil {
			path := host.PG.DataDir + "/" + cmd
			if ex, err := in.ScriptExists(*host, path); err != nil {
				checks = append(checks, warn("script present on primary",
					fmt.Sprintf("cannot check %s: %v", path, err)))
			} else if !ex {
				checks = append(checks, nogo("script present on primary", path+" missing"))
			} else {
				checks = append(checks, ok("script present on primary", path))
			}
		} else {
			checks = append(checks, warn("script present on primary", "cannot check"))
		}
	}
	n := nodeByID(in.Snap, targetID)
	if n == nil {
		checks = append(checks, nogo("target down", fmt.Sprintf("node %d not found", targetID)))
	} else if n.Status != "down" {
		checks = append(checks, nogo("target down", fmt.Sprintf("status=%s", n.Status)))
	} else {
		checks = append(checks, ok("target down", "down"))
	}
	return &Result{Action: "recover", Verdict: verdict(checks), Checks: checks}
}

// Reload is trivially GO when pgpool answers.
func Reload(in Input) *Result {
	c := ok("pgpool reachable", "PGPOOL SHOW ALL answers")
	if !in.Snap.Pgpool.Reachable {
		c = nogo("pgpool reachable", orDash(in.Snap.Pgpool.Err))
	}
	return &Result{Action: "reload", Verdict: verdict([]Check{c}), Checks: []Check{c}}
}

func lsnLess(a, b string) bool {
	av, aok := lsnVal(a)
	bv, bok := lsnVal(b)
	if !aok || !bok {
		return false
	}
	return av < bv
}

func lsnVal(s string) (uint64, bool) {
	var hi, lo uint64
	if _, err := fmt.Sscanf(s, "%X/%X", &hi, &lo); err == nil {
		return hi<<32 | lo, true
	}
	return 0, false
}

func fmtSecs(sec int) string {
	if sec >= 60 {
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%ds", sec)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

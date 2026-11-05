package diagnose

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/model"
)

func subj(b *model.Backend) string {
	if b.Name != "" {
		return b.Name
	}
	return b.Host
}

func reachable(s *model.Snapshot) (out []*model.Backend) {
	for i := range s.Backends {
		if s.Backends[i].Reachable {
			out = append(out, &s.Backends[i])
		}
	}
	return
}

func primaries(s *model.Snapshot) (out []*model.Backend) {
	for _, b := range reachable(s) {
		if !b.InRecovery {
			out = append(out, b)
		}
	}
	return
}

func standbys(s *model.Snapshot) (out []*model.Backend) {
	for _, b := range reachable(s) {
		if b.InRecovery {
			out = append(out, b)
		}
	}
	return
}

func setting(s *model.Snapshot, k string) string { return s.Settings[k] }

func on(s string) bool { return s == "on" || s == "true" || s == "yes" || s == "1" }

func f(sev, subject, cause string, ev []string, next []string, causes []string) model.Finding {
	return model.Finding{Severity: sev, Subject: subject, Cause: cause, Causes: causes,
		Evidence: ev, NextCommands: next}
}

// Catalogue is every rule of design 01 §12, in table order.
var Catalogue = []Rule{
	{ID: "SPLIT_BRAIN", Severity: model.SevCrit,
		Description: "more than one backend reports itself not in recovery",
		DocRef:      "postgresql: streaming replication",
		Fn: func(in Input) []model.Finding {
			ps := primaries(in.Snap)
			if len(ps) <= 1 {
				return nil
			}
			var hosts, ev []string
			for _, b := range ps {
				hosts = append(hosts, subj(b))
				ev = append(ev, fmt.Sprintf("pg_is_in_recovery()=false on %s:%d", subj(b), b.Port))
			}
			sort.Strings(hosts)
			return []model.Finding{f(model.SevCrit, "cluster",
				fmt.Sprintf("split-brain: %d backends are primary (%s)", len(ps), strings.Join(hosts, ", ")),
				ev,
				[]string{"poolwarden nodes", "poolwarden logs --classify --since 30m"},
				[]string{"failed switchover left two primaries", "a standby was promoted manually outside pgpool", "replication was re-pointed by hand"})}
		}},
	{ID: "ROLE_MISMATCH", Severity: model.SevCrit,
		Description: "pgpool's role for a node differs from the role the backend reports",
		DocRef:      "pgpool: SHOW POOL_NODES",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, n := range in.Snap.Pgpool.Nodes {
				b := backendFor(in, n)
				if b == nil || !b.Reachable || n.Role == "" {
					continue
				}
				real := "primary"
				if b.InRecovery {
					real = "standby"
				}
				if real != n.Role {
					out = append(out, f(model.SevCrit, hostOf(in, n),
						fmt.Sprintf("pgpool says %s but %s reports %s", n.Role, subj(b), real),
						[]string{fmt.Sprintf("SHOW POOL_NODES role=%s", n.Role),
							fmt.Sprintf("pg_is_in_recovery()=%v on %s", b.InRecovery, subj(b))},
						[]string{"poolwarden nodes " + itoa(n.ID), "poolwarden diagnose"},
						[]string{"promotion happened outside poolwarden and pgpool was not told", "pgpool's failover_command did not run to completion"}))
				}
			}
			return out
		}},
	{ID: "NODE_DOWN", Severity: model.SevWarn,
		Description: "pgpool marks a node down or quarantine (CRIT when it is the primary)",
		DocRef:      "pgpool: pcp_attach_node",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, n := range in.Snap.Pgpool.Nodes {
				if n.Status != "down" && n.Status != "quarantine" {
					continue
				}
				b := backendFor(in, n)
				sev := model.SevWarn
				if n.Role == "primary" {
					sev = model.SevCrit
				}
				ev := []string{fmt.Sprintf("SHOW POOL_NODES status=%s", n.Status)}
				next := []string{"poolwarden explain NODE_DOWN:" + hostOf(in, n)}
				if n.Status == "down" && b != nil && b.Reachable && b.InRecovery {
					next = append(next, fmt.Sprintf("poolwarden attach %d", n.ID))
				}
				out = append(out, f(sev, hostOf(in, n),
					fmt.Sprintf("node %d (%s) is %s in pgpool", n.ID, hostOf(in, n), n.Status),
					ev, next,
					[]string{"backend connection failure (network or PostgreSQL down)", "health check failed enough times to mark the node down", "manual detach"}))
			}
			return out
		}},
	{ID: "STATUS_DISAGREE", Severity: model.SevWarn,
		Description: "pgpool says a node is up but the direct connection fails, or the reverse",
		DocRef:      "pgpool: health check",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, n := range in.Snap.Pgpool.Nodes {
				b := backendFor(in, n)
				if b == nil {
					continue
				}
				var ev []string
				var what string
				if n.Status == "up" && !b.Reachable {
					what = "pgpool up / direct down"
					ev = []string{"SHOW POOL_NODES status=up",
						fmt.Sprintf("direct connect to %s:%d failed: %s", subj(b), b.Port, b.Err)}
				} else if n.Status == "down" && b.Reachable {
					what = "pgpool down / direct up"
					ev = []string{"SHOW POOL_NODES status=down",
						fmt.Sprintf("direct connect to %s:%d succeeds", subj(b), b.Port)}
				} else {
					continue
				}
				out = append(out, f(model.SevWarn, hostOf(in, n), what, ev,
					[]string{"poolwarden nodes " + itoa(n.ID), "poolwarden errors --source conn --host " + hostOf(in, n)},
					[]string{"pgpool connects via a different path (socket vs TCP) that is failing", "the backend refuses pgpool's health checks but accepts ours"}))
			}
			return out
		}},
	{ID: "REPL_LAG", Severity: model.SevWarn,
		Description: "replay delay above warn_seconds (CRIT above crit_seconds)",
		DocRef:      "postgresql: pg_last_xact_replay_timestamp",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, b := range standbys(in.Snap) {
				if b.ReplayDelaySec < 0 {
					continue
				}
				var sev string
				d := time.Duration(b.ReplayDelaySec * float64(time.Second))
				switch {
				case b.ReplayDelaySec >= float64(in.Cfg.Lag.CritSeconds):
					sev = model.SevCrit
				case b.ReplayDelaySec >= float64(in.Cfg.Lag.WarnSeconds):
					sev = model.SevWarn
				default:
					continue
				}
				out = append(out, f(sev, subj(b),
					fmt.Sprintf("replay delay %s on %s", fmtDur(d), subj(b)),
					[]string{fmt.Sprintf("replay delay %s ≥ %s (D13)", fmtDur(d), fmtDur(time.Duration(in.Cfg.Lag.WarnSeconds)*time.Second)),
						fmt.Sprintf("pg_last_xact_replay_timestamp on %s", subj(b))},
					[]string{"poolwarden lag", "poolwarden forecast", "poolwarden explain REPL_LAG:" + subj(b)},
					[]string{"standby cannot replay fast enough (slow disk or queries)", "long-running query on the standby holds back replay", "primary produces WAL faster than the link carries it"}))
			}
			return out
		}},
	{ID: "REPL_LAG_GROWING", Severity: model.SevWarn,
		Description: "lag trend is rising and reaches the crit threshold within 15 minutes",
		DocRef:      "postgresql: streaming replication",
		Fn: func(in Input) []model.Finding {
			if in.Trends == nil {
				return nil
			}
			var out []model.Finding
			crit := float64(in.Cfg.Lag.CritSeconds)
			for host, tr := range in.Trends.ReplayDelay {
				scale := tr.Last
				if crit > scale {
					scale = crit
				}
				if forecast.Classify(tr, scale) != "rising" {
					continue
				}
				eta, ok := forecast.ETA(tr, crit)
				if !ok || eta > 15*time.Minute {
					continue
				}
				out = append(out, f(model.SevWarn, host,
					fmt.Sprintf("lag rising on %s, crit in %s", host, fmtDur(eta)),
					[]string{fmt.Sprintf("replay delay slope %+.1fs/min R²=%.2f ETA→crit %s", tr.Slope*60, tr.R2, fmtDur(eta))},
					[]string{"poolwarden lag --history 1h", "poolwarden forecast"},
					[]string{"lag is growing, not transient", "replay rate is slower than WAL production and will breach the threshold soon"}))
			}
			return out
		}},
	{ID: "WAL_RECEIVER_MISSING", Severity: model.SevCrit,
		Description: "a standby in recovery has no WAL receiver process",
		DocRef:      "postgresql: pg_stat_wal_receiver",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, b := range standbys(in.Snap) {
				if !b.WALReceiver {
					out = append(out, f(model.SevCrit, subj(b),
						fmt.Sprintf("%s is in recovery but has no WAL receiver", subj(b)),
						[]string{fmt.Sprintf("pg_stat_wal_receiver empty on %s", subj(b))},
						[]string{"poolwarden errors --source replication --host " + subj(b), "poolwarden logs --host " + subj(b) + " --component pg"},
						[]string{"WAL receiver crashed and cannot reconnect", "primary conninfo wrong after a failover", "network partition between standby and primary"}))
				}
			}
			return out
		}},
	{ID: "REPLAY_PAUSED", Severity: model.SevWarn,
		Description: "pg_is_wal_replay_paused() is true on a standby",
		DocRef:      "postgresql: pg_is_wal_replay_paused",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, b := range standbys(in.Snap) {
				if b.ReplayPaused {
					out = append(out, f(model.SevWarn, subj(b),
						fmt.Sprintf("WAL replay is paused on %s", subj(b)),
						[]string{fmt.Sprintf("pg_is_wal_replay_paused()=true on %s", subj(b))},
						[]string{"poolwarden nodes " + itoa(b.NodeID)},
						[]string{"someone ran pg_wal_replay_pause()", "a hot-standby conflict was paused on purpose"}))
				}
			}
			return out
		}},
	{ID: "STANDBY_NOT_STREAMING", Severity: model.SevCrit,
		Description: "a reachable standby is missing from pg_stat_replication on the primary",
		DocRef:      "postgresql: pg_stat_replication",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			p := primaryOne(in.Snap)
			if p == nil {
				return nil
			}
			for _, b := range standbys(in.Snap) {
				found := false
				for _, st := range p.Standbys {
					if model.StandbyMatches(st, *b) {
						found = true
					}
				}
				if !found {
					out = append(out, f(model.SevCrit, subj(b),
						fmt.Sprintf("%s missing from pg_stat_replication on %s", subj(b), subj(p)),
						[]string{fmt.Sprintf("pg_stat_replication on %s has no row for %s", subj(p), subj(b))},
						[]string{"poolwarden errors --source replication --host " + subj(b), "poolwarden attach " + itoa(b.NodeID)},
						[]string{"WAL receiver not running (see WAL_RECEIVER_MISSING)", "replication slot removed", "standby re-pointed to another primary"}))
				}
			}
			return out
		}},
	{ID: "SLOT_INACTIVE", Severity: model.SevWarn,
		Description: "inactive replication slot retaining WAL; CRIT when wal_status is lost or unreserved",
		DocRef:      "postgresql: pg_replication_slots",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, b := range reachable(in.Snap) {
				for _, sl := range b.Slots {
					if sl.Active {
						continue
					}
					sev := model.SevWarn
					why := "inactive slot retaining WAL"
					if sl.WALStatus == "lost" || sl.WALStatus == "unreserved" {
						sev = model.SevCrit
						why = "slot " + sl.WALStatus + ": WAL already removed"
					}
					out = append(out, f(sev, sl.Name,
						fmt.Sprintf("replication slot %s on %s: %s", sl.Name, subj(b), why),
						[]string{fmt.Sprintf("pg_replication_slots %s active=%v wal_status=%s retained=%dB on %s",
							sl.Name, sl.Active, sl.WALStatus, sl.RetainedBytes, subj(b))},
						[]string{"poolwarden slots", "poolwarden explain SLOT_INACTIVE:" + sl.Name,
							`poolwarden sql primary "select * from pg_replication_slots"`},
						[]string{"replica permanently removed or offline for a long time", "slot created for a one-off job and forgotten"}))
				}
			}
			return out
		}},
	{ID: "WAL_DISK_ETA", Severity: model.SevWarn,
		Description: "forecast: pg_wal fills within 24h (CRIT within 2h)",
		DocRef:      "postgresql: WAL retention",
		Fn: func(in Input) []model.Finding {
			if in.Trends == nil || int64(in.Cfg.WALVolumeBytes) <= 0 || in.Trends.RetainedWAL.N == 0 {
				return nil
			}
			eta, ok := forecast.ETA(in.Trends.RetainedWAL, float64(in.Cfg.WALVolumeBytes))
			if !ok || eta > 24*time.Hour {
				return nil
			}
			sev := model.SevWarn
			if eta < 2*time.Hour {
				sev = model.SevCrit
			}
			return []model.Finding{f(sev, "cluster",
				fmt.Sprintf("pg_wal fills in %s", fmtDur(eta)),
				[]string{fmt.Sprintf("retained WAL %s rising %+.1fB/min R²=%.2f, volume %dB, ETA %s",
					humanBytes(int64(in.Trends.RetainedWAL.Last)), in.Trends.RetainedWAL.Slope*60,
					in.Trends.RetainedWAL.R2, int64(in.Cfg.WALVolumeBytes), fmtDur(eta))},
				[]string{"poolwarden slots", "poolwarden forecast"},
				[]string{"inactive slot retaining WAL", "replica fallen behind (see REPL_LAG)", "archive/backup agent stopped consuming WAL"})}
		}},
	{ID: "XMIN_HOLDBACK", Severity: model.SevWarn,
		Description: "transaction over 5 minutes or idle-in-transaction on the primary",
		DocRef:      "postgresql: pg_stat_activity",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, p := range primaries(in.Snap) {
				if p.LongTxns > 0 {
					out = append(out, f(model.SevWarn, subj(p),
						fmt.Sprintf("%d transaction(s) older than 5m on %s", p.LongTxns, subj(p)),
						[]string{fmt.Sprintf("oldest xact %s on %s", fmtDur(time.Duration(p.OldestXactSec*float64(time.Second))), subj(p))},
						[]string{"poolwarden conns", `poolwarden sql primary "select pid, xact_start, state, query from pg_stat_activity order by xact_start"`},
						[]string{"a client holds an open transaction and blocks vacuum/replication slots", "forgotten idle-in-transaction session"}))
				}
			}
			return out
		}},
	{ID: "REPL_CONFLICTS", Severity: model.SevWarn,
		Description: "hot-standby conflict counter increased since the last sample",
		DocRef:      "postgresql: pg_stat_database_conflicts",
		Fn: func(in Input) []model.Finding {
			if in.Prev == nil {
				return nil
			}
			var out []model.Finding
			for _, b := range standbys(in.Snap) {
				prev := prevNode(in, b.Name)
				if prev == nil || prev.Conflicts < 0 {
					continue
				}
				if b.Conflicts > prev.Conflicts {
					out = append(out, f(model.SevWarn, subj(b),
						fmt.Sprintf("replication conflicts rising on %s (+%d)", subj(b), b.Conflicts-prev.Conflicts),
						[]string{fmt.Sprintf("pg_stat_database_conflicts %d → %d on %s", prev.Conflicts, b.Conflicts, subj(b))},
						[]string{"poolwarden errors --source replication --host " + subj(b)},
						[]string{"standby queries cancelled by replayed changes", "long read queries fighting WAL replay"}))
				}
			}
			return out
		}},
	{ID: "HEALTHCHECK_FAILING", Severity: model.SevWarn,
		Description: "health-check fails rising, or no successful check within 3×period×(retries+1)",
		DocRef:      "pgpool: SHOW POOL_HEALTH_CHECK_STATS",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			period := atof(setting(in.Snap, "health_check_period"), 10)
			retries := atof(setting(in.Snap, "health_check_max_retries"), 3)
			budget := period * (retries + 1) * 3
			for _, h := range in.Snap.Pgpool.HealthStats {
				var prev *struct{ F int64 }
				if in.Prev != nil {
					for i := range in.Prev.Nodes {
						if in.Prev.Nodes[i].NodeID == h.NodeID {
							pf := struct{ F int64 }{in.Prev.Nodes[i].HealthFail}
							prev = &pf
						}
					}
				}
				var ev []string
				sev := ""
				if prev != nil && prev.F >= 0 && h.FailCount > prev.F {
					sev = model.SevWarn
					ev = append(ev, fmt.Sprintf("fail_count %d → %d since last sample", prev.F, h.FailCount))
				}
				if t := parseTS(h.LastSuccessfulHealthCheck); !t.IsZero() && !in.Now.IsZero() &&
					in.Now.Sub(t) > time.Duration(budget)*time.Second {
					sev = model.SevCrit
					ev = append(ev, fmt.Sprintf("last successful check %s ago (budget %ds)",
						fmtDur(in.Now.Sub(t)), int64(budget)))
				}
				if sev == "" {
					continue
				}
				out = append(out, f(sev, hostName(in, h.Hostname),
					fmt.Sprintf("health checks failing on %s", h.Hostname), ev,
					[]string{"poolwarden health", "poolwarden errors --source health --host " + h.Hostname},
					[]string{"backend slow to answer health checks", "health_check_period too aggressive", "backend actually failing"}))
			}
			return out
		}},
	{ID: "CHILD_EXHAUSTION", Severity: model.SevWarn,
		Description: "busy children ≥ 80% (CRIT ≥ 95%) of num_init_children",
		DocRef:      "pgpool: num_init_children",
		Fn: func(in Input) []model.Finding {
			max := atof(setting(in.Snap, "num_init_children"), 0)
			if max <= 0 {
				return nil
			}
			busy := 0
			for _, p := range in.Snap.Pgpool.Processes {
				if p.Status != "" && p.Status != "Wait for connection" && p.Status != "Idle" {
					busy++
				}
			}
			pct := busy * 100 / int(max)
			var sev string
			switch {
			case pct >= 95:
				sev = model.SevCrit
			case pct >= 80:
				sev = model.SevWarn
			default:
				return nil
			}
			return []model.Finding{f(sev, "cluster",
				fmt.Sprintf("children %d/%d busy (%d%%)", busy, int(max), pct),
				[]string{fmt.Sprintf("SHOW POOL_PROCESSES: %d busy of %d num_init_children", busy, int(max))},
				[]string{"poolwarden procs", "poolwarden conns"},
				[]string{"pool saturated: raise num_init_children or shed load", "slow queries holding children", "connection leak in the app"})}
		}},
	{ID: "CONN_BUDGET", Severity: model.SevWarn,
		Description: "num_init_children × max_pool exceeds backend connection budget",
		DocRef:      "pgpool: pooling",
		Fn: func(in Input) []model.Finding {
			nc := atof(setting(in.Snap, "num_init_children"), 0)
			mp := atof(setting(in.Snap, "max_pool"), 0)
			if nc <= 0 || mp <= 0 {
				return nil
			}
			var out []model.Finding
			for _, b := range reachable(in.Snap) {
				if b.MaxConns <= 0 {
					continue
				}
				budget := b.MaxConns - 3
				need := int(nc * mp)
				if need > budget {
					out = append(out, f(model.SevWarn, subj(b),
						fmt.Sprintf("pool budget over on %s", subj(b)),
						[]string{fmt.Sprintf("num_init_children × max_pool = %d×%d=%d > max_connections − reserved = %d − 3 = %d",
							int(nc), int(mp), need, b.MaxConns, budget)},
						[]string{"poolwarden config get num_init_children", "poolwarden config get max_pool"},
						[]string{"pgpool can open more connections than the backend allows"}))
				}
			}
			return out
		}},
	{ID: "WD_QUORUM", Severity: model.SevCrit,
		Description: "watchdog quorum state is not QUORUM EXIST / ON THE EDGE",
		DocRef:      "pgpool: watchdog",
		Fn: func(in Input) []model.Finding {
			w := in.Snap.Watchdog
			if !on(setting(in.Snap, "use_watchdog")) || !w.Available {
				return nil
			}
			q := w.QuorumState
			if strings.Contains(q, "QUORUM EXIST") || strings.Contains(q, "ON THE EDGE") {
				return nil
			}
			return []model.Finding{f(model.SevCrit, "cluster",
				"watchdog has no quorum: "+q,
				[]string{"pcp_watchdog_info quorum=" + q,
					fmt.Sprintf("alive %d of %d", w.AliveRemote+1, w.TotalNodes)},
				[]string{"poolwarden watchdog", "poolwarden topology show"},
				[]string{"pgpool peer down or partitioned", "watchdog ports blocked"})}
		}},
	{ID: "WD_HALF_VOTES_2NODE", Severity: model.SevWarn,
		Description: "2-node watchdog with half-votes on: a network split can give both sides quorum",
		DocRef:      "pgpool: enable_consensus_with_half_votes",
		Fn: func(in Input) []model.Finding {
			w := in.Snap.Watchdog
			if w.TotalNodes != 2 {
				return nil
			}
			if on(setting(in.Snap, "enable_consensus_with_half_votes")) {
				return []model.Finding{f(model.SevWarn, "cluster",
					"2-node watchdog with half-votes on: network split can give both sides quorum",
					[]string{"enable_consensus_with_half_votes=on, 2 watchdog nodes (D14/V4: accepted split risk for auto-failover)"},
					[]string{"poolwarden watchdog", "poolwarden explain WD_HALF_VOTES_2NODE:cluster"},
					[]string{"deliberate trade-off: without half-votes a lost peer blocks failover entirely", "add a third pgpool node to remove the risk"})}
			}
			return []model.Finding{f(model.SevInfo, "cluster",
				"half-votes off: losing one pgpool node blocks failover (backend quarantined)",
				[]string{"enable_consensus_with_half_votes=off, 2 watchdog nodes"},
				[]string{"poolwarden watchdog"},
				[]string{"deliberate availability trade-off"})}
		}},
	{ID: "VIP_DUPLICATE", Severity: model.SevCrit,
		Description: "the delegate IP is present on more than one host",
		DocRef:      "pgpool: delegate_ip",
		Fn: func(in Input) []model.Finding {
			var hosts []string
			for _, v := range in.Snap.VIP {
				if v.Present {
					hosts = append(hosts, v.Host)
				}
			}
			if len(hosts) <= 1 {
				return nil
			}
			sort.Strings(hosts)
			return []model.Finding{f(model.SevCrit, "cluster",
				"VIP up on multiple hosts: "+strings.Join(hosts, ", "),
				[]string{fmt.Sprintf("ip -o addr show on %s all carry %s", strings.Join(hosts, "/"), delegateIP(in))},
				[]string{"poolwarden watchdog", "poolwarden topology show"},
				[]string{"split-brain watchdog: both sides believe they hold the VIP", "stale VIP left after a failed failover"})}
		}},
	{ID: "VIP_MISSING", Severity: model.SevWarn,
		Description: "delegate_ip is set but present on no host (WARN when state unknown)",
		DocRef:      "pgpool: delegate_ip",
		Fn: func(in Input) []model.Finding {
			if delegateIP(in) == "" || !in.Snap.Watchdog.Available {
				return nil
			}
			if len(in.Snap.VIP) == 0 {
				return nil
			}
			present, errored := 0, 0
			for _, v := range in.Snap.VIP {
				if v.Present {
					present++
				}
				if v.Err != "" {
					errored++
				}
			}
			if present > 0 {
				return nil
			}
			if errored > 0 {
				var ev []string
				for _, v := range in.Snap.VIP {
					if v.Err != "" {
						ev = append(ev, "VIP state unknown on "+v.Host+": "+v.Err)
					}
				}
				return []model.Finding{f(model.SevWarn, "cluster", "VIP state unknown",
					ev,
					[]string{"poolwarden watchdog", "poolwarden config self-check"},
					[]string{"cannot reach hosts to check the VIP"})}
			}
			return []model.Finding{f(model.SevCrit, "cluster",
				fmt.Sprintf("VIP %s up on no host", delegateIP(in)),
				[]string{fmt.Sprintf("delegate_ip=%s not found on any pgpool host", delegateIP(in))},
				[]string{"poolwarden watchdog"},
				[]string{"watchdog lost the VIP: escalation never ran or was removed externally"})}
		}},
	{ID: "WD_ESCALATION_UNSET", Severity: model.SevWarn,
		Description: "delegate_ip set but the escalation/de-escalation commands are empty",
		DocRef:      "pgpool: wd_escalation_command",
		Fn: func(in Input) []model.Finding {
			if delegateIP(in) == "" {
				return nil
			}
			var missing []string
			if setting(in.Snap, "wd_escalation_command") == "" {
				missing = append(missing, "wd_escalation_command")
			}
			if setting(in.Snap, "wd_de_escalation_command") == "" {
				missing = append(missing, "wd_de_escalation_command")
			}
			if len(missing) == 0 {
				return nil
			}
			return []model.Finding{f(model.SevWarn, "cluster",
				"VIP escalation commands unset: "+strings.Join(missing, ", "),
				[]string{fmt.Sprintf("delegate_ip=%s but %s empty in live settings", delegateIP(in), strings.Join(missing, "/"))},
				[]string{"poolwarden config get wd_escalation_command"},
				[]string{"the VIP will never move on failover"})}
		}},
	{ID: "WD_PEER_CONFIG_DRIFT", Severity: model.SevWarn,
		Description: "watchdog/heartbeat params differ between pgpool peers",
		DocRef:      "pgpool: watchdog",
		Fn: func(in Input) []model.Finding {
			if in.FileSettings == nil {
				return nil
			}
			var out []model.Finding
			for _, p := range wdParams {
				vals := map[string][]string{}
				for host, m := range in.FileSettings {
					if v, ok := m[p]; ok {
						vals[v] = append(vals[v], host)
					}
				}
				if len(vals) > 1 {
					var ev []string
					for v, hs := range vals {
						ev = append(ev, fmt.Sprintf("%s=%q on %s", p, v, strings.Join(hs, "/")))
					}
					out = append(out, f(model.SevWarn, "cluster",
						"watchdog param "+p+" differs between peers", ev,
						[]string{"poolwarden config get " + p, "poolwarden config diff"},
						[]string{"config edited outside poolwarden or an apply left drift"}))
				}
			}
			return out
		}},
	{ID: "FAILOVER_CMD_UNSET", Severity: model.SevCrit,
		Description: "failover_command or follow_primary_command is empty",
		DocRef:      "pgpool: failover_command",
		Fn: func(in Input) []model.Finding {
			var missing []string
			if setting(in.Snap, "failover_command") == "" {
				missing = append(missing, "failover_command")
			}
			if setting(in.Snap, "follow_primary_command") == "" {
				missing = append(missing, "follow_primary_command")
			}
			if len(missing) == 0 {
				return nil
			}
			return []model.Finding{f(model.SevCrit, "cluster",
				"failover commands unset: "+strings.Join(missing, ", "),
				[]string{strings.Join(missing, "/") + " empty in live settings"},
				[]string{"poolwarden config get failover_command"},
				[]string{"failover cannot run: a dead primary will never be replaced"})}
		}},
	{ID: "RECOVERY_CMD_UNSET", Severity: model.SevInfo,
		Description: "recovery_1st_stage_command is empty (recover is unavailable)",
		DocRef:      "pgpool: online recovery",
		Fn: func(in Input) []model.Finding {
			if setting(in.Snap, "recovery_1st_stage_command") != "" {
				return nil
			}
			return []model.Finding{f(model.SevInfo, "cluster",
				"recovery_1st_stage_command empty: poolwarden recover is unavailable",
				[]string{"recovery_1st_stage_command empty in live settings"},
				[]string{"poolwarden config get recovery_1st_stage_command"},
				[]string{"online recovery not configured"})}
		}},
	{ID: "CONFIG_DRIFT", Severity: model.SevWarn,
		Description: "file value differs from the live value, or files differ between hosts",
		DocRef:      "pgpool: configuration",
		Fn: func(in Input) []model.Finding {
			if in.FileSettings == nil {
				return nil
			}
			var out []model.Finding
			local := in.Local
			for p, live := range in.Snap.Settings {
				var fileV string
				var have bool
				for host, m := range in.FileSettings {
					v, ok := m[normParam(p)]
					if !ok {
						continue
					}
					if host == local || (local == "" && !have) {
						fileV, have = v, true
					}
					_ = fileV
				}
				if have && normVal(fileV) != normVal(live) {
					out = append(out, f(model.SevWarn, "cluster",
						fmt.Sprintf("%s: file %q != live %q", p, fileV, live),
						[]string{fmt.Sprintf("file=%q on %s, PGPOOL SHOW=%q", fileV, orLocal(local), live)},
						[]string{"poolwarden config get " + p, "poolwarden reload"},
						[]string{"config changed but not reloaded", "edited outside poolwarden"}))
				}
			}
			for p := range collectFileParams(in.FileSettings) {
				if isWDParam(p) {
					continue
				}
				vals := map[string][]string{}
				for host, m := range in.FileSettings {
					if v, ok := m[p]; ok {
						vals[normVal(v)] = append(vals[normVal(v)], host)
					}
				}
				if len(vals) > 1 {
					var ev []string
					for v, hs := range vals {
						ev = append(ev, fmt.Sprintf("%s=%q on %s", p, v, strings.Join(hs, "/")))
					}
					out = append(out, f(model.SevWarn, "cluster",
						"config file for "+p+" differs between hosts", ev,
						[]string{"poolwarden config get " + p, "poolwarden config diff"},
						[]string{"file drift between pgpool hosts"}))
				}
			}
			return out
		}},
	{ID: "DEGRADED", Severity: model.SevWarn,
		Description: "pgpool is not answering SQL; using PCP and direct connections",
		DocRef:      "pgpool: health check",
		Fn: func(in Input) []model.Finding {
			if !in.Snap.Degraded {
				return nil
			}
			return []model.Finding{f(model.SevWarn, "cluster",
				"degraded mode: "+in.Snap.DegradedReason,
				[]string{in.Snap.DegradedReason},
				[]string{"poolwarden status", "poolwarden errors --source pgpool"},
				[]string{"pgpool SQL port unreachable or all children busy"})}
		}},
	{ID: "SELF_CHECK", Severity: model.SevWarn,
		Description: "a configured path is missing, or a log_line_prefix doesn't match the live setting",
		DocRef:      "poolwarden: config self-check",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, c := range in.PathChecks {
				if !c.Failed {
					continue
				}
				out = append(out, f(model.SevWarn, c.Host,
					c.Check+" failed on "+c.Host, []string{c.Detail},
					[]string{"poolwarden config self-check"},
					[]string{"path moved or renamed", "config written for a different layout"}))
			}
			return out
		}},
	{ID: "COLOCATED_HOST", Severity: model.SevInfo,
		Description: "a host carries both the pgpool and pg roles: losing it removes a watchdog vote and a backend at once (WARN when it is also the watchdog leader running the primary) — a trade-off reminder, not a defect",
		DocRef:      "pgpool: watchdog / D6 topology",
		Fn: func(in Input) []model.Finding {
			var out []model.Finding
			for _, h := range in.Cfg.Hosts {
				var pgpool, pg bool
				for _, r := range h.Roles {
					if r == config.RolePgpool {
						pgpool = true
					}
					if r == config.RolePG {
						pg = true
					}
				}
				if !pgpool || !pg {
					continue
				}
				leader, primary := false, false
				var be *model.Backend
				for _, n := range in.Snap.Watchdog.Nodes {
					if n.Status == "LEADER" && (n.Host == h.Name || n.Host == h.Address) {
						leader = true
					}
				}
				for i := range in.Snap.Backends {
					b := &in.Snap.Backends[i]
					if b.Name == h.Name || b.Host == h.Name || b.Host == h.Address {
						if b.Reachable && !b.InRecovery {
							primary = true
						}
						be = b
					}
				}
				sev := model.SevInfo
				if leader && primary {
					sev = model.SevWarn
				}
				ev := []string{"roles: pgpool,pg (config)"}
				if leader {
					ev = append(ev, "watchdog: LEADER")
				}
				if primary {
					ev = append(ev, "backend: primary")
				}
				nic := atof(setting(in.Snap, "num_init_children"), 0)
				mp := atof(setting(in.Snap, "max_pool"), 0)
				if nic > 0 && mp > 0 {
					ev = append(ev, fmt.Sprintf("pgpool budget num_init_children×max_pool = %d", int(nic*mp)))
				}
				if be != nil && be.MaxConns > 0 {
					ev = append(ev, fmt.Sprintf("pg max_connections = %d", be.MaxConns))
				}
				causes := []string{
					"pgpool and PostgreSQL share one host: a host failure removes a watchdog vote and a backend together"}
				if sev == model.SevWarn {
					causes = append([]string{
						"this host is the watchdog leader and the primary: one failure moves the VIP and triggers failover at the same time"}, causes...)
				}
				out = append(out, f(sev, h.Name,
					fmt.Sprintf("%s hosts both pgpool and PostgreSQL", h.Name), ev,
					[]string{"poolwarden topology show", "poolwarden watchdog",
						"poolwarden explain COLOCATED_HOST:" + h.Name}, causes))
			}
			return out
		}},
}

// helpers

var zone2 = regexp.MustCompile(`^(.* )([+-]\d{2})$`)

func itoa(i int) string { return fmt.Sprint(i) }

func hostOf(in Input, n model.PoolNode) string {
	if h, ok := in.Cfg.HostByName(n.Hostname); ok {
		return h.Name
	}
	return n.Hostname
}

func hostName(in Input, s string) string {
	if h, ok := in.Cfg.HostByName(s); ok {
		return h.Name
	}
	return s
}

func backendFor(in Input, n model.PoolNode) *model.Backend {
	for i := range in.Snap.Backends {
		if in.Snap.Backends[i].NodeID == n.ID && n.ID >= 0 {
			return &in.Snap.Backends[i]
		}
	}
	addr := n.Hostname
	if h, ok := in.Cfg.HostByName(n.Hostname); ok {
		addr = h.Address
	}
	for i := range in.Snap.Backends {
		b := &in.Snap.Backends[i]
		if b.Port == n.Port && (b.Host == addr || b.Host == n.Hostname) {
			return b
		}
	}
	return nil
}

func primaryOne(s *model.Snapshot) *model.Backend {
	for _, b := range reachable(s) {
		if !b.InRecovery {
			return b
		}
	}
	return nil
}

func prevNode(in Input, host string) *history.NodeSample {
	if in.Prev == nil {
		return nil
	}
	for i := range in.Prev.Nodes {
		if in.Prev.Nodes[i].Host == host {
			return &in.Prev.Nodes[i]
		}
	}
	return nil
}

func delegateIP(in Input) string {
	for _, n := range in.Snap.Watchdog.Nodes {
		if n.DelegateIP != "" {
			return n.DelegateIP
		}
	}
	return setting(in.Snap, "delegate_ip")
}

var wdParams = []string{
	"delegate_ip", "wd_port", "wd_hostname", "heartbeat_hostname0", "heartbeat_port0",
	"heartbeat_hostname1", "heartbeat_port1", "heartbeat_device", "other_pgpool_hostname0",
	"other_pgpool_port0", "other_pgpool_hostname1", "other_pgpool_port1",
	"other_wd_port0", "other_wd_port1",
	"enable_consensus_with_half_votes", "failover_when_quorum_exists",
	"failover_require_consensus", "wd_escalation_command", "wd_de_escalation_command",
	"wd_lifecheck_method", "wd_interval", "wd_heartbeat_keepalive", "wd_heartbeat_deadtime",
}

func isWDParam(p string) bool {
	if strings.HasPrefix(p, "wd_") || strings.HasPrefix(p, "heartbeat_") || strings.HasPrefix(p, "other_") {
		return true
	}
	for _, x := range wdParams {
		if x == p {
			return true
		}
	}
	return false
}

func collectFileParams(fs map[string]map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, m := range fs {
		for p := range m {
			out[p] = true
		}
	}
	return out
}

func normParam(p string) string { return p }

func normVal(v string) string {
	v = strings.TrimSpace(strings.Trim(v, `'"`))
	switch strings.ToLower(v) {
	case "true", "yes", "1":
		return "on"
	case "false", "no", "0":
		return "off"
	}
	return v
}

func orLocal(s string) string {
	if s == "" {
		return "local"
	}
	return s
}

func atof(s string, def float64) float64 {
	if s == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return def
	}
	return f
}

// parseTS parses pgpool timestamps. Naive stamps are the server's local
// wall time (tool runs on the pgpool host, 02 §0); stamps carrying a zone
// abbreviation or numeric offset honour it.
func parseTS(s string) time.Time {
	// normalise a trailing 2-digit numeric zone ("+01" → "+01:00")
	if m := zone2.FindStringSubmatch(s); m != nil {
		s = zone2.ReplaceAllString(s, "${1}${2}:00")
	}
	zoned := []string{time.RFC3339,
		"2006-01-02 15:04:05.000 -07:00", "2006-01-02 15:04:05 -07:00",
		"2006-01-02 15:04:05.000 MST", "2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05.000 -0700", "2006-01-02 15:04:05 -0700"}
	for _, l := range zoned {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05.999999"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func fmtDur(d time.Duration) string {
	switch {
	case d < 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1fGB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1fMB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1fKB", float64(n)/k)
	}
	return fmt.Sprintf("%dB", n)
}

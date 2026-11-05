package diagnose

import (
	"context"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

func baseInput() Input {
	cfg := config.Defaults()
	cfg.Cluster = "prod-a"
	cfg.Lag = config.Lag{WarnSeconds: 1800, CritSeconds: 3600}
	cfg.WALVolumeBytes = 100 << 30
	cfg.Hosts = []config.Host{
		{Name: "db1", Address: "10.0.0.11", Roles: []config.Role{config.RolePgpool, config.RolePG}},
		{Name: "db2", Address: "10.0.0.12", Roles: []config.Role{config.RolePgpool, config.RolePG}},
	}
	return Input{
		Snap: modeltest.SampleSnapshot(),
		Cfg:  &cfg,
		Now:  time.Date(2026, 11, 5, 12, 3, 1, 0, time.Local),
	}
}

func runRule(t *testing.T, id string, in Input) []model.Finding {
	t.Helper()
	for _, r := range Catalogue {
		if r.ID == id {
			return r.Fn(in)
		}
	}
	t.Fatalf("no rule %s", id)
	return nil
}

func hasSev(fs []model.Finding, sev string) bool {
	for _, f := range fs {
		if f.Severity == sev {
			return true
		}
	}
	return false
}

func mutate(in Input, f func(*model.Snapshot)) Input {
	f(in.Snap)
	return in
}

func TestSplitBrain(t *testing.T) {
	if got := runRule(t, "SPLIT_BRAIN", baseInput()); len(got) != 0 {
		t.Fatalf("healthy: %+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Backends[1].InRecovery = false
	})
	got := runRule(t, "SPLIT_BRAIN", in)
	if len(got) != 1 || !hasSev(got, model.SevCrit) || got[0].Subject != "cluster" {
		t.Fatalf("%+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) { s.Backends[0].Reachable = false })
	if got := runRule(t, "SPLIT_BRAIN", in); len(got) != 0 {
		t.Fatalf("unreachable primary should not count: %+v", got)
	}
}

func TestRoleMismatch(t *testing.T) {
	if got := runRule(t, "ROLE_MISMATCH", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Backends[1].InRecovery = false // real primary, pgpool says standby
	})
	got := runRule(t, "ROLE_MISMATCH", in)
	if len(got) != 1 || got[0].Subject != "db2" || !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].Reachable = false })
	if got := runRule(t, "ROLE_MISMATCH", in); len(got) != 0 {
		t.Fatalf("unreachable: %+v", got)
	}
}

func TestNodeDown(t *testing.T) {
	if got := runRule(t, "NODE_DOWN", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Pgpool.Nodes[1].Status = "down" })
	got := runRule(t, "NODE_DOWN", in)
	if len(got) != 1 || !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) { s.Pgpool.Nodes[0].Status = "down" })
	got = runRule(t, "NODE_DOWN", in)
	if !hasSev(got, model.SevCrit) {
		t.Fatalf("primary down should be CRIT: %+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) {
		s.Pgpool.Nodes[1].Status = "down"
		s.Backends[1].InRecovery = true
	})
	got = runRule(t, "NODE_DOWN", in)
	found := false
	for _, c := range got[0].NextCommands {
		if c == "poolwarden attach 1" {
			found = true
		}
	}
	if !found {
		t.Errorf("attach hint missing: %v", got[0].NextCommands)
	}
}

func TestStatusDisagree(t *testing.T) {
	if got := runRule(t, "STATUS_DISAGREE", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].Reachable = false })
	if got := runRule(t, "STATUS_DISAGREE", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) {
		s.Pgpool.Nodes[1].Status = "down"
	})
	if got := runRule(t, "STATUS_DISAGREE", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestReplLag(t *testing.T) {
	if got := runRule(t, "REPL_LAG", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].ReplayDelaySec = 1900 })
	got := runRule(t, "REPL_LAG", in)
	if len(got) != 1 || !hasSev(got, model.SevWarn) || got[0].Subject != "db2" {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[1].ReplayDelaySec = 3700
	if got := runRule(t, "REPL_LAG", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[1].ReplayDelaySec = -1
	if got := runRule(t, "REPL_LAG", in); len(got) != 0 {
		t.Fatalf("unknown delay: %+v", got)
	}
}

func TestReplLagGrowing(t *testing.T) {
	if got := runRule(t, "REPL_LAG_GROWING", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.Trends = &forecast.Trends{ReplayDelay: map[string]forecast.Trend{
		"db2": {Slope: 100, R2: 0.9, Last: 1200, Span: 5 * time.Minute},
	}}
	if got := runRule(t, "REPL_LAG_GROWING", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in.Trends.ReplayDelay["db2"] = forecast.Trend{Slope: 1, R2: 0.9, Last: 100, Span: 5 * time.Minute}
	if got := runRule(t, "REPL_LAG_GROWING", in); len(got) != 0 {
		t.Fatalf("slow slope: %+v", got)
	}
	in.Trends = nil
	if got := runRule(t, "REPL_LAG_GROWING", in); len(got) != 0 {
		t.Fatalf("nil trends: %+v", got)
	}
}

func TestWalReceiverMissing(t *testing.T) {
	if got := runRule(t, "WAL_RECEIVER_MISSING", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].WALReceiver = false })
	if got := runRule(t, "WAL_RECEIVER_MISSING", in); len(got) != 1 || !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[1].Reachable = false
	if got := runRule(t, "WAL_RECEIVER_MISSING", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestReplayPaused(t *testing.T) {
	if got := runRule(t, "REPLAY_PAUSED", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].ReplayPaused = true })
	if got := runRule(t, "REPLAY_PAUSED", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[1].Reachable = false
	if got := runRule(t, "REPLAY_PAUSED", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestStandbyNotStreaming(t *testing.T) {
	if got := runRule(t, "STANDBY_NOT_STREAMING", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[0].Standbys = nil })
	got := runRule(t, "STANDBY_NOT_STREAMING", in)
	if len(got) != 1 || !hasSev(got, model.SevCrit) || got[0].Subject != "db2" {
		t.Fatalf("%+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].Reachable = false })
	if got := runRule(t, "STANDBY_NOT_STREAMING", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSlotInactive(t *testing.T) {
	if got := runRule(t, "SLOT_INACTIVE", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Backends[0].Slots[0].Active = false
	})
	got := runRule(t, "SLOT_INACTIVE", in)
	if len(got) != 1 || !hasSev(got, model.SevWarn) || got[0].Subject != "slot1" {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[0].Slots[0].WALStatus = "lost"
	if got := runRule(t, "SLOT_INACTIVE", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[0].Slots[0].Active = true
	if got := runRule(t, "SLOT_INACTIVE", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWalDiskETA(t *testing.T) {
	if got := runRule(t, "WAL_DISK_ETA", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.Trends = &forecast.Trends{RetainedWAL: forecast.Trend{
		N: 20, Slope: 1e6, R2: 0.9, Last: 50 << 30, Span: 5 * time.Minute}}
	if got := runRule(t, "WAL_DISK_ETA", in); !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
	in.Trends.RetainedWAL.Slope = 1e7
	if got := runRule(t, "WAL_DISK_ETA", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Cfg.WALVolumeBytes = 0
	if got := runRule(t, "WAL_DISK_ETA", in); len(got) != 0 {
		t.Fatalf("unknown volume: %+v", got)
	}
}

func TestXminHoldback(t *testing.T) {
	if got := runRule(t, "XMIN_HOLDBACK", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Backends[0].LongTxns = 2
		s.Backends[0].OldestXactSec = 600
	})
	if got := runRule(t, "XMIN_HOLDBACK", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[0].InRecovery = true
	if got := runRule(t, "XMIN_HOLDBACK", in); len(got) != 0 {
		t.Fatalf("standby txns don't count: %+v", got)
	}
}

func TestReplConflicts(t *testing.T) {
	if got := runRule(t, "REPL_CONFLICTS", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].Conflicts = 5 })
	in.Prev = &history.Sample{Nodes: []history.NodeSample{
		{Host: "db2", Conflicts: 5},
	}}
	if got := runRule(t, "REPL_CONFLICTS", in); len(got) != 0 {
		t.Fatalf("no increase: %+v", got)
	}
	in.Prev.Nodes[0].Conflicts = 2
	if got := runRule(t, "REPL_CONFLICTS", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in.Prev = nil
	if got := runRule(t, "REPL_CONFLICTS", in); len(got) != 0 {
		t.Fatalf("nil prev: %+v", got)
	}
}

func TestHealthcheckFailing(t *testing.T) {
	if got := runRule(t, "HEALTHCHECK_FAILING", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.Prev = &history.Sample{Nodes: []history.NodeSample{
		{Host: "db2", NodeID: 1, HealthFail: 0},
	}}
	if got := runRule(t, "HEALTHCHECK_FAILING", in); len(got) != 1 || !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
	in2 := mutate(baseInput(), func(s *model.Snapshot) {
		s.Pgpool.HealthStats[1].LastSuccessfulHealthCheck = "2026-11-05 10:00:00"
		s.Pgpool.HealthStats[1].FailCount = 0
	})
	if got := runRule(t, "HEALTHCHECK_FAILING", in2); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
}

func TestChildExhaustion(t *testing.T) {
	if got := runRule(t, "CHILD_EXHAUSTION", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		for i := 0; i < 85; i++ {
			s.Pgpool.Processes = append(s.Pgpool.Processes,
				model.PoolProcess{PID: 40000 + i, Status: "Active"})
		}
	})
	if got := runRule(t, "CHILD_EXHAUSTION", in); !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
	for i := 0; i < 15; i++ {
		in.Snap.Pgpool.Processes = append(in.Snap.Pgpool.Processes,
			model.PoolProcess{PID: 41000 + i, Status: "Active"})
	}
	if got := runRule(t, "CHILD_EXHAUSTION", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["num_init_children"] = ""
	if got := runRule(t, "CHILD_EXHAUSTION", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestConnBudget(t *testing.T) {
	if got := runRule(t, "CONN_BUDGET", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) { s.Backends[1].MaxConns = 100 })
	got := runRule(t, "CONN_BUDGET", in)
	if len(got) != 1 || got[0].Subject != "db2" {
		t.Fatalf("%+v", got)
	}
	in.Snap.Backends[1].MaxConns = 0
	if got := runRule(t, "CONN_BUDGET", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWDQuorum(t *testing.T) {
	if got := runRule(t, "WD_QUORUM", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Watchdog.QuorumState = "QUORUM LOST"
	})
	if got := runRule(t, "WD_QUORUM", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Watchdog.QuorumState = "IS ON THE EDGE"
	if got := runRule(t, "WD_QUORUM", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["use_watchdog"] = "off"
	in.Snap.Watchdog.QuorumState = "QUORUM LOST"
	if got := runRule(t, "WD_QUORUM", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWDHalfVotes(t *testing.T) {
	got := runRule(t, "WD_HALF_VOTES_2NODE", baseInput())
	if len(got) != 1 || !hasSev(got, model.SevWarn) {
		t.Fatalf("standing warn: %+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Settings["enable_consensus_with_half_votes"] = "off"
	})
	got = runRule(t, "WD_HALF_VOTES_2NODE", in)
	if len(got) != 1 || !hasSev(got, model.SevInfo) {
		t.Fatalf("off → info: %+v", got)
	}
	in = mutate(baseInput(), func(s *model.Snapshot) { s.Watchdog.TotalNodes = 3 })
	if got := runRule(t, "WD_HALF_VOTES_2NODE", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestVIPDuplicate(t *testing.T) {
	if got := runRule(t, "VIP_DUPLICATE", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.VIP[1].Present = true
	})
	if got := runRule(t, "VIP_DUPLICATE", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
}

func TestVIPMissing(t *testing.T) {
	if got := runRule(t, "VIP_MISSING", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.VIP[0].Present = false
	})
	if got := runRule(t, "VIP_MISSING", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
	in.Snap.VIP[0].Err = "ssh failed"
	in.Snap.VIP[1].Err = "ssh failed"
	if got := runRule(t, "VIP_MISSING", in); !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["delegate_ip"] = ""
	in.Snap.Watchdog.Nodes[0].DelegateIP = ""
	in.Snap.Watchdog.Nodes[1].DelegateIP = ""
	if got := runRule(t, "VIP_MISSING", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWDEscalation(t *testing.T) {
	in := baseInput()
	if got := runRule(t, "WD_ESCALATION_UNSET", in); len(got) != 1 {
		t.Fatalf("sample has empty escalation cmds: %+v", got)
	}
	in.Snap.Settings["wd_escalation_command"] = "/x"
	in.Snap.Settings["wd_de_escalation_command"] = "/x"
	if got := runRule(t, "WD_ESCALATION_UNSET", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["delegate_ip"] = ""
	in.Snap.Watchdog.Nodes[0].DelegateIP = ""
	in.Snap.Watchdog.Nodes[1].DelegateIP = ""
	in.Snap.Settings["wd_escalation_command"] = ""
	if got := runRule(t, "WD_ESCALATION_UNSET", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestWDPeerConfigDrift(t *testing.T) {
	if got := runRule(t, "WD_PEER_CONFIG_DRIFT", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.FileSettings = map[string]map[string]string{
		"db1": {"wd_port": "9000"},
		"db2": {"wd_port": "9000"},
	}
	if got := runRule(t, "WD_PEER_CONFIG_DRIFT", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in.FileSettings["db2"]["wd_port"] = "9001"
	if got := runRule(t, "WD_PEER_CONFIG_DRIFT", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestFailoverCmdUnset(t *testing.T) {
	in := baseInput()
	if got := runRule(t, "FAILOVER_CMD_UNSET", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("sample has empty failover_command: %+v", got)
	}
	in.Snap.Settings["failover_command"] = "/x"
	in.Snap.Settings["follow_primary_command"] = "/y"
	if got := runRule(t, "FAILOVER_CMD_UNSET", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["follow_primary_command"] = ""
	if got := runRule(t, "FAILOVER_CMD_UNSET", in); !hasSev(got, model.SevCrit) {
		t.Fatalf("%+v", got)
	}
}

func TestRecoveryCmdUnset(t *testing.T) {
	in := baseInput()
	if got := runRule(t, "RECOVERY_CMD_UNSET", in); !hasSev(got, model.SevInfo) {
		t.Fatalf("%+v", got)
	}
	in.Snap.Settings["recovery_1st_stage_command"] = "/x"
	if got := runRule(t, "RECOVERY_CMD_UNSET", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestConfigDrift(t *testing.T) {
	if got := runRule(t, "CONFIG_DRIFT", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.Local = "db1"
	in.FileSettings = map[string]map[string]string{
		"db1": {"num_init_children": "64", "max_pool": "4"},
		"db2": {"num_init_children": "100", "max_pool": "4"},
	}
	got := runRule(t, "CONFIG_DRIFT", in)
	// file 64 != live 100 on db1; files also differ across hosts
	if len(got) < 2 {
		t.Fatalf("%+v", got)
	}
	in.FileSettings["db1"]["num_init_children"] = "100"
	in.FileSettings["db2"]["num_init_children"] = "100"
	if got := runRule(t, "CONFIG_DRIFT", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in.FileSettings["db1"]["max_pool"] = "yes" // normalised? yes→on, not equal to "4"
	got = runRule(t, "CONFIG_DRIFT", in)
	if len(got) == 0 {
		t.Fatalf("expected drift for max_pool: %+v", got)
	}
}

func TestDegraded(t *testing.T) {
	if got := runRule(t, "DEGRADED", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Degraded = true
		s.DegradedReason = "pgpool SQL timeout"
	})
	if got := runRule(t, "DEGRADED", in); len(got) != 1 || !hasSev(got, model.SevWarn) {
		t.Fatalf("%+v", got)
	}
}

func TestSelfCheck(t *testing.T) {
	if got := runRule(t, "SELF_CHECK", baseInput()); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	in := baseInput()
	in.PathChecks = []PathCheck{{Check: "path", Host: "db1", Detail: "missing", Failed: true}}
	if got := runRule(t, "SELF_CHECK", in); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	in.PathChecks[0].Failed = false
	if got := runRule(t, "SELF_CHECK", in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestEngineOrderingAndFirstSeen(t *testing.T) {
	e := NewEngine()
	in := mutate(baseInput(), func(s *model.Snapshot) {
		s.Backends[1].InRecovery = false // SPLIT_BRAIN + ROLE_MISMATCH
	})
	fs1 := e.Run(context.Background(), in)
	if len(fs1) == 0 {
		t.Fatal("no findings")
	}
	for i := 1; i < len(fs1); i++ {
		if fs1[i].Severity == model.SevCrit && fs1[i-1].Severity == model.SevWarn {
			t.Error("WARN before CRIT")
		}
	}
	time.Sleep(2 * time.Millisecond)
	fs2 := e.Run(context.Background(), in)
	for i := range fs1 {
		if i < len(fs2) && !fs2[i].FirstSeen.Equal(fs1[i].FirstSeen) {
			t.Errorf("FirstSeen changed for %s", fs1[i].ID)
		}
	}
	for _, f := range fs1 {
		if f.ID == "" || f.Rule == "" || f.DocRef == "" {
			t.Errorf("incomplete finding %+v", f)
		}
	}
}

func TestExplain(t *testing.T) {
	f := model.Finding{ID: "SPLIT_BRAIN:cluster", Evidence: []string{"e1"},
		Causes: []string{"c1", "c2"}, NextCommands: []string{"poolwarden nodes"}}
	r := Catalogue[0]
	res := Explain(f, r, nil, true)
	if res.What == "" || len(res.LikelyCauses) != 2 || len(res.Fix) != 1 || !res.Firing {
		t.Errorf("%+v", res)
	}
	res2 := Explain(model.Finding{}, r, nil, false)
	if res2.Firing {
		t.Error("should not be firing")
	}
}

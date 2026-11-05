package preflight

import (
	"testing"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

func base() Input {
	cfg := config.Defaults()
	cfg.Lag = config.Lag{WarnSeconds: 1800, CritSeconds: 3600}
	cfg.Hosts = []config.Host{
		{Name: "db1", Address: "10.0.0.11"},
		{Name: "db2", Address: "10.0.0.12"},
	}
	in := Input{Snap: modeltest.SampleSnapshot(), Cfg: &cfg}
	in.Snap.Settings["failover_command"] = "/bin/failover.sh"
	in.Snap.Settings["follow_primary_command"] = "/bin/follow.sh"
	cfg.Hosts[0].PG = &config.PGPaths{DataDir: "/var/lib/pgsql/data"}
	return in
}

func TestPromote(t *testing.T) {
	in := base()
	pf := Promote(in, 1, false)
	if pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	// lagging target → NO-GO
	in.Snap.Backends[1].ReplayDelaySec = 1900
	pf = Promote(in, 1, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	// target is primary → NO-GO
	in = base()
	in.Snap.Backends[1].InRecovery = false
	in.Snap.Backends[0].Reachable = false // avoid split-brain noise? still 2 primaries → NO-GO anyway
	pf = Promote(in, 1, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	// unknown id
	pf = Promote(in, 9, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	// quorum lost → NO-GO
	in = base()
	in.Snap.Watchdog.QuorumState = "QUORUM LOST"
	pf = Promote(in, 1, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
}

func TestPromotePgpoolOnly(t *testing.T) {
	in := base()
	in.Snap.Backends[0].InRecovery = true // no primaries
	pf := Promote(in, 1, true)
	if pf.Verdict != "NO-GO" { // target still in recovery
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Backends[1].InRecovery = false
	pf = Promote(in, 1, true)
	if pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	if pf.Action != "promote --pgpool-only (UNVERIFIED: T1)" {
		t.Errorf("%s", pf.Action)
	}
	in.Snap.Backends[0].InRecovery = false
	pf = Promote(in, 1, true)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("other primary: %+v", pf.Checks)
	}
}

func TestDetach(t *testing.T) {
	in := base()
	pf := Detach(in, 1, false)
	if pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Pgpool.Nodes[1].Status = "down"
	pf = Detach(in, 1, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("already down: %+v", pf.Checks)
	}
	in = base()
	pf = Detach(in, 0, false) // primary → WARN (triggers failover)
	if pf.Verdict != "WARN" {
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Watchdog.QuorumState = "QUORUM LOST"
	pf = Detach(in, 0, false)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
}

func TestAttach(t *testing.T) {
	in := base()
	in.Snap.Pgpool.Nodes[1].Status = "down"
	pf := Attach(in, 1)
	if pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Pgpool.Nodes[1].Status = "up"
	pf = Attach(in, 1)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("already attached: %+v", pf.Checks)
	}
	in = base()
	in.Snap.Pgpool.Nodes[1].Status = "down"
	in.Snap.Backends[1].WALReceiver = false
	pf = Attach(in, 1)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("not streaming: %+v", pf.Checks)
	}
}

func TestRecover(t *testing.T) {
	in := base()
	in.Snap.Pgpool.Nodes[1].Status = "down"
	in.Snap.Settings["recovery_1st_stage_command"] = "recover.sh"
	in.ScriptExists = func(h config.Host, p string) (bool, error) { return true, nil }
	pf := Recover(in, 1)
	if pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Settings["recovery_1st_stage_command"] = ""
	pf = Recover(in, 1)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("unset cmd: %+v", pf.Checks)
	}
	in.Snap.Settings["recovery_1st_stage_command"] = "recover.sh"
	in.Snap.Pgpool.Nodes[1].Status = "up"
	pf = Recover(in, 1)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("not down: %+v", pf.Checks)
	}
}

func TestReload(t *testing.T) {
	in := base()
	if pf := Reload(in); pf.Verdict != "GO" {
		t.Fatalf("%+v", pf.Checks)
	}
	in.Snap.Pgpool.Reachable = false
	if pf := Reload(in); pf.Verdict != "NO-GO" {
		t.Fatalf("%+v", pf.Checks)
	}
}

var _ = model.Finding{}

// BUG Q: a lagging control-file timeline must not NO-GO a streaming standby.
func TestAttachTimelineStaleOK(t *testing.T) {
	in := base()
	in.Snap.Pgpool.Nodes[1].Status = "down"
	in.Snap.Backends[1].Timeline = 1 // stale restartpoint TL after rewind
	in.Snap.Backends[0].Timeline = 4
	pf := Attach(in, 1)
	if pf.Verdict != "GO" {
		t.Fatalf("streaming standby with stale TL must be GO: %+v", pf.Checks)
	}
	// not streaming AND different TL → NO-GO
	in.Snap.Backends[0].Standbys = nil
	pf = Attach(in, 1)
	if pf.Verdict != "NO-GO" {
		t.Fatalf("want NO-GO: %+v", pf.Checks)
	}
}

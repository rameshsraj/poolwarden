package diagnose

import (
	"testing"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
)

func colocatedFindings(t *testing.T, in Input) []model.Finding {
	t.Helper()
	return runRule(t, "COLOCATED_HOST", in)
}

func TestColocatedHost(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Input)
		want int      // findings
		sevs []string // expected severities in order
	}{
		{"pgpool-only host", func(in *Input) {
			in.Cfg.Hosts[0].Roles = []config.Role{config.RolePgpool}
		}, 1, []string{model.SevInfo}},
		{"co-located standby non-leader", func(in *Input) {
			in.Snap.Backends[0].InRecovery = true  // db1 standby
			in.Snap.Backends[1].InRecovery = false // db2 primary
			for i := range in.Snap.Watchdog.Nodes {
				in.Snap.Watchdog.Nodes[i].Status = "STANDBY"
			}
		}, 2, []string{model.SevInfo, model.SevInfo}},
		{"co-located leader+primary", func(in *Input) {
			for i := range in.Snap.Watchdog.Nodes {
				if in.Snap.Watchdog.Nodes[i].Host == "db1" ||
					in.Snap.Watchdog.Nodes[i].Host == "10.0.0.11" {
					in.Snap.Watchdog.Nodes[i].Status = "LEADER"
				} else {
					in.Snap.Watchdog.Nodes[i].Status = "STANDBY"
				}
			}
		}, 2, []string{model.SevWarn, model.SevInfo}},
		{"no watchdog → INFO only", func(in *Input) {
			in.Snap.Watchdog.Nodes = nil
		}, 2, []string{model.SevInfo, model.SevInfo}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := baseInput()
			if c.edit != nil {
				c.edit(&in)
			}
			fs := colocatedFindings(t, in)
			if len(fs) != c.want {
				t.Fatalf("got %d findings %+v", len(fs), fs)
			}
			for i, sev := range c.sevs {
				if fs[i].Severity != sev {
					t.Errorf("finding %d sev %s want %s", i, fs[i].Severity, sev)
				}
			}
		})
	}
}

// default input: db1 is leader+primary? check SampleSnapshot — db1 primary;
// watchdog LEADER is set on db1 in the fixture → both findings exist, db1 WARN.
func TestColocatedDefault(t *testing.T) {
	in := baseInput()
	fs := colocatedFindings(t, in)
	if len(fs) != 2 {
		t.Fatalf("%d findings %+v", len(fs), fs)
	}
	bySubj := map[string]model.Finding{}
	for _, f := range fs {
		bySubj[f.Subject] = f
	}
	// db1 = watchdog leader + primary in the sample → WARN
	if bySubj["db1"].Severity != model.SevWarn {
		t.Errorf("db1 sev %s", bySubj["db1"].Severity)
	}
	if bySubj["db2"].Severity != model.SevInfo {
		t.Errorf("db2 sev %s", bySubj["db2"].Severity)
	}
	// evidence
	found := false
	for _, e := range bySubj["db1"].Evidence {
		if e == "watchdog: LEADER" {
			found = true
		}
	}
	if !found {
		t.Error("leader evidence missing")
	}
	if bySubj["db1"].Causes[0] != "this host is the watchdog leader and the primary: one failure moves the VIP and triggers failover at the same time" {
		t.Errorf("first cause %q", bySubj["db1"].Causes[0])
	}
}

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
	"gopkg.in/yaml.v3"
)

func TestDiscoverApply(t *testing.T) {
	snap := modeltest.SampleSnapshot()
	// new watchdog peer pgp3 and backend host db3 unknown to cfg.Hosts
	snap.Watchdog.Nodes = append(snap.Watchdog.Nodes,
		model.WatchdogNode{ID: 9, Host: "pgp3", Status: "STANDBY"})
	snap.Backends = append(snap.Backends,
		model.Backend{NodeID: 9, Host: "db3", Port: 5432, Reachable: true, InRecovery: true})
	svc, _, c, f := mkSvc(t, snap)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	// seed an existing config file
	f.MkdirAll("/etc", 0o755)
	orig, _ := yaml.Marshal(svc.Cfg)
	f.WriteFileAtomic("/etc/poolwarden.yaml", orig, false)
	rows, err := svc.DiscoverDiff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows %+v", rows)
	}
	res, err := svc.DiscoverApply(context.Background(), rows,
		"/etc/poolwarden.yaml", ControlOpts{TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != "success" {
		t.Fatalf("%+v", res)
	}
	// merged config contains the new hosts
	nb, _ := f.ReadFile("/etc/poolwarden.yaml")
	var merged config.Config
	if err := yaml.Unmarshal(nb, &merged); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, h := range merged.Hosts {
		found[h.Name] = true
	}
	if !found["pgp3"] || !found["db3"] {
		t.Errorf("hosts %v", found)
	}
	// backup + audit diff
	b, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(b), "topology discover") || !strings.Contains(string(b), "finished") {
		t.Error("audit missing")
	}
	var bak bool
	if ents, _ := f.ReadDir("/etc"); ents != nil {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "poolwarden.yaml.bak.") {
				bak = true
			}
		}
	}
	if !bak {
		t.Error("no .bak backup")
	}
}

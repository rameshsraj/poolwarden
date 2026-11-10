package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

// mkMonitorSvc builds a service with one static snapshot for monitor/probe tests.
func mkMonitorSvc(t *testing.T) *Service {
	t.Helper()
	f := fstest.New()
	c := clock.NewFake(t0)
	cfg := confCfg()
	return &Service{
		Cfg: cfg, Collector: &scriptSrc{snaps: nil}, Diagnoser: NoopDiagnoser{},
		PCP: &fakePCP{}, Clock: c, FS: f,
		Recorder: &audit.Recorder{
			W:         &audit.Writer{FS: f, Clock: c, Path: cfg.Audit.File},
			SessionID: "t", Cluster: cfg.Cluster, Clock: c,
			Identity: audit.Identity{Declared: "alice", Hostname: "pgp2"},
		},
		Confirmer: &fakeConfirm{confirmed: true},
	}
}

func TestMonitorBundleSingleCollect(t *testing.T) {
	svc := mkMonitorSvc(t)
	src := &scriptSrc{snaps: []*model.Snapshot{modeltest.SampleSnapshot()}}
	svc.Collector = src
	b, err := svc.Monitor(context.Background())
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	if src.i != 1 {
		t.Fatalf("expected exactly 1 collect, got %d", src.i)
	}
	if b.Status == nil || b.Nodes == nil || b.Pools == nil || b.Watchdog == nil || b.Topology == nil {
		t.Fatalf("bundle missing views: %+v", b)
	}
	if len(b.Status.Nodes) != 2 || len(b.Nodes.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d/%d", len(b.Status.Nodes), len(b.Nodes.Nodes))
	}
	if b.Lag == nil || b.Lag.Primary == "" {
		t.Fatalf("expected lag with primary, got %+v", b.Lag)
	}
	// status and nodes must agree (same snapshot)
	if b.Status.Nodes[0].RealRole != b.Nodes.Nodes[0].RealRole {
		t.Fatal("status and nodes disagree on roles — not one snapshot")
	}
}

func TestProbeReportsEndpoints(t *testing.T) {
	svc := mkMonitorSvc(t)
	svc.Collector = &scriptSrc{snaps: []*model.Snapshot{modeltest.SampleSnapshot()}}
	res := svc.Probe(context.Background())
	if res == nil {
		t.Fatal("no result")
	}
	var pgpool, pg, wd bool
	for _, c := range res.Checks {
		switch c.Name {
		case "pgpool_sql":
			pgpool = c.Result == "OK"
		case "pg":
			pg = true
		case "watchdog":
			wd = true
		}
	}
	if !pgpool || !pg || !wd {
		t.Fatalf("missing checks in %+v", res.Checks)
	}
}

func TestInstallConfigValidation(t *testing.T) {
	svc := mkMonitorSvc(t)
	if _, err := svc.InstallConfig(context.Background(), []byte("bogus: ["), "/etc/poolwarden.yaml",
		ControlOpts{Yes: true}); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := svc.InstallConfig(context.Background(), []byte("cluster: x\n"), "/etc/poolwarden.yaml",
		ControlOpts{Yes: true}); err == nil || !strings.Contains(err.Error(), "hosts") {
		t.Fatalf("expected hosts validation, got %v", err)
	}
}

func TestPlanOnlyReturnsToken(t *testing.T) {
	svc := mkMonitorSvc(t)
	svc.Collector = &scriptSrc{snaps: []*model.Snapshot{
		modeltest.SampleSnapshot(), modeltest.SampleSnapshot(), modeltest.SampleSnapshot()}}
	res, err := svc.Detach(context.Background(), 1, false, ControlOpts{PlanOnly: true})
	if err != nil {
		t.Fatalf("Detach --plan: %v", err)
	}
	if res.Outcome != "planned" || res.Token == "" || res.Preflight == nil {
		t.Fatalf("bad plan result: %+v", res)
	}
	if res.Token != "1" {
		t.Fatalf("expected token 1, got %q", res.Token)
	}
}

func TestProgressEventsEmitted(t *testing.T) {
	down := modeltest.SampleSnapshot()
	down.Pgpool.Nodes[1].Status = "down"
	svc, _, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(), down)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	var mu sync.Mutex
	var stages []string
	svc.Progress = func(ev ProgressEvent) {
		mu.Lock()
		stages = append(stages, ev.Stage)
		mu.Unlock()
	}
	_, err := svc.Detach(context.Background(), 1, false,
		ControlOpts{Yes: true, Confirm: "1"})
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"preflight", "step_start", "step_end", "plan", "confirm", "execute", "outcome"}
	for _, w := range want {
		found := false
		for _, s := range stages {
			if s == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing stage %q in %v", w, stages)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
	"github.com/smarsh/poolwarden/internal/pcp"
)

var update = flag.Bool("update", false, "update golden files")

type fakeSource struct{ snap *model.Snapshot }

func (f fakeSource) Collect(context.Context) *model.Snapshot { return f.snap }

type fakeDiagnose struct{ findings []model.Finding }

func (f fakeDiagnose) Run(_ context.Context, _ diagnose.Input) []model.Finding {
	return f.findings
}

func viewDeps(t *testing.T, findings []model.Finding) (*Deps, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	f := fstest.New()
	f.Add("/etc/poolwarden.yaml", `cluster: prod-a
audit: {file: /var/lib/poolwarden/audit.jsonl}
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
  - name: db2
    address: 10.0.0.12
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
`, 0o600, f.Uid)
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &Deps{
		FS: f, Clock: clock.NewFake(time.Date(2026, 11, 5, 12, 3, 1, 0, time.Local)),
		Stdout: out, Stderr: errb, Stdin: strings.NewReader(""), UID: f.Uid,
		Version:  BuildInfo{Version: "1.0.0", Commit: "abc", Date: "2026-11-05"},
		Source:   fakeSource{snap: modeltest.SampleSnapshot()},
		Diagnose: fakeDiagnose{findings: findings},
		Loc:      time.FixedZone("UTC+2", 7200),
	}, out, errb
}

func goldenView(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "views", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (-update to fix)\nGOT:\n%s", name, got)
	}
}

func TestViewsGolden(t *testing.T) {
	views := []struct {
		name string
		args []string
	}{
		{"status.table", []string{"status"}},
		{"status.json", []string{"-o", "json", "status"}},
		{"nodes.table", []string{"nodes"}},
		{"nodes.wide", []string{"-o", "wide", "nodes"}},
		{"nodes.json", []string{"-o", "json", "nodes"}},
		{"nodes1.table", []string{"nodes", "1"}},
		{"lag.table", []string{"lag"}},
		{"lag.json", []string{"-o", "json", "lag"}},
		{"slots.table", []string{"slots"}},
		{"pools.table", []string{"pools"}},
		{"procs.table", []string{"procs"}},
		{"conns.table", []string{"conns"}},
		{"watchdog.table", []string{"watchdog"}},
		{"health.table", []string{"health"}},
		{"topology.table", []string{"topology", "show"}},
	}
	for _, v := range views {
		t.Run(v.name, func(t *testing.T) {
			d, out, errb := testDepsWith(t, nil)
			code := Execute(v.args, d)
			if code != 0 {
				t.Fatalf("code %d stderr %s", code, errb)
			}
			goldenView(t, v.name+".golden", out.String())
		})
	}
}

func testDepsWith(t *testing.T, findings []model.Finding) (*Deps, *bytes.Buffer, *bytes.Buffer) {
	return viewDeps(t, findings)
}

func warnFinding() []model.Finding {
	return []model.Finding{
		{ID: "WD_HALF_VOTES_2NODE:cluster", Rule: "WD_HALF_VOTES_2NODE",
			Severity: model.SevWarn, Subject: "cluster"},
	}
}

func TestStatusExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		findings []model.Finding
		code     int
	}{
		{"none", nil, 0},
		{"warn", warnFinding(), 10},
		{"crit", []model.Finding{{ID: "X:a", Severity: model.SevCrit, Subject: "a"}}, 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _, _ := testDepsWith(t, tt.findings)
			if code := Execute([]string{"status"}, d); code != tt.code {
				t.Errorf("code %d, want %d", code, tt.code)
			}
		})
	}
}

func TestNodeNotFound(t *testing.T) {
	d, _, errb := testDepsWith(t, nil)
	if code := Execute([]string{"nodes", "7"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestDegradedHeader(t *testing.T) {
	d, out, _ := testDepsWith(t, nil)
	snap := modeltest.SampleSnapshot()
	snap.Degraded = true
	snap.DegradedReason = "pgpool down"
	d.Source = fakeSource{snap: snap}
	if code := Execute([]string{"status"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "· DEGRADED") {
		t.Errorf("out %q", out.String())
	}
}

func TestConnectionFailure(t *testing.T) {
	d, _, errb := testDepsWith(t, nil)
	snap := modeltest.SampleSnapshot()
	snap.Pgpool.Reachable = false
	snap.Pgpool.Err = "connection refused"
	snap.Degraded = true
	for i := range snap.Backends {
		snap.Backends[i].Reachable = false
	}
	snap.Backends = snap.Backends[:0]
	d.Source = fakeSource{snap: snap}
	code := Execute([]string{"status"}, d)
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	s := errb.String()
	for _, want := range []string{"error:", "cause:", "next:", "cannot reach pgpool"} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr missing %q:\n%s", want, s)
		}
	}
}

func TestNagiosWarn(t *testing.T) {
	d, out, _ := testDepsWith(t, warnFinding())
	code := Execute([]string{"--nagios", "status"}, d)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.HasPrefix(out.String(), "POOLWARDEN WARNING - ") {
		t.Errorf("out %q", out.String())
	}
	if !strings.Contains(out.String(), "WD_HALF_VOTES_2NODE:cluster") && !strings.Contains(out.String(), "cluster ok") {
		t.Errorf("out %q", out.String())
	}
}

func TestRefusedPromoteGolden(t *testing.T) {
	snap := modeltest.SampleSnapshot()
	snap.Backends[1].ReplayDelaySec = 1900 // NO-GO: lag over crit
	d, out, _ := viewDeps(t, nil)
	d.Source = fakeSource{snap: snap}
	code := Execute([]string{"--operator", "t", "promote", "1", "--yes", "--confirm", "1"}, d)
	if code == 0 {
		t.Fatal("want refusal")
	}
	goldenView(t, "promote_refused.golden", out.String())
}

type failPCP struct{}

func (failPCP) PromoteNode(context.Context, int, bool, bool) (pcp.Result, error) {
	return pcp.Result{Stdout: "started", Stderr: "backend went away"}, nil
}
func (failPCP) DetachNode(context.Context, int, bool) (pcp.Result, error) {
	return pcp.Result{}, errors.New("pcp connect refused")
}
func (failPCP) AttachNode(context.Context, int) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (failPCP) RecoveryNode(context.Context, int) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (failPCP) ReloadConfig(context.Context, string) (pcp.Result, error) {
	return pcp.Result{}, nil
}

func TestFailedControlGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.PCP = failPCP{}
	t.Cleanup(func() { t.Log(errb.String()) })
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	d.FS.(*fstest.Fake).Add("/etc/poolwarden.yaml", strings.Replace(`cluster: prod-a
audit: {file: /var/lib/poolwarden/audit.jsonl}
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
  - name: db2
    address: 10.0.0.12
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
`, "/var/lib/poolwarden/audit.jsonl", auditPath, 1), 0o600, d.FS.(*fstest.Fake).Uid)
	code := Execute([]string{"--operator", "t", "detach", "1", "--yes", "--confirm", "1"}, d)

	if code == 0 {
		t.Fatal("want failure")
	}
	got := regexp.MustCompile(`event: [0-9a-f]+`).ReplaceAllString(out.String(), "event: <id>")
	goldenView(t, "detach_failed.golden", got)
}

// orderWriter appends every write to a shared log with a stream tag,
// proving stdout content precedes the stderr error block.
type orderWriter struct {
	mu  *sync.Mutex
	log *strings.Builder
	tag string
}

func (w orderWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.log.WriteString(w.tag + ": " + string(p))
}

func TestStdoutBeforeStderr(t *testing.T) {
	d, _, _ := viewDeps(t, nil)
	d.PCP = failPCP{}
	var log strings.Builder
	mu := &sync.Mutex{}
	d.Stdout = orderWriter{mu, &log, "out"}
	d.Stderr = orderWriter{mu, &log, "err"}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	d.FS.(*fstest.Fake).Add("/etc/poolwarden.yaml", strings.Replace(`cluster: prod-a
audit: {file: /var/lib/poolwarden/audit.jsonl}
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
  - name: db2
    address: 10.0.0.12
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
`, "/var/lib/poolwarden/audit.jsonl", auditPath, 1), 0o600, d.FS.(*fstest.Fake).Uid)
	code := Execute([]string{"--operator", "t", "detach", "1", "--yes", "--confirm", "1"}, d)
	if code == 0 {
		t.Fatal("want failure")
	}
	got := log.String()
	plan := strings.Index(got, "detach node 1")
	errIdx := strings.Index(got, "error:")
	if plan < 0 || errIdx < 0 || plan > errIdx {
		t.Fatalf("plan must precede error block:\n%s", got)
	}
}

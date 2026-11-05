package service

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
	"github.com/smarsh/poolwarden/internal/pcp"
)

type fakePCP struct {
	calls []string
	err   error
}

func (f *fakePCP) PromoteNode(ctx context.Context, id int, sw, gr bool) (pcp.Result, error) {
	f.calls = append(f.calls, "pcp_promote_node")
	return pcp.Result{Args: []string{"pcp_promote_node", "-w", "-s", "-n", "1"},
		Stdout: "pcp promote ok", Stderr: "warn: slow"}, f.err
}
func (f *fakePCP) DetachNode(ctx context.Context, id int, gr bool) (pcp.Result, error) {
	f.calls = append(f.calls, "pcp_detach_node")
	return pcp.Result{}, f.err
}
func (f *fakePCP) AttachNode(ctx context.Context, id int) (pcp.Result, error) {
	f.calls = append(f.calls, "pcp_attach_node")
	return pcp.Result{}, f.err
}
func (f *fakePCP) RecoveryNode(ctx context.Context, id int) (pcp.Result, error) {
	f.calls = append(f.calls, "pcp_recovery_node")
	return pcp.Result{}, f.err
}
func (f *fakePCP) ReloadConfig(ctx context.Context, scope string) (pcp.Result, error) {
	f.calls = append(f.calls, "pcp_reload_config "+scope)
	return pcp.Result{Args: []string{"-s", scope}}, f.err
}

// scriptSrc returns snapshots in order, then the last forever.
type scriptSrc struct {
	snaps []*model.Snapshot
	i     int
}

func (s *scriptSrc) Collect(ctx context.Context) *model.Snapshot {
	if s.i < len(s.snaps) {
		s.i++
	}
	return s.snaps[s.i-1]
}

type fakeConfirm struct {
	confirmed bool
	aborted   bool
	operator  string
	ticket    string
	question  string
}

func (f *fakeConfirm) AskOperator(ctx context.Context) (string, error) { return f.operator, nil }
func (f *fakeConfirm) AskTicket(ctx context.Context) (string, error)   { return f.ticket, nil }
func (f *fakeConfirm) Confirm(ctx context.Context, p Prompt) (Decision, error) {
	f.question = p.Question
	return Decision{Confirmed: f.confirmed, Aborted: f.aborted}, nil
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func mkSvc(t *testing.T, snaps ...*model.Snapshot) (*Service, *fakePCP, *clock.Fake, *fstest.Fake) {
	f := fstest.New()
	c := clock.NewFake(t0)
	cfg := config.Defaults()
	cfg.Cluster = "prod-a"
	cfg.Lag = config.Lag{WarnSeconds: 1800, CritSeconds: 3600}
	cfg.Audit.File = "/var/lib/poolwarden/audit.jsonl"
	cfg.Hosts = []config.Host{
		{Name: "db1", Address: "10.0.0.11"}, {Name: "db2", Address: "10.0.0.12"},
	}
	fp := &fakePCP{}
	w := &audit.Writer{FS: f, Clock: c, Path: cfg.Audit.File,
		Flock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}
	svc := &Service{
		Cfg:       &cfg,
		Collector: &scriptSrc{snaps: snaps},
		Diagnoser: NoopDiagnoser{},
		PCP:       fp,
		Clock:     c,
		FS:        f,
		Recorder: &audit.Recorder{W: w, SessionID: "s1", Cluster: "prod-a", Clock: c,
			Identity: audit.Identity{Declared: "alice"}},
	}
	for _, s := range snaps {
		s.Settings["failover_command"] = "/x"
		s.Settings["follow_primary_command"] = "/x"
	}
	return svc, fp, c, f
}

func promotedSnap() *model.Snapshot {
	s := modeltest.SampleSnapshot()
	s.Backends[1].InRecovery = false
	s.Backends[0].InRecovery = true
	s.Pgpool.Nodes[0].Role = "standby"
	s.Pgpool.Nodes[1].Role = "primary"
	s.Backends[0].Standbys = nil
	s.Backends[1].Standbys = []model.StandbyRepl{
		{AppName: "db1", ClientAddr: "10.0.0.11", State: "streaming",
			SentLSN: "3/A1B2C3D4", WriteLSN: "3/A1B2C3D4", FlushLSN: "3/A1B2C3D4",
			ReplayLSN: "3/A1B2C3D4"},
	}
	return s
}

// advance runs the fake clock forward until stop fires (verify polls sleep
// on fake timers, so tests must feed time concurrently).
func advance(c *clock.Fake, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		c.Advance(2 * time.Second)
		time.Sleep(500 * time.Microsecond)
	}
}

func runCtl(fn func() (*ControlResult, error)) (*ControlResult, error) {
	type out struct {
		r   *ControlResult
		err error
	}
	ch := make(chan out, 1)
	go func() {
		r, err := fn()
		ch <- out{r, err}
	}()
	select {
	case o := <-ch:
		return o.r, o.err
	case <-time.After(5 * time.Second):
		return nil, context.DeadlineExceeded
	}
}

func TestPromoteGO(t *testing.T) {
	svc, fp, c, f := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		promotedSnap())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := runCtl(func() (*ControlResult, error) {
		return svc.Promote(context.Background(), 1, false, false, ControlOpts{TTY: true})
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Outcome != "success" || res.EventID == "" {
		t.Fatalf("%+v", res)
	}
	if len(fp.calls) != 1 || fp.calls[0] != "pcp_promote_node" {
		t.Fatalf("%v", fp.calls)
	}
	names := []string{}
	for _, st := range res.Timeline {
		names = append(names, st.Name)
	}
	want := []string{"preflight", "pcp_promote_node", "verify"}
	for i, w := range want {
		if i >= len(names) || names[i] != w {
			t.Fatalf("steps %v", names)
		}
	}
	_ = c
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	lines := countLines(data)
	if lines != 2 {
		t.Fatalf("audit lines %d", lines)
	}
}

func TestPromoteNOGORefused(t *testing.T) {
	s1 := modeltest.SampleSnapshot()
	s1.Backends[1].ReplayDelaySec = 1900
	svc, fp, _, f := mkSvc(t, s1, s1)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	res, err := svc.Promote(context.Background(), 1, false, false,
		ControlOpts{TTY: true})
	if err == nil {
		t.Fatal("want NO-GO error")
	}
	if res == nil || res.Preflight == nil || res.Preflight.Verdict != "NO-GO" {
		t.Fatalf("refused result must carry preflight: %+v", res)
	}
	var nogo int
	for _, ck := range res.Preflight.Checks {
		if ck.Result == "NO-GO" {
			nogo++
		}
	}
	if nogo == 0 {
		t.Error("no NO-GO checks in result")
	}
	var ec *exitcode.Error
	if errors.As(err, &ec) && !strings.Contains(ec.Cause, "NO-GO") {
		t.Errorf("cause %q must list NO-GO checks", ec.Cause)
	}
	if len(fp.calls) != 0 {
		t.Error("pcp called")
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !containsStr(string(data), `"refused"`) {
		t.Error("no refused entry")
	}
	if e := exitCodeOf(err); e != 3 {
		t.Errorf("exit %d", e)
	}
}

func TestPromoteNOGOForce(t *testing.T) {
	s1 := modeltest.SampleSnapshot()
	s1.Backends[1].ReplayDelaySec = 1900
	svc, fp, c, _ := mkSvc(t, s1, s1, promotedSnap())
	fc := &fakeConfirm{confirmed: true}
	svc.Confirmer = fc
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := runCtl(func() (*ControlResult, error) {
		return svc.Promote(context.Background(), 1, false, false, ControlOpts{TTY: true, Force: true})
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Outcome != "success" || len(fp.calls) != 1 {
		t.Fatalf("%v %v", res.Outcome, fp.calls)
	}
	if fc.question == "" {
		t.Error("no force question")
	}
}

func TestPromoteNOGOForceWrongToken(t *testing.T) {
	s1 := modeltest.SampleSnapshot()
	s1.Backends[1].ReplayDelaySec = 1900
	svc, fp, _, _ := mkSvc(t, s1)
	svc.Confirmer = &fakeConfirm{confirmed: false}
	_, err := svc.Promote(context.Background(), 1, false, false,
		ControlOpts{TTY: true, Force: true})
	if err == nil || len(fp.calls) != 0 {
		t.Fatalf("%v %v", err, fp.calls)
	}
}

func TestPromoteVerifyTimeout(t *testing.T) {
	// snapshots never flip → verify times out
	svc, fp, c, _ := mkSvc(t, modeltest.SampleSnapshot())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	done := make(chan error, 1)
	doneRes := make(chan *ControlResult, 1)
	go func() {
		res, err := svc.Promote(context.Background(), 1, false, false,
			ControlOpts{TTY: true})
		done <- err
		doneRes <- res
	}()
	// advance fake clock past the 120s verify budget
	for i := 0; i < 65; i++ {
		c.Advance(2 * time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err == nil || !containsStr(err.Error(), "promotion state unclear") {
			t.Fatalf("%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("verify did not end")
	}
	// timeline carries the pcp step output
	res, _ := <-doneRes
	found := false
	for _, st := range res.Timeline {
		if st.Name == "pcp_promote_node" {
			found = strings.Contains(st.Output, "warn: slow")
		}
	}
	if !found {
		t.Errorf("timeline missing pcp output: %+v", res.Timeline)
	}
	if len(fp.calls) != 1 {
		t.Error("rollback? pcp called twice")
	}
}

func TestDetachPrimaryToken(t *testing.T) {
	svc, fp, _, f := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot())
	fc := &fakeConfirm{confirmed: false} // typed wrong token
	svc.Confirmer = fc
	_, err := svc.Detach(context.Background(), 0, false, ControlOpts{TTY: true})
	if err == nil || len(fp.calls) != 0 {
		t.Fatalf("%v %v", err, fp.calls)
	}
	if !containsStr(fc.question, "detach-primary-0") {
		t.Errorf("question %q", fc.question)
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !containsStr(string(data), `"aborted"`) {
		t.Error("no aborted entry")
	}
}

func TestNonTTY(t *testing.T) {
	svc, fp, _, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		promotedSnap())
	// no confirmer needed; TTY false
	_, err := svc.Promote(context.Background(), 1, false, false,
		ControlOpts{TTY: false})
	if err == nil || len(fp.calls) != 0 {
		t.Fatalf("%v", err)
	}
	svc2, fp2, c2, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		promotedSnap())
	stop2 := make(chan struct{})
	defer close(stop2)
	go advance(c2, stop2)
	res, err := runCtl(func() (*ControlResult, error) {
		return svc2.Promote(context.Background(), 1, false, false,
			ControlOpts{TTY: false, Yes: true, Confirm: "1"})
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Outcome != "success" || len(fp2.calls) != 1 {
		t.Fatalf("%v %v", res.Outcome, fp2.calls)
	}
}

func TestReadOnlyAndAllowed(t *testing.T) {
	svc, fp, _, _ := mkSvc(t, modeltest.SampleSnapshot())
	_, err := svc.Promote(context.Background(), 1, false, false,
		ControlOpts{ReadOnly: true})
	if err == nil || len(fp.calls) != 0 {
		t.Fatalf("%v", err)
	}
	svc.Cfg.AllowedOperators = []string{"bob"}
	_, err = svc.Promote(context.Background(), 1, false, false,
		ControlOpts{})
	if err == nil {
		t.Fatal("want allowed error")
	}
}

func TestAttachWarn(t *testing.T) {
	s0 := modeltest.SampleSnapshot()
	s0.Pgpool.Nodes[1].Status = "down" // preflight needs down
	up := modeltest.SampleSnapshot()
	up.Backends[0].Standbys = nil // attached but not streaming → WARN after 60s
	svc, _, c, _ := mkSvc(t, s0, up, up, up, up)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	done := make(chan struct {
		res *ControlResult
		err error
	}, 1)
	go func() {
		r, err := svc.Attach(context.Background(), 1, ControlOpts{TTY: true})
		done <- struct {
			res *ControlResult
			err error
		}{r, err}
	}()
	for i := 0; i < 40; i++ {
		c.Advance(2 * time.Second)
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("%v", out.err)
		}
		if out.res.Outcome != "success" || out.res.Warn == "" {
			t.Fatalf("%+v", out.res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach hung")
	}
}

func TestReloadScopes(t *testing.T) {
	svc, fp, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	if _, err := runCtl(func() (*ControlResult, error) {
		return svc.Reload(context.Background(), false, ControlOpts{TTY: true})
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if fp.calls[0] != "pcp_reload_config c" {
		t.Errorf("%v", fp.calls)
	}
	if _, err := runCtl(func() (*ControlResult, error) {
		return svc.Reload(context.Background(), true, ControlOpts{TTY: true})
	}); err != nil {
		t.Fatalf("%v", err)
	}
	if fp.calls[1] != "pcp_reload_config l" {
		t.Errorf("%v", fp.calls)
	}
}

func TestCtrlCAborted(t *testing.T) {
	svc, fp, _, f := mkSvc(t, modeltest.SampleSnapshot())
	svc.Confirmer = &fakeConfirm{aborted: true}
	_, err := svc.Promote(context.Background(), 1, false, false, ControlOpts{TTY: true})
	if err == nil || len(fp.calls) != 0 {
		t.Fatalf("%v", err)
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !containsStr(string(data), `"aborted"`) {
		t.Error("no aborted entry")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

func exitCodeOf(err error) int {
	var e *exitcode.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

// TestPlanGolden captures the promote plan text (02 §4.1 shape).
func TestPlanGolden(t *testing.T) {
	svc, fp, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		promotedSnap())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := runCtl(func() (*ControlResult, error) {
		return svc.Promote(context.Background(), 1, false, false, ControlOpts{TTY: true})
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	goldenSvc(t, "promote_plan.golden", res.Plan)
	var tl []string
	for _, s := range res.Timeline {
		tl = append(tl, s.Name+" "+s.Result)
	}
	goldenSvc(t, "promote_timeline.golden", strings.Join(tl, "\n")+"\n")
	_ = fp
}

var svcUpdate = flag.Bool("svcupdate", false, "update goldens")

func goldenSvc(t *testing.T, name, got string) {
	p := "testdata/" + name
	if *svcUpdate {
		os.WriteFile(p, []byte(got), 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil || got != string(want) {
		t.Errorf("%s mismatch (-svcupdate to fix)\nGOT:\n%s", name, got)
	}
}

// TestAuditWriteFails: OpenAppend failure → pcp never called (M8 fix 2).
func TestAuditWriteFails(t *testing.T) {
	svc, fp, c, f := mkSvc(t, modeltest.SampleSnapshot())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	f.FailOpenAppend = true
	_, err := svc.Promote(context.Background(), 1, false, false, ControlOpts{TTY: true})
	if err == nil || !containsStr(err.Error(), "audit") {
		t.Fatalf("%v", err)
	}
	if len(fp.calls) != 0 {
		t.Error("pcp called without audit")
	}
	_ = c
}

// BUG O: after switchover the old primary must rejoin as a streaming standby.
func snapOldPrimaryDown() *model.Snapshot {
	s := promotedSnap()
	// old primary (node 0) is down/unreachable after follow_primary failed
	s.Pgpool.Nodes[0].Status = "down"
	s.Backends[0].Reachable = false
	return s
}

func snapOldPrimaryRejoined() *model.Snapshot {
	return promotedSnap() // node0 standby, streaming from node1 (built into promotedSnap)
}

func TestPromoteOldPrimaryWarn(t *testing.T) {
	svc, fp, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		snapOldPrimaryDown())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	done := make(chan error, 1)
	doneRes := make(chan *ControlResult, 1)
	go func() {
		res, err := svc.Promote(context.Background(), 1, false, false,
			ControlOpts{TTY: true})
		done <- err
		doneRes <- res
	}()
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	err := <-done
	res := <-doneRes
	if err != nil {
		t.Fatalf("switchover should succeed despite failed rejoin: %v", err)
	}
	if res.Outcome != "success" {
		t.Errorf("outcome %q", res.Outcome)
	}
	if !strings.Contains(res.Warn, "old primary") || !strings.Contains(res.Warn, "recover 0") {
		t.Errorf("want old-primary warn, got %q", res.Warn)
	}
	if len(fp.calls) != 1 {
		t.Errorf("pcp called %d times", len(fp.calls))
	}
}

func TestPromoteOldPrimaryRejoined(t *testing.T) {
	svc, _, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(),
		snapOldPrimaryRejoined())
	svc.Confirmer = &fakeConfirm{confirmed: true}
	done := make(chan error, 1)
	doneRes := make(chan *ControlResult, 1)
	go func() {
		res, err := svc.Promote(context.Background(), 1, false, false,
			ControlOpts{TTY: true})
		done <- err
		doneRes <- res
	}()
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	err := <-done
	res := <-doneRes
	if err != nil {
		t.Fatal(err)
	}
	if res.Warn != "" {
		t.Errorf("no warn expected when the old primary rejoins: %q", res.Warn)
	}
}

// BUG P attach variant: streaming standby, pgpool shows it down → attach, not recover.
func TestPromoteOldPrimaryAttach(t *testing.T) {
	snap := snapOldPrimaryRejoined()
	snap.Pgpool.Nodes[0].Status = "down" // pgpool view lags the actual streaming state
	svc, _, c, _ := mkSvc(t, modeltest.SampleSnapshot(), modeltest.SampleSnapshot(), snap)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	done := make(chan error, 1)
	doneRes := make(chan *ControlResult, 1)
	go func() {
		res, err := svc.Promote(context.Background(), 1, false, false,
			ControlOpts{TTY: true})
		done <- err
		doneRes <- res
	}()
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	err := <-done
	res := <-doneRes
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warn, "attach 0") {
		t.Errorf("streaming-but-down must suggest attach: %q", res.Warn)
	}
	if strings.Contains(res.Warn, "recover") {
		t.Errorf("must not suggest recover: %q", res.Warn)
	}
}

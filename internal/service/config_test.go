package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/remote/remotetest"
)

// fakeRemoteOps serves scripted file content per host.
type fakeRemoteOps struct {
	files     map[string][]byte // host:path → bytes
	writes    []string
	failWrite map[string]bool
	execs     []string
}

func (f *fakeRemoteOps) key(h config.Host, p string) string { return h.Name + ":" + p }

func (f *fakeRemoteOps) ReadFile(ctx context.Context, h config.Host, p string) ([]byte, error) {
	b, ok := f.files[f.key(h, p)]
	if !ok {
		return nil, fmtError("missing " + f.key(h, p))
	}
	return b, nil
}

func (f *fakeRemoteOps) WriteFileAtomic(ctx context.Context, h config.Host, p string, b []byte) error {
	if f.failWrite[f.key(h, p)] {
		return fmtError("write failed")
	}
	f.writes = append(f.writes, f.key(h, p))
	f.files[f.key(h, p)] = b
	return nil
}

func (f *fakeRemoteOps) Exec(ctx context.Context, h config.Host, args []string, stdin []byte) (remote.ExecResult, error) {
	f.execs = append(f.execs, h.Name+" "+strings.Join(args, " "))
	return remote.ExecResult{Exit: 0}, nil
}

type fmtError string

func (e fmtError) Error() string { return string(e) }

func confCfg() *config.Config {
	cfg := config.Defaults()
	cfg.StateDir = "/var/lib/poolwarden"
	cfg.Audit.File = "/var/lib/poolwarden/audit.jsonl"
	pg := &config.PgpoolPaths{Conf: "/etc/pgpool-II/pgpool.conf",
		HBA: "/etc/pgpool-II/pool_hba.conf", PCPConf: "/etc/pgpool-II/pcp.conf",
		PoolPasswd: "/etc/pgpool-II/pool_passwd", Service: "pgpool-II"}
	cfg.Hosts = []config.Host{
		{Name: "pgp1", Address: "10.0.0.21", Roles: []config.Role{config.RolePgpool}, Pgpool: pg},
		{Name: "pgp2", Address: "10.0.0.22", Roles: []config.Role{config.RolePgpool}, Pgpool: pg},
	}
	return &cfg
}

func confSvc(t *testing.T, conf []byte, fr *fakeRemoteOps, snaps ...*model.Snapshot) (*Service, *clock.Fake, *fstest.Fake) {
	f := fstest.New()
	c := clock.NewFake(t0)
	cfg := confCfg()
	fp := &fakePCP{}
	w := &audit.Writer{FS: f, Clock: c, Path: cfg.Audit.File,
		Flock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}
	if len(snaps) == 0 {
		snaps = []*model.Snapshot{modeltest.SampleSnapshot()}
	}
	svc := &Service{
		Cfg:       cfg,
		Collector: &scriptSrc{snaps: snaps},
		Diagnoser: NoopDiagnoser{},
		PCP:       fp,
		Clock:     c,
		FS:        f,
		Recorder: &audit.Recorder{W: w, SessionID: "s1", Cluster: "prod-a", Clock: c,
			Identity: audit.Identity{Declared: "alice", Hostname: "pgp1"}},
		ConfigRemote: fr,
		ShowParam: func(ctx context.Context, h config.Host, p string) (string, error) {
			if fr != nil && fr.files != nil {
				if b, ok := fr.files[h.Name+":/etc/pgpool-II/pgpool.conf"]; ok {
					pf, _ := pgconf.Parse(b)
					if v, found, _ := pf.Get(p); found {
						return v, nil
					}
				}
			}
			return "", nil
		},
		Confirmer: &fakeConfirm{confirmed: true},
		Local:     "pgp2",
	}
	return svc, c, f
}

func mkFiles(conf []byte) *fakeRemoteOps {
	return &fakeRemoteOps{
		files: map[string][]byte{
			"pgp1:/etc/pgpool-II/pgpool.conf": conf,
			"pgp2:/etc/pgpool-II/pgpool.conf": conf,
		},
		failWrite: map[string]bool{},
	}
}

func stage(t *testing.T, svc *Service, c pgconf.Change) {
	if err := svc.StageChange(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

var testConf = []byte("num_init_children = 32\nmax_pool = 4\nlog_connections = off\n")

func TestApplyHappyReload(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, f := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Outcome != "success" {
		t.Fatalf("%+v", res)
	}
	if len(fr.writes) != 2 {
		t.Fatalf("writes %v", fr.writes)
	}
	pf1, _ := pgconf.Parse(fr.files["pgp1:/etc/pgpool-II/pgpool.conf"])
	if v, _, _ := pf1.Get("log_connections"); v != "on" {
		t.Error("pgp1 not updated")
	}
	pf2, _ := pgconf.Parse(fr.files["pgp2:/etc/pgpool-II/pgpool.conf"])
	if v, _, _ := pf2.Get("log_connections"); v != "on" {
		t.Error("pgp2 not updated")
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(data), `"backup_version"`) && !strings.Contains(string(data), `"outcome":"success"`) {
		t.Error("audit missing")
	}
	// manifest exists
	dirs, _ := f.ReadDir("/var/lib/poolwarden/config")
	if len(dirs) != 1 {
		t.Fatal("no backup dir")
	}
	mb, _ := f.ReadFile("/var/lib/poolwarden/config/" + dirs[0].Name() + "/manifest.json")
	if !strings.Contains(string(mb), "sha256_before") {
		t.Error("manifest incomplete")
	}
}

func TestApplyWriteFailRollback(t *testing.T) {
	fr := mkFiles(testConf)
	fr.failWrite["pgp2:/etc/pgpool-II/pgpool.conf"] = true
	svc, c, f := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil {
		t.Fatal("want failure")
	}
	if exitCodeOf(err) != 5 {
		t.Errorf("exit %d", exitCodeOf(err))
	}
	// pgp1 was written then restored (leader last → pgp1 non-leader? Local=pgp2 → pgp1 leader? watchdog off → Local last: pgp2 is leader. So pgp1 written first.)
	if v, _, _ := mustParse(fr.files["pgp1:/etc/pgpool-II/pgpool.conf"]).Get("log_connections"); v != "off" {
		t.Error("pgp1 not restored")
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(data), "rolled_back") {
		t.Error("no rolled_back audit")
	}
}

func mustParse(b []byte) *pgconf.File {
	f, _ := pgconf.Parse(b)
	return f
}

func TestApplyRollbackFails(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	// write succeeds on pgp1, fails on pgp2; then restoration also fails on pgp1
	orig := fr.writes
	_ = orig
	fr.failWrite["pgp2:/etc/pgpool-II/pgpool.conf"] = true
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	// flip the switch after pgp1 is written — simplest: make pgp1 write fail too,
	// but then rollback has nothing to restore. Instead inject fail on restore
	// by marking pgp1 fail AFTER first write — use a counter wrapper:
	fr2 := &countingRemote{fakeRemoteOps: fr, failAfter: 1, target: "pgp1"}
	svc.ConfigRemote = fr2
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil {
		t.Fatal("want failure")
	}
	if exitCodeOf(err) != 6 {
		t.Errorf("exit %d want 6", exitCodeOf(err))
	}
}

type countingRemote struct {
	*fakeRemoteOps
	writes    int
	failAfter int
	target    string
}

func (c *countingRemote) WriteFileAtomic(ctx context.Context, h config.Host, p string, b []byte) error {
	c.writes++
	if h.Name == c.target && c.writes > c.failAfter {
		return fmtError("restore failed")
	}
	return c.fakeRemoteOps.WriteFileAtomic(ctx, h, p, b)
}

func TestApplyRestartOrder(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "port", Old: "9999", New: "9998", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	// restart order: non-leader (pgp1) first, leader (pgp2, == Local) last
	if len(fr.execs) != 2 || !strings.Contains(fr.execs[0], "pgp1") || !strings.Contains(fr.execs[1], "pgp2") {
		t.Fatalf("%v", fr.execs)
	}
}

func TestApplyDrift(t *testing.T) {
	fr := mkFiles(testConf)
	fr.files["pgp2:/etc/pgpool-II/pgpool.conf"] = []byte("num_init_children = 64\n")
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil || exitCodeOf(err) != 4 {
		t.Fatalf("%v", err)
	}
	if len(fr.writes) != 0 {
		t.Error("wrote despite drift")
	}
}

func TestApplyLockHeld(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	// a lock whose acquire always fails
	svc.Lock = &remote.ClusterLock{Pool: remote.NewPool(remotetest.NewDialer(), config.SSH{}),
		Cfg: svc.Cfg, Clock: c, FS: svc.FS,
		LocalFlock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return fmtError("held") }, nil
		}}
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil {
		t.Fatal("want lock error")
	}
	if len(fr.writes) != 0 {
		t.Error("wrote despite lock")
	}
}

func TestBackendRemovePrimary(t *testing.T) {
	fr := mkFiles(testConf)
	svc, _, _ := confSvc(t, testConf, fr)
	err := svc.BackendRemove(context.Background(), 0, "alice")
	if err == nil || exitCodeOf(err) != 4 {
		t.Fatalf("%v", err)
	}
}

func TestPCPUserSetNoLeak(t *testing.T) {
	fr := mkFiles(testConf)
	fr.files["pgp1:/etc/pgpool-II/pcp.conf"] = []byte("admin:5f4dcc3b5aa765d61d8327deb882cf99\n")
	fr.files["pgp2:/etc/pgpool-II/pcp.conf"] = fr.files["pgp1:/etc/pgpool-II/pcp.conf"]
	svc, c, f := confSvc(t, testConf, fr)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	err := svc.PCPUserSet(context.Background(), "bob", "topsecret", "alice",
		ControlOpts{TTY: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	data, _ := fr.files["pgp1:/etc/pgpool-II/pcp.conf"]
	if !strings.Contains(string(data), "bob:"+pgconf.MD5Hex("topsecret")) {
		t.Error("user not written")
	}
	if strings.Contains(string(data), "topsecret") {
		t.Error("plaintext leaked to pcp.conf")
	}
	ad, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if strings.Contains(string(ad), "topsecret") {
		t.Error("plaintext leaked to audit")
	}
	// staged.json must not contain the password
	if sb, err := f.ReadFile("/var/lib/poolwarden/staged.json"); err == nil &&
		strings.Contains(string(sb), "topsecret") {
		t.Error("password staged")
	}
}

func TestConfigGetGolden(t *testing.T) {
	fr := mkFiles(testConf)
	svc, _, _ := confSvc(t, testConf, fr)
	row, err := svc.ConfigGet(context.Background(), "num_init_children")
	if err != nil {
		t.Fatal(err)
	}
	if row.PerHost["pgp1"] != "32" || row.Drift != "file≠live" {
		t.Fatalf("%+v", row)
	}
}

func TestConfigDiffExport(t *testing.T) {
	fr := mkFiles(testConf)
	svc, _, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	d, err := svc.ConfigDiff(context.Background())
	if err != nil || !strings.Contains(d, "-log_connections = off") || !strings.Contains(d, "+log_connections = on") {
		t.Fatalf("%s %v", d, err)
	}
	b, err := svc.ConfigExport(context.Background())
	if err != nil || !strings.Contains(string(b), "log_connections = on") {
		t.Fatal("export wrong")
	}
}

func TestApplyPlanGolden(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	goldenSvc(t, "apply_plan.golden", res.Plan)
}

func TestApplyFailGolden(t *testing.T) {
	fr := mkFiles(testConf)
	fr.failWrite["pgp2:/etc/pgpool-II/pgpool.conf"] = true
	svc, c, _ := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	_, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil {
		t.Fatal("want fail")
	}
	var e interface{ Detail() string }
	_ = e
	var ee *exitcode.Error
	if errors.As(err, &ee) {
		goldenSvc(t, "apply_fail.golden", ee.Msg+"\n"+ee.Detail)
	}
}

func TestPoolUserRemove(t *testing.T) {
	fr := mkFiles(testConf)
	fr.files["pgp1:/etc/pgpool-II/pool_passwd"] = []byte("alice:md5x\nbob:md5y\n")
	fr.files["pgp2:/etc/pgpool-II/pool_passwd"] = fr.files["pgp1:/etc/pgpool-II/pool_passwd"]
	svc, c, _ := confSvc(t, testConf, fr)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	if err := svc.PoolUserRemove(context.Background(), "bob", ControlOpts{TTY: true}); err != nil {
		t.Fatal(err)
	}
	if d := string(fr.files["pgp1:/etc/pgpool-II/pool_passwd"]); containsStr(d, "bob:") {
		t.Error("not removed")
	}
}

func TestRollbackReloadClass(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, f := confSvc(t, testConf, fr)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	if _, err := svc.Apply(context.Background(), ControlOpts{TTY: true}); err != nil {
		t.Fatal(err)
	}
	// rollback the version → should reload (log_connections is reload-class)
	dirs, _ := f.ReadDir("/var/lib/poolwarden/config")
	ver := dirs[0].Name()
	fp := svc.PCP.(*fakePCP)
	fp.calls = nil
	fc := &fakeConfirm{confirmed: true}
	svc.Confirmer = fc
	res, err := svc.Rollback(context.Background(), ver, ControlOpts{TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.question, `"apply"`) {
		t.Errorf("reload-class rollback must ask token apply, got %q", fc.question)
	}
	if res.Outcome != "success" {
		t.Fatalf("%+v", res)
	}
	if len(fp.calls) == 0 {
		t.Error("no reload after rollback")
	}
	if v, _, _ := mustParse(fr.files["pgp1:/etc/pgpool-II/pgpool.conf"]).Get("log_connections"); v != "off" {
		t.Error("not restored")
	}
}

func TestApplyVerifyPollsLive(t *testing.T) {
	fr := mkFiles(testConf)
	svc, c, _ := confSvc(t, testConf, fr)
	// ShowParam returns the old value for the first 3 calls per param,
	// then the new one — apply must still succeed (async pgpool reload).
	calls := map[string]int{}
	svc.ShowParam = func(ctx context.Context, h config.Host, p string) (string, error) {
		calls[p]++
		if calls[p] <= 3 {
			return "off", nil
		}
		return "on", nil
	}
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "log_connections", Old: "off", New: "on", Operator: "alice"})
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)
	res, err := svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != "success" {
		t.Fatalf("%+v", res)
	}
	if calls["log_connections"] < 4 {
		t.Errorf("expected polling, got %d calls", calls["log_connections"])
	}
}

func TestValidateWarnVsFail(t *testing.T) {
	fr := mkFiles(testConf)
	// CONN_BUDGET fails but is WARN-class → validate exits clean (fails=0)
	snap := modeltest.SampleSnapshot()
	snap.Settings["num_init_children"] = "9999"
	snap.Backends = []model.Backend{{Name: "db1", MaxConns: 10, Reachable: true}}
	svc, _, _ := confSvc(t, testConf, fr, snap)
	_, fails, err := svc.ConfigValidate(context.Background())
	if err != nil || fails != 0 {
		t.Errorf("CONN_BUDGET warn must not fail validate: fails=%d err=%v", fails, err)
	}
	// SR_CHECK is FAIL-class → fails>0
	snap.Settings["sr_check_user"] = "pgpool"
	snap.Settings["sr_check_period"] = "0"
	svc, _, _ = confSvc(t, testConf, fr, snap)
	if _, fails, _ = svc.ConfigValidate(context.Background()); fails == 0 {
		t.Error("SR_CHECK fail must count")
	}
	// apply preflight: FAIL-class rule → NO-GO
	snap.Settings["num_init_children"] = "x" // invalid int range → value fail
	svc, _, _ = confSvc(t, testConf, fr, snap)
	stage(t, svc, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: "num_init_children", Old: "32", New: "x", Operator: "alice"})
	_, err = svc.Apply(context.Background(), ControlOpts{TTY: true})
	if err == nil || exitCodeOf(err) != 3 {
		t.Errorf("apply must refuse invalid value, err=%v", err)
	}
}

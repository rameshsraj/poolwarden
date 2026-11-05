package cli

import (
	"bytes"
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/remote/remotetest"
	"github.com/smarsh/poolwarden/internal/service"
)

type fakePCPRun struct{ calls []string }

func (f *fakePCPRun) PromoteNode(ctx context.Context, id int, sw, gr bool) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (f *fakePCPRun) DetachNode(ctx context.Context, id int, gr bool) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (f *fakePCPRun) AttachNode(ctx context.Context, id int) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (f *fakePCPRun) RecoveryNode(ctx context.Context, id int) (pcp.Result, error) {
	return pcp.Result{}, nil
}
func (f *fakePCPRun) ReloadConfig(ctx context.Context, s string) (pcp.Result, error) {
	f.calls = append(f.calls, s)
	return pcp.Result{}, nil
}

// cfgRemoteOps fakes the remote file/exec layer for cli config tests.
type cfgFakeRemote struct {
	files  map[string][]byte
	writes []string
}

func (f *cfgFakeRemote) ReadFile(ctx context.Context, h config.Host, p string) ([]byte, error) {
	return f.files[h.Name+":"+p], nil
}
func (f *cfgFakeRemote) WriteFileAtomic(ctx context.Context, h config.Host, p string, b []byte) error {
	f.writes = append(f.writes, h.Name+":"+p)
	f.files[h.Name+":"+p] = b
	return nil
}
func (f *cfgFakeRemote) Exec(ctx context.Context, h config.Host, args []string, stdin []byte) (remote.ExecResult, error) {
	return remote.ExecResult{Exit: 0}, nil
}

var cfgTestConf = []byte("num_init_children = 32\nmax_pool = 4\nlog_connections = off\nuse_watchdog = 'off'\n")

func cfgDeps(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *cfgFakeRemote) {
	d, out, errb := viewDeps(t, nil)
	fr := &cfgFakeRemote{files: map[string][]byte{
		"db1:/etc/pgpool-II/pgpool.conf": cfgTestConf,
		"db2:/etc/pgpool-II/pgpool.conf": cfgTestConf,
		"db1:/etc/pgpool-II/pcp.conf":    []byte("admin:5f4dcc3b5aa765d61d8327deb882cf99\n"),
	}}
	d.ConfigRemote = fr
	d.PCP = &fakePCPRun{}
	d.Flock = func(string) (func() error, func() error, error) {
		return func() error { return nil }, func() error { return nil }, nil
	}
	// cluster lock over fake sessions that accept everything
	sess := remotetest.NewSession()
	sess.Execs[""] = remote.ExecResult{Exit: 0}
	dial := remotetest.NewDialer()
	dial.Sessions["db1"] = sess
	dial.Sessions["db2"] = sess
	d.Lock = &remote.ClusterLock{Pool: remote.NewPool(dial, config.SSH{}),
		Cfg: mustCfg(d), Clock: d.Clock, FS: d.FS,
		LocalFlock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}
	d.ShowParam = func(ctx context.Context, h config.Host, p string) (string, error) {
		b := fr.files[h.Name+":/etc/pgpool-II/pgpool.conf"]
		f, _ := pgconf.Parse(b)
		v, found, _ := f.Get(p)
		if !found {
			return "off", nil
		}
		return v, nil
	}
	return d, out, errb, fr
}

func TestConfigGetList(t *testing.T) {
	d, out, errb, _ := cfgDeps(t)
	if code := Execute([]string{"config", "get", "num_init_children"}, d); code != 0 {
		t.Fatalf("code %d %s", code, errb)
	}
	goldenView(t, "config_get.table.golden", out.String())
	out.Reset()
	if code := Execute([]string{"config", "list"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "config_list.table.golden", out.String())
}

func TestConfigSetDiffDiscard(t *testing.T) {
	d, out, _, _ := cfgDeps(t)
	if code := Execute([]string{"config", "set", "log_connections", "on"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	out.Reset()
	if code := Execute([]string{"config", "diff"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "config_diff.golden", out.String())
	// discard with --yes
	if code := Execute([]string{"config", "discard", "--yes"}, d); code != 0 {
		t.Fatalf("discard %d", code)
	}
	if code := Execute([]string{"config", "diff"}, d); code != 0 {
		t.Fatal("diff after discard")
	}
}

func TestConfigApplyGolden(t *testing.T) {
	d, out, _, fr := cfgDeps(t)
	if code := Execute([]string{"config", "set", "log_connections", "on"}, d); code != 0 {
		t.Fatalf("set %d", code)
	}
	out.Reset()
	// non-TTY apply: --yes --confirm apply
	code := Execute([]string{"config", "apply", "--yes", "--confirm", "apply"}, d)
	if code != 0 {
		t.Fatalf("apply %d: %s", code, out)
	}
	if len(fr.writes) != 2 {
		t.Fatalf("writes %v", fr.writes)
	}
	norm := regexp.MustCompile(`event: [0-9a-f]+`).ReplaceAllString(out.String(), "event: <id>")
	goldenView(t, "config_apply.golden", norm)
}

var _ = time.Now
var _ = service.Service{}

func mustCfg(d *Deps) *config.Config {
	cfg, err := config.Load(d.FS, "/etc/poolwarden.yaml")
	if err != nil {
		panic(err)
	}
	return cfg
}

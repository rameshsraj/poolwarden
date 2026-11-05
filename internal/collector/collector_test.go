package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/backendsql"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/pcp/pcptest"
	"github.com/smarsh/poolwarden/internal/pgpoolsql"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/remote/remotetest"
)

// fake pgpool client

type fakeClient struct {
	version string
	nodes   []model.PoolNode
	sets    map[string]string
	verErr  error
}

func (f *fakeClient) Version(context.Context) (string, error) { return f.version, f.verErr }
func (f *fakeClient) PoolNodes(context.Context) ([]model.PoolNode, error) {
	return f.nodes, nil
}
func (f *fakeClient) PoolProcesses(context.Context) ([]model.PoolProcess, error) {
	return nil, nil
}
func (f *fakeClient) PoolPools(context.Context) ([]model.PoolPool, error) { return nil, nil }
func (f *fakeClient) HealthCheckStats(context.Context) ([]model.HealthStat, error) {
	return nil, nil
}
func (f *fakeClient) BackendStats(context.Context) ([]model.BackendStat, error) {
	return nil, nil
}
func (f *fakeClient) Show(_ context.Context, p string) (string, error) {
	return f.sets[p], nil
}
func (f *fakeClient) ShowAll(context.Context) (map[string]string, error) { return f.sets, nil }
func (f *fakeClient) Close(context.Context) error                        { return nil }

type fakeDialer struct {
	client *fakeClient
	err    error
	block  bool
}

func (d *fakeDialer) Dial(ctx context.Context, ep config.Endpoint) (pgpoolsql.Client, error) {
	if d.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d.err != nil {
		return nil, d.err
	}
	return d.client, nil
}

// fake prober

type fakeProber struct {
	backends map[string]model.Backend
	block    bool
}

func (p *fakeProber) Probe(ctx context.Context, a backendsql.Addr) model.Backend {
	if p.block {
		<-ctx.Done()
		return model.Backend{Host: a.Host, Port: a.Port, Err: ctx.Err().Error()}
	}
	if b, ok := p.backends[a.Host]; ok {
		return b
	}
	return model.Backend{Host: a.Host, Port: a.Port, Reachable: true}
}

func testConfig(to time.Duration) *config.Config {
	cfg := config.Defaults()
	cfg.Cluster = "prod-a"
	cfg.Timeout = to
	cfg.Hosts = []config.Host{
		{Name: "db1", Address: "10.0.0.11", Roles: []config.Role{config.RolePgpool, config.RolePG},
			SSHUser: "root",
			Pgpool:  &config.PgpoolPaths{Conf: "/etc/pgpool-II/pgpool.conf"},
			PG:      &config.PGPaths{Port: 5432, LogDir: "/var/log/pg"}},
		{Name: "db2", Address: "10.0.0.12", Roles: []config.Role{config.RolePgpool, config.RolePG},
			SSHUser: "root",
			Pgpool:  &config.PgpoolPaths{Conf: "/etc/pgpool-II/pgpool.conf"},
			PG:      &config.PGPaths{Port: 5432, LogDir: "/var/log/pg"}},
	}
	return &cfg
}

func readFixture(t *testing.T, pkg, name string) string {
	t.Helper()
	b, err := os.ReadFile("../" + pkg + "/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func buildCollector(cfg *config.Config, d pgpoolsql.Dialer, pr backendsql.Prober,
	fr *pcptest.FakeRunner, remoteSess *remotetest.FakeSession) *Collector {
	p := &pcp.PCP{R: fr, Cfg: cfg.PCP}
	var sess Sessions
	if remoteSess != nil {
		sess = &staticSessions{s: remoteSess}
	}
	return &Collector{Cfg: cfg, Pgpool: d, Backends: pr, PCP: p, Remote: sess}
}

type staticSessions struct{ s remote.Session }

func (s *staticSessions) Session(_ context.Context, _ config.Host) (remote.Session, error) {
	return s.s, nil
}

func TestCollectHappyPath(t *testing.T) {
	cfg := testConfig(2 * time.Second)
	client := &fakeClient{
		version: "4.5.1",
		nodes: []model.PoolNode{
			{ID: 0, Hostname: "db1", Port: 5432, Status: "up", Role: "primary"},
			{ID: 1, Hostname: "10.0.0.12", Port: 5432, Status: "up", Role: "standby"},
		},
		sets: map[string]string{"delegate_ip": "10.0.0.100", "num_init_children": "100"},
	}
	fr := pcptest.New()
	fr.On("*", pcptest.Resp{Stdout: readFixture(t, "pcp", "watchdog_info_45.txt")})
	sess := remotetest.NewSession()
	ipOut, _ := os.ReadFile("testdata/ip_addr_show.txt")
	sess.Execs["*addr show*"] = remote.ExecResult{Stdout: ipOut}
	probe := &fakeProber{backends: map[string]model.Backend{
		"10.0.0.11": {Host: "10.0.0.11", Port: 5432, Reachable: true, Version: 160000},
		"10.0.0.12": {Host: "10.0.0.12", Port: 5432, Reachable: true, InRecovery: true, ReplayDelaySec: 2},
	}}
	c := buildCollector(cfg, &fakeDialer{client: client}, probe, fr, sess)
	s := c.Collect(context.Background())
	if s.Degraded {
		t.Fatalf("degraded: %s", s.DegradedReason)
	}
	if s.Pgpool.Version != "4.5.1" || len(s.Pgpool.Nodes) != 2 {
		t.Fatalf("%+v", s.Pgpool)
	}
	if !s.Watchdog.Available || s.Watchdog.LeaderHost != "db1" {
		t.Fatalf("%+v", s.Watchdog)
	}
	if len(s.Backends) != 2 {
		t.Fatalf("backends %+v", s.Backends)
	}
	for _, b := range s.Backends {
		if b.Name == "" {
			t.Errorf("backend %+v not matched to inventory", b)
		}
		if b.NodeID < 0 {
			t.Errorf("backend %+v not matched to node", b)
		}
	}
	if len(s.VIP) != 2 || !s.VIP[0].Present {
		t.Errorf("vip %+v", s.VIP)
	}
	if s.Settings["num_init_children"] != "100" {
		t.Errorf("settings %v", s.Settings)
	}
}

func TestCollectBlockingBackend(t *testing.T) {
	cfg := testConfig(200 * time.Millisecond)
	client := &fakeClient{nodes: []model.PoolNode{
		{ID: 0, Hostname: "db1", Port: 5432, Status: "up"},
	}}
	fr := pcptest.New()
	fr.On("*", pcptest.Resp{Stdout: ""})
	c := buildCollector(cfg, &fakeDialer{client: client}, &fakeProber{block: true}, fr, nil)
	start := time.Now()
	s := c.Collect(context.Background())
	el := time.Since(start)
	if el > 1300*time.Millisecond {
		t.Fatalf("Collect took %s, want < 1.3s", el)
	}
	_ = s
}

func TestCollectDegradedDialError(t *testing.T) {
	cfg := testConfig(time.Second)
	fr := pcptest.New()
	fr.On("pcp_watchdog_info *", pcptest.Resp{Stdout: readFixture(t, "pcp", "watchdog_info_45.txt")})
	fr.On("pcp_node_info *", pcptest.Resp{Stdout: readFixture(t, "pcp", "node_info_all.txt")})
	c := buildCollector(cfg, &fakeDialer{err: errors.New("connection refused")},
		&fakeProber{}, fr, nil)
	s := c.Collect(context.Background())
	if !s.Degraded {
		t.Fatal("want degraded")
	}
	if len(s.Pgpool.Nodes) != 2 || s.Pgpool.Nodes[1].Hostname != "db2" {
		t.Errorf("nodes %+v", s.Pgpool.Nodes)
	}
	if len(s.Backends) != 2 {
		t.Errorf("backends %+v", s.Backends)
	}
}

func TestCollectDegradedDialTimeout(t *testing.T) {
	cfg := testConfig(150 * time.Millisecond)
	fr := pcptest.New()
	fr.On("pcp_watchdog_info *", pcptest.Resp{Stdout: readFixture(t, "pcp", "watchdog_info_45.txt")})
	fr.On("pcp_node_info *", pcptest.Resp{Stdout: readFixture(t, "pcp", "node_info_all.txt")})
	c := buildCollector(cfg, &fakeDialer{block: true}, &fakeProber{}, fr, nil)
	start := time.Now()
	s := c.Collect(context.Background())
	el := time.Since(start)
	if !s.Degraded {
		t.Fatal("want degraded")
	}
	if !contains(s.DegradedReason, "timeout") {
		t.Errorf("reason %q", s.DegradedReason)
	}
	if el > 1300*time.Millisecond {
		t.Fatalf("took %s", el)
	}
	fmt.Println("elapsed", el)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

package pcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/pcp/pcptest"
)

func testPCP(r *FakeRunnerAlias) *PCP {
	return &PCP{R: r, Cfg: config.PCP{Host: "127.0.0.1", Port: 9898, User: "postgres", BinDir: "/usr/bin"}}
}

type FakeRunnerAlias = pcptest.FakeRunner

func lastArgs(t *testing.T, f *pcptest.FakeRunner) []string {
	t.Helper()
	if len(f.Calls) == 0 {
		t.Fatal("no calls")
	}
	return f.Calls[len(f.Calls)-1].Args
}

func baseArgs() []string {
	return []string{"-h", "127.0.0.1", "-p", "9898", "-U", "postgres", "-w"}
}

func TestArgConstruction(t *testing.T) {
	tests := []struct {
		name string
		call func(ctx context.Context, p *PCP) error
		bin  string
		args []string
	}{
		{"NodeInfo", func(ctx context.Context, p *PCP) error {
			_, _, err := p.NodeInfo(ctx, 1)
			return err
		}, "pcp_node_info", []string{"-v", "1"}},
		{"NodeInfoAll", func(ctx context.Context, p *PCP) error {
			_, _, err := p.NodeInfoAll(ctx)
			return err
		}, "pcp_node_info", []string{"-v", "--all"}},
		{"ProcInfo", func(ctx context.Context, p *PCP) error {
			_, err := p.ProcInfo(ctx, 42)
			return err
		}, "pcp_proc_info", []string{"-v", "-P", "42"}},
		{"ProcInfoAll", func(ctx context.Context, p *PCP) error {
			_, err := p.ProcInfoAll(ctx)
			return err
		}, "pcp_proc_info", []string{"-v", "-a"}},
		{"WatchdogInfo", func(ctx context.Context, p *PCP) error {
			_, _, err := p.WatchdogInfo(ctx)
			return err
		}, "pcp_watchdog_info", []string{"-v"}},
		{"AttachNode", func(ctx context.Context, p *PCP) error {
			_, err := p.AttachNode(ctx, 1)
			return err
		}, "pcp_attach_node", []string{"-n", "1"}},
		{"DetachNode", func(ctx context.Context, p *PCP) error {
			_, err := p.DetachNode(ctx, 2, false)
			return err
		}, "pcp_detach_node", []string{"-n", "2"}},
		{"DetachGraceful", func(ctx context.Context, p *PCP) error {
			_, err := p.DetachNode(ctx, 2, true)
			return err
		}, "pcp_detach_node", []string{"-g", "-n", "2"}},
		{"PromoteSwitchover", func(ctx context.Context, p *PCP) error {
			_, err := p.PromoteNode(ctx, 1, true, false)
			return err
		}, "pcp_promote_node", []string{"-s", "-n", "1"}},
		{"PromoteSwitchoverGraceful", func(ctx context.Context, p *PCP) error {
			_, err := p.PromoteNode(ctx, 1, true, true)
			return err
		}, "pcp_promote_node", []string{"-s", "-g", "-n", "1"}},
		{"PromotePgpoolOnly", func(ctx context.Context, p *PCP) error {
			_, err := p.PromoteNode(ctx, 1, false, false)
			return err
		}, "pcp_promote_node", []string{"-n", "1"}},
		{"Recovery", func(ctx context.Context, p *PCP) error {
			_, err := p.RecoveryNode(ctx, 3)
			return err
		}, "pcp_recovery_node", []string{"-n", "3"}},
		{"ReloadCluster", func(ctx context.Context, p *PCP) error {
			_, err := p.ReloadConfig(ctx, "c")
			return err
		}, "pcp_reload_config", []string{"-s", "c"}},
		{"ReloadLocal", func(ctx context.Context, p *PCP) error {
			_, err := p.ReloadConfig(ctx, "l")
			return err
		}, "pcp_reload_config", []string{"-s", "l"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := pcptest.New()
			f.On("*", pcptest.Resp{Stdout: "ok\n"})
			p := testPCP(f)
			_ = tt.call(context.Background(), p) // output is unparseable for some calls; args are what matters
			c := f.Calls[len(f.Calls)-1]
			if c.Bin != "/usr/bin/"+tt.bin {
				t.Errorf("bin %q", c.Bin)
			}
			want := append(baseArgs(), tt.args...)
			got := lastArgs(t, f)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("args %v, want %v", got, want)
			}
		})
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseWatchdog45(t *testing.T) {
	w, err := ParseWatchdogInfo(fixture(t, "watchdog_info_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if w.TotalNodes != 2 || w.AliveRemote != 1 || w.QuorumState != "QUORUM EXIST" {
		t.Errorf("%+v", w)
	}
	if w.LeaderHost != "db1" || !w.VIPUpLocal {
		t.Errorf("%+v", w)
	}
	if len(w.Nodes) != 2 || w.Nodes[0].Status != "LEADER" || w.Nodes[1].Host != "db2" ||
		w.Nodes[0].DelegateIP != "10.0.0.100" || w.Nodes[0].WDPort != 9000 {
		t.Errorf("%+v", w.Nodes)
	}
}

func TestParseWatchdog44(t *testing.T) {
	w, err := ParseWatchdogInfo(fixture(t, "watchdog_info_44.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if w.TotalNodes != 3 || len(w.Nodes) != 3 || w.RemoteNodes != 2 {
		t.Errorf("%+v", w)
	}
}

func TestParseWatchdogLegacyMaster(t *testing.T) {
	w, err := ParseWatchdogInfo(fixture(t, "watchdog_info_legacy_master.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if w.LeaderHost != "db1" || w.LeaderName == "" {
		t.Errorf("%+v", w)
	}
}

func TestParseNodeInfo(t *testing.T) {
	n, err := ParseNodeInfo(fixture(t, "node_info_v.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n.Hostname != "db2" || n.Port != 5432 || n.Status != "up" ||
		n.Role != "standby" || n.ReplicationDelay != 128 ||
		n.ReplicationState != "streaming" || n.ReplicationSyncState != "async" {
		t.Errorf("%+v", n)
	}
	if n.LastStatusChange.IsZero() {
		t.Error("LastStatusChange")
	}
}

func TestParseNodeInfoAll(t *testing.T) {
	nodes, err := ParseNodeInfoAll(fixture(t, "node_info_all.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Role != "primary" || nodes[1].Hostname != "db2" {
		t.Errorf("%+v", nodes)
	}
}

func TestWatchdogInfoResult(t *testing.T) {
	f := pcptest.New()
	f.On("*", pcptest.Resp{Stdout: fixture(t, "watchdog_info_45.txt")})
	p := testPCP(f)
	w, res, err := p.WatchdogInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !w.Available || res.Exit != 0 || len(res.Args) == 0 {
		t.Errorf("%+v %+v", w, res)
	}
}

func TestNonZeroExit(t *testing.T) {
	f := pcptest.New()
	f.On("*", pcptest.Resp{Stderr: "Authentication failed", Exit: 1})
	p := testPCP(f)
	_, err := p.AttachNode(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "Authentication failed") {
		t.Fatalf("err %v", err)
	}
}

func TestParseProcInfo(t *testing.T) {
	rows, err := ParseProcInfo(fixture(t, "proc_info_v.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].PID != 31001 || rows[0].BackendPID != 40001 ||
		rows[1].BackendID != 1 || rows[1].LoadBalanceNode != 1 || rows[0].PoolCounter != 4 {
		t.Errorf("%+v", rows)
	}
}

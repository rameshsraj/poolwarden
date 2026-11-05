package config

import (
	"errors"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "update golden files")

const validYAML = `cluster: prod-a
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
`

func fakeWith(t *testing.T, content string, mode os.FileMode) *fstest.Fake {
	t.Helper()
	f := fstest.New()
	f.Add("/etc/poolwarden.yaml", content, mode, f.Uid)
	return f
}

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.StateDir != "/var/lib/poolwarden" {
		t.Errorf("state_dir %q", c.StateDir)
	}
	if c.Pgpool.Port != 9999 || c.Pgpool.User != "postgres" {
		t.Errorf("pgpool %+v", c.Pgpool)
	}
	if c.PCP.Port != 9898 || c.PCP.BinDir != "/usr/bin" {
		t.Errorf("pcp %+v", c.PCP)
	}
	if c.Interval.String() != "2s" || c.Timeout.String() != "3s" {
		t.Errorf("interval/timeout %v %v", c.Interval, c.Timeout)
	}
	if c.Lag.WarnSeconds != 1800 || c.Lag.CritSeconds != 3600 {
		t.Errorf("lag %+v", c.Lag)
	}
	if c.Audit.RetentionDays != 31 || !c.Audit.Syslog || c.Audit.ReadCommands {
		t.Errorf("audit %+v", c.Audit)
	}
}

func TestLoadValid(t *testing.T) {
	f := fakeWith(t, validYAML, 0o600)
	cfg, err := Load(f, "/etc/poolwarden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster != "prod-a" {
		t.Errorf("cluster %q", cfg.Cluster)
	}
	h := cfg.Hosts[0]
	if h.SSHUser != "root" {
		t.Errorf("ssh_user %q, want default root", h.SSHUser)
	}
	if h.Pgpool.Service != "pgpool-II" {
		t.Errorf("service %q", h.Pgpool.Service)
	}
	if h.PG.Port != 5432 {
		t.Errorf("pg port %d", h.PG.Port)
	}
}

func TestBadRoleRefused(t *testing.T) {
	f := fakeWith(t, `cluster: x
hosts:
  - {name: h1, address: 1.1.1.1, roles: [bogus]}
`, 0o600)
	_, err := Load(f, "/etc/poolwarden.yaml")
	if err == nil {
		t.Fatal("want error")
	}
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Usage {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(e.Cause, "roles") {
		t.Errorf("cause %q should name roles field", e.Cause)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	f := fakeWith(t, validYAML+"bogus_key: 1\n", 0o600)
	_, err := Load(f, "/etc/poolwarden.yaml")
	if err == nil {
		t.Fatal("want error")
	}
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Usage {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(e.Cause, "bogus_key") {
		t.Errorf("cause %q", e.Cause)
	}
}

func TestPermissions(t *testing.T) {
	uid := fstest.New().Uid
	tests := []struct {
		name    string
		mode    os.FileMode
		owner   int
		wantErr bool
	}{
		{"ok root-owned", 0o600, uid, false},
		{"world-writable", 0o666, uid, true},
		{"group-writable", 0o620, uid, true},
		{"wrong owner", 0o600, uid + 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fstest.New()
			f.Add("/etc/poolwarden.yaml", validYAML, tt.mode, tt.owner)
			err := CheckPermissions(f, "/etc/poolwarden.yaml", uid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				var e *exitcode.Error
				if !errors.As(err, &e) || !strings.Contains(e.Hint, "chmod 0600") {
					t.Errorf("hint %q", e)
				}
			}
		})
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load(fstest.New(), "/etc/poolwarden.yaml")
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.Usage {
		t.Fatalf("err %v", err)
	}
	if e.Hint != "poolwarden init" {
		t.Errorf("hint %q", e.Hint)
	}
}

func TestByteSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		err  bool
	}{
		{"100GB", 100 << 30, false},
		{"16MB", 16 << 20, false},
		{"512", 512, false},
		{"1.5TB", int64(1.5 * float64(int64(1)<<40)), false},
		{"2KiB", 2 << 10, false},
		{"42", 42, false},
		{"", 0, true},
		{"10XB", 0, true},
	}
	for _, tt := range tests {
		var b ByteSize
		err := b.UnmarshalYAML(mustNode(t, tt.in))
		if tt.err {
			if err == nil {
				t.Errorf("%q: want error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
		} else if int64(b) != tt.want {
			t.Errorf("%q = %d, want %d", tt.in, b, tt.want)
		}
	}
}

func mustNode(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("v: "+s+"\n"), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0].Content[1]
}

func TestRoundTripGolden(t *testing.T) {
	f := fstest.New()
	data, err := os.ReadFile("testdata/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.Add("/etc/poolwarden.yaml", string(data), 0o600, f.Uid)
	cfg, err := Load(f, "/etc/poolwarden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("testdata/full.golden.yaml", out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile("testdata/full.golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(golden) {
		t.Errorf("marshal mismatch (-update to fix):\n%s", out)
	}
	// Load(Marshal(x)) deep-equals x
	f2 := fstest.New()
	f2.Add("/etc/poolwarden.yaml", string(out), 0o600, f2.Uid)
	cfg2, err := Load(f2, "/etc/poolwarden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, cfg2) {
		t.Errorf("round trip mismatch:\n%#v\n%#v", cfg, cfg2)
	}
}

func TestHelpers(t *testing.T) {
	f := fakeWith(t, validYAML, 0o600)
	cfg, _ := Load(f, "/etc/poolwarden.yaml")
	if len(cfg.PgpoolHosts()) != 1 || len(cfg.PGHosts()) != 1 {
		t.Fatal("role host lists")
	}
	if _, ok := cfg.HostByName("db1"); !ok {
		t.Fatal("HostByName")
	}
	if _, ok := cfg.HostByName("nope"); ok {
		t.Fatal("HostByName bogus")
	}
}

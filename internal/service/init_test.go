package service

import (
	"context"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
)

type fakePrompter struct{ answers map[string]string }

func (p *fakePrompter) Ask(text, def string) string {
	if v, ok := p.answers[text]; ok {
		return v
	}
	return def // scripted: accept defaults
}
func (p *fakePrompter) Choose(text string, opts []string) int { return 0 }
func (p *fakePrompter) YesNo(text string) bool                { return true }

type fakeProbes struct {
	peers []string
	pgset map[string]string
}

func (f *fakeProbes) TestPgpool(ctx context.Context, ep config.Endpoint, pf string) error {
	return nil
}
func (f *fakeProbes) TestPCP(ctx context.Context, p config.PCP, pf string) error { return nil }
func (f *fakeProbes) WatchdogPeers(ctx context.Context) ([]string, error) {
	return f.peers, nil
}
func (f *fakeProbes) SSHTest(ctx context.Context, h config.Host) error { return nil }
func (f *fakeProbes) PGSettings(ctx context.Context, h config.Host) (map[string]string, error) {
	return f.pgset, nil
}

var initConf = []byte(`port = 9999
pcp_port = 9898
backend_hostname0 = 'db1'
backend_port0 = 5432
backend_data_directory0 = '/var/lib/pgsql/data'
`)

func initSvc(t *testing.T, existing *config.Config) (*Service, *fstest.Fake) {
	f := fstest.New()
	f.MkdirAll("/etc/pgpool-II", 0o755)
	f.WriteFileAtomic("/etc/pgpool-II/pgpool.conf", initConf, false)
	cfg := config.Defaults()
	if existing != nil {
		cfg = *existing
	}
	svc := &Service{Cfg: &cfg, FS: f,
		InitProbes: &fakeProbes{peers: []string{"pgp1", "pgp2"},
			pgset: map[string]string{
				"data_directory":  "/var/lib/pgsql/16/data",
				"log_directory":   "log",
				"log_filename":    "postgresql-%a.log",
				"log_destination": "stderr",
				"log_line_prefix": "%m [%p] ",
			}}}
	return svc, f
}

func TestInitNonInteractive(t *testing.T) {
	svc, f := initSvc(t, nil)
	answers := map[string]string{"cluster": "prod-a"}
	cfg, err := svc.RunInit(context.Background(),
		InitOpts{NonInteractive: true}, &fakePrompter{}, answers)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster != "prod-a" || cfg.Pgpool.Port != 9999 {
		t.Fatalf("%+v", cfg.Pgpool)
	}
	// peers became pgpool hosts
	found := false
	for _, h := range cfg.Hosts {
		if h.Name == "pgp1" {
			found = true
		}
	}
	if !found {
		t.Error("watchdog peers not added")
	}
	diff, err := svc.WriteInitConfig(cfg, "/etc/poolwarden.yaml")
	if err != nil || diff == "" {
		t.Fatal("write failed")
	}
	b, _ := f.ReadFile("/etc/poolwarden.yaml")
	if !strings.Contains(string(b), "prod-a") {
		t.Error("yaml missing")
	}
}

func TestInitRerunKeepsOverrides(t *testing.T) {
	// first run with an override answer
	svc, _ := initSvc(t, nil)
	p1 := &fakePrompter{answers: map[string]string{
		"cluster name":       "custom-cluster",
		"lag warn (seconds)": "999",
	}}
	cfg1, err := svc.RunInit(context.Background(), InitOpts{}, p1, nil)
	if err != nil {
		t.Fatal(err)
	}
	// second run with the same prompter semantics (accept defaults) —
	// pre-filled from cfg1 → overrides must survive
	svc2, _ := initSvc(t, cfg1)
	cfg2, err := svc2.RunInit(context.Background(), InitOpts{}, &fakePrompter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Cluster != "custom-cluster" {
		t.Errorf("cluster override lost: %s", cfg2.Cluster)
	}
	if cfg2.Lag.WarnSeconds != 999 {
		t.Errorf("threshold override lost: %d", cfg2.Lag.WarnSeconds)
	}
}

func TestInitAudited(t *testing.T) {
	svc, f := initSvc(t, nil)
	// recorder + confirmer
	f.MkdirAll("/var/lib/poolwarden", 0o700)
	svc.Recorder = &audit.Recorder{
		W: &audit.Writer{FS: f, Path: "/var/lib/poolwarden/audit.jsonl",
			Flock: func(string) (func() error, func() error, error) {
				return func() error { return nil }, func() error { return nil }, nil
			}},
		Identity: audit.Identity{Declared: "alice"},
	}
	svc.Confirmer = &fakeConfirm{confirmed: true}
	cfg, err := svc.RunInit(context.Background(), InitOpts{}, &fakePrompter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.InitApply(context.Background(), cfg, "/etc/poolwarden.yaml",
		ControlOpts{TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != "success" {
		t.Fatalf("%+v", res)
	}
	b, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(b), `"command":"init"`) ||
		!strings.Contains(string(b), `"diff"`) {
		t.Fatalf("audit: %s", b)
	}
}

func TestDiscoverConfProc(t *testing.T) {
	svc, f := initSvc(t, nil)
	f.MkdirAll("/proc/123", 0o555)
	f.WriteFileAtomic("/proc/123/cmdline",
		[]byte("/usr/sbin/pgpool\x00-n\x00-f\x00/opt/pgpool/pgpool.conf\x00"), false)
	f.MkdirAll("/proc/456", 0o555)
	f.WriteFileAtomic("/proc/456/cmdline", []byte("postgres\x00-D\x00/x\x00"), false)
	got := svc.discoverConf()
	if len(got) == 0 || got[0] != "/opt/pgpool/pgpool.conf" {
		t.Fatalf("%v", got)
	}
	// well-known fallbacks still listed
	found := false
	for _, p := range got {
		if p == "/etc/pgpool-II/pgpool.conf" {
			found = true
		}
	}
	if !found {
		t.Error("fallback missing")
	}
}

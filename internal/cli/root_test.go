package cli

import (
	"time"

	"bytes"
	"github.com/spf13/cobra"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
)

func testDeps() (*Deps, *fstest.Fake, *bytes.Buffer, *bytes.Buffer) {
	f := fstest.New()
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	d := &Deps{
		FS:      f,
		Clock:   clock.Real{},
		Stdout:  out,
		Stderr:  errb,
		Stdin:   strings.NewReader(""),
		UID:     f.Uid,
		Version: BuildInfo{Version: "1.2.3", Commit: "abc", Date: "2026-11-05"},
	}
	return d, f, out, errb
}

const goodConfig = `cluster: prod-a
hosts:
  - name: db1
    address: 10.0.0.11
    roles: [pgpool, pg]
    pgpool: {conf: /etc/pgpool-II/pgpool.conf}
    pg: {log_dir: /var/log/pg}
`

func TestHelpListsEveryCommand(t *testing.T) {
	d, _, out, _ := testDeps()
	if code := Execute([]string{"--help"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	help := out.String()
	want := []string{
		"status", "nodes", "lag", "slots", "pools", "procs", "conns",
		"watchdog", "health", "topology", "watch", "top",
		"diagnose", "explain", "forecast", "errors", "logs",
		"promote", "detach", "attach", "recover", "reload",
		"config", "backend", "hba", "pcpuser", "pooluser", "sql",
		"audit", "init", "version",
	}
	for _, w := range want {
		if !strings.Contains(help, w) {
			t.Errorf("help missing %q", w)
		}
	}
}

func TestBadOutputFormat(t *testing.T) {
	d, f, _, errb := testDeps()
	f.Add("/etc/poolwarden.yaml", goodConfig, 0o600, f.Uid)
	if code := Execute([]string{"-o", "bogus", "status"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "next:") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestBadRoleConfig(t *testing.T) {
	d, f, _, errb := testDeps()
	f.Add("/etc/poolwarden.yaml", `cluster: x
hosts:
  - {name: h1, address: 1.1.1.1, roles: [bogus]}
`, 0o600, f.Uid)
	if code := Execute([]string{"status"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "roles") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestWorldWritableConfig(t *testing.T) {
	d, f, _, errb := testDeps()
	f.Add("/etc/poolwarden.yaml", goodConfig, 0o666, f.Uid)
	if code := Execute([]string{"status"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "chmod 0600") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestVersionNoConfig(t *testing.T) {
	d, _, out, _ := testDeps()
	if code := Execute([]string{"version"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "poolwarden 1.2.3 (abc, 2026-11-05) go") {
		t.Errorf("version output %q", out.String())
	}
}

func TestNagiosStubError(t *testing.T) {
	d, f, out, _ := testDeps()
	f.Add("/etc/poolwarden.yaml", goodConfig, 0o600, f.Uid)
	code := Execute([]string{"--nagios", "status"}, d)
	if code != 3 {
		t.Fatalf("code %d", code)
	}
	if !strings.HasPrefix(out.String(), "POOLWARDEN UNKNOWN - ") {
		t.Errorf("out %q", out.String())
	}
}

func TestStubExitCode(t *testing.T) {
	d, f, _, errb := testDeps()
	f.Add("/etc/poolwarden.yaml", goodConfig, 0o600, f.Uid)
	if code := Execute([]string{"top"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "needs a TTY") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestDeadlineFor(t *testing.T) {
	mk := func(dl string, parent *cobra.Command) *cobra.Command {
		c := &cobra.Command{Use: "x"}
		if dl != "" {
			c.Annotations = map[string]string{"deadline": dl}
		}
		if parent != nil {
			parent.AddCommand(c)
		}
		return c
	}
	to, none := deadlineFor(mk("10m", nil), 5*time.Second)
	if none || to != 10*time.Minute {
		t.Errorf("10m → %v %v", to, none)
	}
	to, none = deadlineFor(mk("", &cobra.Command{Use: "recover",
		Annotations: map[string]string{"deadline": "none"}}), 5*time.Second)
	if !none {
		t.Errorf("recover must have no deadline, got %v", to)
	}
	to, _ = deadlineFor(mk("", nil), 5*time.Second)
	if to != 20*time.Second {
		t.Errorf("default = 4×timeout, got %v", to)
	}
	// command annotations actually set on control commands
	for name, want := range map[string]string{
		"promote": "10m", "detach": "5m", "attach": "5m",
		"recover": "none", "reload": "2m"} {
		c := map[string]*cobra.Command{"promote": promoteCmd(), "detach": detachCmd(),
			"attach": attachCmd(), "recover": recoverCmd(), "reload": reloadCmd()}[name]
		if c.Annotations["deadline"] != want {
			t.Errorf("%s deadline %q want %q", name, c.Annotations["deadline"], want)
		}
	}
}

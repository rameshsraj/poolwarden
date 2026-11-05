package logsrc

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/remote/remotetest"
)

func readFixture(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if !strings.HasPrefix(sc.Text(), "# SYNTHETIC") {
			lines = append(lines, sc.Text())
		}
	}
	return lines
}

func TestPrefixA(t *testing.T) {
	p, unk := ParsePrefix("pgpool", "%m: %a pid %p: ")
	if len(unk) != 0 {
		t.Fatalf("unknown %v", unk)
	}
	lines := readFixture(t, "pgpool_prefix_a.log")
	l, ok := p.Parse(lines[1])
	if !ok || l.Severity != "WARNING" || l.Fields["pid"] != "1234" {
		t.Fatalf("%+v", l)
	}
	if l.TS.IsZero() {
		t.Error("no ts")
	}
	if !strings.Contains(l.Message, "health check") {
		t.Errorf("%q", l.Message)
	}
}

func TestPrefixB(t *testing.T) {
	p, unk := ParsePrefix("pg", "%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h ")
	if len(unk) != 0 {
		t.Fatalf("unknown %v", unk)
	}
	lines := readFixture(t, "pgpool_prefix_b.log")
	l, ok := p.Parse(lines[0])
	if !ok {
		t.Fatalf("no parse: %q", lines[0])
	}
	if l.Severity != "ERROR" || l.Fields["user"] != "postgres" || l.Fields["host"] != "10.0.0.5" {
		t.Errorf("%+v", l)
	}
	if l.Fields["pid"] != "2001" {
		t.Errorf("pid %v", l.Fields)
	}
	if Classify(l) != "CONN_FAIL" {
		t.Errorf("class %q", Classify(l))
	}
	l2, _ := p.Parse(lines[1])
	if Classify(l2) != "CHILD_EXIT" {
		t.Errorf("class2 %q", Classify(l2))
	}
	l3, _ := p.Parse(lines[2])
	if Classify(l3) != "AUTH_FAIL" {
		t.Errorf("class3 %q", Classify(l3))
	}
}

func TestPgPrefix(t *testing.T) {
	p, _ := ParsePrefix("pg", "%m [%p] ")
	l, ok := p.Parse(readFixture(t, "pg_prefix_a.log")[0])
	if !ok || l.Severity != "LOG" || l.Fields["pid"] != "501" {
		t.Fatalf("%+v", l)
	}
	if l.TS.IsZero() {
		t.Error("no ts")
	}
}

func TestContinuation(t *testing.T) {
	if !IsContinuation("\tDETAIL:  x") {
		t.Error("tab detail")
	}
	if !IsContinuation("DETAIL:  x") {
		t.Error("detail")
	}
	if IsContinuation("2026-11-05 log") {
		t.Error("normal")
	}
	p, _ := ParsePrefix("pg", "%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h ")
	l, ok := p.Parse(readFixture(t, "pg_prefix_b.log")[0])
	if ok && l.Severity == "ERROR" {
		// fine
	}
}

func TestUnknownEscape(t *testing.T) {
	p, unk := ParsePrefix("pg", "%Q %t [%p]: ")
	if len(unk) != 1 || unk[0] != "%Q" {
		t.Fatalf("unknown %v", unk)
	}
	if _, ok := p.Parse("whatever 2026-11-05 12:01:00 CET [501]: LOG:  hi"); !ok {
		t.Error("parse failed")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"failover_command started", "FAILOVER"},
		{"promote node 1", "FAILOVER"},
		{"health check failed on node 0", "HEALTHCHECK"},
		{"health_check period elapsed", "HEALTHCHECK"},
		{"child process 123 exits with status 0", "CHILD_EXIT"},
		{"child 55 was terminated by signal 9", "CHILD_EXIT"},
		{"password authentication failed for user x", "AUTH_FAIL"},
		{"no pg_hba.conf entry for host", "AUTH_FAIL"},
		{"watchdog election started", "WD_ELECTION"},
		{"wd_escalation_command executed", "WD_ELECTION"},
		{"connection to server failed", "CONN_FAIL"},
		{"remaining connection slots are reserved", "CONN_FAIL"},
		{"regular notice", "OTHER"},
	}
	for _, c := range cases {
		if got := Classify(Line{Message: c.msg}); got != c.want {
			t.Errorf("%q → %s want %s", c.msg, got, c.want)
		}
	}
}

func TestFileSourceRead(t *testing.T) {
	sess := &remotetest.FakeSession{Execs: map[string]remote.ExecResult{
		"ls -1t -- /var/log/pgpool": {Stdout: []byte("pgpool.log\n"), Exit: 0},
	}}
	p, _ := ParsePrefix("pgpool", "%m: %a pid %p: ")
	fixture, _ := os.ReadFile("testdata/pgpool_prefix_a.log")
	sess.Execs["tail -n 200000 -- /var/log/pgpool/pgpool.log"] = remote.ExecResult{Stdout: fixture, Exit: 0}
	fs := &FileSource{Host: "db1", Component: "pgpool", Dir: "/var/log/pgpool",
		Filename: "pgpool.log", Prefix: p, Session: sess}
	lines, err := fs.Read(context.Background(), ReadOpts{Since: time.Hour,
		Now: time.Date(2026, 11, 5, 12, 3, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines", len(lines))
	}
	if lines[0].Host != "db1" || lines[0].File != "/var/log/pgpool/pgpool.log" {
		t.Errorf("%+v", lines[0])
	}
}

func TestFileSourceFollowCancel(t *testing.T) {
	dir := t.TempDir()
	logf := dir + "/pgpool.log"
	os.WriteFile(logf, []byte(""), 0o600)
	sess := &remote.LocalSession{}
	p, _ := ParsePrefix("pgpool", "%m: %a pid %p: ")
	fs := &FileSource{Host: "local", Component: "pgpool", Dir: dir,
		Filename: "pgpool.log", Prefix: p, Session: sess}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Line, 10)
	done := make(chan error, 1)
	go func() { done <- fs.Follow(ctx, ReadOpts{}, out) }()
	time.Sleep(500 * time.Millisecond)
	// append a line
	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("2026-11-05 12:05:00.001 CET: app pid 1: WARNING:  tail me\n")
	f.Close()
	select {
	case l := <-out:
		if !strings.Contains(l.Message, "tail me") {
			t.Errorf("%+v", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no line streamed")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected ctx err")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not stop within 2s")
	}
}

func TestPgpoolPProcessName(t *testing.T) {
	p, unk := ParsePrefix("pgpool", "%m: %P pid %p: ")
	if len(unk) != 0 {
		t.Fatalf("%v", unk)
	}
	l, ok := p.Parse("2026-11-05 12:01:00.001 CET: watchdog pid 42: LOG:  heartbeat ok")
	if !ok || l.Fields["process"] != "watchdog" {
		t.Fatalf("%+v", l)
	}
	if l.Fields["pid"] != "42" {
		t.Errorf("%+v", l.Fields)
	}
}

func TestPgpoolUnknownEscapes(t *testing.T) {
	// %h %r are PG-only; in a pgpool prefix they are unknown → wildcard+warn
	p, unk := ParsePrefix("pgpool", "%t [%p] client=%h ")
	if len(unk) != 1 || unk[0] != "%h" {
		t.Fatalf("unknown %v", unk)
	}
	if _, ok := p.Parse("2026-11-05 12:01:00 CET [1] client=whatever x"); !ok {
		t.Error("parse failed")
	}
}

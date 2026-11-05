package audit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
)

func TestListFilters(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "detach 1", Target: "db2", Ticket: "INC-1"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	p2, _ := r.Begin(context.Background(), Action{Command: "nodes"})
	p2.Finish(context.Background(), "failed", 1, errTest("x"), nil, "")
	// pending entry with no finish
	r.Begin(context.Background(), Action{Command: "slow"})
	now := c.Now()
	all := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour}, now)
	if len(all) != 5 {
		t.Fatalf("%d", len(all))
	}
	fin := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour, Outcome: "success"}, now)
	if len(fin) != 1 {
		t.Errorf("outcome filter %d", len(fin))
	}
	fail := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour, Outcome: "failed"}, now)
	if len(fail) != 1 {
		t.Errorf("failed %d", len(fail))
	}
	act := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour, Action: "detach"}, now)
	if len(act) != 2 {
		t.Errorf("action %d", len(act))
	}
	tk := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour, Ticket: "INC-1"}, now)
	if len(tk) != 2 {
		t.Errorf("ticket %d", len(tk))
	}
	op := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour, Operator: "alice"}, now)
	if len(op) == 0 {
		t.Error("operator filter")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestAllowed(t *testing.T) {
	cfg := config.Defaults()
	cfg.AllowedOperators = []string{"alice", "bob"}
	if err := Allowed(&cfg, Identity{Declared: "alice"}); err != nil {
		t.Fatalf("%v", err)
	}
	err := Allowed(&cfg, Identity{Declared: "mallory"})
	if err == nil || !strings.Contains(err.Error(), "allowed_operators") {
		t.Fatalf("%v", err)
	}
	cfg.AllowedOperators = nil
	if err := Allowed(&cfg, Identity{Declared: "anyone"}); err != nil {
		t.Error("empty list should allow")
	}
}

func TestMaskEdge(t *testing.T) {
	c := &Change{Diff: "password=hunter2\nport=5432",
		Before: map[string]string{"pool_passwd": "x", "port": "5432"},
		After:  map[string]string{"pool_passwd": "y", "port": "5432"}}
	m := maskChange(c)
	if m.Before["pool_passwd"] != "***" || m.After["pool_passwd"] != "***" {
		t.Errorf("%+v", m)
	}
	if m.Before["port"] != "5432" {
		t.Error("non-secret masked")
	}
	if strings.Contains(m.Diff, "hunter2") {
		t.Errorf("diff leak %q", m.Diff)
	}
}

func TestVerifySwapped(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	for i := 0; i < 3; i++ {
		p, _ := r.Begin(context.Background(), Action{Command: "cmd"})
		p.Finish(context.Background(), "success", 0, nil, nil, "")
		c.Advance(time.Second)
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[0], lines[2] = lines[2], lines[0]
	f.WriteFileAtomic("/var/lib/poolwarden/audit.jsonl", []byte(strings.Join(lines, "\n")+"\n"), false)
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	kinds := map[string]bool{}
	for _, p := range rep.Problems {
		kinds[p.Kind] = true
	}
	if !kinds["chain_break"] && !kinds["reordered"] {
		t.Fatalf("%+v", rep.Problems)
	}
}

func TestRealEnvSmoke(t *testing.T) {
	e := RealEnv()
	_ = e.Getenv("PATH")
	_ = e.Hostname()
	_ = e.Pid()
	_ = e.TTY()
	if _, err := e.LookupUser(0); err != nil {
		t.Logf("root lookup: %v", err)
	}
}

func TestSyslogSmoke(t *testing.T) {
	// no syslog on the test box → error tolerated
	_ = Syslog{}.Write("info", "poolwarden test")
}

func TestResolveSSHConnectionShort(t *testing.T) {
	env := &fakeEnv{env: map[string]string{"SSH_CONNECTION": "10.0.0.9"}, users: map[int]string{}}
	id := Resolve(env, fstest.New(), &config.Config{}, "")
	if id.SSHClient != "" {
		t.Errorf("%+v", id)
	}
}

func TestVerifySinceCutoff(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "old"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	c.Advance(48 * time.Hour)
	p2, _ := r.Begin(context.Background(), Action{Command: "new"})
	p2.Finish(context.Background(), "success", 0, nil, nil, "")
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	if rep.Entries != 2 {
		t.Fatalf("%d", rep.Entries)
	}
	// chain break is expected: prev entry excluded by the window
}

func TestGetMissing(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "x"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	req, fin := Get(f, "/var/lib/poolwarden/audit.jsonl", "nosuchid", time.Hour, c.Now())
	if req != nil || fin != nil {
		t.Error("want nil pair")
	}
}

func TestSessionsIdentityFields(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	r.Identity.SSHClient = "10.0.0.9:51234"
	r.Identity.SSHKeyFingerprint = "SHA256:abc"
	r.SessionStart(context.Background())
	r.SessionEnd(context.Background())
	s := Sessions(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	if len(s) != 1 || s[0].From != "10.0.0.9:51234" || s[0].KeyFP != "SHA256:abc" {
		t.Fatalf("%+v", s)
	}
}

func TestOperatorOrder(t *testing.T) {
	i := Identity{SudoUser: "sudo", LoginUser: "login", OSUser: "os"}
	if i.Operator() != "sudo" {
		t.Error("sudo should win")
	}
	i = Identity{LoginUser: "login", OSUser: "os"}
	if i.Operator() != "login" {
		t.Error("login")
	}
	i = Identity{OSUser: "os"}
	if i.Operator() != "os" {
		t.Error("os")
	}
	i = Identity{}
	if i.Operator() != "" {
		t.Error("empty")
	}
}

func TestAborted(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	if err := r.Aborted(context.Background(), Action{Command: "set x"}, "confirm"); err != nil {
		t.Fatal(err)
	}
	list := List(f, "/var/lib/poolwarden/audit.jsonl", Filter{Since: time.Hour}, c.Now())
	if len(list) != 2 || list[1].Outcome != "aborted" {
		t.Fatalf("%+v", list)
	}
	if !strings.Contains(list[1].Error, "confirm") {
		t.Errorf("%+v", list[1])
	}
}

func TestEntryTSFallbacks(t *testing.T) {
	e := Entry{}
	if !entryTS(&e).IsZero() {
		t.Error("zero")
	}
}

func TestVerifyEmptyFile(t *testing.T) {
	f := fstest.New()
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, t0)
	if rep.Entries != 0 {
		t.Errorf("%d", rep.Entries)
	}
}

package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/remote"
)

type fakeEnv struct {
	env   map[string]string
	users map[int]string
	host  string
	pid   int
	tty   string
}

func (f *fakeEnv) Getenv(k string) string { return f.env[k] }
func (f *fakeEnv) LookupUser(uid int) (string, error) {
	if uid == -2 {
		return "osuser", nil
	}
	if s, ok := f.users[uid]; ok {
		return s, nil
	}
	return "", &fakeErr{"no user"}
}
func (f *fakeEnv) Hostname() string { return f.host }
func (f *fakeEnv) Pid() int         { return f.pid }
func (f *fakeEnv) TTY() string      { return f.tty }

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

type fakeSyslog struct{ msgs []string }

func (f *fakeSyslog) Write(p, m string) error { f.msgs = append(f.msgs, m); return nil }

var sshAuth = `publickey ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB0YY9G1q1z5wJ0a4B9u3n2e3g9B5kX7k3oJ1yJ8p8s9 alice@laptop
publickey ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL2m7a2xN3z2e0h7m3j2k7p3j7y3n0b7b5v8u6w7k9i0 bob@laptop
`

func TestResolveAllSignals(t *testing.T) {
	f := fstest.New()
	f.Add("/home/alice/.ssh/auth", sshAuth, 0o600, f.Uid)
	f.Add("/proc/self/loginuid", "1001", 0o444, 0)
	env := &fakeEnv{
		env: map[string]string{
			"SUDO_USER": "root", "SSH_USER_AUTH": "/home/alice/.ssh/auth",
			"SSH_CONNECTION": "10.0.0.9 51234 10.0.0.11 22",
		},
		users: map[int]string{1001: "alice"},
		host:  "db1", pid: 4242, tty: "pts/0",
	}
	cfg := config.Defaults()
	cfg.Operators = map[string]string{"alice": "SHA256:+w8zVh3m2j0mF4B0P0w0W7z8q5o8y9x0N1p2V3m4x5Y"}
	id := Resolve(env, f, &cfg, "")
	if id.SSHKeyFingerprint == "" || id.SSHKeyComment != "bob@laptop" {
		t.Fatalf("%+v", id)
	}
	if id.LoginUID != 1001 || id.LoginUser != "alice" || id.SudoUser != "root" ||
		id.SSHClient != "10.0.0.9:51234" || id.TTY != "pts/0" || id.Hostname != "db1" || id.PID != 4242 {
		t.Errorf("%+v", id)
	}
}

func TestResolveDeclaredAndMismatch(t *testing.T) {
	f := fstest.New()
	f.Add("/proc/self/loginuid", "4294967295", 0o444, 0)
	env := &fakeEnv{env: map[string]string{}, users: map[int]string{}}
	cfg := config.Defaults()
	id := Resolve(env, f, &cfg, "bob")
	if id.LoginUID != -1 || id.Operator() != "bob" {
		t.Fatalf("%+v", id)
	}
	if id.Mismatch {
		t.Error("no mismatch expected without key owner")
	}
}

func TestResolveMismatch(t *testing.T) {
	f := fstest.New()
	f.Add("/a", sshAuth, 0o600, f.Uid)
	env := &fakeEnv{env: map[string]string{"SSH_USER_AUTH": "/a"}, users: map[int]string{}}
	cfg := config.Defaults()
	fp := ""
	{
		id0 := Resolve(env, f, &cfg, "")
		fp = id0.SSHKeyFingerprint
	}
	cfg.Operators = map[string]string{"alice": fp}
	id := Resolve(env, f, &cfg, "bob")
	if id.KeyOwner != "alice" || !id.Mismatch {
		t.Fatalf("%+v", id)
	}
	if id.Operator() != "alice" {
		t.Errorf("key owner wins: %s", id.Operator())
	}
}

func TestResolveMalformedAuth(t *testing.T) {
	f := fstest.New()
	f.Add("/a", "publickey garbage\nnotakey\n", 0o600, f.Uid)
	env := &fakeEnv{env: map[string]string{"SSH_USER_AUTH": "/a"}, users: map[int]string{}}
	id := Resolve(env, f, &config.Config{}, "")
	if id.SSHKeyFingerprint != "" {
		t.Errorf("%+v", id)
	}
}

func TestMask(t *testing.T) {
	in := `poolwarden sql --password 's3cret' PGPASSWORD=hunter2 password=pw -W wpass scram-sha-256$AAA/BBB md5d41d8cd98f00b204e9800998ecf8427e`
	out := Mask(in)
	for _, bad := range []string{"s3cret", "hunter2", "pw ", "wpass", "scram-sha-256$AAA", "d41d8cd98f00b204e9800998ecf8427e"} {
		if strings.Contains(out, bad) && !strings.Contains(out, "***") {
			t.Errorf("%q survives in %q", bad, out)
		}
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "AAA/BBB") {
		t.Errorf("secret survives: %q", out)
	}
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func mkWriter(t0 time.Time) (*Writer, *fstest.Fake, *clock.Fake, *fakeSyslog) {
	f := fstest.New()
	c := clock.NewFake(t0)
	sl := &fakeSyslog{}
	return &Writer{FS: f, Clock: c, Path: "/var/lib/poolwarden/audit.jsonl",
		Syslog: sl, Version: "1.0.0", Host: "db1", RetentionDays: 30,
		Flock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}, f, c, sl
}

func mkRecorder(w *Writer, c *clock.Fake) *Recorder {
	return &Recorder{W: w, SessionID: "sess1", Cluster: "prod-a", Clock: c,
		Identity: Identity{Declared: "alice"}}
}

func TestBeginFinish(t *testing.T) {
	w, f, c, sl := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "detach 1", Target: "db2", Ticket: "INC-1"})
	c.Advance(time.Second)
	p.Confirmed(c.Now())
	p.Started(c.Now())
	p.AddStep(remote.Step{Host: "db2", Action: "connect", ConnectMS: 41})
	c.Advance(5 * time.Second)
	if err := p.Finish(context.Background(), "success", 0, nil, nil, "v3"); err != nil {
		t.Fatal(err)
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	var req, fin Entry
	json.Unmarshal([]byte(lines[0]), &req)
	json.Unmarshal([]byte(lines[1]), &fin)
	if req.EventID != fin.EventID || req.Phase != "requested" || fin.Phase != "finished" {
		t.Fatal("phases")
	}
	if fin.DurationMS != 5000 {
		t.Errorf("duration %d", fin.DurationMS)
	}
	if len(fin.Steps) != 1 || fin.Steps[0].ConnectMS != 41 {
		t.Errorf("steps %+v", fin.Steps)
	}
	if fin.BackupVersion != "v3" || fin.Outcome != "success" {
		t.Errorf("%+v", fin)
	}
	if len(sl.msgs) != 2 {
		t.Errorf("syslog %d", len(sl.msgs))
	}
	if fin.Hash == "" || fin.PrevHash == "" || fin.PrevHash == fin.Hash {
		t.Error("chain")
	}
}

func TestMaskedEntry(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: `sql --password topsecret`})
	p.Finish(context.Background(), "success", 0, nil, &Change{
		Before: map[string]string{"password": "old", "port": "5432"},
		After:  map[string]string{"password": "new", "port": "5432"},
	}, "")
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	s := string(data)
	if strings.Contains(s, "topsecret") || strings.Contains(s, `"old"`) && strings.Contains(s, "password") {
		t.Errorf("secret in audit: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Error("no mask")
	}
}

func TestRotationAndChain(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "a"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	c.Advance(26 * time.Hour) // next UTC day
	p2, _ := r.Begin(context.Background(), Action{Command: "b"})
	p2.Finish(context.Background(), "success", 0, nil, nil, "")
	if _, err := f.Stat("/var/lib/poolwarden/audit.jsonl.20261105"); err != nil {
		t.Fatal("no rotation")
	}
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", 72*time.Hour, c.Now())
	if len(rep.Problems) != 0 {
		t.Fatalf("%+v", rep.Problems)
	}
	if rep.Entries != 4 {
		t.Errorf("entries %d", rep.Entries)
	}
}

func TestVerifyTamper(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	for i := 0; i < 3; i++ {
		p, _ := r.Begin(context.Background(), Action{Command: "cmd"})
		p.Finish(context.Background(), "success", 0, nil, nil, "")
	}
	// edit one field
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var e Entry
	json.Unmarshal([]byte(lines[2]), &e)
	e.Outcome = "hacked"
	b, _ := json.Marshal(e)
	lines[2] = string(b)
	f.WriteFileAtomic("/var/lib/poolwarden/audit.jsonl", []byte(strings.Join(lines, "\n")+"\n"), false)
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	kinds := map[string]bool{}
	for _, p := range rep.Problems {
		kinds[p.Kind] = true
	}
	if !kinds["hash_mismatch"] {
		t.Fatalf("%+v", rep.Problems)
	}
}

func TestVerifyDeletedLine(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	for i := 0; i < 3; i++ {
		p, _ := r.Begin(context.Background(), Action{Command: "cmd"})
		p.Finish(context.Background(), "success", 0, nil, nil, "")
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines = append(lines[:2], lines[3:]...)
	f.WriteFileAtomic("/var/lib/poolwarden/audit.jsonl", []byte(strings.Join(lines, "\n")+"\n"), false)
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	kinds := map[string]bool{}
	for _, p := range rep.Problems {
		kinds[p.Kind] = true
	}
	if !kinds["chain_break"] || !kinds["seq_gap"] {
		t.Fatalf("%+v", rep.Problems)
	}
}

func TestVerifyTruncated(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "cmd"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	f.WriteFileAtomic("/var/lib/poolwarden/audit.jsonl", append(data, []byte(`{"event_id":"x","seq`)...), false)
	rep := Verify(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	found := false
	for _, p := range rep.Problems {
		if p.Kind == "parse_error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", rep.Problems)
	}
}

func TestRefusedAndSessions(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	r.SessionStart(context.Background())
	r.Refused(context.Background(), Action{Command: "detach 9"}, "not allowed")
	p, _ := r.Begin(context.Background(), Action{Command: "nodes"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	r.SessionEnd(context.Background())
	sessions := Sessions(f, "/var/lib/poolwarden/audit.jsonl", time.Hour, c.Now())
	if len(sessions) != 1 || sessions[0].Commands != 2 {
		t.Fatalf("%+v", sessions)
	}
	if sessions[0].Operator != "alice" {
		t.Errorf("%+v", sessions[0])
	}
	req, fin := Get(f, "/var/lib/poolwarden/audit.jsonl", eventIDOf(f), time.Hour, c.Now())
	if req == nil || fin == nil {
		t.Fatal("get pair")
	}
}

func eventIDOf(f *fstest.Fake) string {
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	for _, l := range strings.Split(string(data), "\n") {
		if l == "" {
			continue
		}
		var e Entry
		json.Unmarshal([]byte(l), &e)
		if e.Phase == "requested" {
			return e.EventID
		}
	}
	return ""
}

func TestRetention(t *testing.T) {
	f := fstest.New()
	c := clock.NewFake(t0)
	old := t0.AddDate(0, 0, -40)
	f.Add("/var/lib/poolwarden/audit.jsonl."+old.Format("20060102"), `{"event_id":"old","seq":1,"hash":"x"}`+"\n", 0o600, f.Uid)
	w := &Writer{FS: f, Clock: c, Path: "/var/lib/poolwarden/audit.jsonl", RetentionDays: 30,
		Flock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "x"})
	p.Finish(context.Background(), "success", 0, nil, nil, "")
	if _, err := f.Stat("/var/lib/poolwarden/audit.jsonl." + old.Format("20060102")); err == nil {
		t.Error("old file kept")
	}
}

func TestPreflightAndChangeInEntry(t *testing.T) {
	w, f, c, _ := mkWriter(t0)
	r := mkRecorder(w, c)
	p, _ := r.Begin(context.Background(), Action{Command: "set x 1", Preflight: &model.PreflightResult{
		Action: "set", Verdict: "go", Checks: []model.Check{{Name: "n", Result: "ok"}}}})
	p.Finish(context.Background(), "success", 0, nil, &Change{Diff: "-x: 0\n+x: 1"}, "v1")
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(data), `"preflight"`) {
		t.Error("preflight missing")
	}
}

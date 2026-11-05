package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/remote"
)

// auditDeps builds deps whose fstest already holds an audit trail.
func auditDeps(t *testing.T, mutate func(f *fstest.Fake)) (*Deps, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	d, out, errb := viewDeps(t, nil)
	f := d.FS.(*fstest.Fake)
	c := clock.NewFake(time.Date(2026, 11, 5, 12, 0, 0, 0, time.Local))
	w := &audit.Writer{FS: f, Clock: c, Path: "/var/lib/poolwarden/audit.jsonl",
		Version: "1.0.0", Host: "db1",
		Flock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}
	rec := &audit.Recorder{W: w, SessionID: "sess-1", Cluster: "prod-a", Clock: c,
		Identity: audit.Identity{Declared: "alice"}}
	rec.SessionStart(context.Background())
	p, _ := rec.Begin(context.Background(), audit.Action{Command: "detach 1", Target: "db2", Ticket: "INC-7"})
	c.Advance(7700 * time.Millisecond)
	p.Confirmed(c.Now())
	p.Started(c.Now())
	p.AddStep(remote.Step{Host: "db2", SSHUser: "root", Action: "connect", ConnectMS: 41,
		Start: c.Now(), End: c.Now().Add(53 * time.Millisecond)})
	c.Advance(15 * time.Second)
	p.Finish(context.Background(), "success", 0, nil, nil, "v2")
	rec.SessionEnd(context.Background())
	if mutate != nil {
		mutate(f)
	}
	return d, out, errb
}

func TestAuditListGolden(t *testing.T) {
	d, out, errb := auditDeps(t, nil)
	if code := Execute([]string{"audit"}, d); code != 0 {
		t.Fatalf("code %d stderr %s", code, errb)
	}
	goldenView(t, "audit.table.golden", out.String())
}

func TestAuditShowGolden(t *testing.T) {
	d, out, _ := auditDeps(t, nil)
	f := d.FS.(*fstest.Fake)
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	var e audit.Entry
	json.Unmarshal([]byte(firstLine(data)), &e)
	if code := Execute([]string{"audit", "show", e.EventID}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "audit_show.table.golden", out.String())
}

func TestAuditVerifyOK(t *testing.T) {
	d, out, _ := auditDeps(t, nil)
	if code := Execute([]string{"audit", "verify"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "audit_verify_ok.golden", out.String())
}

func TestAuditVerifyProblems(t *testing.T) {
	d, out, _ := auditDeps(t, func(f *fstest.Fake) {
		data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
		lines := splitLines(data)
		var e audit.Entry
		json.Unmarshal([]byte(lines[1]), &e)
		e.Outcome = "hacked"
		b, _ := json.Marshal(e)
		lines[1] = string(b)
		f.WriteFileAtomic("/var/lib/poolwarden/audit.jsonl",
			[]byte(joinLines(lines)), false)
	})
	if code := Execute([]string{"audit", "verify"}, d); code != 11 {
		t.Fatalf("code %d", code)
	}
	norm := regexp.MustCompile(`entry [0-9a-f]+`).ReplaceAllString(out.String(), "entry <id>")
	goldenView(t, "audit_verify_bad.golden", norm)
}

func TestAuditSessionsGolden(t *testing.T) {
	d, out, _ := auditDeps(t, nil)
	if code := Execute([]string{"audit", "sessions"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "audit_sessions.table.golden", out.String())
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}

func splitLines(b []byte) []string {
	var out []string
	cur := ""
	for _, c := range string(b) {
		if c == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	return out
}

func joinLines(lines []string) string {
	s := ""
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}

var _ = time.Now
var _ = model.Finding{}

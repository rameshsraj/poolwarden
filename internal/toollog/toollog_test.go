package toollog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/fs/fstest"
)

func TestOpenFallback(t *testing.T) {
	f := fstest.New()
	l, err := Open(f, "/nonexistent/../x") // fake fs may still allow; force failure differently
	if l == nil {
		t.Fatal("nil logger")
	}
	_ = err
	l.Warn("hi")
}

func TestOnce(t *testing.T) {
	f := fstest.New()
	l, _ := Open(f, "/var/lib/poolwarden")
	l.Once("k", "first")
	l.Once("k", "second")
	data, _ := f.ReadFile("/var/lib/poolwarden/tool.log")
	if strings.Count(string(data), "first") != 1 || strings.Contains(string(data), "second") {
		t.Errorf("%s", data)
	}
}

func TestRotation(t *testing.T) {
	f := fstest.New()
	l, _ := Open(f, "/var/lib/poolwarden")
	big := strings.Repeat("x", 4096)
	for i := 0; i < 1400; i++ { // ~5.7MB
		l.Warn(big)
	}
	fi, _ := f.Stat("/var/lib/poolwarden/tool.log")
	if fi.Size() > 6<<20 {
		t.Errorf("main file too big: %d", fi.Size())
	}
	if _, err := f.Stat("/var/lib/poolwarden/tool.log.1"); err != nil {
		t.Error("no rotation")
	}
	if _, err := f.Stat("/var/lib/poolwarden/tool.log.4"); err == nil {
		t.Error("kept >3 backups")
	}
}

func TestRecent(t *testing.T) {
	f := fstest.New()
	l, _ := Open(f, "/var/lib/poolwarden")
	l.Debug("skip me")
	l.Warn("a warning")
	l.Error("an error")
	ev, err := l.Recent(context.Background(), time.Hour)
	if err != nil || len(ev) != 2 {
		t.Fatalf("%v %d", err, len(ev))
	}
	if ev[0].Source != "tool" || ev[0].Message != "a warning" {
		t.Errorf("%+v", ev[0])
	}
}

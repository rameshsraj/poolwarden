package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smarsh/poolwarden/internal/config"
)

func TestLocalSession(t *testing.T) {
	s := &LocalSession{}
	res, err := s.Exec(context.Background(), []string{"echo", "hello"}, nil)
	if err != nil || res.Exit != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if string(res.Stdout) != "hello\n" {
		t.Errorf("stdout %q", res.Stdout)
	}
	res, err = s.Exec(context.Background(), []string{"false"}, nil)
	if err != nil || res.Exit != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestLocalSessionFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.conf")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	s := &LocalSession{}
	if err := s.WriteFileAtomic(context.Background(), p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	fi, err := s.Stat(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode.Perm() != 0o640 {
		t.Errorf("mode %o", fi.Mode.Perm())
	}
	b, err := s.ReadFile(context.Background(), p)
	if err != nil || string(b) != "new" {
		t.Fatalf("%v %q", err, b)
	}
}

func TestIsLocal(t *testing.T) {
	hn, _ := os.Hostname()
	if !IsLocal(config.Host{Name: hn, Address: hn}) {
		t.Errorf("hostname %q should be local", hn)
	}
	if !IsLocal(config.Host{Name: "x", Address: "127.0.0.1"}) {
		t.Error("127.0.0.1 should be local")
	}
	if IsLocal(config.Host{Name: "faraway", Address: "203.0.113.99"}) {
		t.Error("203.0.113.99 should not be local")
	}
}

package fstest

import (
	"os"
	"testing"

	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

func TestFakeBasics(t *testing.T) {
	f := New()
	f.Add("/etc/poolwarden.yaml", "x", 0o600, 0)
	b, err := f.ReadFile("/etc/poolwarden.yaml")
	if err != nil || string(b) != "x" {
		t.Fatalf("read %v %q", err, b)
	}
	fi, err := f.Stat("/etc/poolwarden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
	o, ok := pwfs.OwnerOf(fi)
	if !ok || o.UID != 0 {
		t.Errorf("owner %v %v", o, ok)
	}
	if _, err := f.Stat("/etc/missing"); !os.IsNotExist(err) {
		t.Errorf("want not-exist, got %v", err)
	}
	// atomic write keeps mode/owner
	if err := f.WriteFileAtomic("/etc/poolwarden.yaml", []byte("y"), true); err != nil {
		t.Fatal(err)
	}
	fi, _ = f.Stat("/etc/poolwarden.yaml")
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after write %o", fi.Mode().Perm())
	}
	b, _ = f.ReadFile("/etc/poolwarden.yaml")
	if string(b) != "y" {
		t.Errorf("content %q", b)
	}
	// new file via atomic write needs an existing parent dir
	f.MkdirAll("/var/lib/poolwarden", 0o700)
	if err := f.WriteFileAtomic("/var/lib/poolwarden/x", []byte("z"), true); err != nil {
		t.Fatal(err)
	}
	fi, _ = f.Stat("/var/lib/poolwarden/x")
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %o", fi.Mode().Perm())
	}
	// append
	w, err := f.OpenAppend("/var/lib/poolwarden/x", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("+"))
	w.Close()
	b, _ = f.ReadFile("/var/lib/poolwarden/x")
	if string(b) != "z+" {
		t.Errorf("append got %q", b)
	}
	// readdir
	ents, err := f.ReadDir("/etc")
	if err != nil || len(ents) != 1 || ents[0].Name() != "poolwarden.yaml" {
		t.Fatalf("readdir %v %v", err, ents)
	}
	// rename + remove
	if err := f.Rename("/var/lib/poolwarden/x", "/var/lib/poolwarden/y"); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("/var/lib/poolwarden/y"); err != nil {
		t.Fatal(err)
	}
}

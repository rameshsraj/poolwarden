package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicKeepsModeAndOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgpool.conf")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := (OS{}).WriteFileAtomic(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o, want 640", fi.Mode().Perm())
	}
	owner, ok := OwnerOf(fi)
	if !ok {
		t.Skip("no owner info")
	}
	if owner.UID != os.Getuid() {
		t.Errorf("uid = %d, want %d", owner.UID, os.Getuid())
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new" {
		t.Errorf("content = %q", b)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != "pgpool.conf" {
			t.Errorf("leftover file %q", e.Name())
		}
	}
}

func TestWriteFileAtomicNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.conf")
	if err := (OS{}).WriteFileAtomic(path, []byte("x"), true); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestWriteFileAtomicBadDir(t *testing.T) {
	err := (OS{}).WriteFileAtomic("/nonexistent-dir-xyz/f", []byte("x"), true)
	if err == nil {
		t.Fatal("want error")
	}
}

func TestOSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := (OS{}).WriteFileAtomic(p, []byte("hi"), false); err != nil {
		t.Fatal(err)
	}
	b, err := (OS{}).ReadFile(p)
	if err != nil || string(b) != "hi" {
		t.Fatalf("read: %v %q", err, b)
	}
	if _, err := (OS{}).Stat(p); err != nil {
		t.Fatal(err)
	}
	if err := (OS{}).Rename(p, p+".bak"); err != nil {
		t.Fatal(err)
	}
	if _, err := (OS{}).Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("want not exist")
	}
	if err := (OS{}).Remove(p + ".bak"); err != nil {
		t.Fatal(err)
	}
}

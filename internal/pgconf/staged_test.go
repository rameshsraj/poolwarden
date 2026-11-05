package pgconf

import (
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/fs/fstest"
)

func TestStagedRoundTrip(t *testing.T) {
	f := fstest.New()
	s, err := LoadStaged(f, "/var/lib/poolwarden")
	if err != nil || len(s.Changes) != 0 {
		t.Fatalf("%v %v", s, err)
	}
	s.Changes = append(s.Changes, Change{File: "pgpool.conf", Op: "set",
		Param: "port", Old: "9999", New: "9998", Operator: "alice",
		At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	s.SHAs = map[string]string{"db1": "abc"}
	if err := s.Save(f, "/var/lib/poolwarden"); err != nil {
		t.Fatal(err)
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/staged.json")
	fi, _ := f.Stat("/var/lib/poolwarden/staged.json")
	_ = data
	if fi.Mode()&0o777 != 0o600 {
		t.Errorf("mode %o", fi.Mode())
	}
	s2, err := LoadStaged(f, "/var/lib/poolwarden")
	if err != nil || len(s2.Changes) != 1 || s2.Changes[0].Param != "port" {
		t.Fatalf("%+v %v", s2, err)
	}
}

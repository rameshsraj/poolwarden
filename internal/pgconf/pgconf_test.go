package pgconf

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *File {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRoundTrip(t *testing.T) {
	for _, name := range []string{"pgpool.conf", "pool_hba.conf", "pcp.conf"} {
		b, _ := os.ReadFile("testdata/" + name)
		f, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(f.Bytes()) != string(b) {
			t.Fatalf("%s not byte-identical", name)
		}
	}
}

func TestGetAndQuotedHash(t *testing.T) {
	f := load(t, "pgpool.conf")
	v, found, com := f.Get("port")
	if !found || com || v != "9999" {
		t.Errorf("%q %v %v", v, found, com)
	}
	if v, _, _ := f.Get("logdir_with_hash"); v != "#not-comment" {
		t.Errorf("hash value %q", v)
	}
	if v, found, com := f.Get("unix_socket_directories"); !found || !com {
		t.Errorf("commented %q %v", v, found)
	}
}

func TestSetInPlace(t *testing.T) {
	f := load(t, "pgpool.conf")
	f.Set("port", "9998")
	v, _, _ := f.Get("port")
	if v != "9998" {
		t.Fatal(v)
	}
	for _, l := range f.Lines {
		if l.Name == "port" && !strings.Contains(l.Raw, "port = 9998") {
			t.Fatalf("raw %q", l.Raw)
		}
	}
	// string param keeps quoting
	f.Set("listen_addresses", "*")
	v, _, _ = f.Get("listen_addresses")
	if v != "*" {
		t.Fatal(v)
	}
	found := false
	for _, l := range f.Lines {
		if l.Name == "listen_addresses" && strings.Contains(l.Raw, "'*'") {
			found = true
		}
	}
	if !found {
		t.Error("quote style lost")
	}
}

func TestSetUncommentDefault(t *testing.T) {
	f := load(t, "pgpool.conf")
	f.Set("child_life_time", "600")
	v, found, com := f.Get("child_life_time")
	if !found || com || v != "600" {
		t.Fatalf("%q %v %v", v, found, com)
	}
	seen := false
	for _, l := range f.Lines {
		if l.Name == "child_life_time" && !l.Commented {
			seen = true
		}
	}
	if !seen {
		t.Error("default not uncommented")
	}
}

func TestSetManagedAppend(t *testing.T) {
	f := load(t, "pgpool.conf")
	f.Set("custom_param_xyz", "on")
	// must appear inside the managed block
	in := false
	ok := false
	for _, l := range f.Lines {
		if l.Kind == KindManagedMarker {
			if strings.Contains(l.Raw, "end managed") {
				in = false
			} else {
				in = true
			}
		}
		if in && l.Name == "custom_param_xyz" {
			ok = true
		}
	}
	if !ok {
		t.Error("not appended in managed block")
	}
	// round-trip the modified file
	f2, err := Parse(f.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := f2.Get("custom_param_xyz"); v != "on" {
		t.Error("lost on reparse")
	}
}

func TestSetDuplicateLastWins(t *testing.T) {
	f, _ := Parse([]byte("foo = 1\nfoo = 2\n"))
	f.Set("foo", "3")
	actives := 0
	for _, l := range f.Lines {
		if l.Name == "foo" && !l.Commented {
			actives++
			if l.Value != "3" {
				t.Errorf("value %q", l.Value)
			}
		}
		if l.Name == "foo" && l.Commented && !strings.Contains(l.Raw, "#foo = 1") {
			t.Errorf("earlier not commented: %q", l.Raw)
		}
	}
	if actives != 1 {
		t.Fatalf("%d active", actives)
	}
}

func TestQuotingDoubled(t *testing.T) {
	f, _ := Parse([]byte("p = 'it''s'\n"))
	v, _, _ := f.Get("p")
	if v != "it's" {
		t.Fatalf("%q", v)
	}
	f.Set("p", "a b'c")
	v, _, _ = f.Get("p")
	if v != "a b'c" {
		t.Fatalf("%q", v)
	}
	f2, _ := Parse(f.Bytes())
	v, _, _ = f2.Get("p")
	if v != "a b'c" {
		t.Fatalf("reparse %q", v)
	}
}

func TestUnset(t *testing.T) {
	f := load(t, "pgpool.conf")
	f.Unset("port")
	if _, found, com := f.Get("port"); found && !com {
		t.Error("still active")
	}
	f.Set("temp_param", "x") // goes into managed block
	f.Unset("temp_param")
	for _, l := range f.Lines {
		if l.Name == "temp_param" {
			t.Error("managed line not removed")
		}
	}
}

func TestBackends(t *testing.T) {
	f := load(t, "pgpool.conf")
	bs := f.Backends()
	if len(bs) != 2 || bs[0].Hostname != "db1" || bs[1].Port != 5432 {
		t.Fatalf("%+v", bs)
	}
	f.SetBackend(Backend{ID: 2, Hostname: "db3", Port: 5433, Weight: 2})
	bs = f.Backends()
	if len(bs) != 3 || bs[2].Hostname != "db3" {
		t.Fatalf("%+v", bs)
	}
	f.RemoveBackend(0)
	bs = f.Backends()
	if len(bs) != 2 || bs[0].ID != 1 {
		t.Fatalf("%+v", bs)
	}
}

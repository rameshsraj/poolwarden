package pgconf

import (
	"os"
	"testing"
)

func TestHBARules(t *testing.T) {
	b, _ := os.ReadFile("testdata/pool_hba.conf")
	f, _ := ParseHBA(b)
	if string(f.Bytes()) != string(b) {
		t.Fatal("round-trip")
	}
	rs := f.Rules()
	if len(rs) != 4 || rs[0].Type != "local" || rs[0].Method != "trust" {
		t.Fatalf("%+v", rs)
	}
	if rs[1].Address != "127.0.0.1/32" || rs[1].Method != "scram-sha-256" {
		t.Fatalf("%+v", rs[1])
	}
	f.AddRule(HBARule{Type: "host", Database: "all", User: "bob",
		Address: "10.0.0.0/24", Method: "scram-sha-256"})
	rs = f.Rules()
	if len(rs) != 5 {
		t.Fatal("add failed")
	}
	f.RemoveRule(1)
	rs = f.Rules()
	if len(rs) != 4 || rs[1].Address != "::1/128" {
		t.Fatalf("%+v", rs)
	}
}

func TestPCPUsers(t *testing.T) {
	b, _ := os.ReadFile("testdata/pcp.conf")
	f, _ := ParsePCP(b)
	if string(f.Bytes()) != string(b) {
		t.Fatal("round-trip")
	}
	us := f.Users()
	if len(us) != 1 || us[0] != "admin" {
		t.Fatalf("%v", us)
	}
	f.SetUser("admin", "aaa")
	f.SetUser("new", "bbb")
	us = f.Users()
	if len(us) != 2 {
		t.Fatalf("%v", us)
	}
	f.RemoveUser("admin")
	us = f.Users()
	if len(us) != 1 || us[0] != "new" {
		t.Fatalf("%v", us)
	}
	if MD5Hex("password") != "5f4dcc3b5aa765d61d8327deb882cf99" {
		t.Error("md5 wrong")
	}
}

func TestPoolPasswd(t *testing.T) {
	us := ParsePoolPasswd([]byte("alice:md5abc123\n#comment\nbob:AESCfefe\n"))
	if len(us) != 2 || us[0].User != "alice" || us[1].Hash != "AESCfefe" {
		t.Fatalf("%+v", us)
	}
}

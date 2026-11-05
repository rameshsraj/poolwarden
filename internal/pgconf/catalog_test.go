package pgconf

import (
	"testing"

	"github.com/smarsh/poolwarden/internal/model"
)

func TestLookup(t *testing.T) {
	p, ok := Lookup("port")
	if !ok || p.Context != "restart" || p.Default != "9999" {
		t.Fatalf("%+v", p)
	}
	p, ok = Lookup("backend_hostname0")
	if !ok || p.Type != "string" {
		t.Fatalf("numbered: %+v", p)
	}
	if _, ok := Lookup("no_such_param"); ok {
		t.Error("unknown found")
	}
}

func TestValidateValue(t *testing.T) {
	cases := []struct {
		p, v string
		ok   bool
	}{
		{"port", "9999", true},
		{"port", "0", false},
		{"port", "abc", false},
		{"use_watchdog", "on", true},
		{"use_watchdog", "maybe", false},
		{"backend_weight0", "1.5", true},
		{"backend_flag0", "DISALLOW_TO_FAILOVER", true},
		{"backend_flag0", "nope", false},
		{"memqcache_total_size", "64MB", true},
		{"memqcache_total_size", "abc", false},
		{"health_check_period", "10", true},
		{"search_primary_node_timeout", "5min", true},
		{"search_primary_node_timeout", "whenever", false},
		{"listen_addresses", "*", true},
	}
	for _, c := range cases {
		p, ok := Lookup(c.p)
		if !ok {
			t.Fatalf("%s not found", c.p)
		}
		err := ValidateValue(p, c.v)
		if c.ok != (err == nil) {
			t.Errorf("%s=%q ok=%v err=%v", c.p, c.v, c.ok, err)
		}
	}
}

func TestCrossRules(t *testing.T) {
	vals := map[string]string{
		"num_init_children": "32", "max_pool": "4",
		"reserved_connections": "0", "wd_priority": "1",
		"delegate_ip": "10.0.0.100", "if_up_cmd": "/x", "if_down_cmd": "/x", "arping_cmd": "/x",
		"use_watchdog": "on", "wd_port0": "9000", "hostname0": "pgp1",
		"health_check_max_retries": "3",
		"sr_check_user":            "nobody",
	}
	bs := []model.Backend{{MaxConns: 200}}
	rs := CrossRules(vals, bs)
	for _, r := range rs {
		if !r.OK {
			t.Errorf("rule %s failed: %s", r.Name, r.Detail)
		}
	}
	vals["num_init_children"] = "100" // 100*4=400 > 197
	rs = CrossRules(vals, bs)
	found := false
	for _, r := range rs {
		if r.Name == "CONN_BUDGET" {
			found = true
			if r.OK {
				t.Error("budget should fail")
			}
		}
	}
	if !found {
		t.Error("no CONN_BUDGET")
	}
	vals["num_init_children"] = "32"
	vals["reserved_connections"] = "32"
	for _, r := range CrossRules(vals, bs) {
		if r.Name == "RESERVED_CONNECTIONS" && r.OK {
			t.Error("reserved should fail")
		}
	}
	vals["reserved_connections"] = "0"
	vals["if_up_cmd"] = ""
	for _, r := range CrossRules(vals, bs) {
		if r.Name == "VIP_COMMANDS" && r.OK {
			t.Error("vip cmds should fail")
		}
	}
}

func TestContext(t *testing.T) {
	if Context([]string{"port", "log_connections"}) != "restart" {
		t.Error("want restart")
	}
	if Context([]string{"log_connections", "health_check_period"}) != "reload" {
		t.Error("want reload")
	}
}

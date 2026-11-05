package pgconf

import (
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/model"
)

func TestParseTimeSeconds(t *testing.T) {
	for in, want := range map[string]int{
		"5": 5, "5s": 5, "10min": 600, "2h": 7200, "1d": 86400, "1500ms": 1, "": 0, "abc": 0,
	} {
		if got := ParseTimeSeconds(in); got != want {
			t.Errorf("%q → %d want %d", in, got, want)
		}
	}
}

func TestCrossRulesConnBudgetUnknown(t *testing.T) {
	v := map[string]string{"num_init_children": "32", "max_pool": "4"}
	rs := CrossRules(v, []model.Backend{{Name: "db1"}, {Name: "db2"}})
	for _, r := range rs {
		if r.Name == "CONN_BUDGET" {
			if !r.OK || !strings.Contains(r.Detail, "unknown") {
				t.Errorf("unknown max_connections must be OK skip, got %+v", r)
			}
			return
		}
	}
	t.Error("CONN_BUDGET not emitted")
}

func TestCrossRulesSRCheckUnits(t *testing.T) {
	v := map[string]string{"sr_check_user": "pgpool", "sr_check_period": "5s"}
	for _, r := range CrossRules(v, nil) {
		if r.Name == "SR_CHECK" && !r.OK {
			t.Errorf("5s must parse to 5>0: %+v", r)
		}
	}
}

func TestCrossRulesOKDetails(t *testing.T) {
	v := map[string]string{
		"delegate_ip": "1.2.3.4", "if_up_cmd": "ip a", "if_down_cmd": "ip d", "arping_cmd": "arp",
		"use_watchdog": "on", "wd_port0": "9000", "hostname0": "h1",
	}
	for _, r := range CrossRules(v, nil) {
		switch r.Name {
		case "VIP_COMMANDS":
			if !r.OK || r.Detail != "if_up_cmd, if_down_cmd, arping_cmd all set" {
				t.Errorf("VIP_COMMANDS %+v", r)
			}
		case "WATCHDOG_NODES":
			if !r.OK || r.Detail != "watchdog node entries present" {
				t.Errorf("WATCHDOG_NODES %+v", r)
			}
		}
	}
}

package model

import "testing"

func TestStandbyMatches(t *testing.T) {
	b := Backend{Name: "db2", Host: "db2", Addresses: []string{"172.31.10.12", "db2"}}
	cases := []struct {
		st   StandbyRepl
		want bool
	}{
		{StandbyRepl{AppName: "walreceiver", ClientAddr: "172.31.10.12"}, true},
		{StandbyRepl{AppName: "db2", ClientAddr: "1.2.3.4"}, true},
		{StandbyRepl{AppName: "x", ClientAddr: "172.31.10.12/32"}, true},
		{StandbyRepl{AppName: "x", ClientAddr: "9.9.9.9"}, false},
	}
	for i, c := range cases {
		if got := StandbyMatches(c.st, b); got != c.want {
			t.Errorf("case %d → %v want %v", i, got, c.want)
		}
	}
}

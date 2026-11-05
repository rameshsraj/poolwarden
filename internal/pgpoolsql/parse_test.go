package pgpoolsql

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// loadFixture reads a testdata file: first line is a "# SYNTHETIC" comment,
// second line the tab-separated header, the rest tab-separated rows.
func loadFixture(t *testing.T, name string) []Row {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var header []string
	var rows []Row
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if header == nil {
			header = strings.Split(line, "\t")
			continue
		}
		vals := strings.Split(line, "\t")
		r := Row{}
		for i, h := range header {
			if i < len(vals) {
				r[h] = vals[i]
			}
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if header == nil {
		t.Fatalf("%s: no header", name)
	}
	return rows
}

func TestParsePoolNodes44(t *testing.T) {
	nodes, err := ParsePoolNodes(loadFixture(t, "pool_nodes_44.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes", len(nodes))
	}
	n := nodes[0]
	if n.ID != 0 || n.Hostname != "db1" || n.Port != 5432 || n.Status != "up" ||
		n.Role != "primary" || n.PgRole != "primary" || !n.LoadBalanceNode ||
		n.LBWeight != 0.5 || n.SelectCnt != 1042 {
		t.Errorf("node0 %+v", n)
	}
	s := nodes[1]
	if s.ReplicationState != "streaming" || s.ReplicationSyncState != "async" || s.ReplicationDelay != 128 {
		t.Errorf("node1 %+v", s)
	}
	if s.LastStatusChange.IsZero() {
		t.Error("last_status_change not parsed")
	}
}

func TestParsePoolNodes45(t *testing.T) {
	nodes, err := ParsePoolNodes(loadFixture(t, "pool_nodes_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if nodes[1].Status != "down" || nodes[1].PgStatus != "down" {
		t.Errorf("node1 %+v", nodes[1])
	}
}

func TestParsePoolProcesses(t *testing.T) {
	procs, err := ParsePoolProcesses(loadFixture(t, "pool_processes_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 || procs[0].PID != 31001 || procs[0].Database != "postgres" ||
		procs[0].PoolCounter != 4 || procs[1].Status != "Wait for connection" {
		t.Errorf("%+v", procs)
	}
}

func TestParsePoolPools(t *testing.T) {
	pools, err := ParsePoolPools(loadFixture(t, "pool_pools_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 2 || pools[1].BackendID != 1 || pools[1].LoadBalanceNode != 1 ||
		pools[0].BackendPID != 40001 || pools[0].PoolConnected != 1 {
		t.Errorf("%+v", pools)
	}
}

func TestParseHealthCheckStats(t *testing.T) {
	stats, err := ParseHealthCheckStats(loadFixture(t, "health_check_stats_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	s := stats[1]
	if s.FailCount != 2 || s.RetryCount != 3 || s.MaxDurationMS != 900 ||
		s.AverageDurationMS != 25.1 || s.LastFailedHealthCheck != "2026-11-05 10:22:00" {
		t.Errorf("%+v", s)
	}
}

func TestParseBackendStats(t *testing.T) {
	stats, err := ParseBackendStats(loadFixture(t, "backend_stats_45.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if stats[0].SelectCnt != 2048 || stats[0].ErrorCnt != 1 || stats[1].Role != "standby" {
		t.Errorf("%+v", stats)
	}
}

func TestParseShowAll(t *testing.T) {
	m := ParseShowAll(loadFixture(t, "show_all_45.txt"))
	if m["num_init_children"] != "100" || m["delegate_ip"] != "10.0.0.100" ||
		m["use_watchdog"] != "on" {
		t.Errorf("%v", m)
	}
}

func TestParseShowAllNameSetting(t *testing.T) {
	rows := []Row{
		{"name": "num_init_children", "setting": "32", "description": "x"},
		{"name": "max_pool", "setting": "2", "description": "y"},
	}
	m := ParseShowAll(rows)
	if m["num_init_children"] != "32" || m["max_pool"] != "2" {
		t.Errorf("%v", m)
	}
}

func TestShowValue(t *testing.T) {
	if v := showValue(Row{"item": "num_init_children", "value": "100"}); v != "100" {
		t.Errorf("%q", v)
	}
	if v := showValue(Row{"name": "num_init_children", "setting": "32"}); v != "32" {
		t.Errorf("%q", v)
	}
}

// Package modeltest provides synthetic model fixtures for tests.
package modeltest

import (
	"time"

	"github.com/smarsh/poolwarden/internal/model"
)

// SampleSnapshot is a fixed synthetic two-node cluster used by tests and
// examples: db1 primary, db2 standby with 2s delay, watchdog quorum, the VIP
// on db1, half-votes on.
func SampleSnapshot() *model.Snapshot {
	taken := time.Date(2026, 11, 5, 12, 3, 1, 0, time.UTC)
	return &model.Snapshot{
		Taken:    taken,
		Duration: 410 * time.Millisecond,
		Pgpool: model.PgpoolState{
			Reachable: true,
			Version:   "4.5.1",
			Nodes: []model.PoolNode{
				{ID: 0, Hostname: "db1", Port: 5432, Status: "up", PgStatus: "up",
					Role: "primary", PgRole: "primary", LBWeight: 0.5, SelectCnt: 2048,
					LoadBalanceNode: true, LastStatusChange: taken.Add(-2 * time.Hour)},
				{ID: 1, Hostname: "db2", Port: 5432, Status: "up", PgStatus: "up",
					Role: "standby", PgRole: "standby", LBWeight: 0.5, SelectCnt: 900,
					ReplicationDelay: 128, ReplicationState: "streaming",
					ReplicationSyncState: "async", LastStatusChange: taken.Add(-2 * time.Hour)},
			},
			Processes: []model.PoolProcess{
				{PID: 31001, StartTime: "2026-11-05 09:00:00", ClientConnectionCount: 1,
					Database: "postgres", Username: "app",
					BackendConnectionTime: "2026-11-05 09:00:05", PoolCounter: 4, Status: "Idle"},
				{PID: 31002, StartTime: "2026-11-05 09:00:01", ClientConnectionCount: 0,
					Status: "Wait for connection"},
			},
			Pools: []model.PoolPool{
				{PID: 31001, PoolID: 0, BackendID: 0, Database: "postgres", Username: "app",
					BackendConnectionTime: "2026-11-05 09:00:05", PoolCounter: 4,
					BackendPID: 40001, PoolConnected: 1, Status: "1", LoadBalanceNode: 0},
				{PID: 31001, PoolID: 0, BackendID: 1, Database: "postgres", Username: "app",
					BackendConnectionTime: "2026-11-05 09:00:05", PoolCounter: 4,
					BackendPID: 40002, PoolConnected: 1, Status: "1", LoadBalanceNode: 1},
			},
			HealthStats: []model.HealthStat{
				{NodeID: 0, Hostname: "db1", Port: 5432, Status: "up", Role: "primary",
					TotalCount: 100, SuccessCount: 100, AverageDurationMS: 20.5,
					MaxDurationMS: 45, LastHealthCheck: "2026-11-05 12:03:00",
					LastSuccessfulHealthCheck: "2026-11-05 12:03:00"},
				{NodeID: 1, Hostname: "db2", Port: 5432, Status: "up", Role: "standby",
					TotalCount: 100, SuccessCount: 98, FailCount: 2, RetryCount: 3,
					AverageRetryCount: 0.03, MaxRetryCount: 2, AverageDurationMS: 25.1,
					MaxDurationMS: 900, LastHealthCheck: "2026-11-05 12:03:00",
					LastSuccessfulHealthCheck: "2026-11-05 12:02:58",
					LastFailedHealthCheck:     "2026-11-05 10:22:00"},
			},
		},
		Backends: []model.Backend{
			{Name: "db1", NodeID: 0, Host: "10.0.0.11", Port: 5432, Reachable: true,
				Version: 160003, Timeline: 3, CurrentLSN: "3/A1B2C3D4",
				MaxConns: 500, Conns: 80,
				Clients: []model.ClientGroup{
					{Database: "postgres", User: "app", Application: "web", Count: 40},
				},
				Standbys: []model.StandbyRepl{
					{AppName: "db2", ClientAddr: "10.0.0.12", State: "streaming",
						SyncState: "async", SentLSN: "3/A1B2C3D4", WriteLSN: "3/A1B2C000",
						FlushLSN: "3/A1B2B000", ReplayLSN: "3/A1B2A000",
						LagBytes: 13000, WriteLag: 0.1, FlushLag: 0.1, ReplayLag: 0.3},
				},
				Slots: []model.Slot{
					{Name: "slot1", Type: "physical", Active: true,
						WALStatus: "reserved", RetainedBytes: 1 << 20},
				},
			},
			{Name: "db2", NodeID: 1, Host: "10.0.0.12", Port: 5432, Reachable: true,
				InRecovery: true, Version: 160003, Timeline: 3,
				CurrentLSN: "3/A1B2A000", ReceiveLSN: "3/A1B2C000",
				ReplayDelaySec: 2, WALReceiver: true, MaxConns: 500, Conns: 40,
				Clients: []model.ClientGroup{
					{Database: "postgres", User: "app", Application: "web", Count: 20},
				},
			},
		},
		Watchdog: model.Watchdog{
			Available: true, TotalNodes: 2, RemoteNodes: 1, AliveRemote: 1,
			QuorumState: "QUORUM EXIST", LeaderHost: "db1", VIPUpLocal: true,
			Nodes: []model.WatchdogNode{
				{ID: 0, Name: "db1:9999 Linux db1", Host: "db1", Port: 9999, WDPort: 9000,
					Status: "LEADER", Priority: 1, DelegateIP: "10.0.0.100"},
				{ID: 1, Name: "db2:9999 Linux db2", Host: "db2", Port: 9999, WDPort: 9000,
					Status: "STANDBY", Priority: 1, DelegateIP: "10.0.0.100"},
			},
		},
		VIP: []model.VIPCheck{
			{Host: "db1", Present: true, Interfaces: []string{"eth0"}},
			{Host: "db2", Present: false},
		},
		Settings: map[string]string{
			"delegate_ip": "10.0.0.100", "num_init_children": "100", "max_pool": "4",
			"if_up_cmd": "ip addr add", "if_down_cmd": "ip addr del", "arping_cmd": "arping -U",
			"hostname0": "db1", "wd_port0": "9000", "pgpool_port0": "9999",
			"enable_consensus_with_half_votes": "on",
			"failover_when_quorum_exists":      "on",
			"failover_require_consensus":       "on",
			"use_watchdog":                     "on",
		},
	}
}

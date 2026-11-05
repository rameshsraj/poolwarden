package pgpoolsql

import (
	"strconv"
	"time"

	"github.com/smarsh/poolwarden/internal/model"
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func parseTS(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02 15:04:05.999999"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ParsePoolNodes parses SHOW POOL_NODES rows (4.4 and 4.5 column sets).
func ParsePoolNodes(rows []Row) ([]model.PoolNode, error) {
	out := make([]model.PoolNode, 0, len(rows))
	for _, r := range rows {
		n := model.PoolNode{
			ID:                   atoi(r["node_id"]),
			Hostname:             r["hostname"],
			Port:                 atoi(r["port"]),
			Status:               r["status"],
			PgStatus:             r["pg_status"],
			Role:                 r["role"],
			PgRole:               r["pg_role"],
			LBWeight:             atof(r["lb_weight"]),
			SelectCnt:            atoi64(r["select_cnt"]),
			LoadBalanceNode:      r["load_balance_node"] == "true" || r["load_balance_node"] == "t",
			ReplicationDelay:     atoi64(r["replication_delay"]),
			ReplicationState:     r["replication_state"],
			ReplicationSyncState: r["replication_sync_state"],
			LastStatusChange:     parseTS(r["last_status_change"]),
			Raw:                  r,
		}
		out = append(out, n)
	}
	return out, nil
}

// ParsePoolProcesses parses SHOW POOL_PROCESSES rows.
func ParsePoolProcesses(rows []Row) ([]model.PoolProcess, error) {
	out := make([]model.PoolProcess, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.PoolProcess{
			PID:                   atoi(r["pool_pid"]),
			StartTime:             r["start_time"],
			ClientConnectionCount: atoi(r["client_connection_count"]),
			Database:              r["database"],
			Username:              r["username"],
			BackendConnectionTime: r["backend_connection_time"],
			PoolCounter:           atoi(r["pool_counter"]),
			Status:                r["status"],
			Raw:                   r,
		})
	}
	return out, nil
}

// ParsePoolPools parses SHOW POOL_POOLS rows.
func ParsePoolPools(rows []Row) ([]model.PoolPool, error) {
	out := make([]model.PoolPool, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.PoolPool{
			PID:                     atoi(r["pool_pid"]),
			StartTime:               r["start_time"],
			ClientConnectionCount:   atoi(r["client_connection_count"]),
			PoolID:                  atoi(r["pool_id"]),
			BackendID:               atoi(r["backend_id"]),
			Database:                r["database"],
			Username:                r["username"],
			BackendConnectionTime:   r["backend_connection_time"],
			ClientConnectionTime:    r["client_connection_time"],
			ClientDisconnectionTime: r["client_disconnection_time"],
			ClientIdleDuration:      r["client_idle_duration"],
			MajorVersion:            atoi(r["majorversion"]),
			MinorVersion:            atoi(r["minorversion"]),
			PoolCounter:             atoi(r["pool_counter"]),
			BackendPID:              atoi(r["pool_backendpid"]),
			PoolConnected:           atoi(r["pool_connected"]),
			Status:                  r["status"],
			LoadBalanceNode:         atoi(r["load_balance_node"]),
			Raw:                     r,
		})
	}
	return out, nil
}

// ParseHealthCheckStats parses SHOW POOL_HEALTH_CHECK_STATS rows.
func ParseHealthCheckStats(rows []Row) ([]model.HealthStat, error) {
	out := make([]model.HealthStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.HealthStat{
			NodeID:                    atoi(r["node_id"]),
			Hostname:                  r["hostname"],
			Port:                      atoi(r["port"]),
			Status:                    r["status"],
			Role:                      r["role"],
			LastStatusChange:          r["last_status_change"],
			TotalCount:                atoi64(r["total_count"]),
			SuccessCount:              atoi64(r["success_count"]),
			FailCount:                 atoi64(r["fail_count"]),
			SkipCount:                 atoi64(r["skip_count"]),
			RetryCount:                atoi64(r["retry_count"]),
			AverageRetryCount:         atof(r["average_retry_count"]),
			MaxRetryCount:             atoi64(r["max_retry_count"]),
			MaxDurationMS:             atoi64(r["max_duration"]),
			MinDurationMS:             atoi64(r["min_duration"]),
			AverageDurationMS:         atof(r["average_duration"]),
			LastHealthCheck:           r["last_health_check"],
			LastSuccessfulHealthCheck: r["last_successful_health_check"],
			LastSkipHealthCheck:       r["last_skip_health_check"],
			LastFailedHealthCheck:     r["last_failed_health_check"],
			Raw:                       r,
		})
	}
	return out, nil
}

// ParseBackendStats parses SHOW POOL_BACKEND_STATS rows.
func ParseBackendStats(rows []Row) ([]model.BackendStat, error) {
	out := make([]model.BackendStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.BackendStat{
			NodeID:    atoi(r["node_id"]),
			Hostname:  r["hostname"],
			Port:      atoi(r["port"]),
			Status:    r["status"],
			Role:      r["role"],
			SelectCnt: atoi64(r["select_cnt"]),
			InsertCnt: atoi64(r["insert_cnt"]),
			UpdateCnt: atoi64(r["update_cnt"]),
			DeleteCnt: atoi64(r["delete_cnt"]),
			DDLCnt:    atoi64(r["ddl_cnt"]),
			OtherCnt:  atoi64(r["other_cnt"]),
			PanicCnt:  atoi64(r["panic_cnt"]),
			FatalCnt:  atoi64(r["fatal_cnt"]),
			ErrorCnt:  atoi64(r["error_cnt"]),
			Raw:       r,
		})
	}
	return out, nil
}

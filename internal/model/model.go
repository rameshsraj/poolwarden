// Package model holds the shared domain types (design 01 §6): the cluster
// Snapshot and everything inside it. Pure types, no dependencies beyond the
// standard library, so adapters and consumers never import each other.
package model

import (
	"encoding/json"
	"strings"
	"time"
)

// Snapshot is one collection pass over every data source.
type Snapshot struct {
	Taken          time.Time
	Duration       time.Duration
	Degraded       bool
	DegradedReason string
	Pgpool         PgpoolState
	Backends       []Backend
	Watchdog       Watchdog
	VIP            []VIPCheck
	Settings       map[string]string
	Errors         []SourceError
}

// PgpoolState is everything fetched through pgpool's SQL interface.
type PgpoolState struct {
	Reachable    bool
	Err          string
	Version      string
	Nodes        []PoolNode
	Processes    []PoolProcess
	Pools        []PoolPool
	HealthStats  []HealthStat
	BackendStats []BackendStat
}

// PoolNode is one row of SHOW POOL_NODES.
type PoolNode struct {
	ID                   int
	Hostname             string
	Port                 int
	Status               string // up | down | waiting | quarantine | unused
	PgStatus             string
	Role                 string // primary | standby
	PgRole               string
	LBWeight             float64
	SelectCnt            int64
	LoadBalanceNode      bool
	ReplicationDelay     int64 // bytes; -1 unknown
	ReplicationState     string
	ReplicationSyncState string
	LastStatusChange     time.Time
	Raw                  map[string]string
}

// PoolProcess is one row of SHOW POOL_PROCESSES.
type PoolProcess struct {
	PID                   int
	StartTime             string
	ClientConnectionCount int
	Database              string
	Username              string
	BackendConnectionTime string
	PoolCounter           int
	Status                string
	Raw                   map[string]string
}

// PoolPool is one row of SHOW POOL_POOLS.
type PoolPool struct {
	PID                     int
	StartTime               string
	ClientConnectionCount   int
	PoolID                  int
	BackendID               int
	Database                string
	Username                string
	BackendConnectionTime   string
	ClientConnectionTime    string
	ClientDisconnectionTime string
	ClientIdleDuration      string
	MajorVersion            int
	MinorVersion            int
	PoolCounter             int
	BackendPID              int
	PoolConnected           int
	Status                  string
	LoadBalanceNode         int
	Raw                     map[string]string
}

// HealthStat is one row of SHOW POOL_HEALTH_CHECK_STATS.
type HealthStat struct {
	NodeID                    int
	Hostname                  string
	Port                      int
	Status                    string
	Role                      string
	LastStatusChange          string
	TotalCount                int64
	SuccessCount              int64
	FailCount                 int64
	SkipCount                 int64
	RetryCount                int64
	AverageRetryCount         float64
	MaxRetryCount             int64
	MaxDurationMS             int64
	MinDurationMS             int64
	AverageDurationMS         float64
	LastHealthCheck           string
	LastSuccessfulHealthCheck string
	LastSkipHealthCheck       string
	LastFailedHealthCheck     string
	Raw                       map[string]string
}

// BackendStat is one row of SHOW POOL_BACKEND_STATS.
type BackendStat struct {
	NodeID    int
	Hostname  string
	Port      int
	Status    string
	Role      string
	SelectCnt int64
	InsertCnt int64
	UpdateCnt int64
	DeleteCnt int64
	DDLCnt    int64
	OtherCnt  int64
	PanicCnt  int64
	FatalCnt  int64
	ErrorCnt  int64
	Raw       map[string]string
}

// StandbyRepl is one pg_stat_replication row on the primary.
type StandbyRepl struct {
	AppName    string
	ClientAddr string
	State      string
	SyncState  string
	SentLSN    string
	WriteLSN   string
	FlushLSN   string
	ReplayLSN  string
	LagBytes   int64
	WriteLag   float64 // seconds; -1 null
	FlushLag   float64
	ReplayLag  float64
}

// Slot is one pg_replication_slots row.
type Slot struct {
	Name          string
	Type          string
	Active        bool
	WALStatus     string
	RetainedBytes int64
	Database      string
}

// Backend is a direct probe of one PostgreSQL node (design 01 §6).
type Backend struct {
	Name   string // inventory host name; empty if not matched
	NodeID int    // pool node id; -1 if not matched
	Host   string
	// Addresses lists every way this backend may appear in
	// pg_stat_replication.client_addr (host, inventory address, resolved IPs).
	Addresses      []string
	Port           int
	Reachable      bool
	Err            string
	InRecovery     bool
	Version        int // server_version_num
	Timeline       int
	CurrentLSN     string
	ReceiveLSN     string
	ReplayPaused   bool
	WALReceiver    bool
	ReplayDelaySec float64 // -1 = unknown
	Standbys       []StandbyRepl
	Slots          []Slot
	LongTxns       int
	OldestXactSec  float64
	Conflicts      int64
	Deadlocks      int64
	XactRollback   int64
	TempFiles      int64
	MaxConns       int
	Conns          int
	Clients        []ClientGroup
}

// WatchdogNode is one node block of pcp_watchdog_info -v.
type WatchdogNode struct {
	ID         int
	Name       string
	Host       string
	Port       int
	WDPort     int
	Status     string
	Priority   int
	DelegateIP string
}

// Watchdog is the parsed pcp_watchdog_info -v -w output.
type Watchdog struct {
	Available   bool
	Err         string
	Raw         string
	TotalNodes  int
	RemoteNodes int
	AliveRemote int
	QuorumState string
	LeaderName  string
	LeaderHost  string
	VIPUpLocal  bool
	Nodes       []WatchdogNode
}

// VIPCheck is the result of looking for the delegate IP on one pgpool host.
type VIPCheck struct {
	Host       string
	Present    bool
	Err        string
	Interfaces []string
}

// SourceError records one failed data source for the errors view.
type SourceError struct {
	Source string
	Host   string
	Msg    string
}

// Severity levels, ordered.
const (
	SevInfo = "INFO"
	SevWarn = "WARN"
	SevCrit = "CRIT"
)

// Finding is one diagnosis rule hit (design 01 §6).
type Finding struct {
	ID           string // stable rule id + subject, e.g. "REPL_LAG_CRIT:db2"
	Rule         string
	Severity     string // INFO | WARN | CRIT
	Subject      string // inventory host name for node rules; "cluster" otherwise
	Evidence     []string
	Cause        string   // Causes[0]; kept for the single-cause case
	Causes       []string // ranked likely causes for explain
	NextCommands []string
	DocRef       string
	FirstSeen    time.Time
}

// ErrorEvent is a normalised error from any log source (design 01 §6).
type ErrorEvent struct {
	ID       string
	TS       time.Time
	Source   string
	Host     string
	Node     int
	Severity string
	Code     string
	Message  string
	Raw      string
	Ref      string
}

// MaxSeverity returns the highest severity among findings ("" when none).
func MaxSeverity(fs []Finding) string {
	max := ""
	for _, f := range fs {
		switch f.Severity {
		case SevCrit:
			return SevCrit
		case SevWarn:
			max = SevWarn
		case SevInfo:
			if max == "" {
				max = SevInfo
			}
		}
	}
	return max
}

// ExitCodeFor maps findings to the process exit code (11 crit, 10 warn).
func ExitCodeFor(fs []Finding) int {
	switch MaxSeverity(fs) {
	case SevCrit:
		return 11
	case SevWarn:
		return 10
	}
	return 0
}

// ClientGroup counts client connections on one backend by db/user/app.
type ClientGroup struct {
	Database    string
	User        string
	Application string
	Count       int
}

// JSONTime marshals as RFC 3339 with milliseconds in UTC.
type JSONTime time.Time

// UnmarshalJSON accepts the RFC 3339 output of MarshalJSON.
func (t *JSONTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	*t = JSONTime(parsed)
	return nil
}

func (t JSONTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format("2006-01-02T15:04:05.000Z07:00"))
}

// Check is one preflight check result (design 01 §11).
type Check struct {
	Name, Result, Detail string
}

// PreflightResult is recorded in the audit entry (design 01 §11, M8).
type PreflightResult struct {
	Action  string  `json:"action"`
	Verdict string  `json:"verdict"`
	Checks  []Check `json:"checks"`
}

// StandbyMatches reports whether a pg_stat_replication row belongs to backend b.
func StandbyMatches(st StandbyRepl, b Backend) bool {
	if st.AppName != "" && (st.AppName == b.Name || st.AppName == b.Host) {
		return true
	}
	ca := st.ClientAddr
	if i := strings.LastIndex(ca, "/"); i > 0 {
		ca = ca[:i]
	}
	if i := strings.LastIndex(ca, ":"); i > 0 && strings.Count(ca, ":") == 1 {
		ca = ca[:i]
	}
	for _, a := range b.Addresses {
		if a == ca {
			return true
		}
	}
	return ca != "" && (ca == b.Host || ca == b.Name)
}

package pcp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/model"
)

// ParseWatchdogInfo parses pcp_watchdog_info -v -w output. It accepts both
// the "Leader" (4.x) and "Master" (older) wording.
func ParseWatchdogInfo(raw string) (model.Watchdog, error) {
	w := model.Watchdog{Available: true, Raw: raw}
	var blocks []map[string]string
	cur := map[string]string{}
	section := 0 // 0 = cluster info, 1 = node blocks
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.Contains(line, "Node Information") {
				section = 1
			}
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if section == 0 {
			switch key {
			case "Total Nodes":
				w.TotalNodes = atoi(val)
			case "Remote Nodes":
				w.RemoteNodes = atoi(val)
			case "Alive Remote Nodes":
				w.AliveRemote = atoi(val)
			case "Quorum state":
				w.QuorumState = val
			case "Leader Node Name", "Master Node Name":
				w.LeaderName = val
			case "Leader Host Name", "Master Host Name":
				w.LeaderHost = val
			case "VIP up on local node":
				w.VIPUpLocal = strings.EqualFold(val, "YES") || strings.EqualFold(val, "TRUE")
			}
			continue
		}
		if _, dup := cur["Node Name"]; dup && key == "Node Name" {
			flush()
		}
		cur[key] = val
	}
	flush()
	for _, b := range blocks {
		n := model.WatchdogNode{
			Name:       b["Node Name"],
			Host:       b["Host Name"],
			Port:       atoi(b["Pgpool port"]),
			WDPort:     atoi(b["Watchdog port"]),
			Status:     b["Status Name"],
			Priority:   atoi(b["Node priority"]),
			DelegateIP: b["Delegate IP"],
		}
		if n.Status == "" {
			n.Status = b["Status"]
		}
		w.Nodes = append(w.Nodes, n)
	}
	for i := range w.Nodes {
		w.Nodes[i].ID = i
	}
	if w.TotalNodes == 0 && len(w.Nodes) == 0 && w.QuorumState == "" {
		return w, fmt.Errorf("pcp_watchdog_info: unparseable output")
	}
	return w, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ParseNodeInfo parses one `pcp_node_info -v` block.
func ParseNodeInfo(raw string) (model.PoolNode, error) {
	nodes, err := ParseNodeInfoAll(raw)
	if err != nil {
		return model.PoolNode{}, err
	}
	if len(nodes) == 0 {
		return model.PoolNode{}, fmt.Errorf("pcp_node_info: unparseable output")
	}
	return nodes[0], nil
}

// ParseNodeInfoAll parses `pcp_node_info -v --all` (or a single -v block):
// repeated Hostname/Port/... blocks.
func ParseNodeInfoAll(raw string) ([]model.PoolNode, error) {
	var nodes []model.PoolNode
	cur := map[string]string{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		nodes = append(nodes, nodeFromKV(cur))
		cur = map[string]string{}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "Hostname" {
			if _, dup := cur["Hostname"]; dup {
				flush()
			}
		}
		cur[key] = strings.TrimSpace(v)
	}
	flush()
	return nodes, nil
}

func nodeFromKV(kv map[string]string) model.PoolNode {
	n := model.PoolNode{
		Hostname:             kv["Hostname"],
		Port:                 atoi(kv["Port"]),
		Status:               kv["Status Name"],
		PgStatus:             kv["Backend Status Name"],
		Role:                 kv["Role"],
		PgRole:               kv["Backend Role"],
		LBWeight:             atof(kv["Weight"]),
		ReplicationDelay:     int64(atoi(kv["Replication Delay"])),
		ReplicationState:     kv["Replication State"],
		ReplicationSyncState: kv["Replication Sync State"],
		LastStatusChange:     parseTS(kv["Last Status Change"]),
		Raw:                  kv,
	}
	if st, ok := kv["Status"]; ok && n.Status == "" {
		n.Status = st
	}
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func parseTS(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ProcInfoRow is one verbose pcp_proc_info block (per backend connection).
type ProcInfoRow struct {
	Database                string
	Username                string
	StartTime               string
	ClientConnectionCount   int
	Major                   int
	Minor                   int
	BackendConnectionTime   string
	ClientConnectionTime    string
	ClientIdleDuration      string
	ClientDisconnectionTime string
	PoolCounter             int
	BackendPID              int
	Connected               int
	PID                     int
	BackendID               int
	Status                  string
	LoadBalanceNode         int
	Raw                     map[string]string
}

// ParseProcInfo parses `pcp_proc_info -v [-P pid|-a]` output: repeated
// "Key : value" blocks.
func ParseProcInfo(raw string) ([]ProcInfoRow, error) {
	var out []ProcInfoRow
	cur := map[string]string{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, ProcInfoRow{
			Database:                cur["Database"],
			Username:                cur["Username"],
			StartTime:               cur["Start time"],
			ClientConnectionCount:   atoi(cur["Client connection count"]),
			Major:                   atoi(cur["Major"]),
			Minor:                   atoi(cur["Minor"]),
			BackendConnectionTime:   cur["Backend connection time"],
			ClientConnectionTime:    cur["Client connection time"],
			ClientIdleDuration:      cur["Client idle duration"],
			ClientDisconnectionTime: cur["Client disconnection time"],
			PoolCounter:             atoi(cur["Pool Counter"]),
			BackendPID:              atoi(cur["Backend PID"]),
			Connected:               atoi(cur["Connected"]),
			PID:                     atoi(cur["PID"]),
			BackendID:               atoi(cur["Backend ID"]),
			Status:                  cur["Status"],
			LoadBalanceNode:         atoi(cur["Load balance node"]),
			Raw:                     cur,
		})
		cur = map[string]string{}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "Database" {
			if _, dup := cur["Database"]; dup {
				flush()
			}
		}
		cur[key] = strings.TrimSpace(v)
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("pcp_proc_info: unparseable output")
	}
	return out, nil
}

// Package history is the on-disk sample ring (design 01 §16): raw samples
// for the last hour, 1-minute averaged buckets for the day, crash-safe
// appends, atomic rewrites.
package history

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/model"
)

// NodeSample is per-node data captured each interval.
type NodeSample struct {
	Host             string  `json:"host"`
	NodeID           int     `json:"node_id"`
	Reachable        bool    `json:"reachable"`
	InRecovery       bool    `json:"in_recovery"`
	ReplayDelaySec   float64 `json:"replay_delay_sec"`   // -1 unknown
	LagBytes         int64   `json:"lag_bytes"`          // -1 unknown
	RetainedWALBytes int64   `json:"retained_wal_bytes"` // -1 unknown; primary only
	Conns            int     `json:"conns"`
	MaxConns         int     `json:"max_conns"`
	Conflicts        int64   `json:"conflicts"`
	Deadlocks        int64   `json:"deadlocks"`
	XactRollback     int64   `json:"xact_rollback"`
	TempFiles        int64   `json:"temp_files"`
	HealthTotal      int64   `json:"health_total"` // -1 unknown
	HealthSuccess    int64   `json:"health_success"`
	HealthFail       int64   `json:"health_fail"`
	HealthSkip       int64   `json:"health_skip"`
	HealthRetry      int64   `json:"health_retry"`
}

// ChildrenSample is the pgpool pool occupancy.
type ChildrenSample struct {
	Busy    int `json:"busy"`
	Total   int `json:"total"`
	Clients int `json:"clients"`
}

// Sample is one collection point.
type Sample struct {
	TS       time.Time      `json:"ts"`
	Nodes    []NodeSample   `json:"nodes"`
	Children ChildrenSample `json:"children"`
	Degraded bool           `json:"degraded,omitempty"`
}

// FromSnapshot converts a snapshot into a history sample (pure).
func FromSnapshot(s *model.Snapshot) Sample {
	smp := Sample{TS: s.Taken, Degraded: s.Degraded}
	healthByNode := map[int]model.HealthStat{}
	for _, h := range s.Pgpool.HealthStats {
		healthByNode[h.NodeID] = h
	}
	lagByName := map[string]int64{}
	if p := primaryOf(s); p != nil {
		var maxRetained int64 = -1
		for _, sl := range p.Slots {
			if sl.RetainedBytes > maxRetained {
				maxRetained = sl.RetainedBytes
			}
		}
		for _, st := range p.Standbys {
			name := st.AppName
			for i := range s.Backends {
				if model.StandbyMatches(st, s.Backends[i]) && s.Backends[i].Name != "" {
					name = s.Backends[i].Name
					break
				}
			}
			lagByName[name] = st.LagBytes
		}
		for _, b := range s.Backends {
			ns := NodeSample{
				Host: b.Name, NodeID: b.NodeID, Reachable: b.Reachable,
				InRecovery: b.InRecovery, ReplayDelaySec: b.ReplayDelaySec,
				LagBytes: -1, RetainedWALBytes: -1,
				Conns: b.Conns, MaxConns: b.MaxConns, Conflicts: b.Conflicts,
				Deadlocks: b.Deadlocks, XactRollback: b.XactRollback, TempFiles: b.TempFiles,
				HealthTotal: -1, HealthSuccess: -1, HealthFail: -1,
				HealthSkip: -1, HealthRetry: -1,
			}
			if lag, ok := lagByName[b.Name]; ok {
				ns.LagBytes = lag
			}
			if !b.InRecovery {
				ns.RetainedWALBytes = maxRetained
			}
			if h, ok := healthByNode[b.NodeID]; ok {
				ns.HealthTotal = h.TotalCount
				ns.HealthSuccess = h.SuccessCount
				ns.HealthFail = h.FailCount
				ns.HealthSkip = h.SkipCount
				ns.HealthRetry = h.RetryCount
			}
			smp.Nodes = append(smp.Nodes, ns)
		}
		return smp
	}
	for _, b := range s.Backends {
		ns := NodeSample{
			Host: b.Name, NodeID: b.NodeID, Reachable: b.Reachable,
			InRecovery: b.InRecovery, ReplayDelaySec: b.ReplayDelaySec,
			LagBytes: -1, RetainedWALBytes: -1,
			Conns: b.Conns, MaxConns: b.MaxConns, Conflicts: b.Conflicts,
			HealthTotal: -1, HealthSuccess: -1, HealthFail: -1,
			HealthSkip: -1, HealthRetry: -1,
		}
		if h, ok := healthByNode[b.NodeID]; ok {
			ns.HealthTotal = h.TotalCount
			ns.HealthSuccess = h.SuccessCount
			ns.HealthFail = h.FailCount
			ns.HealthSkip = h.SkipCount
			ns.HealthRetry = h.RetryCount
		}
		smp.Nodes = append(smp.Nodes, ns)
	}
	return smp
}

func primaryOf(s *model.Snapshot) *model.Backend {
	for i := range s.Backends {
		if s.Backends[i].Reachable && !s.Backends[i].InRecovery {
			return &s.Backends[i]
		}
	}
	return nil
}

// Store is the on-disk ring: current.jsonl (raw) + archive.jsonl (1m buckets).
type Store struct {
	FS              pwfs.FS
	Dir             string
	Clock           clock.Clock
	MaxBytes        int64         // default 32MB
	Retention       time.Duration // default 24h
	DownsampleAfter time.Duration // default 1h
	Bucket          time.Duration // default 1m
}

func (s *Store) defaults() {
	if s.MaxBytes <= 0 {
		s.MaxBytes = 32 << 20
	}
	if s.Retention <= 0 {
		s.Retention = 24 * time.Hour
	}
	if s.DownsampleAfter <= 0 {
		s.DownsampleAfter = time.Hour
	}
	if s.Bucket <= 0 {
		s.Bucket = time.Minute
	}
	if s.FS == nil {
		s.FS = pwfs.OS{}
	}
}

func (s *Store) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

func (s *Store) path(name string) string { return s.Dir + "/" + name }

func (s *Store) readFile(name string) ([]Sample, error) {
	data, err := s.FS.ReadFile(s.path(name))
	if err != nil {
		return nil, err
	}
	return decodeSamples(data), nil
}

// decodeSamples parses JSONL, skipping blank lines and a truncated/corrupt
// last line (crash safety).
func decodeSamples(data []byte) []Sample {
	var out []Sample
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var sm Sample
		if err := json.Unmarshal([]byte(line), &sm); err != nil {
			continue
		}
		out = append(out, sm)
	}
	return out
}

// Append writes one sample, then compacts/caps when needed.
func (s *Store) Append(sm Sample) error {
	s.defaults()
	if err := s.FS.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	f, err := s.FS.OpenAppend(s.path("current.jsonl"), 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(sm)
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	raw, err := s.readFile("current.jsonl")
	if err != nil {
		return err
	}
	if len(raw) == 0 || s.now().Sub(raw[0].TS) <= s.DownsampleAfter {
		return s.capSize()
	}
	return s.Compact()
}

// Compact moves raw samples older than DownsampleAfter into archive buckets
// and drops archive entries older than Retention.
func (s *Store) Compact() error {
	s.defaults()
	raw, _ := s.readFile("current.jsonl")
	arc, _ := s.readFile("archive.jsonl")
	cutoff := s.now().Add(-s.DownsampleAfter)
	var keepRaw, old []Sample
	for _, sm := range raw {
		if sm.TS.Before(cutoff) {
			old = append(old, sm)
		} else {
			keepRaw = append(keepRaw, sm)
		}
	}
	arc = append(arc, bucketize(old, s.Bucket)...)
	var keepArc []Sample
	for _, sm := range arc {
		if s.now().Sub(sm.TS) <= s.Retention {
			keepArc = append(keepArc, sm)
		}
	}
	if len(old) > 0 || len(keepArc) != len(arc) {
		if err := s.writeSamples("archive.jsonl", keepArc); err != nil {
			return err
		}
	}
	if len(old) > 0 {
		if err := s.writeSamples("current.jsonl", keepRaw); err != nil {
			return err
		}
	}
	return s.capSize()
}

// bucketize averages samples into fixed buckets: means for numeric fields,
// last value for health counters, majority for booleans.
func bucketize(in []Sample, bucket time.Duration) []Sample {
	sort.Slice(in, func(i, j int) bool { return in[i].TS.Before(in[j].TS) })
	var out []Sample
	byBucket := map[int64][]Sample{}
	var order []int64
	for _, sm := range in {
		k := sm.TS.Unix() / int64(bucket.Seconds())
		if _, ok := byBucket[k]; !ok {
			order = append(order, k)
		}
		byBucket[k] = append(byBucket[k], sm)
	}
	for _, k := range order {
		out = append(out, average(byBucket[k], time.Unix(k*int64(bucket.Seconds()), 0)))
	}
	return out
}

func average(samples []Sample, ts time.Time) Sample {
	out := Sample{TS: ts}
	n := len(samples)
	if n == 0 {
		return out
	}
	mean := func(vals []float64) float64 {
		var sum float64
		var cnt int
		for _, v := range vals {
			if v >= 0 {
				sum += v
				cnt++
			}
		}
		if cnt == 0 {
			return -1
		}
		return sum / float64(cnt)
	}
	meanI := func(vals []int64) int64 {
		var sum int64
		var cnt int
		for _, v := range vals {
			if v >= 0 {
				sum += v
				cnt++
			}
		}
		if cnt == 0 {
			return -1
		}
		return sum / int64(cnt)
	}
	hosts := map[string][]NodeSample{}
	var hostOrder []string
	for _, sm := range samples {
		for _, nd := range sm.Nodes {
			if _, ok := hosts[nd.Host]; !ok {
				hostOrder = append(hostOrder, nd.Host)
			}
			hosts[nd.Host] = append(hosts[nd.Host], nd)
		}
		out.Children.Busy += sm.Children.Busy
		out.Children.Total += sm.Children.Total
		out.Children.Clients += sm.Children.Clients
	}
	out.Children.Busy /= n
	out.Children.Total /= n
	out.Children.Clients /= n
	for _, host := range hostOrder {
		list := hosts[host]
		last := list[len(list)-1]
		ns := NodeSample{
			Host: host, NodeID: last.NodeID,
			ReplayDelaySec:   mean(eachF(list, func(n NodeSample) float64 { return n.ReplayDelaySec })),
			LagBytes:         meanI(eachI(list, func(n NodeSample) int64 { return n.LagBytes })),
			RetainedWALBytes: meanI(eachI(list, func(n NodeSample) int64 { return n.RetainedWALBytes })),
			Conns:            int(meanI(eachI(list, func(n NodeSample) int64 { return int64(n.Conns) }))),
			MaxConns:         int(meanI(eachI(list, func(n NodeSample) int64 { return int64(n.MaxConns) }))),
			Conflicts:        last.Conflicts,
			HealthTotal:      last.HealthTotal,
			HealthSuccess:    last.HealthSuccess,
			HealthFail:       last.HealthFail,
			HealthSkip:       last.HealthSkip,
			HealthRetry:      last.HealthRetry,
			Reachable:        majority(list, func(n NodeSample) bool { return n.Reachable }),
			InRecovery:       majority(list, func(n NodeSample) bool { return n.InRecovery }),
		}
		out.Nodes = append(out.Nodes, ns)
	}
	return out
}

func eachF(list []NodeSample, f func(NodeSample) float64) []float64 {
	var out []float64
	for _, n := range list {
		out = append(out, f(n))
	}
	return out
}

func eachI(list []NodeSample, f func(NodeSample) int64) []int64 {
	var out []int64
	for _, n := range list {
		out = append(out, f(n))
	}
	return out
}

func majority(list []NodeSample, f func(NodeSample) bool) bool {
	yes := 0
	for _, n := range list {
		if f(n) {
			yes++
		}
	}
	return yes*2 > len(list)
}

func (s *Store) writeSamples(name string, samples []Sample) error {
	var b strings.Builder
	for _, sm := range samples {
		l, _ := json.Marshal(sm)
		b.Write(l)
		b.WriteByte('\n')
	}
	return s.FS.WriteFileAtomic(s.path(name), []byte(b.String()), false)
}

// capSize drops the oldest archive lines, then oldest raw lines, until both
// files fit under MaxBytes.
func (s *Store) capSize() error {
	for _, name := range []string{"archive.jsonl", "current.jsonl"} {
		fi, err := s.FS.Stat(s.path(name))
		if err != nil || fi.Size() <= s.MaxBytes {
			continue
		}
		samples, _ := s.readFile(name)
		for len(samples) > 0 {
			var b strings.Builder
			for _, sm := range samples {
				l, _ := json.Marshal(sm)
				b.Write(l)
				b.WriteByte('\n')
			}
			if int64(b.Len()) <= s.MaxBytes {
				return s.FS.WriteFileAtomic(s.path(name), []byte(b.String()), false)
			}
			samples = samples[1:]
		}
		return s.FS.WriteFileAtomic(s.path(name), nil, false)
	}
	return nil
}

// Since returns archive+raw samples merged and sorted, within [now-d, now].
func (s *Store) Since(d time.Duration) ([]Sample, error) {
	s.defaults()
	var out []Sample
	for _, name := range []string{"archive.jsonl", "current.jsonl"} {
		samples, err := s.readFile(name)
		if err != nil {
			continue
		}
		out = append(out, samples...)
	}
	cutoff := s.now().Add(-d)
	var res []Sample
	for _, sm := range out {
		if !sm.TS.Before(cutoff) {
			res = append(res, sm)
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].TS.Before(res[j].TS) })
	return res, nil
}

// Latest returns the newest available sample.
func (s *Store) Latest() (Sample, bool, error) {
	samples, err := s.Since(s.Retention + s.DownsampleAfter + time.Hour)
	if err != nil {
		return Sample{}, false, err
	}
	if len(samples) == 0 {
		return Sample{}, false, nil
	}
	return samples[len(samples)-1], true, nil
}

// Span is newest minus oldest available sample.
func (s *Store) Span() (time.Duration, error) {
	s.defaults()
	var all []Sample
	for _, name := range []string{"archive.jsonl", "current.jsonl"} {
		samples, _ := s.readFile(name)
		all = append(all, samples...)
	}
	if len(all) == 0 {
		return 0, nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TS.Before(all[j].TS) })
	return all[len(all)-1].TS.Sub(all[0].TS), nil
}

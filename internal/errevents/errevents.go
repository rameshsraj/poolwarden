// Package errevents normalises error events across all sources (docs/01 §5,
// docs/04 I13): pgpool/pg logs, health deltas, replication findings, PCP
// failures, connection failures, db-stat deltas, tool.log.
package errevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
)

// EventID is stable per (source|host|ts|raw).
func EventID(source, host string, ts time.Time, raw string) string {
	h := sha256.Sum256([]byte(source + "|" + host + "|" + ts.Format(time.RFC3339Nano) + "|" + raw))
	return hex.EncodeToString(h[:])[:8]
}

// HistoryReader is the history surface needed for delta sources.
type HistoryReader interface {
	Since(time.Duration) ([]history.Sample, error)
}

// Collector builds the unified event list.
type Collector struct {
	Logs       []logsrc.Reader
	History    HistoryReader
	PCPResults func() []pcp.Result
	Tool       interface {
		Recent(ctx context.Context, since time.Duration) ([]model.ErrorEvent, error)
	}
	Findings func(ctx context.Context) []model.Finding
	Clock    interface{ Now() time.Time }
	// HostOf maps a log host to inventory; empty passthrough.
}

func (c *Collector) now() time.Time {
	if c.Clock != nil {
		return c.Clock.Now()
	}
	return time.Now()
}

// Filter selects events (02 §3.4).
type Filter struct {
	Since       time.Duration
	Sources     map[string]bool // empty = all
	MinSeverity string          // "" = all
	Host        string
	Node        *int
	Grep        *regexp.Regexp
}

var replRules = map[string]bool{
	"WAL_RECEIVER_MISSING": true, "STANDBY_NOT_STREAMING": true,
	"REPL_CONFLICTS": true, "SLOT_INACTIVE": true,
}

var sevRank = map[string]int{"INFO": 0, "LOG": 0, "NOTICE": 1, "WARNING": 2, "WARN": 2, "ERROR": 3, "FATAL": 4, "PANIC": 4, "CRIT": 4}

// Events collects and normalises all sources within the filter window.
func (c *Collector) Events(ctx context.Context, f Filter) ([]model.ErrorEvent, error) {
	since := f.Since
	if since <= 0 {
		since = time.Hour
	}
	var out []model.ErrorEvent
	want := func(s string) bool { return len(f.Sources) == 0 || f.Sources[s] }

	if want("pgpool") || want("pg") || want("conn") {
		events, _ := c.fromLogs(ctx, f, since, want)
		out = append(out, events...)
	}
	if want("health") {
		out = append(out, c.healthDeltas()...)
	}
	if want("replication") && c.Findings != nil {
		for _, fd := range c.Findings(ctx) {
			if replRules[fd.Rule] {
				ev := strings.Join(fd.Evidence, "; ")
				out = append(out, model.ErrorEvent{
					ID: EventID("replication", fd.Subject, c.now(), fd.ID),
					TS: c.now(), Source: "replication", Host: fd.Subject,
					Severity: sevMap(fd.Severity), Code: fd.Rule,
					Message: ev, Ref: fd.ID,
				})
			}
		}
	}
	if want("pcp") && c.PCPResults != nil {
		for _, r := range c.PCPResults() {
			if r.Exit != 0 {
				msg := strings.TrimSpace(r.Stderr)
				if i := strings.IndexByte(msg, '\n'); i >= 0 {
					msg = msg[:i]
				}
				out = append(out, model.ErrorEvent{
					ID: EventID("pcp", "", r.Finished, strings.Join(r.Args, " ")),
					TS: r.Finished, Source: "pcp", Severity: "ERROR",
					Code:    fmt.Sprintf("PCP_EXIT_%d", r.Exit),
					Message: msg, Raw: strings.Join(r.Args, " "),
				})
			}
		}
	}
	if want("db") {
		out = append(out, c.dbDeltas()...)
	}
	if want("tool") && c.Tool != nil {
		ev, _ := c.Tool.Recent(ctx, since)
		out = append(out, ev...)
	}
	// filter
	var res []model.ErrorEvent
	cutoff := c.now().Add(-since)
	for _, e := range out {
		if e.ID == "" {
			e.ID = EventID(e.Source, e.Host, e.TS, e.Raw)
		}
		if !e.TS.IsZero() && e.TS.Before(cutoff) {
			continue
		}
		if f.Host != "" && e.Host != f.Host {
			continue
		}
		if f.Node != nil && e.Node != *f.Node {
			continue
		}
		if f.Grep != nil && !f.Grep.MatchString(e.Message) && !f.Grep.MatchString(e.Raw) {
			continue
		}
		if f.MinSeverity != "" && sevRank[normSev(e.Severity)] < sevRank[normSev(f.MinSeverity)] {
			continue
		}
		res = append(res, e)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].TS.After(res[j].TS) })
	return res, nil
}

func normSev(s string) string {
	if s == "WARN" {
		return "WARNING"
	}
	return s
}

func sevMap(s string) string {
	switch s {
	case model.SevCrit:
		return "ERROR"
	case model.SevWarn:
		return "WARNING"
	default:
		return "INFO"
	}
}

func (c *Collector) fromLogs(ctx context.Context, f Filter, since time.Duration, want func(string) bool) ([]model.ErrorEvent, error) {
	var out []model.ErrorEvent
	for _, rd := range c.Logs {
		if f.Host != "" && rd != nil {
			if frd, ok := rd.(*logsrc.FileSource); ok && frd.Host != f.Host {
				continue
			}
		}
		lines, err := rd.Read(ctx, logsrc.ReadOpts{Since: since, Grep: f.Grep, Now: c.now()})
		if err != nil {
			continue
		}
		for _, l := range lines {
			tag := logsrc.Classify(l)
			// conn is a cross-component source for CONN_FAIL/AUTH_FAIL
			switch tag {
			case "CONN_FAIL", "AUTH_FAIL":
				if want("conn") {
					out = append(out, c.event("conn", l, tag))
				}
				continue
			}
			sig := tag == "FAILOVER" || tag == "HEALTHCHECK" || tag == "CHILD_EXIT" || tag == "WD_ELECTION"
			if sevRank[l.Severity] < sevRank["WARNING"] && !sig {
				continue
			}
			if !want(l.Component) {
				continue
			}
			e := c.event(l.Component, l, tag)
			if l.Component == "pg" && l.Fields["sqlstate"] != "" {
				e.Code = l.Fields["sqlstate"]
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *Collector) event(src string, l logsrc.Line, tag string) model.ErrorEvent {
	return model.ErrorEvent{
		ID: EventID(src, l.Host, l.TS, l.Raw), TS: l.TS,
		Source: src, Host: l.Host, Severity: l.Severity,
		Code: tagOrSev(tag, l.Severity), Message: l.Message, Raw: l.Raw,
		Ref: fmt.Sprintf("%s:%d", l.File, l.LineNo),
	}
}

func tagOrSev(tag, sev string) string {
	if tag != "OTHER" {
		return tag
	}
	return sev
}

func (c *Collector) latest2() ([]history.NodeSample, []history.NodeSample) {
	if c.History == nil {
		return nil, nil
	}
	samples, err := c.History.Since(30 * time.Minute)
	if err != nil || len(samples) < 2 {
		return nil, nil
	}
	return samples[len(samples)-1].Nodes, samples[len(samples)-2].Nodes
}

func (c *Collector) healthDeltas() []model.ErrorEvent {
	cur, prev := c.latest2()
	var out []model.ErrorEvent
	for _, n := range cur {
		var p *history.NodeSample
		for i := range prev {
			if prev[i].Host == n.Host {
				p = &prev[i]
			}
		}
		if p == nil || n.HealthFail < 0 || p.HealthFail < 0 {
			continue
		}
		if n.HealthFail > p.HealthFail || n.HealthRetry > p.HealthRetry {
			out = append(out, model.ErrorEvent{
				ID: EventID("health", n.Host, c.now(), n.Host),
				TS: c.now(), Source: "health", Host: n.Host, Node: n.NodeID,
				Severity: "WARNING", Code: "HEALTHCHECK_FAIL",
				Message: fmt.Sprintf("health checks failing on %s: fail %d→%d retry %d→%d",
					n.Host, p.HealthFail, n.HealthFail, p.HealthRetry, n.HealthRetry),
			})
		}
	}
	return out
}

func (c *Collector) dbDeltas() []model.ErrorEvent {
	cur, prev := c.latest2()
	var out []model.ErrorEvent
	for _, n := range cur {
		var p *history.NodeSample
		for i := range prev {
			if prev[i].Host == n.Host {
				p = &prev[i]
			}
		}
		if p == nil {
			continue
		}
		mk := func(code, msg string) {
			out = append(out, model.ErrorEvent{
				ID: EventID("db", n.Host, c.now(), code+n.Host),
				TS: c.now(), Source: "db", Host: n.Host, Node: n.NodeID,
				Severity: "WARNING", Code: code, Message: msg,
			})
		}
		if n.Deadlocks > p.Deadlocks {
			mk("DEADLOCK", fmt.Sprintf("deadlocks on %s: %d→%d", n.Host, p.Deadlocks, n.Deadlocks))
		}
		if n.TempFiles > p.TempFiles {
			mk("TEMP_FILES", fmt.Sprintf("temp files on %s: %d→%d", n.Host, p.TempFiles, n.TempFiles))
		}
		if n.XactRollback-p.XactRollback > 100 {
			mk("ROLLBACK_SPIKE", fmt.Sprintf("rollbacks on %s: %d→%d", n.Host, p.XactRollback, n.XactRollback))
		}
	}
	return out
}

// SummaryRow is one line of `errors summary`.
type SummaryRow struct {
	Source string    `json:"source"`
	Code   string    `json:"code"`
	Count  int       `json:"count"`
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
}

// Summary groups events by (source, code).
func Summary(events []model.ErrorEvent) []SummaryRow {
	m := map[[2]string]*SummaryRow{}
	var order []*SummaryRow
	for _, e := range events {
		k := [2]string{e.Source, e.Code}
		if r, ok := m[k]; ok {
			r.Count++
			if e.TS.Before(r.First) {
				r.First = e.TS
			}
			if e.TS.After(r.Last) {
				r.Last = e.TS
			}
		} else {
			r := &SummaryRow{Source: e.Source, Code: e.Code, Count: 1, First: e.TS, Last: e.TS}
			m[k] = r
			order = append(order, r)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].Source != order[j].Source {
			return order[i].Source < order[j].Source
		}
		return order[i].Code < order[j].Code
	})
	out := make([]SummaryRow, len(order))
	for i, r := range order {
		out[i] = *r
	}
	return out
}

// ShowResult is `errors show <id>`.
type ShowResult struct {
	Event    model.ErrorEvent `json:"event"`
	Context  []string         `json:"context"`
	Findings []model.Finding  `json:"findings"`
	Explain  *ExplainLike     `json:"explain,omitempty"`
}

// ExplainLike avoids importing diagnose (cycle); the service fills it.
type ExplainLike struct {
	What         string   `json:"what"`
	LikelyCauses []string `json:"likely_causes"`
	Fix          []string `json:"fix"`
}

// Show finds one event by id and gathers context lines.
func (c *Collector) Show(ctx context.Context, id string, since time.Duration) (*ShowResult, error) {
	events, err := c.Events(ctx, Filter{Since: since})
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		if e.ID == id {
			res := &ShowResult{Event: e}
			if file, line, ok := parseRef(e.Ref); ok {
				for _, rd := range c.Logs {
					if fs, ok := rd.(*logsrc.FileSource); ok {
						if strings.Contains(file, fs.Dir) {
							if ctx2, err2 := fs.Context(ctx, file, line, 10); err2 == nil {
								res.Context = ctx2
							}
						}
					}
				}
			}
			return res, nil
		}
	}
	return nil, fmt.Errorf("no error with id %s in the window", id)
}

func parseRef(ref string) (string, int64, bool) {
	i := strings.LastIndexByte(ref, ':')
	if i < 0 {
		return "", 0, false
	}
	var n int64
	if _, err := fmt.Sscanf(ref[i+1:], "%d", &n); err != nil {
		return "", 0, false
	}
	return ref[:i], n, true
}

// Recent implements service.ErrorSource for explain's related errors.
func (c *Collector) Recent(ctx context.Context, subject string, n int) []model.ErrorEvent {
	ev, err := c.Events(ctx, Filter{Since: 24 * time.Hour})
	if err != nil {
		return nil
	}
	var out []model.ErrorEvent
	for _, e := range ev {
		if e.Host == subject || strings.Contains(e.Ref, subject) {
			out = append(out, e)
			if len(out) >= n {
				break
			}
		}
	}
	return out
}

// Follow fans in all log readers' Follow (pgpool/pg/conn sources only).
func (c *Collector) Follow(ctx context.Context, f Filter, out chan<- model.ErrorEvent) error {
	if len(c.Logs) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	inner := make(chan logsrc.Line, 64)
	done := make(chan error, len(c.Logs))
	for _, rd := range c.Logs {
		rd := rd
		go func() { done <- rd.Follow(ctx, logsrc.ReadOpts{}, inner) }()
	}
	active := len(c.Logs)
	for active > 0 {
		select {
		case <-ctx.Done():
			for i := 0; i < active; i++ {
				<-done
			}
			return ctx.Err()
		case err := <-done:
			active--
			if err != nil {
				return err
			}
		case l := <-inner:
			tag := logsrc.Classify(l)
			src := l.Component
			if tag == "CONN_FAIL" || tag == "AUTH_FAIL" {
				src = "conn"
			}
			if len(f.Sources) > 0 && !f.Sources[src] {
				continue
			}
			if f.Grep != nil && !f.Grep.MatchString(l.Message) {
				continue
			}
			ev := c.event(src, l, tag)
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

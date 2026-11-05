package errevents

import (
	"context"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
)

type fakeReader struct{ lines []logsrc.Line }

func (f *fakeReader) Read(ctx context.Context, o logsrc.ReadOpts) ([]logsrc.Line, error) {
	return f.lines, nil
}
func (f *fakeReader) Follow(ctx context.Context, o logsrc.ReadOpts, out chan<- logsrc.Line) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeReader) Context(ctx context.Context, file string, n int64, k int) ([]string, error) {
	return []string{"ctx1", "ctx2"}, nil
}

type fakeHist struct{ samples []history.Sample }

func (f *fakeHist) Since(d time.Duration) ([]history.Sample, error) { return f.samples, nil }
func (f *fakeHist) Span() (time.Duration, error)                    { return time.Hour, nil }

func mkLine(host, comp, sev, msg string) logsrc.Line {
	return logsrc.Line{Host: host, Component: comp, Severity: sev, Message: msg,
		TS: time.Now(), Raw: msg, File: "/var/log/pgpool/pgpool.log", LineNo: 1}
}

func TestEventIDStable(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := EventID("pg", "db1", ts, "raw")
	b := EventID("pg", "db1", ts, "raw")
	if a != b || len(a) != 8 {
		t.Fatalf("%s %s", a, b)
	}
	if EventID("pg", "db1", ts, "raw2") == a {
		t.Error("same id for different raw")
	}
}

func TestEventsFromLogs(t *testing.T) {
	c := &Collector{Logs: []logsrc.Reader{
		&fakeReader{lines: []logsrc.Line{
			mkLine("db1", "pgpool", "LOG", "everything normal"),
			mkLine("db1", "pgpool", "WARNING", "health check timed out"),
			mkLine("db1", "pgpool", "LOG", "failover_command started"),
			mkLine("db1", "pg", "ERROR", "password authentication failed"),
		}},
	}}
	ev, err := c.Events(context.Background(), Filter{Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 3 {
		t.Fatalf("%d events: %+v", len(ev), ev)
	}
	codes := map[string]string{}
	for _, e := range ev {
		codes[e.Code] = e.Source
	}
	if codes["HEALTHCHECK"] != "pgpool" || codes["FAILOVER"] != "pgpool" || codes["AUTH_FAIL"] != "conn" {
		t.Errorf("%v", codes)
	}
	// source filter
	ev2, _ := c.Events(context.Background(), Filter{Since: time.Hour, Sources: map[string]bool{"conn": true}})
	if len(ev2) != 1 || ev2[0].Source != "conn" {
		t.Errorf("conn filter: %+v", ev2)
	}
}

func TestHealthDbDeltas(t *testing.T) {
	h := &fakeHist{samples: []history.Sample{
		{Nodes: []history.NodeSample{{Host: "db2", NodeID: 1, HealthFail: 2, HealthRetry: 0,
			Deadlocks: 0, XactRollback: 100, TempFiles: 3}}},
		{Nodes: []history.NodeSample{{Host: "db2", NodeID: 1, HealthFail: 5, HealthRetry: 1,
			Deadlocks: 1, XactRollback: 250, TempFiles: 3}}},
	}}
	c := &Collector{History: h}
	ev, _ := c.Events(context.Background(), Filter{Since: time.Hour})
	var codes []string
	for _, e := range ev {
		codes = append(codes, e.Code)
	}
	want := map[string]bool{"HEALTHCHECK_FAIL": true, "DEADLOCK": true, "ROLLBACK_SPIKE": true}
	for _, w := range []string{"HEALTHCHECK_FAIL", "DEADLOCK", "ROLLBACK_SPIKE"} {
		found := false
		for _, cd := range codes {
			if cd == w {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s in %v", w, codes)
		}
	}
	if len(want) == 0 {
		t.Error()
	}
	// no TempFiles event (3→3)
	for _, e := range ev {
		if e.Code == "TEMP_FILES" {
			t.Errorf("unexpected %+v", e)
		}
	}
}

func TestPCPSource(t *testing.T) {
	c := &Collector{PCPResults: func() []pcp.Result {
		return []pcp.Result{
			{Args: []string{"-h", "x"}, Exit: 2, Stderr: "auth failed\nmore", Finished: time.Now()},
			{Exit: 0},
		}
	}}
	ev, _ := c.Events(context.Background(), Filter{Since: time.Hour})
	if len(ev) != 1 || ev[0].Code != "PCP_EXIT_2" || ev[0].Message != "auth failed" {
		t.Fatalf("%+v", ev)
	}
}

func TestReplicationSource(t *testing.T) {
	c := &Collector{Findings: func(ctx context.Context) []model.Finding {
		return []model.Finding{
			{ID: "STANDBY_NOT_STREAMING:db2", Rule: "STANDBY_NOT_STREAMING",
				Severity: model.SevCrit, Subject: "db2", Evidence: []string{"no row"}},
			{ID: "X:y", Rule: "OTHER_RULE"},
		}
	}}
	ev, _ := c.Events(context.Background(), Filter{Since: time.Hour})
	if len(ev) != 1 || ev[0].Source != "replication" || ev[0].Ref != "STANDBY_NOT_STREAMING:db2" {
		t.Fatalf("%+v", ev)
	}
}

func TestSummary(t *testing.T) {
	now := time.Now()
	evs := []model.ErrorEvent{
		{Source: "pg", Code: "CONN_FAIL", TS: now.Add(-time.Hour)},
		{Source: "pg", Code: "CONN_FAIL", TS: now},
		{Source: "tool", Code: "TOOL", TS: now},
	}
	rows := Summary(evs)
	if len(rows) != 2 || rows[0].Count != 2 {
		t.Fatalf("%+v", rows)
	}
	if !rows[0].First.Before(rows[0].Last) {
		t.Error("ordering")
	}
}

func TestShow(t *testing.T) {
	l := mkLine("db1", "pg", "ERROR", "boom")
	c := &Collector{Logs: []logsrc.Reader{&fakeReader{lines: []logsrc.Line{l}}}}
	ev, _ := c.Events(context.Background(), Filter{Since: time.Hour})
	if len(ev) != 1 {
		t.Fatalf("%d", len(ev))
	}
	res, err := c.Show(context.Background(), ev[0].ID, time.Hour)
	if err != nil || res.Event.Message != "boom" {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := c.Show(context.Background(), "deadbeef", time.Hour); err == nil {
		t.Error("want error for unknown id")
	}
}

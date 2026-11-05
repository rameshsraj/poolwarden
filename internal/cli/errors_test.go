package cli

import (
	"context"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/logsrc"
)

type fakeReader struct{ lines []logsrc.Line }

func (f *fakeReader) Read(ctx context.Context, o logsrc.ReadOpts) ([]logsrc.Line, error) {
	out := make([]logsrc.Line, len(f.lines))
	copy(out, f.lines)
	return out, nil
}
func (f *fakeReader) Follow(ctx context.Context, o logsrc.ReadOpts, out chan<- logsrc.Line) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeReader) Context(ctx context.Context, file string, n int64, k int) ([]string, error) {
	return []string{"prev line", "the line", "next line"}, nil
}

func testErrColl() *errevents.Collector {
	ts := time.Date(2026, 11, 5, 12, 2, 0, 0, time.Local)
	return &errevents.Collector{Logs: []logsrc.Reader{
		&fakeReader{lines: []logsrc.Line{
			{Host: "db1", Component: "pgpool", Severity: "WARNING",
				Message: "health check timed out on node 1", TS: ts,
				Raw: "raw warn", File: "/var/log/pgpool/pgpool.log", LineNo: 42},
			{Host: "db1", Component: "pgpool", Severity: "ERROR",
				Message: "failover_command started for node 1", TS: ts.Add(time.Minute),
				Raw: "raw failover", File: "/var/log/pgpool/pgpool.log", LineNo: 43},
			{Host: "db2", Component: "pg", Severity: "FATAL",
				Message: "password authentication failed for user \"bob\"", TS: ts.Add(2 * time.Minute),
				Raw: "raw auth", File: "/var/log/pg/pg.log", LineNo: 7},
		}},
	}}
}

func TestErrorsGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.ErrorsColl = testErrColl()
	if code := Execute([]string{"errors", "--since", "1h"}, d); code != 0 {
		t.Fatalf("code %d stderr %s", code, errb)
	}
	goldenView(t, "errors.table.golden", out.String())
}

func TestErrorsSummaryGolden(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.ErrorsColl = testErrColl()
	if code := Execute([]string{"errors", "summary"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "errors_summary.table.golden", out.String())
}

func TestErrorsShowGolden(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	coll := testErrColl()
	d.ErrorsColl = coll
	ev, _ := coll.Events(context.Background(), errevents.Filter{Since: time.Hour})
	if len(ev) == 0 {
		t.Fatal("no events")
	}
	if code := Execute([]string{"errors", "show", ev[0].ID}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "errors_show.table.golden", out.String())
}

func TestLogsClassifyGolden(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.ErrorsColl = testErrColl()
	if code := Execute([]string{"logs", "--classify"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	goldenView(t, "logs_classify.table.golden", out.String())
}

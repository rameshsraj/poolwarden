package service

import (
	"context"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

func TestSingleStatement(t *testing.T) {
	cases := []struct {
		stmt string
		ok   bool
	}{
		{"select 1", true},
		{"select 1;", true},
		{"select 1;  ", true},
		{"select 1; -- trailing", false}, // comment after ; → rejected? no:
		{"select 1; select 2", false},
		{"select ';'", true},
		{"select 'a;b' from t", true},
		{"select 1 -- ; comment", true},
		{"select $$a;b$$", false}, // dollar-quoting not handled → rejected (documented limitation)
	}
	for _, c := range cases {
		if got := SingleStatement(c.stmt); got != c.ok {
			t.Errorf("%q → %v want %v", c.stmt, got, c.ok)
		}
	}
}

type fakeSQL struct {
	seq  []string
	cols []string
	rows [][]string
	err  error
}

func (f *fakeSQL) Exec(ctx context.Context, a string, p int, q string) error {
	f.seq = append(f.seq, q)
	return nil
}
func (f *fakeSQL) Query(ctx context.Context, a string, p int, q string) ([]string, [][]string, error) {
	f.seq = append(f.seq, q)
	return f.cols, f.rows, f.err
}

func TestSQLFlow(t *testing.T) {
	fsq := &fakeSQL{cols: []string{"n"}, rows: [][]string{{"1"}, {"2"}}}
	svc, _, _, f := mkSvc(t, modeltest.SampleSnapshot())
	svc.SQLRunner = fsq
	res, err := svc.SQL(context.Background(), "primary", "select 1", SQLOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Cols[0] != "n" {
		t.Fatalf("%+v", res)
	}
	want := []string{"BEGIN READ ONLY", "SET LOCAL statement_timeout = '30s'", "select 1", "ROLLBACK"}
	if len(fsq.seq) != 4 {
		t.Fatalf("%v", fsq.seq)
	}
	for i, w := range want {
		if fsq.seq[i] != w {
			t.Fatalf("step %d %q", i, fsq.seq[i])
		}
	}
	data, _ := f.ReadFile("/var/lib/poolwarden/audit.jsonl")
	if !strings.Contains(string(data), "sql primary") {
		t.Error("not audited")
	}
}

func TestSQLMultiReject(t *testing.T) {
	fsq := &fakeSQL{}
	svc, _, _, _ := mkSvc(t, modeltest.SampleSnapshot())
	svc.SQLRunner = fsq
	_, err := svc.SQL(context.Background(), "primary", "select 1; select 2", SQLOpts{})
	if err == nil || !strings.Contains(err.Error(), "one statement") {
		t.Fatalf("%v", err)
	}
	if len(fsq.seq) != 0 {
		t.Error("query ran")
	}
}

func TestSQLLimit(t *testing.T) {
	var rows [][]string
	for i := 0; i < 1500; i++ {
		rows = append(rows, []string{"x"})
	}
	fsq := &fakeSQL{cols: []string{"c"}, rows: rows}
	svc, _, _, _ := mkSvc(t, modeltest.SampleSnapshot())
	svc.SQLRunner = fsq
	res, err := svc.SQL(context.Background(), "primary", "select 1", SQLOpts{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1000 {
		t.Fatalf("%d", len(res.Rows))
	}
}

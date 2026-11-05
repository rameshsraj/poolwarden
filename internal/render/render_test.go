package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "update golden files")

var testLoc = time.FixedZone("UTC+2", 7200)

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch, got:\n%s", name, got)
	}
}

func newR(sb *strings.Builder, f Format, color bool) *Renderer {
	r := &Renderer{Out: sb, Format: f, Color: color, Loc: testLoc}
	return r
}

var testTable = Table{
	Columns: []string{"ID", "HOST", "STATUS", "DELAY"},
	Rows: [][]string{
		{"0", "db1:5432", "up", "-"},
		{"1", "db2:5432", "up", "2s"},
	},
	WideColumns: []string{"ID", "HOST", "STATUS", "DELAY", "LSN", "SCORE"},
	WideRows: [][]string{
		{"0", "db1:5432", "up", "-", "3/A1B2C3D4", "100"},
		{"1", "db2:5432", "up", "2s", "3/A1B20000", "95"},
	},
}

func TestTableGolden(t *testing.T) {
	var sb strings.Builder
	newR(&sb, FormatTable, false).WriteTable(testTable)
	golden(t, "table.golden", sb.String())
}

func TestWideGolden(t *testing.T) {
	var sb strings.Builder
	newR(&sb, FormatWide, false).WriteTable(testTable)
	golden(t, "wide.golden", sb.String())
}

func TestWideFallback(t *testing.T) {
	var sb strings.Builder
	newR(&sb, FormatWide, false).WriteTable(Table{Columns: testTable.Columns, Rows: testTable.Rows})
	golden(t, "table.golden", sb.String())
}

func TestJSONGolden(t *testing.T) {
	var sb strings.Builder
	r := newR(&sb, FormatJSON, false)
	err := r.JSON(map[string]any{
		"cluster": "prod-a",
		"at":      JSONTime(time.Date(2026, 11, 5, 10, 3, 1, 0, time.UTC)),
		"delay":   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "json.golden", sb.String())
}

func TestNagiosGolden(t *testing.T) {
	var sb strings.Builder
	if code := newR(&sb, FormatNagios, false).Nagios(11, "1 CRIT finding"); code != 2 {
		t.Fatalf("code %d", code)
	}
	golden(t, "nagios.golden", sb.String())
}

func TestHeaderGolden(t *testing.T) {
	h := HeaderInfo{
		Cluster: "prod-a", PgpoolVer: "4.5.x", Local: "db1", LocalRole: "LEADER",
		VIPHost:   "db1",
		Collected: time.Date(2026, 11, 5, 12, 3, 1, 0, testLoc),
		Duration:  410 * time.Millisecond,
	}
	var sb strings.Builder
	newR(&sb, FormatTable, false).Header(h)
	golden(t, "header.golden", sb.String())

	sb.Reset()
	h.Degraded = true
	newR(&sb, FormatTable, false).Header(h)
	golden(t, "header_degraded.golden", sb.String())

	sb.Reset()
	newR(&sb, FormatJSON, false).Header(h)
	if sb.String() != "" {
		t.Error("json mode must not print the header")
	}
}

func TestSeverityGolden(t *testing.T) {
	var sb strings.Builder
	plain := newR(&sb, FormatTable, false)
	for _, s := range []string{"CRIT", "WARN", "OK", "INFO"} {
		sb.WriteString(plain.Severity(s) + "\n")
	}
	golden(t, "severity_plain.golden", sb.String())

	sb.Reset()
	colored := newR(&sb, FormatTable, true)
	for _, s := range []string{"CRIT", "WARN", "OK", "INFO"} {
		sb.WriteString(colored.Severity(s) + "\n")
	}
	golden(t, "severity_color.golden", sb.String())
}

func TestFormats(t *testing.T) {
	for _, s := range []string{"table", "wide", "json", "nagios"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	if _, err := ParseFormat("bogus"); err == nil {
		t.Error("bogus should fail")
	}
}

func TestHelpers(t *testing.T) {
	r := newR(&strings.Builder{}, FormatTable, false)
	cases := []struct {
		got, want string
	}{
		{r.Bytes(512), "512B"},
		{r.Bytes(2048), "2.0KB"},
		{r.Bytes(5 << 20), "5.0MB"},
		{r.Bytes(3 << 30), "3.0GB"},
		{r.Bytes(2 << 40), "2.0TB"},
		{r.Duration(2 * time.Second), "2s"},
		{r.Duration(3*time.Minute + 12*time.Second), "3m12s"},
		{r.Duration(65 * time.Minute), "1h05m"},
		{r.Duration(-time.Second), "-"},
		{r.Time(time.Date(2026, 11, 5, 10, 3, 1, 0, time.UTC)), "2026-11-05 12:03:01 +0200"},
		{r.TimeShort(time.Date(2026, 11, 5, 10, 3, 1, 0, time.UTC)), "12:03:01"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
}

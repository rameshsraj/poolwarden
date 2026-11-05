// Package render turns typed results into table, wide, json or nagios output.
// Colour is used only on a TTY and can be forced off with --no-color.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

type Format string

const (
	FormatTable  Format = "table"
	FormatWide   Format = "wide"
	FormatJSON   Format = "json"
	FormatNagios Format = "nagios"
)

func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatTable, FormatWide, FormatJSON, FormatNagios:
		return Format(s), nil
	}
	return "", fmt.Errorf("unknown output format %q", s)
}

// HeaderInfo is the context shown on the one-line header of view commands.
type HeaderInfo struct {
	Cluster   string
	PgpoolVer string
	Local     string
	LocalRole string
	VIPHost   string
	Collected time.Time
	Duration  time.Duration
	Degraded  bool
}

// Table is a grid of strings; WideColumns/WideRows replace Columns/Rows in
// wide output when set.
type Table struct {
	Columns     []string
	Rows        [][]string
	WideColumns []string
	WideRows    [][]string
}

// Renderer writes all user-facing output for one invocation.
type Renderer struct {
	Out    io.Writer
	Format Format
	Color  bool
	Loc    *time.Location
}

// New builds a Renderer; Color is on only when out is a terminal and noColor
// is false.
func New(out io.Writer, format Format, noColor bool) *Renderer {
	return &Renderer{
		Out:    out,
		Format: format,
		Color:  !noColor && isTerminal(out),
		Loc:    time.Local,
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Header writes the context line, except in JSON mode.
func (r *Renderer) Header(h HeaderInfo) {
	if r.Format == FormatJSON {
		return
	}
	parts := []string{
		"cluster " + h.Cluster,
		"pgpool " + h.PgpoolVer,
	}
	if h.Local != "" && h.Local != "-" {
		if h.LocalRole != "" && h.LocalRole != "-" {
			parts = append(parts, fmt.Sprintf("local %s (%s)", h.Local, h.LocalRole))
		} else {
			parts = append(parts, "local "+h.Local)
		}
	}
	if h.VIPHost != "" && h.VIPHost != "-" {
		parts = append(parts, "VIP on "+h.VIPHost)
	}
	parts = append(parts,
		fmt.Sprintf("collected %s (%s)", r.TimeShort(h.Collected), h.Duration.Round(time.Millisecond)))
	if h.Degraded {
		parts = append(parts, "DEGRADED")
	}
	fmt.Fprintln(r.Out, strings.Join(parts, " · "))
}

// WriteTable renders t in the renderer's format (table or wide).
func (r *Renderer) WriteTable(t Table) {
	cols, rows := t.Columns, t.Rows
	if r.Format == FormatWide && len(t.WideColumns) > 0 {
		cols, rows = t.WideColumns, t.WideRows
	}
	w := tabwriter.NewWriter(r.Out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

// JSON writes v indented with 2 spaces.
func (r *Renderer) JSON(v any) error {
	enc := json.NewEncoder(r.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// KV writes aligned key/value pairs.
func (r *Renderer) KV(pairs [][2]string) {
	w := tabwriter.NewWriter(r.Out, 0, 2, 2, ' ', 0)
	for _, p := range pairs {
		fmt.Fprintf(w, "%s\t%s\n", p[0], p[1])
	}
	w.Flush()
}

var sevColors = map[string]string{
	"CRIT": "\033[31m",
	"WARN": "\033[33m",
	"OK":   "\033[32m",
	"INFO": "\033[36m",
}

// Severity returns the mark, coloured when Color is on.
func (r *Renderer) Severity(sev string) string {
	if !r.Color {
		return sev
	}
	if c, ok := sevColors[sev]; ok {
		return c + sev + "\033[0m"
	}
	return sev
}

// Bytes formats n in human units (1024 base, one decimal).
func (r *Renderer) Bytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k*k:
		return fmt.Sprintf("%.1fTB", float64(n)/(k*k*k*k))
	case n >= k*k*k:
		return fmt.Sprintf("%.1fGB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1fMB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1fKB", float64(n)/k)
	}
	return fmt.Sprintf("%dB", n)
}

// Duration formats d like "2s", "3m12s", "1h05m"; "-" for negative.
func (r *Renderer) Duration(d time.Duration) string {
	if d < 0 {
		return "-"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// Time formats t in local time with the numeric offset.
func (r *Renderer) Time(t time.Time) string {
	return t.In(r.Loc).Format("2006-01-02 15:04:05 -0700")
}

// TimeShort formats t as local HH:MM:SS.
func (r *Renderer) TimeShort(t time.Time) string {
	return t.In(r.Loc).Format("15:04:05")
}

// Nagios writes the single status line for --nagios runs.
func (r *Renderer) Nagios(code int, summary string) int {
	status, label := exitcode.Nagios(code)
	fmt.Fprintf(r.Out, "POOLWARDEN %s - %s\n", label, summary)
	return status
}

// JSONTime marshals as RFC 3339 with milliseconds in UTC (see model.JSONTime).
type JSONTime = model.JSONTime

var sparkChars = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders values as a width-capped ASCII/unicode sparkline;
// longer series are bucket-averaged down to width.
func Sparkline(vals []float64, width int) string {
	if len(vals) == 0 || width <= 0 {
		return ""
	}
	if len(vals) > width {
		var ds []float64
		for i := 0; i < width; i++ {
			lo := i * len(vals) / width
			hi := (i + 1) * len(vals) / width
			if hi <= lo {
				hi = lo + 1
			}
			var sum float64
			for _, v := range vals[lo:hi] {
				sum += v
			}
			ds = append(ds, sum/float64(hi-lo))
		}
		vals = ds
	}
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	out := make([]rune, len(vals))
	for i, v := range vals {
		idx := 0
		if max > min {
			idx = int((v - min) / (max - min) * float64(len(sparkChars)-1))
		}
		out[i] = sparkChars[idx]
	}
	return string(out)
}

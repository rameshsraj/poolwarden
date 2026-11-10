package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/render"
	"github.com/smarsh/poolwarden/internal/service"
)

// machineEvent is one NDJSON line on the monitor/progress streams.
type machineEvent struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	Seq  uint64          `json:"seq"`
	TS   model.JSONTime  `json:"ts"`
	Data json.RawMessage `json:"data"`
}

func writeEvent(w *json.Encoder, seq uint64, typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = w.Encode(machineEvent{
		V: 1, Type: typ, Seq: seq, TS: model.JSONTime(time.Now()),
		Data: raw,
	})
}

// monitorCmd streams a shared collect as NDJSON: one snapshot bundle per tick
// plus error-event and log tails. The console gateway runs ONE of these per
// cluster and fans the events out to browser SSE subscribers.
func monitorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:         "monitor",
		Short:       "stream cluster state as NDJSON events (for the console)",
		Annotations: map[string]string{"deadline": "none"},
		GroupID:     "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			iv := a.cfg.Interval
			if s, _ := cmd.Flags().GetString("interval"); s != "" {
				d, err := time.ParseDuration(s)
				if err != nil {
					return exitcode.New(exitcode.Usage, "bad --interval")
				}
				iv = render.ClampInterval(d, 1)
			}
			if iv <= 0 {
				iv = 2 * time.Second
			}
			return runMonitor(cmd, a, iv)
		},
	}
	c.Flags().String("interval", "", "collect interval, e.g. 2s")
	return c
}

func runMonitor(cmd *cobra.Command, a *app, iv time.Duration) error {
	svc := newService(a)
	out := json.NewEncoder(a.deps.Stdout)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var seq uint64
	writeEvent(out, seq, "hello", map[string]any{
		"cluster": a.cfg.Cluster, "version": a.deps.Version.Version,
		"interval_ms": iv.Milliseconds(),
	})
	flushOut(a.deps.Stdout)

	// follow tails: error events and classified log lines
	evCh := make(chan model.ErrorEvent, 64)
	logCh := make(chan logsrc.Line, 64)
	go func() { _ = svc.ErrorsFollow(ctx, errevents.Filter{}, evCh) }()
	go func() { _ = svc.LogsFollow(ctx, service.LogsOpts{}, logCh) }()

	var prev *service.MonitorBundle
	var prevAt time.Time
	tick := func() {
		seq++
		b, err := svc.Monitor(ctx)
		if err != nil {
			msg := map[string]any{"message": err.Error()}
			var ee *exitcode.Error
			if errors.As(err, &ee) {
				if ee.Cause != "" {
					msg["cause"] = ee.Cause
				}
				if ee.Hint != "" {
					msg["hint"] = ee.Hint
				}
			}
			writeEvent(out, seq, "collect_error", msg)
		} else {
			now := time.Now()
			b.Rates = monitorRates(prev, prevAt, b, now)
			prev, prevAt = b, now
			writeEvent(out, seq, "snapshot", b)
		}
		flushOut(a.deps.Stdout)
	}
	tick()
	tk := time.NewTicker(iv)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			writeEvent(out, seq, "bye", map[string]string{"reason": "shutdown"})
			flushOut(a.deps.Stdout)
			return nil
		case ev := <-evCh:
			writeEvent(out, seq, "error_event", ev)
			flushOut(a.deps.Stdout)
		case l := <-logCh:
			writeEvent(out, seq, "log", l)
			flushOut(a.deps.Stdout)
		case <-tk.C:
			tick()
		}
	}
}

// monitorRates derives per-second rates from two consecutive bundles: primary
// WAL generation (LSN delta), per-node select rate, and per-standby replay
// rate. Returns nil on the first tick or when the interval is degenerate.
func monitorRates(prev *service.MonitorBundle, prevAt time.Time,
	cur *service.MonitorBundle, now time.Time) *service.MonitorRates {
	if prev == nil || cur == nil {
		return nil
	}
	dt := now.Sub(prevAt).Seconds()
	if dt <= 0 {
		return nil
	}
	r := &service.MonitorRates{IntervalMS: int64(dt * 1000), WalWriteBps: -1,
		Nodes: map[int]float64{}, Standbys: map[string]float64{}}
	if cur.WAL != nil && prev.WAL != nil && cur.WAL.Primary == prev.WAL.Primary {
		if d := lsnDiff(cur.WAL.PrimaryLSN, prev.WAL.PrimaryLSN); d >= 0 {
			r.WalWriteBps = float64(d) / dt
		}
	}
	prevNodes := map[int]int64{}
	if prev.Nodes != nil {
		for _, n := range prev.Nodes.Nodes {
			prevNodes[n.ID] = n.Selects
		}
	}
	if cur.Nodes != nil {
		for _, n := range cur.Nodes.Nodes {
			if pv, ok := prevNodes[n.ID]; ok && n.Selects >= pv {
				r.Nodes[n.ID] = float64(n.Selects-pv) / dt
			}
		}
	}
	if cur.Lag != nil && prev.Lag != nil {
		prevSt := map[string]string{}
		for _, st := range prev.Lag.Standbys {
			prevSt[st.Standby] = st.ReplayLSN
		}
		for _, st := range cur.Lag.Standbys {
			if pv, ok := prevSt[st.Standby]; ok {
				if d := lsnDiff(st.ReplayLSN, pv); d >= 0 {
					r.Standbys[st.Standby] = float64(d) / dt
				}
			}
		}
	}
	return r
}

// lsnDiff returns (b - a) for pg_lsn values, or -1 when unparseable/negative
// (timeline or role switch between ticks).
func lsnDiff(cur, prev string) int64 {
	c, ok1 := parseLSN(cur)
	p, ok2 := parseLSN(prev)
	if !ok1 || !ok2 || c < p {
		return -1
	}
	return c - p
}

func parseLSN(s string) (int64, bool) {
	var hi, lo uint64
	// pg_lsn text form: "X/YYYYYY" (both hex)
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%x", &hi); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%x", &lo); err != nil {
		return 0, false
	}
	return int64(hi<<32 | lo), true
}

// probeCmd is the connection test behind the console setup wizard:
// per-endpoint results against the loaded (candidate or installed) config.
func probeCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "probe",
		Short:       "test every configured connection (pgpool SQL, PG, SSH, watchdog)",
		Annotations: map[string]string{"deadline": "90s"},
		GroupID:     "setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			svc := newService(a)
			res := svc.Probe(cmd.Context())
			r := render.New(a.deps.Stdout, a.format, a.noColor)
			if a.format == render.FormatJSON {
				if err := r.JSON(res); err != nil {
					return exitcode.New(exitcode.Usage, "cannot render JSON").Wrap(err)
				}
				return done(0)
			}
			t := render.Table{Columns: []string{"CHECK", "HOST", "RESULT", "DETAIL"}}
			for _, c := range res.Checks {
				t.Rows = append(t.Rows, []string{c.Name, orText(c.Host, "-"), c.Result, c.Detail})
			}
			r.WriteTable(t)
			return done(0)
		},
	}
}

// progressNDJSON wires service progress events to stderr as NDJSON so a
// supervising process (console gateway) can follow real action progress while
// stdout keeps the final JSON result.
func progressNDJSON(a *app, svc *service.Service) {
	if !a.progress {
		return
	}
	enc := json.NewEncoder(a.deps.Stderr)
	svc.Progress = func(ev service.ProgressEvent) {
		writeEvent(enc, 0, "progress", ev)
		if f, ok := a.deps.Stderr.(interface{ Flush() error }); ok {
			f.Flush()
		}
	}
}

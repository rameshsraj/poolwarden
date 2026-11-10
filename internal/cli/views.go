package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/render"
	"github.com/smarsh/poolwarden/internal/service"
)

// done signals "output already written; exit with this code".
func done(code int) error {
	return &exitcode.Error{Code: code}
}

// present renders one view result in the selected format and returns the
// findings-driven exit code.
func present(a *app, res any, h service.Header, findings []model.Finding,
	tableFn func(r *render.Renderer)) error {
	code := model.ExitCodeFor(findings)
	if a.nagios {
		r := render.New(a.deps.Stdout, render.FormatNagios, true)
		crit, warn := 0, 0
		first := ""
		for _, f := range findings {
			switch f.Severity {
			case model.SevCrit:
				crit++
				if first == "" {
					first = f.ID
				}
			case model.SevWarn:
				warn++
			}
		}
		summary := fmt.Sprintf("%d CRIT, %d WARN; %s", crit, warn, orText(first, "cluster ok"))
		return done(r.Nagios(code, summary))
	}
	r := render.New(a.deps.Stdout, a.format, a.noColor)
	if a.deps.Loc != nil {
		r.Loc = a.deps.Loc
	}
	if a.format == render.FormatJSON {
		if err := r.JSON(res); err != nil {
			return exitcode.New(exitcode.Usage, "cannot render JSON").Wrap(err)
		}
		return done(code)
	}
	if !a.quiet {
		r.Header(render.HeaderInfo{
			Cluster: h.Cluster, PgpoolVer: h.PgpoolVersion, Local: h.LocalHost,
			LocalRole: h.LocalWDRole, VIPHost: h.VIPHost,
			Collected: time.Time(h.Collected), Duration: time.Duration(h.DurationMS) * time.Millisecond,
			Degraded: h.Degraded,
		})
		fmt.Fprintln(a.deps.Stdout)
	}
	tableFn(r)
	return done(code)
}

// presentPlain renders without the cluster header (e.g. explain).
func presentPlain(a *app, res any, tableFn func(r *render.Renderer)) error {
	return presentSkipHeader(a, res, tableFn)
}

// auditReadCommand audits a read-only command pair when read_commands is on.
func auditReadCommand(ctx context.Context, a *app, exitCode int, err error) {
	if a.cfg == nil || !a.cfg.Audit.ReadCommands {
		return
	}
	svc := newService(a)
	if svc.Recorder == nil {
		return
	}
	outcome := "success"
	if err != nil || exitCode != 0 {
		outcome = "failed"
	}
	p, _ := svc.Recorder.Begin(ctx, audit.Action{Command: strings.Join(os.Args[1:], " ")})
	p.Finish(ctx, outcome, exitCode, err, nil, "")
}

func presentSkipHeader(a *app, res any, tableFn func(r *render.Renderer)) error {
	r := render.New(a.deps.Stdout, a.format, a.noColor)
	if a.deps.Loc != nil {
		r.Loc = a.deps.Loc
	}
	if a.format == render.FormatJSON {
		if err := r.JSON(res); err != nil {
			return exitcode.New(exitcode.Usage, "cannot render JSON").Wrap(err)
		}
		return done(0)
	}
	if a.nagios {
		return done(r.Nagios(0, "explain"))
	}
	tableFn(r)
	return done(0)
}

func orText(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func fmtSecs(r *render.Renderer, sec float64) string {
	if sec < 0 {
		return "-"
	}
	if sec < 1 {
		return fmt.Sprintf("%.1fs", sec)
	}
	return r.Duration(time.Duration(sec * float64(time.Second)))
}

func nodeRow(r *render.Renderer, n service.NodeRow, wide bool) []string {
	delay := "-"
	if n.RealRole == "standby" && n.DelaySec >= 0 {
		delay = fmtSecs(r, n.DelaySec)
	}
	row := []string{
		fmt.Sprint(n.ID), n.HostPort, n.Status, orText(n.PgpoolRole, "-"),
		n.RealRole, fmt.Sprintf("%.2f", n.LBWeight), fmt.Sprint(n.Selects),
		delay, n.ReplState, n.Sync,
		r.TimeShort(time.Time(n.LastChange)), fmt.Sprint(n.Score), orText(n.Note, ""),
	}
	if wide {
		row = append(row, orText(n.CurrentLSN, "-"), orText(n.ReceiveLSN, "-"),
			fmt.Sprint(n.Timeline), fmt.Sprintf("%d/%d", max0(n.Conns), max0(n.MaxConns)))
	}
	return row
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

var nodeCols = []string{"ID", "HOST:PORT", "STATUS", "PGPOOL-ROLE", "REAL-ROLE",
	"LB-WEIGHT", "SELECTS", "DELAY", "REPL-STATE", "SYNC", "LAST-CHANGE", "SCORE", "NOTE"}

var nodeColsWide = append(append([]string{}, nodeCols...), "LSN", "RECEIVE-LSN", "TIMELINE", "CONNS")

func renderNodes(r *render.Renderer, res *service.NodesResult) {
	t := render.Table{Columns: nodeCols, WideColumns: nodeColsWide}
	for _, n := range res.Nodes {
		t.Rows = append(t.Rows, nodeRow(r, n, false))
		t.WideRows = append(t.WideRows, nodeRow(r, n, true))
	}
	r.WriteTable(t)
}

func renderNodeDetail(r *render.Renderer, res *service.NodeDetail) {
	n := res.Node
	r.KV([][2]string{
		{"node", fmt.Sprintf("%d  %s  %s  %s", n.ID, n.HostPort, n.Status, n.RealRole)},
		{"pgpool role", orText(n.PgpoolRole, "-")},
		{"real role", n.RealRole},
		{"timeline", fmt.Sprint(n.Timeline)},
		{"current lsn", orText(n.CurrentLSN, "-")},
		{"receive lsn", orText(n.ReceiveLSN, "-")},
		{"delay", fmtSecs(r, n.DelaySec)},
		{"repl", fmt.Sprintf("%s %s", n.ReplState, n.Sync)},
		{"lb weight", fmt.Sprintf("%.2f", n.LBWeight)},
		{"selects", fmt.Sprint(n.Selects)},
		{"conns", fmt.Sprintf("%d/%d", max0(n.Conns), max0(n.MaxConns))},
		{"oldest txn", fmtSecs(r, n.OldestXactSec)},
		{"conflicts", fmt.Sprint(max64(n.Conflicts))},
		{"score", fmt.Sprint(n.Score)},
		{"note", orText(n.Note, "-")},
	})
	fs := findingsFor(res.Findings, n)
	if len(fs) > 0 {
		fmt.Fprintln(r.Out)
		for _, f := range fs {
			fmt.Fprintf(r.Out, "%s  %s  %s\n", r.Severity(f.Severity), f.ID, f.Cause)
		}
	}
}

func findingsFor(fs []model.Finding, n service.NodeRow) []model.Finding {
	var out []model.Finding
	for _, f := range fs {
		host := n.HostPort
		if i := strings.Index(host, ":"); i > 0 {
			host = host[:i]
		}
		if f.Subject == host || f.Subject == fmt.Sprintf("node%d", n.ID) || f.Subject == n.HostPort {
			out = append(out, f)
		}
	}
	return out
}

func max64(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

func renderLag(r *render.Renderer, res *service.LagResult) {
	fmt.Fprintf(r.Out, "primary %s  wal %s\n\n", res.Primary, res.PrimaryLSN)
	cols := []string{"STANDBY", "STATE", "SYNC", "LAG-BYTES", "WRITE", "FLUSH",
		"REPLAY", "DELAY", "TREND", "ETA→CRIT"}
	wide := append(append([]string{}, cols...), "SENT-LSN", "REPLAY-LSN")
	t := render.Table{Columns: cols, WideColumns: wide}
	for _, s := range res.Standbys {
		lagB := "-"
		if s.LagBytes >= 0 {
			lagB = r.Bytes(s.LagBytes)
		}
		eta := "-"
		if s.ETASec >= 0 {
			eta = fmtSecs(r, s.ETASec)
		}
		delay := fmtSecs(r, s.DelaySec)
		if s.DelaySec >= 0 && s.DelaySeverity != model.SevInfo {
			delay = r.Severity(s.DelaySeverity[:1] + " " + delay)
		}
		row := []string{s.Standby, s.State, s.Sync, lagB,
			fmtSecs(r, s.WriteLagSec), fmtSecs(r, s.FlushLagSec),
			fmtSecs(r, s.ReplayLagSec), delay, s.Trend, eta}
		t.Rows = append(t.Rows, row)
		t.WideRows = append(t.WideRows, append(append([]string{}, row...),
			orText(s.SentLSN, "-"), orText(s.ReplayLSN, "-")))
	}
	r.WriteTable(t)
}

func renderSlots(r *render.Renderer, res *service.SlotsResult) {
	t := render.Table{Columns: []string{"SLOT", "TYPE", "ACTIVE", "WAL-STATUS", "RETAINED", "ETA-DISK-FULL"}}
	for _, s := range res.Slots {
		eta := "-"
		if s.ETASec >= 0 {
			eta = fmtSecs(r, s.ETASec)
		}
		active := "no"
		if s.Active {
			active = "yes"
		}
		t.Rows = append(t.Rows, []string{s.Name, s.Type, active, s.WALStatus,
			r.Bytes(s.RetainedBytes), eta})
	}
	r.WriteTable(t)
}

func renderPools(r *render.Renderer, res *service.PoolsResult) {
	fmt.Fprintf(r.Out, "children %d/%d busy · clients %d\n\n",
		res.ChildrenBusy, res.ChildrenMax, res.Clients)
	t := render.Table{Columns: []string{"DATABASE", "USER", "BACKENDS", "POOLS", "CONNECTED", "LOAD-BALANCE-NODES"}}
	for _, g := range res.Groups {
		t.Rows = append(t.Rows, []string{g.Database, g.User, fmt.Sprint(g.Backends),
			fmt.Sprint(g.Pools), fmt.Sprint(g.Connected), fmt.Sprint(g.LoadBalanceNodes)})
	}
	r.WriteTable(t)
}

func renderProcs(r *render.Renderer, res *service.ProcsResult) {
	t := render.Table{Columns: []string{"PID", "START", "CLIENT-CONNS", "DATABASE",
		"USER", "BACKEND-CONN-TIME", "POOL-COUNTER", "STATUS"}}
	for _, p := range res.Procs {
		t.Rows = append(t.Rows, []string{fmt.Sprint(p.PID), p.StartTime,
			fmt.Sprint(p.ClientConns), p.Database, p.User, p.BackendConnTime,
			fmt.Sprint(p.PoolCounter), p.Status})
	}
	r.WriteTable(t)
}

func renderProcDetail(r *render.Renderer, res *service.ProcDetailResult) {
	t := render.Table{Columns: []string{"DATABASE", "USER", "BACKEND-ID", "BACKEND-PID",
		"CONNECTED", "STATUS", "CLIENT-IDLE", "LB-NODE"}}
	for _, p := range res.Rows {
		t.Rows = append(t.Rows, []string{p.Database, p.Username, fmt.Sprint(p.BackendID),
			fmt.Sprint(p.BackendPID), fmt.Sprint(p.Connected), p.Status,
			p.ClientIdleDuration, fmt.Sprint(p.LoadBalanceNode)})
	}
	r.WriteTable(t)
}

func renderConns(r *render.Renderer, res *service.ConnsResult) {
	t := render.Table{Columns: []string{"WHERE", "DATABASE", "USER", "APPLICATION", "STATE", "CONNS", "LIMIT"}}
	rows := append([]service.ConnRow{}, res.Conns...)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Where+rows[i].Database+rows[i].State < rows[j].Where+rows[j].Database+rows[j].State
	})
	for _, c := range rows {
		t.Rows = append(t.Rows, []string{c.Where, c.Database, c.User,
			c.Application, c.State, fmt.Sprint(c.Conns), fmt.Sprint(c.Limit)})
	}
	r.WriteTable(t)
}

func renderWatchdog(r *render.Renderer, res *service.WatchdogResult) {
	if res.VIPBanner != "" {
		fmt.Fprintln(r.Out, r.Severity("CRIT")+" "+res.VIPBanner)
		fmt.Fprintln(r.Out)
	}
	if !res.Available {
		fmt.Fprintln(r.Out, "watchdog: unavailable")
		return
	}
	fmt.Fprintf(r.Out, "quorum: %s · leader %s · %d/%d alive\n\n", res.Quorum, res.Leader, res.Alive, res.Total)
	r.KV([][2]string{
		{"enable_consensus_with_half_votes", res.Params["enable_consensus_with_half_votes"]},
		{"failover_when_quorum_exists", res.Params["failover_when_quorum_exists"]},
		{"failover_require_consensus", res.Params["failover_require_consensus"]},
		{"use_watchdog", res.Params["use_watchdog"]},
		{"delegate_ip", res.Params["delegate_ip"]},
	})
	fmt.Fprintln(r.Out)
	t := render.Table{Columns: []string{"NODE", "HOST", "STATUS", "PRIORITY", "VIP-PRESENT"}}
	for _, n := range res.Nodes {
		vip := "no"
		if n.VIPPresent {
			vip = "yes"
		}
		t.Rows = append(t.Rows, []string{fmt.Sprint(n.Node), n.Host, n.Status,
			fmt.Sprint(n.Priority), vip})
	}
	r.WriteTable(t)
}

func renderHealth(r *render.Renderer, res *service.HealthResult) {
	t := render.Table{Columns: []string{"NODE", "HOST", "STATUS", "TOTAL", "SUCCESS",
		"FAIL", "SKIP", "RETRY", "AVG", "MAX", "LAST-FAIL", "DELTA"}}
	for _, h := range res.Nodes {
		delta := "-"
		if h.DeltaFail != nil {
			delta = fmt.Sprintf("%+d", *h.DeltaFail)
		}
		t.Rows = append(t.Rows, []string{fmt.Sprint(h.Node), h.Host, h.Status,
			fmt.Sprint(h.Total), fmt.Sprint(h.Success), fmt.Sprint(h.Fail),
			fmt.Sprint(h.Skip), fmt.Sprint(h.Retry), fmt.Sprintf("%.1f", h.AvgMS),
			fmt.Sprint(h.MaxMS), h.LastFail, delta})
	}
	r.WriteTable(t)
}

func renderTopology(r *render.Renderer, res *service.TopologyResult) {
	t := render.Table{Columns: []string{"HOST", "ADDRESS", "ROLES", "SSH-USER",
		"WATCHDOG", "VIP", "BACKEND-ROLE"}}
	for _, h := range res.Hosts {
		vip := "no"
		if h.VIP {
			vip = "yes"
		}
		t.Rows = append(t.Rows, []string{h.Host, h.Address, strings.Join(h.Roles, ","),
			h.SSHUser, h.Watchdog, vip, h.BackendRole})
	}
	r.WriteTable(t)
}

func renderStatus(r *render.Renderer, res *service.StatusResult) {
	const pad = 8
	first := true
	for _, n := range res.Nodes {
		label := strings.Repeat(" ", pad)
		if first {
			label = "NODES" + strings.Repeat(" ", pad-len("NODES"))
			first = false
		}
		line := label + fmt.Sprintf("%d %s %s  %s", n.ID, n.HostPort, n.RealRole, n.Status)
		if n.RealRole == "standby" && n.DelaySec >= 0 {
			line += "   delay " + fmtSecs(r, n.DelaySec)
		}
		line += fmt.Sprintf("   score %d", n.Score)
		if n.Note != "" {
			line += "   " + n.Note
		}
		fmt.Fprintln(r.Out, line)
	}
	if first {
		fmt.Fprintln(r.Out, "NODES"+strings.Repeat(" ", pad-len("NODES"))+"-")
	}
	wd := "unavailable"
	if res.WatchdogOK {
		wd = fmt.Sprintf("quorum: %s · leader %s · %d/%d alive", res.Quorum, res.Leader, res.Alive, res.Total)
		if res.HalfVotes {
			wd += " · half-votes ON"
			if res.Total == 2 {
				wd += " (2-node split risk: see WD_HALF_VOTES_2NODE)"
			}
		}
	}
	fmt.Fprintf(r.Out, "WATCHDOG %s\n", wd)
	fmt.Fprintf(r.Out, "POOLS   children %d/%d busy · clients %d\n",
		res.ChildrenBusy, res.ChildrenMax, res.Clients)
	fmt.Fprintf(r.Out, "FINDINGS %d CRIT · %d WARN · %d INFO        → poolwarden diagnose\n",
		res.CountCrit, res.CountWarn, res.CountInfo)
}

func renderForecast(r *render.Renderer, res *service.ForecastResult) {
	if res.Insufficient != nil {
		fmt.Fprintf(r.Out, "insufficient history (have %s, need %s)\n",
			insufDur(res.Insufficient.Have), insufDur(res.Insufficient.Need))
		return
	}
	t := render.Table{Columns: []string{"METRIC", "SUBJECT", "NOW", "SLOPE", "R²", "ETA", "THRESHOLD"}}
	for _, f := range res.Rows {
		slope := "-"
		if f.Slope != 0 {
			slope = fmt.Sprintf("%+.1f%s/min", f.Slope*60, f.Unit)
		}
		eta := "-"
		if f.ETASec >= 0 {
			eta = fmtSecs(r, f.ETASec)
		}
		th := "-"
		if f.Threshold >= 0 {
			th = fmt.Sprintf("%.0f%s", f.Threshold, f.Unit)
		}
		now := fmt.Sprintf("%.1f%s", f.Now, f.Unit)
		t.Rows = append(t.Rows, []string{f.Metric, f.Subject, now, slope,
			fmt.Sprintf("%.2f", f.R2), eta, th})
	}
	r.WriteTable(t)
}

func insufDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func renderLagSparklines(r *render.Renderer, res *service.LagResult) {
	if len(res.Series) == 0 {
		return
	}
	var names []string
	for k := range res.Series {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintln(r.Out)
	for _, n := range names {
		vals := res.Series[n]
		mn, mx := -1.0, -1.0
		var usable []float64
		for _, v := range vals {
			if v >= 0 {
				usable = append(usable, v)
				if mn < 0 || v < mn {
					mn = v
				}
				if v > mx {
					mx = v
				}
			}
		}
		if len(usable) == 0 {
			continue
		}
		fmt.Fprintf(r.Out, "%s  %s  min %s  max %s\n", n,
			render.Sparkline(usable, 60), fmtSecs(r, mn), fmtSecs(r, mx))
	}
}

func renderDiagnose(r *render.Renderer, res *service.DiagnoseResult) {
	t := render.Table{Columns: []string{"SEV", "ID", "SUBJECT", "SUMMARY"}}
	for _, d := range res.Rows {
		t.Rows = append(t.Rows, []string{r.Severity(d.Severity), d.ID, d.Subject, d.Summary})
	}
	r.WriteTable(t)
	if res.Next != "" {
		fmt.Fprintf(r.Out, "→ %s\n", res.Next)
	}
}

func renderExplain(r *render.Renderer, res *diagnose.ExplainResult) {
	fmt.Fprintf(r.Out, "What: %s\n\n", res.What)
	if !res.Firing {
		fmt.Fprintln(r.Out, "(not currently firing)")
		fmt.Fprintln(r.Out)
	}
	if len(res.Evidence) > 0 {
		fmt.Fprintln(r.Out, "Evidence:")
		for _, e := range res.Evidence {
			fmt.Fprintf(r.Out, "  %s\n", e)
		}
		fmt.Fprintln(r.Out)
	}
	if len(res.LikelyCauses) > 0 {
		fmt.Fprintln(r.Out, "Likely cause:")
		for i, c := range res.LikelyCauses {
			fmt.Fprintf(r.Out, "  %d. %s\n", i+1, c)
		}
		fmt.Fprintln(r.Out)
	}
	if len(res.RelatedErrors) > 0 {
		fmt.Fprintln(r.Out, "Related errors:")
		for _, e := range res.RelatedErrors {
			fmt.Fprintf(r.Out, "  %s %s %s\n", r.Time(e.TS), e.Severity, e.Message)
		}
		fmt.Fprintln(r.Out)
	}
	if len(res.Fix) > 0 {
		fmt.Fprintln(r.Out, "Fix:")
		for _, f := range res.Fix {
			fmt.Fprintf(r.Out, "  %s\n", f)
		}
		fmt.Fprintln(r.Out)
	}
	if res.Reference != "" {
		fmt.Fprintf(r.Out, "Reference: %s\n", res.Reference)
	}
}

func renderErrors(r *render.Renderer, res *service.ErrorsResult) {
	t := render.Table{Columns: []string{"TIME", "SEV", "SOURCE", "HOST", "CODE", "MESSAGE"}}
	for _, e := range res.Events {
		msg := e.Message
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		t.Rows = append(t.Rows, []string{r.TimeShort(e.TS), e.Severity, e.Source, e.Host, e.Code, msg})
	}
	r.WriteTable(t)
}

func renderLogs(r *render.Renderer, res *service.LogLinesResult, classify bool) {
	cols := []string{"TIME", "HOST", "COMP", "SEV", "MESSAGE"}
	if classify {
		cols = []string{"TIME", "HOST", "COMP", "SEV", "TAG", "MESSAGE"}
	}
	t := render.Table{Columns: cols}
	for _, l := range res.Lines {
		row := []string{r.TimeShort(l.TS), l.Host, l.Component, l.Severity}
		if classify {
			row = append(row, logsrc.Classify(l))
		}
		msg := l.Message
		if len(msg) > 90 {
			msg = msg[:87] + "..."
		}
		row = append(row, msg)
		t.Rows = append(t.Rows, row)
	}
	r.WriteTable(t)
}

func renderAuditShow(r *render.Renderer, res *service.AuditShowResult) {
	fin := res.Finished
	req := res.Requested
	if fin == nil && req != nil {
		fin = req
	}
	if fin == nil {
		return
	}
	fmt.Fprintf(r.Out, "command %s  operator %s  cluster %s\n",
		fin.Command, fin.Operator, fin.Cluster)
	auditTSLine(r, "requested", fin.RequestedAt, nil)
	auditTSLine(r, "confirmed", fin.ConfirmedAt, fin.RequestedAt)
	auditTSLine(r, "started", fin.StartedAt, fin.ConfirmedAt)
	for _, st := range fin.Steps {
		fmt.Fprintf(r.Out, "  %s  ssh %s  connect %dms  %s %s-%s\n",
			st.Host, st.SSHUser, st.ConnectMS, st.Action,
			r.TimeShort(st.Start), r.TimeShort(st.End))
	}
	if fin.FinishedAt != nil {
		fmt.Fprintf(r.Out, "finished %s  duration %s  outcome %s\n",
			r.TimeShort(time.Time(*fin.FinishedAt)),
			fmtSecs(r, float64(fin.DurationMS)/1000), fin.Outcome)
	}
	if fin.Error != "" {
		fmt.Fprintf(r.Out, "error: %s\n", fin.Error)
	}
}

func auditTSLine(r *render.Renderer, label string, t, from *model.JSONTime) {
	if t == nil {
		return
	}
	line := fmt.Sprintf("%s %s", label, r.TimeShort(time.Time(*t)))
	if from != nil {
		d := time.Time(*t).Sub(time.Time(*from))
		line += fmt.Sprintf(" (+%s)", fmtSecs(r, d.Seconds()))
	}
	fmt.Fprintln(r.Out, line)
}

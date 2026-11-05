package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/render"
	"github.com/smarsh/poolwarden/internal/service"
)

func appOf(cmd *cobra.Command) *app { return appFrom(cmd) }

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "one-screen cluster summary", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Status(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderStatus(r, res)
			})
		},
	}
}

func nodesCmd() *cobra.Command {
	return &cobra.Command{
		Use: "nodes [id]", Short: "backend nodes: pgpool view vs real role", GroupID: "monitoring",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			svc := newService(a)
			if len(args) == 0 {
				res, err := svc.Nodes(cmd.Context())
				if err != nil {
					return err
				}
				return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
					renderNodes(r, res)
				})
			}
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return exitcode.New(exitcode.Usage, "node id must be a number").Wrap(err)
			}
			res, err := svc.Node(cmd.Context(), id)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderNodeDetail(r, res)
			})
		},
	}
}

func lagCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "lag", Short: "replication lag per standby", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Lag(cmd.Context())
			if err != nil {
				return err
			}
			showHist, _ := cmd.Flags().GetString("history")
			if showHist != "" {
				d, err := time.ParseDuration(showHist)
				if err != nil || d <= 0 {
					return exitcode.New(exitcode.Usage, "invalid --history value "+strconv.Quote(showHist)).
						WithHint("use a duration like 30m, 1h or 6h")
				}
				if d > 24*time.Hour {
					d = 24 * time.Hour
				}
				res.Series = newService(a).DelaySeries(cmd.Context(), d)
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderLag(r, res)
				if showHist != "" {
					renderLagSparklines(r, res)
				}
			})
		},
	}
	c.Flags().String("history", "", "show delay history, e.g. 1h")
	return c
}

func slotsCmd() *cobra.Command {
	return &cobra.Command{Use: "slots", Short: "replication slots and retained WAL", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Slots(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderSlots(r, res)
			})
		}}
}

func poolsCmd() *cobra.Command {
	return &cobra.Command{Use: "pools", Short: "connection pools by database and user", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Pools(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderPools(r, res)
			})
		}}
}

func procsCmd() *cobra.Command {
	return &cobra.Command{Use: "procs [pid]", Short: "pgpool child processes", GroupID: "monitoring",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			svc := newService(a)
			if len(args) == 0 {
				res, err := svc.Procs(cmd.Context())
				if err != nil {
					return err
				}
				return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
					renderProcs(r, res)
				})
			}
			pid, err := strconv.Atoi(args[0])
			if err != nil {
				return exitcode.New(exitcode.Usage, "pid must be a number").Wrap(err)
			}
			res, err := svc.Proc(cmd.Context(), pid)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderProcDetail(r, res)
			})
		}}
}

func connsCmd() *cobra.Command {
	return &cobra.Command{Use: "conns", Short: "client connections by db, user and application", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Conns(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderConns(r, res)
			})
		}}
}

func watchdogCmd() *cobra.Command {
	return &cobra.Command{Use: "watchdog", Short: "watchdog leader, quorum and VIP", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Watchdog(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderWatchdog(r, res)
			})
		}}
}

func healthCmd() *cobra.Command {
	return &cobra.Command{Use: "health", Short: "health-check and backend stats", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Health(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderHealth(r, res)
			})
		}}
}

func topologyShowCmd() *cobra.Command {
	return &cobra.Command{Use: "show", Short: "show hosts and their roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).TopologyShow(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderTopology(r, res)
			})
		}}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "print poolwarden version and build info", GroupID: "setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			bi := a.deps.Version
			fmt.Fprintf(a.deps.Stdout, "poolwarden %s (%s, %s) %s %s/%s\n",
				bi.Version, bi.Commit, bi.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			cfg, err := config.LoadFor(a.deps.FS, a.configPath, a.deps.UID)
			if err != nil {
				return nil // best effort only
			}
			a.cfg = cfg
			res, err := newService(a).Version(cmd.Context())
			if err != nil || res == nil {
				return nil
			}
			fmt.Fprintf(a.deps.Stdout, "pgpool %s\n", res.Pgpool)
			var names []string
			for n := range res.Backends {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintf(a.deps.Stdout, "pg %s: %s\n", n, res.Backends[n])
			}
			return nil
		},
	}
}

func forecastCmd() *cobra.Command {
	return &cobra.Command{Use: "forecast", Short: "lag, WAL disk and connection projections", GroupID: "issues",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			res, err := newService(a).Forecast(cmd.Context())
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderForecast(r, res)
			})
		}}
}

func diagnoseCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "diagnose", Short: "run the diagnosis rules", GroupID: "issues",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			sev, _ := cmd.Flags().GetString("severity")
			rule, _ := cmd.Flags().GetString("rule")
			res, err := newService(a).Diagnose(cmd.Context(), sev, rule)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, res.Findings, func(r *render.Renderer) {
				renderDiagnose(r, res)
			})
		},
	}
	c.Flags().String("severity", "", "minimum severity: info|warn|crit")
	c.Flags().String("rule", "", "run only this rule id")
	return c
}

func explainCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "explain <finding-id>",
		Short:             "explain a finding and how to fix it",
		GroupID:           "issues",
		ValidArgsFunction: findingIDs(nil),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			res, err := newService(a).Explain(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return presentPlain(a, res, func(r *render.Renderer) {
				renderExplain(r, res)
			})
		},
	}
}

func parseSince(cmd *cobra.Command, def time.Duration) (time.Duration, error) {
	s, _ := cmd.Flags().GetString("since")
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, exitcode.New(exitcode.Usage, "invalid --since "+strconv.Quote(s)).
			WithHint("use a duration like 30m, 1h or 24h")
	}
	return d, nil
}

func errorsFilter(cmd *cobra.Command, def time.Duration) (errevents.Filter, error) {
	var f errevents.Filter
	d, err := parseSince(cmd, def)
	if err != nil {
		return f, err
	}
	f.Since = d
	src, _ := cmd.Flags().GetString("source")
	if src != "" {
		f.Sources = map[string]bool{}
		for _, s := range strings.Split(src, ",") {
			f.Sources[strings.TrimSpace(s)] = true
		}
	}
	f.Host, _ = cmd.Flags().GetString("host")
	f.MinSeverity, _ = cmd.Flags().GetString("severity")
	if n, _ := cmd.Flags().GetString("node"); n != "" && cmd.Flags().Changed("node") {
		if v, err := strconv.Atoi(n); err == nil {
			f.Node = &v
		} else {
			return f, exitcode.New(exitcode.Usage, "invalid --node "+strconv.Quote(n))
		}
	}
	if g, _ := cmd.Flags().GetString("grep"); g != "" {
		re, err := regexp.Compile(g)
		if err != nil {
			return f, exitcode.New(exitcode.Usage, "invalid --grep").Wrap(err)
		}
		f.Grep = re
	}
	return f, nil
}

func errorsCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "errors", Short: "unified error view across all sources", GroupID: "issues",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			f, err := errorsFilter(cmd, time.Hour)
			if err != nil {
				return err
			}
			svc := newService(a)
			if fl, _ := cmd.Flags().GetBool("follow"); fl {
				return followErrors(cmd, svc, f)
			}
			res, err := svc.Errors(cmd.Context(), f)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, nil, func(r *render.Renderer) {
				renderErrors(r, res)
			})
		},
	}
	c.Flags().String("since", "", "look back this far (default 1h)")
	c.Flags().String("source", "", "pgpool|pg|conn|health|replication|pcp|db|tool (comma list)")
	c.Flags().String("host", "", "limit to one host")
	c.Flags().String("severity", "", "minimum severity")
	c.Flags().String("node", "", "limit to one node id")
	c.Flags().String("grep", "", "filter by regular expression")
	c.Flags().BoolP("follow", "f", false, "follow live")
	return c
}

func errorsSummaryCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "summary", Short: "aggregate error counts by source",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			f, err := errorsFilter(cmd, 24*time.Hour)
			if err != nil {
				return err
			}
			res, err := newService(a).ErrorsSummary(cmd.Context(), f)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, nil, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"SOURCE", "CODE", "COUNT", "FIRST", "LAST"}}
				for _, row := range res.Rows {
					t.Rows = append(t.Rows, []string{row.Source, row.Code,
						fmt.Sprint(row.Count), r.TimeShort(row.First), r.TimeShort(row.Last)})
				}
				r.WriteTable(t)
			})
		},
	}
	c.Flags().String("since", "", "look back this far (default 24h)")
	c.Flags().String("source", "", "")
	c.Flags().String("host", "", "")
	c.Flags().String("severity", "", "")
	c.Flags().String("grep", "", "")
	return c
}

func errorsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <id>", Short: "show one event with context", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			res, err := newService(a).ErrorsShow(cmd.Context(), args[0], 24*time.Hour)
			if err != nil {
				return err
			}
			return present(a, res, res.Header, nil, func(r *render.Renderer) {
				e := res.Event
				fmt.Fprintf(r.Out, "%s  %s %s %s %s\n", r.TimeShort(e.TS), e.Severity, e.Source, e.Host, e.Code)
				fmt.Fprintf(r.Out, "  %s\n\n", e.Message)
				if e.Ref != "" {
					fmt.Fprintf(r.Out, "Ref: %s\n\n", e.Ref)
				}
				if len(res.Context) > 0 {
					fmt.Fprintln(r.Out, "Context:")
					for _, c := range res.Context {
						fmt.Fprintf(r.Out, "  %s\n", c)
					}
				}
				if res.Explain != nil {
					fmt.Fprintln(r.Out)
					renderExplain(r, res.Explain)
				}
			})
		},
	}
}

func logsCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "logs", Short: "tail and classify pgpool/PG logs", GroupID: "issues",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			svc := newService(a)
			var o service.LogsOpts
			d, err := parseSince(cmd, time.Hour)
			if err != nil {
				return err
			}
			o.Since = d
			o.Host, _ = cmd.Flags().GetString("host")
			o.Component, _ = cmd.Flags().GetString("component")
			if g, _ := cmd.Flags().GetString("grep"); g != "" {
				re, err := regexp.Compile(g)
				if err != nil {
					return exitcode.New(exitcode.Usage, "invalid --grep").Wrap(err)
				}
				o.Grep = re
			}
			classify, _ := cmd.Flags().GetBool("classify")
			if fl, _ := cmd.Flags().GetBool("follow"); fl {
				return followLogs(cmd, svc, o, classify)
			}
			res, err := svc.Logs(cmd.Context(), o)
			if err != nil {
				return err
			}
			return present(a, res, service.Header{}, nil, func(r *render.Renderer) {
				renderLogs(r, res, classify)
			})
		},
	}
	c.Flags().String("host", "", "limit to one host")
	c.Flags().String("component", "", "pgpool|pg")
	c.Flags().String("since", "", "look back this far (default 1h)")
	c.Flags().String("grep", "", "filter by regular expression")
	c.Flags().Bool("classify", false, "add a classification tag column")
	c.Flags().BoolP("follow", "f", false, "follow live")
	return c
}

func followErrors(cmd *cobra.Command, svc *service.Service, f errevents.Filter) error {
	a := appOf(cmd)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := make(chan model.ErrorEvent, 64)
	errch := make(chan error, 1)
	go func() { errch <- svc.ErrorsFollow(ctx, f, out) }()
	r := render.New(a.deps.Stdout, a.format, a.noColor)
	for {
		select {
		case err := <-errch:
			if err == context.Canceled {
				return done(0)
			}
			return err
		case e := <-out:
			if a.format == render.FormatJSON {
				if err := r.JSON(e); err != nil {
					return err
				}
				continue
			}
			fmt.Fprintf(r.Out, "%s  %s %s %s %s  %s\n",
				r.TimeShort(e.TS), e.Severity, e.Source, e.Host, e.Code, e.Message)
		}
	}
}

func followLogs(cmd *cobra.Command, svc *service.Service, o service.LogsOpts, classify bool) error {
	a := appOf(cmd)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := make(chan logsrc.Line, 64)
	errch := make(chan error, 1)
	go func() { errch <- svc.LogsFollow(ctx, o, out) }()
	r := render.New(a.deps.Stdout, a.format, a.noColor)
	for {
		select {
		case err := <-errch:
			if err == context.Canceled {
				return done(0)
			}
			return err
		case l := <-out:
			if a.format == render.FormatJSON {
				r.JSON(l)
				continue
			}
			tag := ""
			if classify {
				tag = " " + logsrc.Classify(l)
			}
			fmt.Fprintf(r.Out, "%s %s %s %s%s  %s\n",
				r.TimeShort(l.TS), l.Host, l.Component, l.Severity, tag, l.Message)
		}
	}
}

func auditListCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "list", Short: "recent audit entries",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			f := audit.Filter{}
			d, err := parseSince(cmd, 24*time.Hour)
			if err != nil {
				return err
			}
			f.Since = d
			f.Operator, _ = cmd.Flags().GetString("operator")
			f.Action, _ = cmd.Flags().GetString("action")
			f.Ticket, _ = cmd.Flags().GetString("ticket")
			f.Outcome, _ = cmd.Flags().GetString("outcome")
			res, err := newService(a).AuditList(cmd.Context(), f)
			if err != nil {
				return err
			}
			return present(a, res, service.Header{}, nil, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"TIME", "OPERATOR", "ACTION", "TARGET", "TICKET", "OUTCOME", "DURATION"}}
				for _, row := range res.Rows {
					cmd2 := row.Command
					t.Rows = append(t.Rows, []string{r.TimeShort(row.TS), row.Operator, cmd2,
						orText(row.Target, "-"), orText(row.Ticket, "-"), row.Outcome,
						fmtSecs(r, float64(row.DurationMS)/1000)})
				}
				r.WriteTable(t)
			})
		},
	}
	c.Flags().String("since", "", "look back this far (default 24h)")
	c.Flags().String("operator", "", "")
	c.Flags().String("action", "", "")
	c.Flags().String("ticket", "", "")
	c.Flags().String("outcome", "", "")
	return c
}

func auditShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <event-id>", Short: "full entry with timing breakdown", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			res, err := newService(a).AuditShow(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return presentPlain(a, res, func(r *render.Renderer) {
				renderAuditShow(r, res)
			})
		},
	}
}

func auditVerifyCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "verify", Short: "recompute the hash chain",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			d, err := parseSince(cmd, 24*time.Hour)
			if err != nil {
				return err
			}
			rep, err := newService(a).AuditVerify(cmd.Context(), d)
			if err != nil {
				return err
			}
			if a.nagios {
				r := render.New(a.deps.Stdout, render.FormatNagios, true)
				if len(rep.Problems) > 0 {
					return done(r.Nagios(2, fmt.Sprintf("%d audit problems", len(rep.Problems))))
				}
				return done(r.Nagios(0, fmt.Sprintf("audit chain OK (%d entries)", rep.Entries)))
			}
			_ = present(a, rep, service.Header{}, nil, func(r *render.Renderer) {
				if len(rep.Problems) == 0 {
					fmt.Fprintf(r.Out, "OK: %d entries\n", rep.Entries)
					return
				}
				t := render.Table{Columns: []string{"FILE", "LINE", "KIND", "DETAIL"}}
				for _, p := range rep.Problems {
					t.Rows = append(t.Rows, []string{p.File, fmt.Sprint(p.Line), p.Kind, p.Detail})
				}
				r.WriteTable(t)
			})
			if len(rep.Problems) > 0 {
				return done(11)
			}
			return done(0)
		},
	}
	c.Flags().String("since", "", "look back this far (default 24h)")
	return c
}

func auditSessionsCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "sessions", Short: "shell sessions grouped by session id",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			d, err := parseSince(cmd, 24*time.Hour)
			if err != nil {
				return err
			}
			rows, err := newService(a).AuditSessions(cmd.Context(), d)
			if err != nil {
				return err
			}
			return present(a, rows, service.Header{}, nil, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"START", "END", "OPERATOR", "FROM", "KEY-FP", "COMMANDS"}}
				for _, s := range rows {
					t.Rows = append(t.Rows, []string{r.TimeShort(s.Start), r.TimeShort(s.End),
						s.Operator, orText(s.From, "-"), orText(s.KeyFP, "-"), fmt.Sprint(s.Commands)})
				}
				r.WriteTable(t)
			})
		},
	}
	c.Flags().String("since", "", "")
	return c
}

func controlOpts(cmd *cobra.Command, a *app) service.ControlOpts {
	conf := newConfirmer(a)
	tty := false
	if tc, ok := conf.(*ttyConfirmer); ok {
		tty = tc.isTTY
	}
	yes, _ := cmd.Flags().GetBool("yes")
	confirmTok, _ := cmd.Flags().GetString("confirm")
	force, _ := cmd.Flags().GetBool("force")
	ticket, _ := cmd.Flags().GetString("ticket")
	return service.ControlOpts{
		Yes: yes, Confirm: confirmTok, Force: force,
		ReadOnly: a.readOnly, TTY: tty, Ticket: ticket,
	}
}

func finishControl(cmd *cobra.Command, res *service.ControlResult, err error) error {
	a := appOf(cmd)
	r := render.New(a.deps.Stdout, a.format, a.noColor)
	if err != nil {
		if res == nil {
			return err
		}
		if a.format == render.FormatJSON {
			r.JSON(res)
			return err
		}
		if res.Plan != "" {
			fmt.Fprintln(r.Out, res.Plan)
		}
		if res.Preflight != nil && res.Preflight.Verdict != "" {
			t := render.Table{Columns: []string{"CHECK", "RESULT", "DETAIL"}}
			for _, c := range res.Preflight.Checks {
				t.Rows = append(t.Rows, []string{c.Name, c.Result, c.Detail})
			}
			r.WriteTable(t)
		}
		t := render.Table{Columns: []string{"STEP", "START", "END", "DURATION", "RESULT"}}
		for _, s := range res.Timeline {
			t.Rows = append(t.Rows, []string{s.Name, r.TimeShort(s.Start), r.TimeShort(s.End),
				fmtSecs(r, s.Duration.Seconds()), s.Result})
		}
		if len(t.Rows) > 0 {
			r.WriteTable(t)
		}
		if res.Outcome != "" {
			fmt.Fprintf(r.Out, "outcome: %s  event: %s\n", res.Outcome, res.EventID)
		}
		flushOut(r.Out)
		return err
	}
	if a.format == render.FormatJSON {
		if err := r.JSON(res); err != nil {
			return exitcode.New(exitcode.Usage, "cannot render JSON").Wrap(err)
		}
		return done(0)
	}
	t := render.Table{Columns: []string{"STEP", "START", "END", "DURATION", "RESULT"}}
	for _, s := range res.Timeline {
		t.Rows = append(t.Rows, []string{s.Name, r.TimeShort(s.Start), r.TimeShort(s.End),
			fmtSecs(r, s.Duration.Seconds()), s.Result})
	}
	r.WriteTable(t)
	if res.Warn != "" {
		fmt.Fprintf(r.Out, "warn: %s\n", res.Warn)
	}
	fmt.Fprintf(r.Out, "outcome: %s  event: %s\n", res.Outcome, res.EventID)
	return done(0)
}

func argID(args []string) (int, error) {
	id, err := strconv.Atoi(args[0])
	if err != nil || id < 0 {
		return 0, exitcode.New(exitcode.Usage, "invalid node id "+strconv.Quote(args[0]))
	}
	return id, nil
}

func promoteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "promote <id>",
		Short:             "promote a standby (pre-flight + confirm)",
		Annotations:       map[string]string{"deadline": "10m"},
		GroupID:           "control",
		ValidArgsFunction: nodeIDs(nil),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := argID(args)
			if err != nil {
				return err
			}
			a := appOf(cmd)
			gr, _ := cmd.Flags().GetBool("gracefully")
			only, _ := cmd.Flags().GetBool("pgpool-only")
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Promote(cmd.Context(), id, gr, only, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	c.Flags().Bool("gracefully", false, "pass -g to pcp_promote_node")
	c.Flags().Bool("pgpool-only", false, "only change pgpool's view (UNVERIFIED)")
	return c
}

func detachCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "detach <id>",
		Annotations:       map[string]string{"deadline": "5m"},
		Short:             "detach a node (pre-flight + confirm)",
		GroupID:           "control",
		ValidArgsFunction: nodeIDs(nil),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := argID(args)
			if err != nil {
				return err
			}
			a := appOf(cmd)
			gr, _ := cmd.Flags().GetBool("gracefully")
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Detach(cmd.Context(), id, gr, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	c.Flags().Bool("gracefully", false, "pass -g to pcp_detach_node")
	return c
}

func attachCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "attach <id>",
		Annotations:       map[string]string{"deadline": "5m"},
		Short:             "attach a node (pre-flight + confirm)",
		GroupID:           "control",
		ValidArgsFunction: nodeIDs(nil),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := argID(args)
			if err != nil {
				return err
			}
			a := appOf(cmd)
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Attach(cmd.Context(), id, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	return c
}

func recoverCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "recover <id>",
		Annotations:       map[string]string{"deadline": "none"},
		Short:             "online recovery of a node",
		GroupID:           "control",
		ValidArgsFunction: nodeIDs(nil),
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := argID(args)
			if err != nil {
				return err
			}
			a := appOf(cmd)
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			var to time.Duration
			if ts, _ := cmd.Flags().GetString("timeout"); ts != "" {
				if d, err := time.ParseDuration(ts); err == nil {
					to = d
				} else {
					return exitcode.New(exitcode.Usage, "invalid --timeout "+strconv.Quote(ts))
				}
			}
			res, err := svc.Recover(cmd.Context(), id, to, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	c.Flags().String("timeout", "", "give up after this long (no limit by default)")
	return c
}

func reloadCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "reload", Short: "reload pgpool configuration", GroupID: "control",
		Annotations: map[string]string{"deadline": "2m"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			local, _ := cmd.Flags().GetBool("local")
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Reload(cmd.Context(), local, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	c.Flags().Bool("local", false, "reload only this host (-s l)")
	return c
}

func topologyDiscoverCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "discover", Short: "diff live cluster vs inventory, optionally stage",
		Annotations: map[string]string{"deadline": "10m"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			svc := newService(a)
			rows, err := svc.DiscoverDiff(cmd.Context())
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(a.deps.Stdout, "inventory matches the live cluster")
				return done(0)
			}
			for _, r := range rows {
				fmt.Fprintf(a.deps.Stdout, "%-6s %-14s %s\n", r.Action, r.Host, r.Detail)
			}
			apply, _ := cmd.Flags().GetBool("apply")
			if !apply {
				fmt.Fprintln(a.deps.Stdout, "re-run with --apply to write these into the config")
				return done(0)
			}
			res, err := svc.DiscoverApply(cmd.Context(), rows, a.configPath, controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}
	c.Flags().Bool("apply", false, "write the discovered changes into the config")
	c.Flags().Bool("yes", false, "skip the y/N confirmation")
	c.Flags().String("confirm", "", "confirmation token")
	c.Flags().String("ticket", "", "change ticket")
	return c
}

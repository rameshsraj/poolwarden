package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/shell"
)

// runShell starts the interactive shell on reeflective/console.
func runShell(cmd *cobra.Command, a *app) error {
	if a.cfg == nil {
		return nil
	}
	svc := newService(a)
	// background sampler: svc.collect already appends to History; we still
	// want periodic sampling while the shell is up — drive it on a ticker
	// through History.Since for the prompt's DEGRADED marker.
	local := svc.Local
	if local == "" {
		local = "localhost"
	}
	lastDegraded := func() bool {
		if svc.History == nil {
			return false
		}
		// last sampler snapshot drives the marker
		if samples, err := svc.History.Since(30 * time.Minute); err == nil && len(samples) > 0 {
			return samples[len(samples)-1].Degraded
		}
		return false
	}
	prompt := func() string {
		degraded := ""
		if lastDegraded() {
			degraded = " DEGRADED"
		}
		return fmt.Sprintf("pgpool[%s@%s%s]> ", a.cfg.Cluster, local, degraded)
	}
	// session audit
	if svc.Recorder != nil {
		svc.Recorder.SessionStart(cmd.Context())
		defer svc.Recorder.SessionEnd(cmd.Context())
	}
	// header: version + CRIT findings
	fmt.Fprintf(a.deps.Stdout, "poolwarden %s — %s\n", a.deps.Version.Version, a.cfg.Cluster)
	if svc.Diagnoser != nil {
		if res, err := svc.Diagnose(cmd.Context(), "", ""); err == nil {
			for _, row := range res.Rows {
				if row.Severity == "CRIT" {
					fmt.Fprintf(a.deps.Stdout, "CRIT  %s\n", row.ID)
				}
			}
		}
	}
	// operator once (skip when --operator given or key resolves)
	if svc.Recorder != nil && a.operator == "" && svc.Recorder.Identity.Operator() == "" {
		fmt.Fprint(a.deps.Stdout, "Operator (name/ID): ")
		var line string
		fmt.Fscanln(a.deps.Stdin, &line)
		if line != "" {
			svc.Recorder.Identity.Declared = line
		}
	}
	code := shell.Run(cmd.Context(), shell.Deps{
		In: a.deps.Stdin, Out: a.deps.Stdout, IsTTY: true,
		Prompt:     prompt,
		NewRoot:    func() *cobra.Command { return buildRoot(a) },
		HistoryDir: a.cfg.StateDir,
		Operator:   a.operator,
	})
	if code != 0 {
		return done(code)
	}
	return done(0)
}

var _ = model.Finding{}
var _ = term.IsTerminal
var _ = context.Background

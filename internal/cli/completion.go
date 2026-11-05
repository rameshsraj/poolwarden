package cli

import (
	"github.com/spf13/cobra"

	"fmt"

	"github.com/smarsh/poolwarden/internal/pgconf"
	"github.com/smarsh/poolwarden/internal/render"
)

// nodeIDs completes pool node ids from the last snapshot.
func nodeIDs(_ *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		a := appOf(cmd)
		var ids []string
		if snap := newService(a).Collector.Collect(cmd.Context()); snap != nil {
			for _, n := range snap.Pgpool.Nodes {
				ids = append(ids, fmt.Sprint(n.ID))
			}
		}
		return toComps(ids), cobra.ShellCompDirectiveNoFileComp
	}
}

// hostNames completes inventory host names.
func hostNames(_ *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		a := appOf(cmd)
		var names []string
		for _, h := range a.cfg.Hosts {
			names = append(names, h.Name)
		}
		return toComps(names), cobra.ShellCompDirectiveNoFileComp
	}
}

var _ = hostNames // referenced by --host flags wired in commands.go

// paramNames completes from the 4.4/4.5 catalogue.
func paramNames() cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var names []string
		for _, p := range pgconf.All() {
			names = append(names, p.Name)
		}
		return toComps(names), cobra.ShellCompDirectiveNoFileComp
	}
}

// findingIDs completes current finding ids (last snapshot's diagnosis).
func findingIDs(_ *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		a := appOf(cmd)
		var ids []string
		if res, err := newService(a).Diagnose(cmd.Context(), "", ""); err == nil {
			for _, r := range res.Rows {
				ids = append(ids, r.ID)
			}
		}
		return toComps(ids), cobra.ShellCompDirectiveNoFileComp
	}
}

// watchViewNames completes watch/top views.
func watchViewNames() cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var out []string
		for name := range render.WatchViews {
			out = append(out, name)
		}
		return toComps(out), cobra.ShellCompDirectiveNoFileComp
	}
}

func toComps(ss []string) []cobra.Completion {
	out := make([]cobra.Completion, len(ss))
	copy(out, ss)
	return out
}

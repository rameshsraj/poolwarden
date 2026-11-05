package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"gopkg.in/yaml.v3"
)

// DiscoverRow is one diff row between the live cluster and cfg.Hosts.
type DiscoverRow struct {
	Action string // ADD|REMOVE|CHANGE
	Host   string
	Detail string
}

// DiscoverDiff computes 02 §2.8 diffs: watchdog peers + backend_hostnameN
// from the live snapshot's pgpool.conf values vs cfg.Hosts.
func (s *Service) DiscoverDiff(ctx context.Context) ([]DiscoverRow, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	// live set: watchdog nodes (pgpool hosts) + backend hostnames (pg hosts)
	livePG := map[string]int{} // host -> port
	if s.FileSettingsFn != nil {
		// per-host param→value maps; pull backend_hostnameN + backend_portN
		for _, vals := range s.FileSettingsFn(ctx) {
			for name, v := range vals {
				if strings.HasPrefix(name, "backend_hostname") {
					livePG[v] = atoiS(vals["backend_port"+name[len("backend_hostname"):]])
				}
			}
		}
	}
	for _, be := range r.snap.Backends {
		if be.Host != "" {
			livePG[be.Host] = be.Port
		}
	}
	livePgp := map[string]bool{}
	for _, n := range r.snap.Watchdog.Nodes {
		livePgp[n.Host] = true
	}
	known := map[string]config.Host{}
	for _, h := range s.Cfg.Hosts {
		known[h.Name] = h
	}
	var rows []DiscoverRow
	for _, n := range r.snap.Watchdog.Nodes {
		h, ok := known[n.Host]
		if !ok {
			rows = append(rows, DiscoverRow{"ADD", n.Host, "watchdog peer not in inventory"})
			continue
		}
		if !hasRole(h, config.RolePgpool) {
			rows = append(rows, DiscoverRow{"CHANGE", h.Name, "watchdog says pgpool but pgpool role missing"})
		}
	}
	for host, port := range livePG {
		h, ok := known[host]
		if !ok {
			rows = append(rows, DiscoverRow{"ADD", host, fmt.Sprintf("backend (port %d) not in inventory", port)})
		} else if !hasRole(h, config.RolePG) {
			rows = append(rows, DiscoverRow{"CHANGE", host, "is a backend but pg role missing"})
		}
	}
	for _, h := range s.Cfg.Hosts {
		if _, isPG := livePG[h.Name]; !livePgp[h.Name] && !isPG {
			rows = append(rows, DiscoverRow{"REMOVE", h.Name, "in inventory but absent from the live cluster"})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Host < rows[j].Host })
	return rows, nil
}

// DiscoverApply stages the diffs into poolwarden's own config: C-flow with a
// YesNo confirm, backed up to <path>.bak.<ts>, written 0600 root.
func (s *Service) DiscoverApply(ctx context.Context, rows []DiscoverRow, cfgPath string, o ControlOpts) (*ControlResult, error) {
	a := Action{Name: "topology discover", Command: "topology discover", YesNo: true}
	a.Preflight = func(context.Context) (*model.PreflightResult, error) {
		res := &model.PreflightResult{Action: "topology discover", Verdict: "GO"}
		for _, r := range rows {
			res.Checks = append(res.Checks, model.Check{
				Name: r.Host, Result: "OK", Detail: r.Action + " " + r.Detail})
		}
		if len(rows) == 0 {
			res.Checks = append(res.Checks, model.Check{Name: "diff", Result: "NO-GO", Detail: "nothing to apply"})
			res.Verdict = "NO-GO"
		}
		return res, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		var b strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&b, "%-6s %-14s %s\n", r.Action, r.Host, r.Detail)
		}
		return b.String()
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("write config", func() (string, error) {
			merged := *s.Cfg
			merged.Hosts = append([]config.Host{}, s.Cfg.Hosts...)
			byName := map[string]*config.Host{}
			for i := range merged.Hosts {
				byName[merged.Hosts[i].Name] = &merged.Hosts[i]
			}
			for _, r := range rows {
				switch r.Action {
				case "ADD":
					nh := config.Host{Name: r.Host, Address: r.Host, Roles: []config.Role{config.RolePG}}
					if strings.Contains(r.Detail, "pgpool") {
						nh.Roles = []config.Role{config.RolePgpool}
					}
					merged.Hosts = append(merged.Hosts, nh)
				case "REMOVE":
					var keep []config.Host
					for _, h := range merged.Hosts {
						if h.Name != r.Host {
							keep = append(keep, h)
						}
					}
					merged.Hosts = keep
				case "CHANGE":
					if h, ok := byName[r.Host]; ok {
						if strings.Contains(r.Detail, "pgpool") {
							h.Roles = append(h.Roles, config.RolePgpool)
						} else {
							h.Roles = append(h.Roles, config.RolePG)
						}
					}
				}
			}
			old, _ := s.fsys().ReadFile(cfgPath)
			nb, err := yaml.Marshal(&merged)
			if err != nil {
				return "", err
			}
			diff := pgconf.Diff(cfgPath, cfgPath+" (new)", old, nb)
			a.Change = &audit.Change{Diff: diff}
			bak := cfgPath + ".bak." + s.now().UTC().Format("20060102T150405")
			if len(old) > 0 {
				s.fsys().WriteFileAtomic(bak, old, true)
			}
			if err := s.fsys().WriteFileAtomic(cfgPath, nb, false); err != nil {
				return "", err
			}
			return "written " + cfgPath, nil
		})
	}
	return s.RunControl(ctx, &a, o)
}

var _ = time.Now

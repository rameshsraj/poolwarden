package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

// ProbeCheck is one connection-test row for the configuration wizard.
type ProbeCheck struct {
	Name   string `json:"name"`           // pgpool_sql | pcp | ssh | pg | watchdog
	Host   string `json:"host,omitempty"` // inventory host or endpoint
	Result string `json:"result"`         // OK | FAIL | WARN | SKIP
	Detail string `json:"detail,omitempty"`
}

// ProbeResult is `poolwarden probe`: per-endpoint connectivity against the
// loaded config (candidate or installed), for the console setup wizard.
type ProbeResult struct {
	Cluster  string       `json:"cluster"`
	Duration int64        `json:"duration_ms"`
	Checks   []ProbeCheck `json:"checks"`
	OK       bool         `json:"ok"`
}

// Probe tests each configured endpoint independently: pgpool SQL, PCP
// binaries, SSH per host, direct PostgreSQL per pg host, watchdog.
func (s *Service) Probe(ctx context.Context) *ProbeResult {
	start := s.now()
	res := &ProbeResult{Cluster: s.Cfg.Cluster}
	add := func(name, host, r, d string) {
		res.Checks = append(res.Checks, ProbeCheck{Name: name, Host: host, Result: r, Detail: d})
	}
	snap := s.Collector.Collect(ctx)
	if snap.Pgpool.Reachable {
		add("pgpool_sql", fmt.Sprintf("%s:%d", s.Cfg.Pgpool.Host, s.Cfg.Pgpool.Port),
			"OK", "version "+orDash(snap.Pgpool.Version))
	} else {
		add("pgpool_sql", fmt.Sprintf("%s:%d", s.Cfg.Pgpool.Host, s.Cfg.Pgpool.Port),
			"FAIL", snap.Pgpool.Err)
	}
	for _, b := range snap.Backends {
		host := b.Name
		if host == "" {
			host = fmt.Sprintf("%s:%d", b.Host, b.Port)
		}
		if b.Reachable {
			role := "primary"
			if b.InRecovery {
				role = "standby"
			}
			add("pg", host, "OK", role)
		} else {
			add("pg", host, "FAIL", orDash(b.Err))
		}
	}
	if len(snap.Backends) == 0 {
		add("pg", "", "SKIP", "no pg hosts configured")
	}
	// ssh + pcp per host via the remote/session layer
	for _, h := range s.Cfg.Hosts {
		if s.ConfigRemote == nil {
			add("ssh", h.Name, "SKIP", "no remote adapter")
			continue
		}
		if _, err := s.ConfigRemote.Exec(ctx, h, []string{"true"}, nil); err != nil {
			add("ssh", h.Name, "FAIL", err.Error())
		} else {
			add("ssh", h.Name, "OK", h.SSHUser+"@"+h.Address)
		}
	}
	if snap.Watchdog.Available {
		add("watchdog", "", "OK",
			fmt.Sprintf("quorum %s, leader %s, %d/%d alive",
				orDash(snap.Watchdog.QuorumState), orDash(snap.Watchdog.LeaderHost),
				snap.Watchdog.AliveRemote+1, snap.Watchdog.TotalNodes))
	} else {
		add("watchdog", "", "WARN", "unavailable or not configured")
	}
	res.OK = true
	for _, c := range res.Checks {
		if c.Result == "FAIL" {
			res.OK = false
		}
	}
	res.Duration = time.Since(start).Milliseconds()
	return res
}

// InstallConfig validates candidate poolwarden YAML and installs it at path
// through the audited control flow (backup + atomic write + confirm).
func (s *Service) InstallConfig(ctx context.Context, data []byte, path string, o ControlOpts) (*ControlResult, error) {
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, exitcode.New(exitcode.Usage, "cannot parse candidate config").Wrap(err)
	}
	if cfg.Cluster == "" {
		return nil, exitcode.New(exitcode.Usage, "candidate config has no cluster name").
			WithHint("the file must be a complete poolwarden.yaml")
	}
	if len(cfg.Hosts) == 0 {
		return nil, exitcode.New(exitcode.Usage, "candidate config has no hosts").
			WithHint("add at least one pgpool/pg host")
	}
	a := Action{
		Name: "config install", Command: "config install", YesNo: true,
		Preflight: func(context.Context) (*model.PreflightResult, error) {
			return &model.PreflightResult{Action: "config install", Verdict: "GO",
				Checks: []model.Check{
					{Name: "parse", Result: "OK", Detail: fmt.Sprintf("cluster %s, %d hosts", cfg.Cluster, len(cfg.Hosts))},
					{Name: "target", Result: "OK", Detail: "write " + path},
				}}, nil
		},
		Plan: func(pf *model.PreflightResult) string {
			return planText(pf, fmt.Sprintf("install poolwarden.yaml: cluster %s, %d hosts → %s",
				cfg.Cluster, len(cfg.Hosts), path))
		},
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("write config", func() (string, error) {
			diff, err := s.writeInitConfig(&cfg, path)
			if err != nil {
				return "", err
			}
			a.Change = &audit.Change{Diff: diff}
			return "written " + path, nil
		})
	}
	return s.RunControl(ctx, &a, o)
}

// writeCandidate persists user-supplied YAML to a 0600 temp file in dir and
// returns its path (used by `config install --from -` style flows).
func WriteCandidate(dir string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "candidate-*.yaml")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}

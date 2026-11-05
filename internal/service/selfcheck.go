package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
)

// SelfCheckRow is one 02 §1.2 row.
type SelfCheckRow struct {
	Check, Host, Result, Detail string
}

// SelfCheck runs the config self-check; results are cached to
// <StateDir>/selfcheck.json for the SELF_CHECK diagnosis rule.
func (s *Service) SelfCheck(ctx context.Context) []SelfCheckRow {
	var rows []SelfCheckRow
	add := func(check, host, res, det string) {
		rows = append(rows, SelfCheckRow{check, host, res, det})
	}
	for _, h := range s.Cfg.Hosts {
		// ssh works
		if s.ConfigRemote != nil {
			if _, err := s.ConfigRemote.Exec(ctx, h, []string{"true"}, nil); err != nil {
				add("ssh", h.Name, "FAIL", err.Error())
			} else {
				add("ssh", h.Name, "OK", "")
			}
		}
		// configured paths exist
		for _, p := range hostPaths(h) {
			if s.ConfigRemote == nil {
				continue
			}
			if _, err := s.ConfigRemote.ReadFile(ctx, h, p); err != nil {
				res, _ := s.ConfigRemote.Exec(ctx, h, []string{"test", "-e", p}, nil)
				if res.Exit != 0 {
					add("path", h.Name, "WARN", p+" missing")
				} else {
					add("path", h.Name, "OK", p)
				}
			} else {
				add("path", h.Name, "OK", p)
			}
		}
	}
	// live log_line_prefix match per pgpool host
	for _, h := range s.Cfg.PgpoolHosts() {
		if s.ShowParam == nil || h.Pgpool == nil {
			continue
		}
		got, err := s.ShowParam(ctx, h, "log_line_prefix")
		if err != nil {
			add("log_line_prefix", h.Name, "WARN", err.Error())
		} else if got != h.Pgpool.LogLinePrefix {
			add("log_line_prefix", h.Name, "WARN",
				fmt.Sprintf("live %q ≠ conf %q", got, h.Pgpool.LogLinePrefix))
		} else {
			add("log_line_prefix", h.Name, "OK", "")
		}
	}
	// pcp binaries + systemctl/ip present (local stat)
	for _, bin := range []string{"pcp_node_info", "pg_md5", "pg_enc"} {
		p := path.Join(s.Cfg.PCP.BinDir, bin)
		if _, err := s.fsys().Stat(p); err != nil {
			add("binary", "local", "WARN", p+" missing")
		} else {
			add("binary", "local", "OK", bin)
		}
	}
	for _, b := range []string{s.Cfg.Bins.Systemctl, s.Cfg.Bins.IP} {
		if b == "" {
			continue
		}
		if _, err := s.fsys().Stat(b); err != nil {
			add("binary", "local", "WARN", b+" missing")
		} else {
			add("binary", "local", "OK", path.Base(b))
		}
	}
	// config file permissions (local)
	if s.CfgPath != "" {
		fi, err := s.fsys().Stat(s.CfgPath)
		if err == nil && fi.Mode().Perm()&0o077 != 0 {
			add("config", "local", "WARN", s.CfgPath+" is group/world accessible")
		} else if err == nil {
			add("config", "local", "OK", "")
		}
	}
	// cache for SELF_CHECK
	type cached struct {
		At   time.Time      `json:"at"`
		Rows []SelfCheckRow `json:"rows"`
	}
	b, _ := json.Marshal(cached{At: s.now(), Rows: rows})
	s.fsys().MkdirAll(s.Cfg.StateDir, 0o700)
	s.fsys().WriteFileAtomic(path.Join(s.Cfg.StateDir, "selfcheck.json"), b, true)
	return rows
}

// PathChecks feeds diagnose.Input.PathChecks from the cached self-check.
func (s *Service) PathChecks() []string {
	b, err := s.fsys().ReadFile(path.Join(s.Cfg.StateDir, "selfcheck.json"))
	if err != nil {
		return nil
	}
	var c struct {
		Rows []SelfCheckRow `json:"rows"`
	}
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	var out []string
	for _, r := range c.Rows {
		if r.Result != "OK" {
			out = append(out, r.Check+" "+r.Host+" "+r.Detail)
		}
	}
	return out
}

func hostPaths(h config.Host) []string {
	var out []string
	if h.Pgpool != nil {
		for _, p := range []string{h.Pgpool.Conf, h.Pgpool.HBA, h.Pgpool.PCPConf,
			h.Pgpool.PoolPasswd, h.Pgpool.LogDir} {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	if h.PG != nil {
		for _, p := range []string{h.PG.DataDir, h.PG.LogDir} {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

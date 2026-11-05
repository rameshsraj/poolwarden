package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"gopkg.in/yaml.v3"
)

// Prompter is the wizard interface (cli implements it over Stdin/Stdout).
type Prompter interface {
	Ask(text, def string) string
	Choose(text string, options []string) int
	YesNo(text string) bool
}

// InitOpts for `poolwarden init`.
type InitOpts struct {
	Host           string
	NonInteractive bool
	FromFile       string
	OutPath        string
}

// discoverConf finds candidate pgpool.conf paths: running pgpool processes
// (Linux /proc) + well-known paths.
func (s *Service) discoverConf() []string {
	var out []string
	seen := map[string]bool{}
	// /proc/*/cmdline for -f <conf>
	if entries, err := s.fsys().ReadDir("/proc"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			b, err := s.fsys().ReadFile("/proc/" + e.Name() + "/cmdline")
			if err != nil {
				continue
			}
			parts := strings.Split(string(b), "\x00")
			if len(parts) == 0 || !strings.HasSuffix(parts[0], "pgpool") {
				continue
			}
			for i, p := range parts {
				if p == "-f" && i+1 < len(parts) && !seen[parts[i+1]] {
					seen[parts[i+1]] = true
					out = append(out, parts[i+1])
				}
			}
		}
	}
	for _, p := range []string{"/etc/pgpool-II/pgpool.conf", "/etc/pgpool/pgpool.conf"} {
		if !seen[p] {
			out = append(out, p)
		}
	}
	return out
}

// RunInit is the 02 §1.1 wizard; answers come from the Prompter (scriptable)
// or a non-interactive answers file.
func (s *Service) RunInit(ctx context.Context, o InitOpts, p Prompter, answers map[string]string) (*config.Config, error) {
	ask := func(key, text, def string) string {
		if o.NonInteractive {
			if v, ok := answers[key]; ok && v != "" {
				return v
			}
			if def != "" {
				return def
			}
			return ""
		}
		return p.Ask(text, def)
	}
	need := func(key, text, def string) (string, error) {
		v := ask(key, text, def)
		if v == "" && o.NonInteractive {
			return "", exitcode.New(exitcode.Usage, "missing required answer: "+key)
		}
		return v, nil
	}
	cfg := config.Defaults()
	// re-run: pre-fill from the existing config when present
	if s.Cfg != nil {
		cfg = *s.Cfg
	}
	// step 1: find pgpool.conf
	cands := s.discoverConf()
	var confPath string
	if o.NonInteractive {
		confPath, _ = need("conf", "pgpool.conf path",
			firstOr(cands, "/etc/pgpool-II/pgpool.conf"))
	} else {
		opts := append(append([]string{}, cands...), "type a path")
		i := p.Choose("pgpool.conf location", opts)
		if i == len(opts)-1 {
			confPath = p.Ask("path", firstOr(cands, "/etc/pgpool-II/pgpool.conf"))
		} else {
			confPath = opts[i]
		}
	}
	confPath, err := need("conf", "pgpool.conf path", confPath)
	if err != nil {
		return nil, err
	}
	// step 2: parse and confirm values
	b, err := s.fsys().ReadFile(confPath)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, "cannot read "+confPath).Wrap(err)
	}
	cf, err := pgconf.Parse(b)
	if err != nil {
		return nil, err
	}
	getV := func(k, def string) string {
		if v, ok, _ := cf.Get(k); ok {
			return v
		}
		return def
	}
	dir := pathDir(confPath)
	cfg.Pgpool.Port = atoiS(ask("pgpool.port", "pgpool port (from "+confPath+")", orStr(itoaS(cfg.Pgpool.Port), getV("port", "9999"))))
	cfg.PCP.Port = atoiS(ask("pcp.port", "pcp port", orStr(itoaS(cfg.PCP.Port), getV("pcp_port", "9898"))))
	cfg.Pgpool.Passfile = ask("pgpool.passfile", "PGPASSFILE", orStr(cfg.Pgpool.Passfile, "~postgres/.pgpass"))
	cfg.PCP.Passfile = ask("pcp.passfile", "PCPPASSFILE", orStr(cfg.PCP.Passfile, "~postgres/.pcppass"))
	// step 3: test connections with the passfiles set on the child env only
	if s.InitProbes != nil {
		if err := s.InitProbes.TestPgpool(ctx, cfg.Pgpool, cfg.Pgpool.Passfile); err != nil {
			ask("pgpool.test", "pgpool SQL test failed ("+err.Error()+") — continue anyway?", "y")
		}
		if err := s.InitProbes.TestPCP(ctx, cfg.PCP, cfg.PCP.Passfile); err != nil {
			ask("pcp.test", "pcp test failed ("+err.Error()+") — continue anyway?", "y")
		}
	}
	// hosts: existing config + later discoveries
	hosts := cfg.Hosts
	// step 4: watchdog peers (via probe) → pgpool-role hosts + ssh checks
	if s.InitProbes != nil {
		peers, _ := s.InitProbes.WatchdogPeers(ctx)
		for _, peer := range peers {
			exists := false
			for _, h := range hosts {
				if h.Name == peer || h.Address == peer {
					exists = true
				}
			}
			if !exists && (o.Host == "" || o.Host == peer) {
				roles := ask("host."+peer+".roles", "roles for "+peer, "pgpool,pg")
				hosts = append(hosts, config.Host{
					Name: peer, Address: peer,
					Roles:   parseRoles(roles),
					SSHUser: ask("host."+peer+".ssh_user", "ssh_user", "root"),
				})
			}
		}
	}
	for i, h := range hosts {
		if o.Host != "" && h.Name != o.Host {
			continue
		}
		if h.SSHUser == "" {
			hosts[i].SSHUser = ask("host."+h.Name+".ssh_user", "ssh_user for "+h.Name, "root")
		}
		if s.InitProbes != nil {
			if err := s.InitProbes.SSHTest(ctx, h); err != nil {
				ask("host."+h.Name+".ssh", "ssh to "+h.Name+" failed: "+err.Error()+
					" (hint: ssh-keyscan -H "+h.Address+" >> "+cfg.SSH.KnownHosts+") — continue?", "y")
			}
		}
	}
	// step 5: per-PG-host SHOW settings via probe
	for i, h := range hosts {
		if o.Host != "" && h.Name != o.Host {
			continue
		}
		if s.InitProbes == nil || !hasRole(h, config.RolePG) {
			continue
		}
		setts, err := s.InitProbes.PGSettings(ctx, h)
		if err != nil {
			continue
		}
		if h.PG == nil {
			h.PG = &config.PGPaths{}
			hosts[i].PG = h.PG
		}
		hosts[i].PG.DataDir = ask("host."+h.Name+".data_dir", "data_directory",
			orStr(h.PG.DataDir, setts["data_directory"]))
		ld := setts["log_directory"]
		if ld != "" && !strings.HasPrefix(ld, "/") && hosts[i].PG.DataDir != "" {
			ld = hosts[i].PG.DataDir + "/" + ld
		}
		hosts[i].PG.LogDir = ask("host."+h.Name+".log_dir", "log_directory",
			orStr(h.PG.LogDir, ld))
		hosts[i].PG.LogFilename = ask("host."+h.Name+".log_filename", "log_filename",
			orStr(h.PG.LogFilename, setts["log_filename"]))
		dest := ask("host."+h.Name+".log_destination", "log_destination",
			orStr(h.PG.LogDestination, setts["log_destination"]))
		if dest != "stderr" {
			ask("host."+h.Name+".log_dest_warn",
				"warning: log_destination is "+dest+", text-log sources need stderr (I9) — continue?", "y")
		}
		hosts[i].PG.LogDestination = dest
		hosts[i].PG.LogLinePrefix = ask("host."+h.Name+".log_line_prefix", "log_line_prefix",
			orStr(h.PG.LogLinePrefix, setts["log_line_prefix"]))
	}
	_ = dir
	// step 3: backends → inventory hosts
	if o.Host != "" {
		var keep []config.Host
		for _, h := range hosts {
			if h.Name == o.Host {
				keep = append(keep, h)
			}
		}
		hosts = keep
	}
	for _, be := range cf.Backends() {
		exists := false
		for _, h := range hosts {
			if h.Name == be.Hostname || h.Address == be.Hostname {
				exists = true
			}
		}
		if !exists {
			roles := ask("host."+be.Hostname+".roles", "roles for "+be.Hostname, "pg")
			hosts = append(hosts, config.Host{
				Name: be.Hostname, Address: be.Hostname,
				Roles:   parseRoles(roles),
				SSHUser: ask("host."+be.Hostname+".ssh_user", "ssh_user", "root"),
				PG:      &config.PGPaths{DataDir: be.DataDirectory},
			})
		}
	}
	cfg.Hosts = hosts
	// step 6: thresholds + paths
	cfg.Lag.WarnSeconds = atoiS(ask("lag.warn_seconds", "lag warn (seconds)", itoaDef(cfg.Lag.WarnSeconds, 1800)))
	cfg.Lag.CritSeconds = atoiS(ask("lag.crit_seconds", "lag crit (seconds)", itoaDef(cfg.Lag.CritSeconds, 3600)))
	cfg.Audit.File = ask("audit.file", "audit file", orStr(cfg.Audit.File, "/var/lib/poolwarden/audit.jsonl"))
	cfg.Audit.RetentionDays = atoiS(ask("audit.retention_days", "audit retention days", itoaDef(cfg.Audit.RetentionDays, 31)))
	cfg.StateDir = ask("state_dir", "state dir", orStr(cfg.StateDir, "/var/lib/poolwarden"))
	if _, err := exec.LookPath("systemctl"); err == nil {
		cfg.Bins.Systemctl, _ = exec.LookPath("systemctl")
	}
	if _, err := exec.LookPath("ip"); err == nil {
		cfg.Bins.IP, _ = exec.LookPath("ip")
	}
	cfg.Cluster = ask("cluster", "cluster name", orStr(cfg.Cluster, "prod-a"))
	return &cfg, nil
}

func parseRoles(s string) []config.Role {
	var out []config.Role
	for _, r := range strings.Split(s, ",") {
		switch strings.TrimSpace(r) {
		case "pgpool":
			out = append(out, config.RolePgpool)
		case "pg":
			out = append(out, config.RolePG)
		}
	}
	return out
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

func firstOr(s []string, def string) string {
	if len(s) > 0 {
		return s[0]
	}
	return def
}

func atoiS(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

// InitApply runs the final write through RunControl (02 §1.1 step 7:
// confirmation, audit with the config diff).
func (s *Service) InitApply(ctx context.Context, cfg *config.Config, path string, o ControlOpts) (*ControlResult, error) {
	a := Action{Name: "init", Command: "init", YesNo: true}
	a.Preflight = func(context.Context) (*model.PreflightResult, error) {
		return &model.PreflightResult{Action: "init", Verdict: "GO",
			Checks: []model.Check{{Name: "config", Result: "OK", Detail: "write " + path}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return "write " + path
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("write config", func() (string, error) {
			diff, err := s.writeInitConfig(cfg, path)
			if err != nil {
				return "", err
			}
			a.Change = &audit.Change{Diff: diff}
			return "written " + path, nil
		})
	}
	return s.RunControl(ctx, &a, o)
}

// WriteInitConfig writes the YAML with backup + 0600 (02 §1.1 step 7) and
// returns the written path + diff text for auditing.
func (s *Service) WriteInitConfig(cfg *config.Config, path string) (string, error) {
	return s.writeInitConfig(cfg, path)
}

func (s *Service) writeInitConfig(cfg *config.Config, path string) (string, error) {
	fsys := s.fsys()
	var old []byte
	if b, err := fsys.ReadFile(path); err == nil {
		old = b
		bak := path + "." + s.now().UTC().Format("20060102T150405")
		fsys.WriteFileAtomic(bak, b, true)
	}
	nb, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	diff := pgconf.Diff(path, path+" (new)", old, nb)
	if err := fsys.WriteFileAtomic(path, nb, false); err != nil {
		return "", err
	}
	return diff, nil
}

var _ = time.Now

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasRole(h config.Host, r config.Role) bool {
	for _, x := range h.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// InitProbes is the testable seam for init's external probes.
type InitProbes interface {
	TestPgpool(ctx context.Context, ep config.Endpoint, passfile string) error
	TestPCP(ctx context.Context, pcp config.PCP, passfile string) error
	WatchdogPeers(ctx context.Context) ([]string, error)
	SSHTest(ctx context.Context, h config.Host) error
	PGSettings(ctx context.Context, h config.Host) (map[string]string, error)
}

func itoaS(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}
func itoaDef(n, def int) string {
	if n == 0 {
		return fmt.Sprint(def)
	}
	return fmt.Sprint(n)
}

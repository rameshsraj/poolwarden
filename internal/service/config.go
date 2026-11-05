package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"github.com/smarsh/poolwarden/internal/pgpoolsql"
	"github.com/smarsh/poolwarden/internal/remote"
)

// Config seams (injectable for tests).
var _ = pgpoolsql.Client(nil)

// RemoteOps is what the apply flow needs from remote sessions.
type RemoteOps interface {
	ReadFile(ctx context.Context, h config.Host, path string) ([]byte, error)
	WriteFileAtomic(ctx context.Context, h config.Host, path string, data []byte) error
	Exec(ctx context.Context, h config.Host, args []string, stdin []byte) (remote.ExecResult, error)
}

// ShowParamFn verifies one live param on a host.
type ShowParamFn func(ctx context.Context, h config.Host, param string) (string, error)

// FileSpec maps a staged file name to its per-host path.
func confPath(h config.Host, file string) string {
	p := h.Pgpool
	if p == nil {
		return ""
	}
	switch file {
	case "pgpool.conf":
		return p.Conf
	case "pool_hba.conf":
		return p.HBA
	case "pcp.conf":
		return p.PCPConf
	case "pool_passwd":
		return p.PoolPasswd
	}
	return p.Conf
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// StageChange appends a change to staged.json and records the current file
// sha (local view via RemoteOps on the first pgpool host) when available.
func (s *Service) StageChange(ctx context.Context, c pgconf.Change) error {
	fsys := s.FS
	if fsys == nil {
		fsys = pwfs.OS{}
	}
	st, err := pgconf.LoadStaged(fsys, s.Cfg.StateDir)
	if err != nil {
		return err
	}
	c.At = s.now()
	st.Changes = append(st.Changes, c)
	// record the sha of the file on the first pgpool host if readable
	if s.ConfigRemote != nil && len(s.Cfg.PgpoolHosts()) > 0 {
		h := s.Cfg.PgpoolHosts()[0]
		if b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, c.File)); err == nil {
			if st.SHAs == nil {
				st.SHAs = map[string]string{}
			}
			st.SHAs[c.File] = sha(b)
		}
	}
	return st.Save(fsys, s.Cfg.StateDir)
}

// LoadStaged returns the current staging file.
func (s *Service) LoadStaged() (*pgconf.Staged, error) {
	fsys := s.FS
	if fsys == nil {
		fsys = pwfs.OS{}
	}
	return pgconf.LoadStaged(fsys, s.Cfg.StateDir)
}

// DiscardStaging clears staged.json (YesNo prompt handled by the caller).
func (s *Service) DiscardStaging() error {
	st := &pgconf.Staged{}
	return st.Save(s.fsys(), s.Cfg.StateDir)
}

func (s *Service) fsys() pwfs.FS {
	if s.FS != nil {
		return s.FS
	}
	return pwfs.OS{}
}

// ApplyFileBytes computes the new bytes of a file from current bytes + staged
// changes for that file.
func ApplyFileBytes(file string, cur []byte, changes []pgconf.Change) ([]byte, error) {
	var relevant []pgconf.Change
	for _, c := range changes {
		if c.File == file {
			relevant = append(relevant, c)
		}
	}
	if len(relevant) == 0 {
		return cur, nil
	}
	switch file {
	case "pool_hba.conf":
		hf, err := pgconf.ParseHBA(cur)
		if err != nil {
			return nil, err
		}
		for _, c := range relevant {
			switch c.Op {
			case "hba_add":
				var r pgconf.HBARule
				json.Unmarshal(c.Payload, &r)
				hf.AddRule(r)
			case "hba_remove":
				var idx int
				fmt.Sscanf(c.Param, "%d", &idx)
				hf.RemoveRule(idx)
			}
		}
		return hf.Bytes(), nil
	case "pcp.conf":
		pf, err := pgconf.ParsePCP(cur)
		if err != nil {
			return nil, err
		}
		for _, c := range relevant {
			switch c.Op {
			case "pcpuser_set":
				var u struct{ User, Hash string }
				json.Unmarshal(c.Payload, &u)
				pf.SetUser(u.User, u.Hash)
			case "pcpuser_remove":
				pf.RemoveUser(c.Param)
			}
		}
		return pf.Bytes(), nil
	}
	f, err := pgconf.Parse(cur)
	if err != nil {
		return nil, err
	}
	for _, c := range relevant {
		switch c.Op {
		case "set":
			f.Set(c.Param, c.New)
		case "unset":
			f.Unset(c.Param)
		case "backend_set":
			var b pgconf.Backend
			json.Unmarshal(c.Payload, &b)
			f.SetBackend(b)
		case "backend_remove":
			var id int
			fmt.Sscanf(c.Param, "%d", &id)
			f.RemoveBackend(id)
		}
	}
	return f.Bytes(), nil
}

// Manifest is a config backup version.
type Manifest struct {
	Version  string         `json:"version"`
	Time     time.Time      `json:"time"`
	Operator string         `json:"operator"`
	Ticket   string         `json:"ticket"`
	Hosts    []string       `json:"hosts"`
	Files    []ManifestFile `json:"files"`
	Diff     string         `json:"diff"`
	Summary  string         `json:"summary"`
}

type ManifestFile struct {
	Host         string `json:"host"`
	Path         string `json:"path"`
	SHA256Before string `json:"sha256_before"`
	SHA256After  string `json:"sha256_after,omitempty"`
}

func (s *Service) backupDir(v string) string {
	return path.Join(s.Cfg.StateDir, "config", v)
}

// Apply is the config-apply RunControl Action (01 §9 steps 1–7).
func (s *Service) Apply(ctx context.Context, o ControlOpts) (*ControlResult, error) {
	a := Action{Name: "config apply", Command: "config apply",
		Token: "apply", ForceToken: "apply"}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		st, err := s.LoadStaged()
		if err != nil {
			return nil, err
		}
		var checks []model.Check
		if len(st.Changes) == 0 {
			checks = append(checks, model.Check{Name: "staging", Result: "NO-GO", Detail: "nothing staged"})
		} else {
			checks = append(checks, model.Check{Name: "staging", Result: "OK",
				Detail: fmt.Sprintf("%d staged change(s)", len(st.Changes))})
		}
		// context class
		var names []string
		for _, c := range st.Changes {
			if c.Op == "set" {
				names = append(names, c.Param)
			}
		}
		ctxt := pgconf.Context(names)
		if ctxt == "restart" {
			a.Token, a.ForceToken = "restart", "restart"
		}
		checks = append(checks, model.Check{Name: "context", Result: "OK", Detail: ctxt})
		// validation gate: catalogue type/range + FAIL-class cross rules
		var valFails, valWarns []string
		for _, ch := range st.Changes {
			if ch.Op != "set" {
				continue
			}
			p, ok := pgconf.Lookup(ch.Param)
			if !ok {
				valFails = append(valFails, ch.Param+" (not in catalogue)")
				continue
			}
			if err := pgconf.ValidateValue(p, ch.New); err != nil {
				valFails = append(valFails, ch.Param+": "+err.Error())
			}
		}
		if snap := s.Collector.Collect(ctx); snap != nil {
			values := map[string]string{}
			for k, val := range snap.Settings {
				values[k] = val
			}
			for _, ch := range st.Changes {
				if ch.Op == "set" {
					values[ch.Param] = ch.New
				}
			}
			for _, r := range pgconf.CrossRules(values, snap.Backends) {
				if !r.OK {
					if pgconf.WarnRules[r.Name] {
						valWarns = append(valWarns, r.Detail)
					} else {
						valFails = append(valFails, r.Detail)
					}
				}
			}
		}
		switch {
		case len(valFails) > 0:
			checks = append(checks, model.Check{Name: "validation", Result: "NO-GO",
				Detail: strings.Join(valFails, "; ")})
		case len(valWarns) > 0:
			checks = append(checks, model.Check{Name: "validation", Result: "WARN",
				Detail: strings.Join(valWarns, "; ")})
		default:
			checks = append(checks, model.Check{Name: "validation", Result: "OK", Detail: "all checks pass"})
		}
		v := "GO"
		for _, c := range checks {
			if c.Result == "NO-GO" {
				v = "NO-GO"
			}
		}
		return &model.PreflightResult{Action: "config apply", Verdict: v, Checks: checks}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		st, _ := s.LoadStaged()
		var b strings.Builder
		b.WriteString("staged changes:\n")
		for _, c := range st.Changes {
			fmt.Fprintf(&b, "  %s %s: %s → %s\n", c.File, c.Param, c.Old, c.New)
		}
		// cross-rule summary
		values := map[string]string{}
		var bk []model.Backend
		if snap := s.Collector.Collect(ctx); snap != nil {
			for k, v := range snap.Settings {
				values[k] = v
			}
			bk = snap.Backends
		}
		for _, r := range pgconf.CrossRules(values, bk) {
			fmt.Fprintf(&b, "  %s %s\n", map[bool]string{true: "ok", false: "FAIL"}[r.OK], r.Detail)
		}
		order := "non-leaders first, leader last"
		if snap := s.Collector.Collect(ctx); snap != nil && snap.Watchdog.Available {
			order += " (VIP moves last)"
		}
		fmt.Fprintf(&b, "order: %s\nbackup: %s\nlock: cluster\n",
			order, path.Join(s.Cfg.StateDir, "config", "<new-version>"))
		b.WriteString(planText(pf, ""))
		return b.String()
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return s.configApplySteps(ctx, rec, &a)
	}
	return s.RunControl(ctx, &a, o)
}

// hostState tracks per-host apply state for rollback/failure reporting.
type hostState struct {
	host    config.Host
	path    string
	before  []byte
	after   []byte
	written bool
	loaded  bool // reloaded/restarted
	leader  bool
}

func (s *Service) configApplySteps(ctx context.Context, rec *StepRecorder, a *Action) error {
	st, err := s.LoadStaged()
	if err != nil {
		return err
	}
	// (1) lock
	if s.Lock != nil {
		var rel func()
		err := rec.Step("lock", func() (string, error) {
			var lerr error
			rel, lerr = s.Lock.Acquire(ctx, remote.LockOwner{
				Operator: s.Recorder.Identity.Operator(),
				Host:     s.Recorder.Identity.Hostname, PID: s.Recorder.Identity.PID,
			}, false)
			if lerr != nil {
				return "", lerr
			}
			return "acquired", nil
		})
		if err != nil {
			return err
		}
		defer rel()
	}
	hosts := s.Cfg.PgpoolHosts()
	files := map[string]bool{}
	for _, c := range st.Changes {
		files[c.File] = true
	}
	var fileList []string
	for f := range files {
		fileList = append(fileList, f)
	}
	sort.Strings(fileList)
	// (2) read + drift checks per file
	var states []hostState
	snapRead := s.Collector.Collect(ctx)
	err = rec.Step("read", func() (string, error) {
		for _, h := range hosts {
			leader := h.Name == leaderName(s, snapRead)
			for _, f := range fileList {
				p := confPath(h, f)
				b, rerr := s.ConfigRemote.ReadFile(ctx, h, p)
				if rerr != nil {
					return "", rerr
				}
				states = append(states, hostState{host: h, path: p, before: b, leader: leader})
			}
		}
		// byte-identical across hosts per file
		for _, f := range fileList {
			var first []byte
			var diffs []string
			for i, st2 := range states {
				if !strings.HasSuffix(st2.path, f) {
					continue
				}
				if first == nil {
					first = st2.before
				} else if sha(st2.before) != sha(first) {
					diffs = append(diffs, states[i].host.Name+" "+sha(st2.before)[:12])
				}
			}
			if len(diffs) > 0 {
				return "", exitcode.New(4, "config drift between hosts: "+f).
					WithDetail(strings.Join(diffs, "; ")).
					WithHint("poolwarden config get " + f)
			}
			if want := st.SHAs[f]; want != "" && sha(first) != want {
				return "", exitcode.New(4, "file changed since staged: "+f).
					WithHint("poolwarden config discard")
			}
		}
		return "read", nil
	})
	if err != nil {
		return err
	}
	// compute new bytes per file once
	newBytes := map[string][]byte{}
	for _, f := range fileList {
		var cur []byte
		for _, st2 := range states {
			if strings.HasSuffix(st2.path, f) {
				cur = st2.before
			}
		}
		nb, cerr := ApplyFileBytes(f, cur, st.Changes)
		if cerr != nil {
			return cerr
		}
		newBytes[f] = nb
	}
	// (3) backup into <StateDir>/config/<version>/<host>/<basename>
	version := s.now().UTC().Format("20060102T150405")
	var diffText strings.Builder
	err = rec.Step("backup", func() (string, error) {
		for i := range states {
			st2 := &states[i]
			for f, nb := range newBytes {
				if strings.HasSuffix(st2.path, f) {
					st2.after = nb
					diffText.WriteString(pgconf.Diff(st2.host.Name+":"+f+".before",
						st2.host.Name+":"+f+".after", st2.before, nb))
				}
			}
			dst := path.Join(s.backupDir(version), st2.host.Name, path.Base(st2.path))
			if werr := s.fsys().MkdirAll(path.Dir(dst), 0o700); werr != nil {
				return "", werr
			}
			if werr := s.fsys().WriteFileAtomic(dst, st2.before, true); werr != nil {
				return "", werr
			}
		}
		return "backed up", nil
	})
	if err != nil {
		return err
	}
	// manifest
	man := Manifest{Version: version, Time: s.now(), Operator: s.Recorder.Identity.Operator(),
		Diff: diffText.String()}
	for _, h := range hosts {
		man.Hosts = append(man.Hosts, h.Name)
	}
	_ = man.Hosts
	for _, st2 := range states {
		mf := ManifestFile{Host: st2.host.Name, Path: st2.path, SHA256Before: sha(st2.before)}
		if len(st2.after) > 0 {
			mf.SHA256After = sha(st2.after)
		}
		man.Files = append(man.Files, mf)
	}
	man.Summary = fmt.Sprintf("%d change(s)", len(st.Changes))
	mb, _ := json.MarshalIndent(man, "", "  ")
	s.fsys().WriteFileAtomic(path.Join(s.backupDir(version), "manifest.json"), mb, true)
	a.BackupVersion = version
	if a.Change == nil {
		a.Change = &audit.Change{Diff: diffText.String(), Before: map[string]string{}, After: map[string]string{}}
		for _, st2 := range states {
			a.Change.Before[st2.host.Name+":"+st2.path] = sha(st2.before)
			if len(st2.after) > 0 {
				a.Change.After[st2.host.Name+":"+st2.path] = sha(st2.after)
			}
		}
	}
	// (4) write non-leaders first, leader last
	var ordered []int
	for i, st2 := range states {
		if !st2.leader {
			ordered = append(ordered, i)
		}
	}
	for i, st2 := range states {
		if st2.leader {
			ordered = append(ordered, i)
		}
	}
	restart := false
	var names []string
	for _, c := range st.Changes {
		if c.Op == "set" {
			names = append(names, c.Param)
		}
	}
	restart = pgconf.Context(names) == "restart"
	rollback := func(cause error) error {
		// restore written hosts in reverse order
		var failed []string
		for i := len(ordered) - 1; i >= 0; i-- {
			st2 := &states[ordered[i]]
			if st2.written {
				if werr := s.ConfigRemote.WriteFileAtomic(ctx, st2.host, st2.path, st2.before); werr != nil {
					failed = append(failed, st2.host.Name+" restore✗")
				}
			}
			if st2.loaded {
				// re-apply previous state: restart again
				if restart {
					s.ConfigRemote.Exec(ctx, st2.host,
						[]string{s.Cfg.Bins.Systemctl, "restart", serviceName(st2.host)}, nil)
				}
			}
		}
		if len(failed) > 0 {
			return exitcode.New(6, "apply failed and rollback incomplete").
				WithCause(cause.Error()).
				WithHint("manual recovery per host: cp <backup> <conf> && systemctl restart " + serviceName(hosts[0]))
		}
		return exitcode.New(5, "apply failed; rolled back").
			WithCause(cause.Error()).
			WithDetail(hostStateTable(states))
	}
	err = rec.Step("write", func() (string, error) {
		for _, i := range ordered {
			st2 := &states[i]
			nb := newBytes[path.Base(st2.path)]
			if nb == nil {
				for f, n := range newBytes {
					if strings.HasSuffix(st2.path, f) {
						nb = n
					}
				}
			}
			if nb == nil {
				continue
			}
			if werr := s.ConfigRemote.WriteFileAtomic(ctx, st2.host, st2.path, nb); werr != nil {
				return "", werr
			}
			st2.written = true
		}
		return "written", nil
	})
	if err != nil {
		return rollback(err)
	}
	// (5) reload or (6) restart
	if !restart {
		err = rec.Step("reload", func() (string, error) {
			if _, rerr := s.PCP.ReloadConfig(ctx, "c"); rerr != nil {
				return "", rerr
			}
			for i := range states {
				states[i].loaded = true
			}
			return "reloaded", nil
		})
		if err != nil {
			return rollback(err)
		}
		err = rec.Step("verify", func() (string, error) {
			for _, st2 := range states {
				for _, c := range st.Changes {
					if c.Op != "set" || (!st2.loaded && !st2.written) {
						continue
					}
					// pgpool reloads asynchronously: poll the live value.
					deadline := s.now().Add(15 * time.Second)
					for {
						got, verr := s.ShowParam(ctx, st2.host, c.Param)
						if verr != nil {
							return "", verr
						}
						if normVal(got) == normVal(c.New) {
							break
						}
						if !s.now().Before(deadline) {
							return "", fmt.Errorf("%s: %s live %q ≠ %q",
								st2.host.Name, c.Param, got, c.New)
						}
						if serr := s.sleep(ctx, time.Second); serr != nil {
							return "", serr
						}
					}
				}
			}
			return "verified", nil
		})
		if err != nil {
			return rollback(err)
		}
	} else {
		for _, i := range ordered {
			st2 := &states[i]
			err := rec.Step("restart "+st2.host.Name, func() (string, error) {
				if _, rerr := s.ConfigRemote.Exec(ctx, st2.host,
					[]string{s.Cfg.Bins.Systemctl, "restart", serviceName(st2.host)}, nil); rerr != nil {
					return "", rerr
				}
				st2.loaded = true
				// poll rejoin via watchdog status (fakeable: collect snapshots)
				if s.Clock != nil {
					end := s.now().Add(120 * time.Second)
					for s.now().Before(end) {
						snap := s.Collector.Collect(ctx)
						if snap != nil && snap.Pgpool.Reachable {
							return "rejoined", nil
						}
						s.sleep(ctx, 2*time.Second)
					}
				}
				return "restarted", nil
			})
			if err != nil {
				return rollback(err)
			}
		}
	}
	return nil
}

func leaderName(s *Service, snap *model.Snapshot) string {
	if snap == nil || !snap.Watchdog.Available {
		// watchdog off → local host last
		if s.Local != "" {
			return s.Local
		}
		return ""
	}
	for _, n := range snap.Watchdog.Nodes {
		if n.Status == "LEADER" {
			return n.Name
		}
	}
	return ""
}

func serviceName(h config.Host) string {
	if h.Pgpool != nil && h.Pgpool.Service != "" {
		return h.Pgpool.Service
	}
	return "pgpool-II"
}

func normVal(v string) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "on", "true", "yes", "1":
		return "on"
	case "off", "false", "no", "0":
		return "off"
	}
	return v
}

func hostStateTable(states []hostState) string {
	var b strings.Builder
	for _, st := range states {
		fmt.Fprintf(&b, "%s %s written=%v loaded=%v\n", st.host.Name, st.path, st.written, st.loaded)
	}
	return b.String()
}

// History lists backup manifests newest-first.
func (s *Service) ConfigHistory() ([]Manifest, error) {
	entries, err := s.fsys().ReadDir(path.Join(s.Cfg.StateDir, "config"))
	if err != nil {
		return nil, nil // no backups yet
	}
	var out []Manifest
	for i := len(entries) - 1; i >= 0; i-- {
		mb, err := s.fsys().ReadFile(path.Join(s.Cfg.StateDir, "config", entries[i].Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(mb, &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// Rollback stages restore-changes? No: rollback replays the backup bytes via
// the apply flow as a "restore" transaction.
func (s *Service) Rollback(ctx context.Context, version string, o ControlOpts) (*ControlResult, error) {
	// For Part 2 we restore files by writing the `before` bytes through the
	// same write path: implemented as a staged payload-free transaction.
	a := Action{Name: "config rollback", Command: "config rollback " + version,
		Token: "apply", ForceToken: "apply"}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		dir := s.backupDir(version)
		if _, err := s.fsys().ReadFile(path.Join(dir, "manifest.json")); err != nil {
			return nil, exitcode.New(exitcode.Usage, "no such backup version "+version).
				WithHint("poolwarden config history")
		}
		// token class matches the apply flow: restart-class diff → "restart"
		if d, _ := s.rollbackDiff(ctx, version); pgconf.Context(d) == "restart" {
			a.Token, a.ForceToken = "restart", "restart"
		}
		return &model.PreflightResult{Action: "config rollback", Verdict: "GO",
			Checks: []model.Check{{Name: "backup", Result: "OK", Detail: version}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, "restore config from backup "+version)
	}
	var differing []string
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("restore", func() (string, error) {
			mb, _ := s.fsys().ReadFile(path.Join(s.backupDir(version), "manifest.json"))
			var man Manifest
			if err := json.Unmarshal(mb, &man); err != nil {
				return "", err
			}
			if differing == nil {
				if d, _ := s.rollbackDiff(ctx, version); len(d) > 0 {
					differing = d
				}
			}
			for _, mf := range man.Files {
				var h config.Host
				found := false
				for _, hh := range s.Cfg.PgpoolHosts() {
					if hh.Name == mf.Host {
						h = hh
						found = true
					}
				}
				if !found {
					continue
				}
				data, rerr := s.fsys().ReadFile(path.Join(s.backupDir(version), mf.Host, path.Base(mf.Path)))
				if rerr != nil {
					return "", rerr
				}
				if werr := s.ConfigRemote.WriteFileAtomic(ctx, h, mf.Path, data); werr != nil {
					return "", werr
				}
			}
			return "restored", nil
		})
	}
	a.Verify = func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
		var vo VerifyOutcome
		restart := pgconf.Context(differing) == "restart"
		for _, n := range differing {
			if _, ok := pgconf.Lookup(n); !ok {
				restart = true
			}
		}
		if restart {
			for _, h := range s.Cfg.PgpoolHosts() {
				hh := h
				err := rec.Step("restart "+hh.Name, func() (string, error) {
					if _, rerr := s.ConfigRemote.Exec(ctx, hh,
						[]string{s.Cfg.Bins.Systemctl, "restart", serviceName(hh)}, nil); rerr != nil {
						return "", rerr
					}
					return "restarted", nil
				})
				if err != nil {
					return vo, err
				}
			}
		} else {
			err := rec.Step("reload", func() (string, error) {
				if _, rerr := s.PCP.ReloadConfig(ctx, "c"); rerr != nil {
					return "", rerr
				}
				return "reloaded", nil
			})
			if err != nil {
				return vo, err
			}
		}
		vo.OK = true
		return vo, nil
	}
	return s.RunControl(ctx, &a, o)
}

// rollbackDiff returns params whose restored bytes differ from the live files.
func (s *Service) rollbackDiff(ctx context.Context, version string) ([]string, error) {
	mb, err := s.fsys().ReadFile(path.Join(s.backupDir(version), "manifest.json"))
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		return nil, err
	}
	var differing []string
	for _, mf := range man.Files {
		var h config.Host
		for _, hh := range s.Cfg.PgpoolHosts() {
			if hh.Name == mf.Host {
				h = hh
			}
		}
		if h.Name == "" {
			continue
		}
		data, err := s.fsys().ReadFile(path.Join(s.backupDir(version), mf.Host, path.Base(mf.Path)))
		if err != nil {
			continue
		}
		cur, err := s.ConfigRemote.ReadFile(ctx, h, mf.Path)
		if err != nil {
			continue
		}
		cf, _ := pgconf.Parse(cur)
		nf, _ := pgconf.Parse(data)
		names := map[string]bool{}
		for _, f := range []*pgconf.File{nf, cf} {
			for _, l := range f.Lines {
				if l.Kind == pgconf.KindParam && !l.Commented {
					names[l.Name] = true
				}
			}
		}
		for n := range names {
			cv, cok, _ := cf.Get(n)
			nv, nok, _ := nf.Get(n)
			if cok != nok || cv != nv {
				differing = append(differing, n)
			}
		}
	}
	return differing, nil
}

package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pgconf"
)

// ParamRow is one config get/list row.
type ParamRow struct {
	Param, File, Live, Default, Context, Drift string
	PerHost                                    map[string]string
}

// readConfs returns parsed pgpool.conf per pgpool host (best-effort).
func (s *Service) readConfs(ctx context.Context) map[string]*pgconf.File {
	out := map[string]*pgconf.File{}
	if s.ConfigRemote == nil {
		return out
	}
	for _, h := range s.Cfg.PgpoolHosts() {
		b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pgpool.conf"))
		if err != nil {
			continue
		}
		if f, err := pgconf.Parse(b); err == nil {
			out[h.Name] = f
		}
	}
	return out
}

// FileSettingsMap feeds diagnose.Input.FileSettings.
func (s *Service) FileSettingsMap(ctx context.Context) map[string]map[string]string {
	out := map[string]map[string]string{}
	for name, f := range s.readConfs(ctx) {
		m := map[string]string{}
		for _, l := range f.Lines {
			if l.Kind == pgconf.KindParam && !l.Commented {
				m[l.Name] = l.Value
			}
		}
		out[name] = m
	}
	return out
}

// ConfigGet implements `config get`.
func (s *Service) ConfigGet(ctx context.Context, name string) (*ParamRow, error) {
	p, ok := pgconf.Lookup(name)
	if !ok {
		return nil, exitcode.New(exitcode.Usage, "unknown parameter (not in the 4.4/4.5 catalogue): "+name).
			WithHint("poolwarden config describe " + name)
	}
	row := &ParamRow{Param: name, File: "pgpool.conf", Default: p.Default,
		Context: p.Context, PerHost: map[string]string{}}
	snap := s.Collector.Collect(ctx)
	if snap != nil {
		row.Live = snap.Settings[name]
	}
	confs := s.readConfs(ctx)
	var files []string
	for h, f := range confs {
		v, found, _ := f.Get(name)
		if !found {
			v = p.Default
		}
		row.PerHost[h] = v
		files = append(files, v)
	}
	drift := ""
	seen := map[string]bool{}
	for _, v := range files {
		seen[v] = true
	}
	if len(seen) > 1 {
		drift = "drift"
	} else if row.Live != "" && len(files) > 0 && normVal(files[0]) != normVal(row.Live) {
		drift = "file≠live"
	}
	row.Drift = drift
	return row, nil
}

// ConfigList implements `config list`.
type ConfigListOpts struct {
	Changed, Drift bool
	Grep           string
}

func (s *Service) ConfigList(ctx context.Context, o ConfigListOpts) ([]ParamRow, error) {
	snap := s.Collector.Collect(ctx)
	confs := s.readConfs(ctx)
	var names []string
	seenNames := map[string]bool{}
	for h := range confs {
		for _, l := range confs[h].Lines {
			if l.Kind == pgconf.KindParam && !seenNames[l.Name] {
				seenNames[l.Name] = true
				names = append(names, l.Name)
			}
		}
	}
	if snap != nil {
		for k := range snap.Settings {
			if !seenNames[k] {
				seenNames[k] = true
				names = append(names, k)
			}
		}
	}
	sort.Strings(names)
	var out []ParamRow
	for _, n := range names {
		p, ok := pgconf.Lookup(n)
		row := ParamRow{Param: n, File: "pgpool.conf", PerHost: map[string]string{}}
		if ok {
			row.Default = p.Default
			row.Context = p.Context
		}
		if snap != nil {
			row.Live = snap.Settings[n]
		}
		var files []string
		for h, f := range confs {
			v, found, _ := f.Get(n)
			if !found {
				v = row.Default
			}
			row.PerHost[h] = v
			files = append(files, v)
		}
		u := map[string]bool{}
		for _, v := range files {
			u[v] = true
		}
		if len(u) > 1 {
			row.Drift = "drift"
		}
		changed := row.Live != "" && len(files) > 0 && normVal(files[0]) != normVal(row.Live)
		if o.Changed && !changed && row.Drift == "" {
			continue
		}
		if o.Drift && row.Drift == "" {
			continue
		}
		if o.Grep != "" && !strings.Contains(n, o.Grep) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// ConfigDescribe implements `config describe`.
func (s *Service) ConfigDescribe(name string) (pgconf.Param, error) {
	p, ok := pgconf.Lookup(name)
	if !ok {
		return p, exitcode.New(exitcode.Usage, "unknown parameter: "+name)
	}
	return p, nil
}

// ConfigSet validates + stages a set.
func (s *Service) ConfigSet(ctx context.Context, name, value, operator string) error {
	p, ok := pgconf.Lookup(name)
	if !ok {
		return exitcode.New(exitcode.Usage, "unknown parameter (not in the 4.4/4.5 catalogue): "+name).
			WithHint("poolwarden config describe " + name)
	}
	if err := pgconf.ValidateValue(p, value); err != nil {
		return exitcode.New(exitcode.Usage, err.Error())
	}
	// old value from conf (first host) or live
	old := p.Default
	if s.ConfigRemote != nil && len(s.Cfg.PgpoolHosts()) > 0 {
		h := s.Cfg.PgpoolHosts()[0]
		if b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pgpool.conf")); err == nil {
			if f, err := pgconf.Parse(b); err == nil {
				if v, found, _ := f.Get(name); found {
					old = v
				}
			}
		}
	}
	return s.StageChange(ctx, pgconf.Change{File: "pgpool.conf", Op: "set",
		Param: name, Old: old, New: value, Operator: operator})
}

// ConfigUnset stages an unset.
func (s *Service) ConfigUnset(ctx context.Context, name, operator string) error {
	if _, ok := pgconf.Lookup(name); !ok {
		return exitcode.New(exitcode.Usage, "unknown parameter: "+name)
	}
	return s.StageChange(ctx, pgconf.Change{File: "pgpool.conf", Op: "unset",
		Param: name, Operator: operator})
}

// ConfigDiff renders staged diffs per file.
func (s *Service) ConfigDiff(ctx context.Context) (string, error) {
	st, err := s.LoadStaged()
	if err != nil || len(st.Changes) == 0 {
		return "", err
	}
	var b strings.Builder
	files := map[string]bool{}
	for _, c := range st.Changes {
		files[c.File] = true
	}
	var fl []string
	for f := range files {
		fl = append(fl, f)
	}
	sort.Strings(fl)
	var cur []byte
	if s.ConfigRemote != nil && len(s.Cfg.PgpoolHosts()) > 0 {
		h := s.Cfg.PgpoolHosts()[0]
		cur, _ = s.ConfigRemote.ReadFile(ctx, h, confPath(h, fl[0]))
	}
	for _, f := range fl {
		nb, _ := ApplyFileBytes(f, cur, st.Changes)
		b.WriteString(pgconf.Diff(f, f+" (staged)", cur, nb))
		for _, c := range st.Changes {
			if c.File == f {
				fmt.Fprintf(&b, "# staged by %s at %s\n", c.Operator, c.At.Format("15:04:05"))
			}
		}
	}
	return b.String(), nil
}

// ConfigValidate runs catalogue + cross rules.
type ValidateRow struct {
	Name, Result, Detail string
}

func (s *Service) ConfigValidate(ctx context.Context) ([]ValidateRow, int, error) {
	snap := s.Collector.Collect(ctx)
	values := map[string]string{}
	if snap != nil {
		values = snap.Settings
	}
	var rows []ValidateRow
	fails := 0
	// catalogue value checks on staged changes
	st, _ := s.LoadStaged()
	for _, c := range st.Changes {
		if c.Op != "set" {
			continue
		}
		p, ok := pgconf.Lookup(c.Param)
		if !ok {
			rows = append(rows, ValidateRow{c.Param, "FAIL", "unknown param"})
			fails++
			continue
		}
		if err := pgconf.ValidateValue(p, c.New); err != nil {
			rows = append(rows, ValidateRow{c.Param, "FAIL", err.Error()})
			fails++
		} else {
			rows = append(rows, ValidateRow{c.Param, "OK", c.New})
		}
	}
	var backends []model.Backend
	if snap != nil {
		backends = snap.Backends
	}
	for _, r := range pgconf.CrossRules(values, backends) {
		res := "OK"
		if !r.OK {
			res = "FAIL"
			if !pgconf.WarnRules[r.Name] {
				fails++
			} else {
				res = "WARN"
			}
		}
		rows = append(rows, ValidateRow{r.Name, res, r.Detail})
	}
	return rows, fails, nil
}

// ConfigLoad stages diffs from a config file's bytes.
func (s *Service) ConfigLoad(ctx context.Context, data []byte, operator string) (int, error) {
	f, err := pgconf.Parse(data)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range f.Lines {
		if l.Kind == pgconf.KindParam && !l.Commented {
			if err := s.ConfigSet(ctx, l.Name, l.Value, operator); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// ConfigExport returns the effective config bytes (conf + staged).
func (s *Service) ConfigExport(ctx context.Context) ([]byte, error) {
	st, err := s.LoadStaged()
	if err != nil {
		return nil, err
	}
	var cur []byte
	if s.ConfigRemote != nil && len(s.Cfg.PgpoolHosts()) > 0 {
		h := s.Cfg.PgpoolHosts()[0]
		cur, _ = s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pgpool.conf"))
	}
	return ApplyFileBytes("pgpool.conf", cur, st.Changes)
}

// BackendRemove guards + stages a backend removal.
func (s *Service) BackendRemove(ctx context.Context, id int, operator string) error {
	snap := s.Collector.Collect(ctx)
	if snap != nil {
		for i, b := range snap.Backends {
			if (b.NodeID == id || i == id) && !b.InRecovery {
				return exitcode.New(4, "cannot remove the current primary").
					WithHint("poolwarden promote first")
			}
		}
	}
	return s.StageChange(ctx, pgconf.Change{File: "pgpool.conf", Op: "backend_remove",
		Param: fmt.Sprint(id), Operator: operator})
}

// SecretPrompter reads a password without echo (TTY only).
type SecretPrompter interface {
	AskSecret(ctx context.Context, prompt string) (string, error)
}

// PCPUserAdd runs an immediate pcp.conf transaction (I27: never staged).
func (s *Service) PCPUserSet(ctx context.Context, user, password, operator string, o ControlOpts) error {
	pw := password
	if pw == "" {
		if !o.TTY {
			return exitcode.New(exitcode.Usage, "password prompt needs a TTY")
		}
		sp, ok := s.Confirmer.(SecretPrompter)
		if !ok {
			return exitcode.New(exitcode.Usage, "password prompt needs a TTY")
		}
		p1, err := sp.AskSecret(ctx, "New password: ")
		if err != nil {
			return err
		}
		p2, err := sp.AskSecret(ctx, "Confirm password: ")
		if err != nil {
			return err
		}
		if p1 != p2 {
			return exitcode.New(exitcode.Usage, "passwords do not match")
		}
		pw = p1
	}
	hash := pgconf.MD5Hex(pw)
	a := Action{Name: "pcpuser set", Command: "pcpuser set " + user,
		Token: user, ForceToken: user}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		return &model.PreflightResult{Action: "pcpuser set", Verdict: "GO",
			Checks: []model.Check{{Name: "pcp.conf", Result: "OK", Detail: "user " + user}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, "write pcp.conf on all pgpool hosts (md5 hash, password never logged)")
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("write pcp.conf", func() (string, error) {
			for _, h := range s.Cfg.PgpoolHosts() {
				b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pcp.conf"))
				if err != nil {
					return "", err
				}
				pf, err := pgconf.ParsePCP(b)
				if err != nil {
					return "", err
				}
				pf.SetUser(user, hash)
				if err := s.ConfigRemote.WriteFileAtomic(ctx, h, confPath(h, "pcp.conf"), pf.Bytes()); err != nil {
					return "", err
				}
			}
			return "written", nil
		})
	}
	_, err := s.RunControl(ctx, &a, o)
	return err
}

// PCPUserList / Remove.
func (s *Service) PCPUserList(ctx context.Context) ([]string, error) {
	if s.ConfigRemote == nil || len(s.Cfg.PgpoolHosts()) == 0 {
		return nil, nil
	}
	h := s.Cfg.PgpoolHosts()[0]
	b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pcp.conf"))
	if err != nil {
		return nil, err
	}
	f, err := pgconf.ParsePCP(b)
	if err != nil {
		return nil, err
	}
	return f.Users(), nil
}

func (s *Service) PCPUserRemove(ctx context.Context, user string, o ControlOpts) error {
	a := Action{Name: "pcpuser remove", Command: "pcpuser remove " + user,
		Token: user, ForceToken: user}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		return &model.PreflightResult{Action: "pcpuser remove", Verdict: "GO",
			Checks: []model.Check{{Name: "pcp.conf", Result: "OK", Detail: "remove " + user}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, "remove pcp user "+user+" on all pgpool hosts")
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("write pcp.conf", func() (string, error) {
			for _, h := range s.Cfg.PgpoolHosts() {
				b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pcp.conf"))
				if err != nil {
					return "", err
				}
				pf, err := pgconf.ParsePCP(b)
				if err != nil {
					return "", err
				}
				pf.RemoveUser(user)
				if err := s.ConfigRemote.WriteFileAtomic(ctx, h, confPath(h, "pcp.conf"), pf.Bytes()); err != nil {
					return "", err
				}
			}
			return "removed", nil
		})
	}
	_, err := s.RunControl(ctx, &a, o)
	return err
}

// PoolUserSet runs pg_md5/pg_enc -p with the password on stdin.
// Tool choice: pg_enc when existing pool_passwd entries are AES-prefixed,
// else pg_md5 -m (see `pooluser passwd --help`, Long help documents this).
func (s *Service) PoolUserSet(ctx context.Context, user, password string, o ControlOpts) error {
	if !o.TTY && password == "" {
		return exitcode.New(exitcode.Usage, "password prompt needs a TTY")
	}
	pw := password
	if pw == "" {
		sp, ok := s.Confirmer.(SecretPrompter)
		if !ok {
			return exitcode.New(exitcode.Usage, "password prompt needs a TTY")
		}
		p1, err := sp.AskSecret(ctx, "New password: ")
		if err != nil {
			return err
		}
		p2, err := sp.AskSecret(ctx, "Confirm password: ")
		if err != nil {
			return err
		}
		if p1 != p2 {
			return exitcode.New(exitcode.Usage, "passwords do not match")
		}
		pw = p1
	}
	a := Action{Name: "pooluser set", Command: "pooluser set " + user,
		Token: user, ForceToken: user}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		return &model.PreflightResult{Action: "pooluser set", Verdict: "GO",
			Checks: []model.Check{{Name: "pool_passwd", Result: "OK", Detail: "user " + user}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, "update pool_passwd via pg_md5/pg_enc -p (password on stdin, never on argv)")
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("pool_passwd update", func() (string, error) {
			// md5 entries are computed in-process: "md5"+md5(password||username).
			// AES entries go through pg_enc -i <tmpfile> (password never on argv).
			for _, h := range s.Cfg.PgpoolHosts() {
				pp := confPath(h, "pool_passwd")
				aes := false
				var b []byte
				if rb, err := s.ConfigRemote.ReadFile(ctx, h, pp); err == nil {
					b = rb
					for _, u := range pgconf.ParsePoolPasswd(b) {
						if strings.HasPrefix(u.Hash, "AES") {
							aes = true
						}
						break
					}
				}
				if aes {
					tmp := s.Cfg.StateDir + "/.pooluser-" + randHex(6)
					s.fsys().WriteFileAtomic(tmp, []byte(user+":"+pw+"\n"), true)
					res, err := s.ConfigRemote.Exec(ctx, h,
						[]string{path.Join(s.Cfg.PCP.BinDir, "pg_enc"), "-m",
							"-k", "/var/lib/pgsql/.pgpoolkey",
							"-f", confPath(h, "pgpool.conf"), "-i", tmp}, nil)
					s.fsys().WriteFileAtomic(tmp, make([]byte, 64), true)
					s.fsys().Remove(tmp)
					if err != nil {
						return "", err
					}
					if res.Exit != 0 {
						return "", fmt.Errorf("pg_enc exit %d: %s", res.Exit, res.Stderr)
					}
					continue
				}
				// md5 path: rewrite pool_passwd in-process
				line := user + ":md5" + md5Hex(pw+user) + "\n"
				var kept []string
				done := false
				for _, l := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(l, user+":") {
						if !done {
							kept = append(kept, strings.TrimRight(line, "\n"))
							done = true
						}
						continue
					}
					kept = append(kept, l)
				}
				if !done {
					kept = append(kept, line)
				}
				if err := s.ConfigRemote.WriteFileAtomic(ctx, h, pp,
					[]byte(strings.Join(kept, "\n"))); err != nil {
					return "", err
				}
			}
			return "written", nil
		})
	}
	_, err := s.RunControl(ctx, &a, o)
	return err
}

// PoolUserList reads pool_passwd (hashes only, never returned).
func (s *Service) PoolUserList(ctx context.Context) ([]string, error) {
	if s.ConfigRemote == nil || len(s.Cfg.PgpoolHosts()) == 0 {
		return nil, nil
	}
	h := s.Cfg.PgpoolHosts()[0]
	b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pool_passwd"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range pgconf.ParsePoolPasswd(b) {
		out = append(out, u.User)
	}
	return out, nil
}

// HBAList reads rules from the first pgpool host.
func (s *Service) HBAList(ctx context.Context) ([]pgconf.HBARule, error) {
	if s.ConfigRemote == nil || len(s.Cfg.PgpoolHosts()) == 0 {
		return nil, nil
	}
	h := s.Cfg.PgpoolHosts()[0]
	b, err := s.ConfigRemote.ReadFile(ctx, h, confPath(h, "pool_hba.conf"))
	if err != nil {
		return nil, err
	}
	f, err := pgconf.ParseHBA(b)
	if err != nil {
		return nil, err
	}
	return f.Rules(), nil
}

// HBAAdd stages a rule.
func (s *Service) HBAAdd(ctx context.Context, r pgconf.HBARule, operator string) error {
	payload, _ := json.Marshal(r)
	return s.StageChange(ctx, pgconf.Change{File: "pool_hba.conf", Op: "hba_add",
		Param: r.User, Payload: payload, Operator: operator})
}

// HBARemove stages a removal by index.
func (s *Service) HBARemove(ctx context.Context, idx, operator string) error {
	return s.StageChange(ctx, pgconf.Change{File: "pool_hba.conf", Op: "hba_remove",
		Param: idx, Operator: operator})
}

// PoolUserRemove rewrites pool_passwd in-process (pgpool has no delete tool)
// on every pgpool host, then reloads (UNVERIFIED: pool_passwd re-read on
// reload is assumed from the docs).
func (s *Service) PoolUserRemove(ctx context.Context, user string, o ControlOpts) error {
	a := Action{Name: "pooluser remove", Command: "pooluser remove " + user,
		Token: user, ForceToken: user}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		return &model.PreflightResult{Action: "pooluser remove", Verdict: "GO",
			Checks: []model.Check{{Name: "pool_passwd", Result: "OK", Detail: "remove " + user}}}, nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, "remove "+user+" from pool_passwd on all pgpool hosts (in-process rewrite)")
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step("rewrite pool_passwd", func() (string, error) {
			for _, h := range s.Cfg.PgpoolHosts() {
				pp := confPath(h, "pool_passwd")
				b, err := s.ConfigRemote.ReadFile(ctx, h, pp)
				if err != nil {
					return "", err
				}
				var kept []string
				removed := false
				for _, line := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), user+":") {
						removed = true
						continue
					}
					kept = append(kept, line)
				}
				if !removed {
					return "", fmt.Errorf("%s: user %s not in pool_passwd", h.Name, user)
				}
				if err := s.ConfigRemote.WriteFileAtomic(ctx, h, pp,
					[]byte(strings.Join(kept, "\n"))); err != nil {
					return "", err
				}
			}
			return "removed", nil
		})
	}
	a.Verify = func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
		var vo VerifyOutcome
		err := rec.Step("reload", func() (string, error) {
			if _, rerr := s.PCP.ReloadConfig(ctx, "c"); rerr != nil {
				return "", rerr
			}
			vo.OK = true
			return "reloaded", nil
		})
		return vo, err
	}
	_, err := s.RunControl(ctx, &a, o)
	return err
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	return hex.EncodeToString(b)
}

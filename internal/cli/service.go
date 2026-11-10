package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/backendsql"
	"github.com/smarsh/poolwarden/internal/collector"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/pgpoolsql"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/service"
	"github.com/smarsh/poolwarden/internal/toollog"
)

// newService wires the real adapters for one command invocation.
func newService(a *app) *service.Service {
	cfg := a.cfg
	if cfg == nil {
		d := config.Defaults() // noConfigLoad commands (init, config install)
		cfg = &d
	}
	pr := &pcp.PCP{R: pcp.OSRunner{}, Cfg: cfg.PCP}
	var source service.SnapshotSource
	if a.deps.Source != nil {
		source = a.deps.Source
	} else {
		pool := remote.NewPool(remote.SSHDialer{}, cfg.SSH)
		source = &collector.Collector{
			Cfg: cfg, Pgpool: pgpoolsql.PGDialer{}, Backends: backendsql.PGProber{},
			PCP: pr, Remote: pool, Clock: a.deps.Clock,
		}
	}
	diag := service.Diagnoser(diagnose.NewEngine())
	if a.deps.Diagnose != nil {
		diag = a.deps.Diagnose
	}
	var proc service.ProcInfoer = pr
	if a.deps.ProcInfo != nil {
		proc = a.deps.ProcInfo
	}
	local := ""
	for _, h := range cfg.Hosts {
		if remote.IsLocal(h) {
			local = h.Name
		}
	}
	var hist service.HistoryReader
	if a.deps.History != nil {
		hist = a.deps.History
	} else {
		hist = &history.Store{Dir: cfg.StateDir + "/samples", Clock: a.deps.Clock}
	}
	// error-event collector: one log source per host×component
	var ecoll *errevents.Collector
	if a.deps.ErrorsColl != nil {
		ecoll = a.deps.ErrorsColl
	} else {
		pool := remote.NewPool(remote.SSHDialer{}, cfg.SSH)
		ecoll = &errevents.Collector{
			History:    histAdapter(hist),
			PCPResults: pr.Results,
			Tool:       a.tool,
			Clock:      a.deps.Clock,
		}
		for _, h := range cfg.Hosts {
			// lazy: the session dials on first log read with the caller's ctx
			var sess remote.Session = &remote.LazySession{Pool: pool, Host: h}
			if h.Pgpool != nil && h.Pgpool.LogDir != "" {
				pfx, unk := logsrc.ParsePrefix("pgpool", orStr(h.Pgpool.LogLinePrefix, "%m: %a pid %p: "))
				for _, u := range unk {
					if a.tool != nil {
						a.tool.Once("prefix:"+u, "unknown log_line_prefix escape", "escape", u)
					}
				}
				ecoll.Logs = append(ecoll.Logs, &logsrc.FileSource{
					Host: h.Name, Component: "pgpool", Dir: h.Pgpool.LogDir,
					Filename: "pgpool-%Y-%m-%d_%H%M%S.log",
					Prefix:   pfx, Session: sess})
			}
			if h.PG != nil && h.PG.LogDir != "" {
				pfx, _ := logsrc.ParsePrefix("pg", h.PG.LogLinePrefix)
				ecoll.Logs = append(ecoll.Logs, &logsrc.FileSource{
					Host: h.Name, Component: "pg", Dir: h.PG.LogDir,
					Filename: h.PG.LogFilename, Prefix: pfx, Session: sess})
			}
		}
	}
	tool := a.tool
	if tool == nil {
		tool = toollog.Discard()
	}
	// audit identity + recorder (shell reuses it in M10)
	env := a.deps.Env
	if env == nil {
		env = audit.RealEnv()
	}
	id := audit.Resolve(env, a.deps.FS, cfg, a.operator)
	if a.sessionID == "" {
		a.sessionID = fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	a.identity = id
	var rec *audit.Recorder
	if cfg.Audit.File != "" {
		rec = &audit.Recorder{
			W: &audit.Writer{
				FS: a.deps.FS, Clock: a.deps.Clock, Path: cfg.Audit.File,
				Flock:   a.deps.Flock,
				Version: a.deps.Version.Version, Host: id.Hostname,
				RetentionDays: cfg.Audit.RetentionDays,
			},
			Identity: id, SessionID: a.sessionID, Cluster: cfg.Cluster,
			Clock: a.deps.Clock,
		}
	}
	svc := &service.Service{
		Cfg: cfg, Collector: source, Diagnoser: diag, PCPProc: proc,
		History: hist, ErrSrc: ecoll, ErrorsColl: ecoll, Tool: tool,
		Recorder: rec, FS: a.deps.FS,
		PCP: pcpRunner(a, pr), ScriptExists: scriptExists(a, cfg),
		ConfigRemote: cfgRemoteOps(a, cfg), ShowParam: showParamFn(a, cfg),
		Lock: cfgLock(a, cfg), FileSettingsFn: fileSettingsFn(a, cfg),
		SQLRunner: pgxRunner{},
		Clock:     a.deps.Clock, BuildVersion: a.deps.Version.Version, Local: local,
	}
	progressNDJSON(a, svc)
	return svc
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func histAdapter(h service.HistoryReader) errevents.HistoryReader {
	if h == nil {
		return nil
	}
	return h
}

var _ = time.Now

func pcpRunner(a *app, pr *pcp.PCP) service.PCPRunner {
	if a.deps.PCP != nil {
		return a.deps.PCP
	}
	return pr
}

func scriptExists(a *app, cfg *config.Config) func(context.Context, config.Host, string) (bool, error) {
	if a.deps.ScriptExists != nil {
		return a.deps.ScriptExists
	}
	pool := remote.NewPool(remote.SSHDialer{}, cfg.SSH)
	return func(ctx context.Context, h config.Host, path string) (bool, error) {
		sess, err := pool.Session(ctx, h)
		if err != nil {
			return false, err
		}
		res, err := sess.Exec(ctx, []string{"test", "-f", path}, nil)
		if err != nil {
			return false, err
		}
		return res.Exit == 0, nil
	}
}

// cfgRemoteOps implements service.RemoteOps over the ssh pool (or fakes).
type cfgRemote struct {
	pool *remote.Pool
	deps *Deps
	cfg  *config.Config
}

func (r cfgRemote) session(ctx context.Context, h config.Host) (remote.Session, error) {
	if r.deps.Sessions != nil && r.deps.Sessions[h.Name] != nil {
		return r.deps.Sessions[h.Name], nil
	}
	return r.pool.Session(ctx, h)
}

func (r cfgRemote) ReadFile(ctx context.Context, h config.Host, p string) ([]byte, error) {
	s, err := r.session(ctx, h)
	if err != nil {
		return nil, err
	}
	return s.ReadFile(ctx, p)
}

func (r cfgRemote) WriteFileAtomic(ctx context.Context, h config.Host, p string, b []byte) error {
	s, err := r.session(ctx, h)
	if err != nil {
		return err
	}
	return s.WriteFileAtomic(ctx, p, b)
}

func (r cfgRemote) Exec(ctx context.Context, h config.Host, args []string, stdin []byte) (remote.ExecResult, error) {
	s, err := r.session(ctx, h)
	if err != nil {
		return remote.ExecResult{Exit: -1}, err
	}
	return s.Exec(ctx, args, stdin)
}

func cfgRemoteOps(a *app, cfg *config.Config) service.RemoteOps {
	if a.deps.ConfigRemote != nil {
		return a.deps.ConfigRemote
	}
	pool := remote.NewPool(remote.SSHDialer{}, cfg.SSH)
	return cfgRemote{pool: pool, deps: a.deps, cfg: cfg}
}

func showParamFn(a *app, cfg *config.Config) service.ShowParamFn {
	if a.deps.ShowParam != nil {
		return a.deps.ShowParam
	}
	d := pgpoolsql.PGDialer{}
	return func(ctx context.Context, h config.Host, param string) (string, error) {
		ep := cfg.Pgpool
		ep.Host = h.Address
		if ep.Port == 0 {
			ep.Port = 9999
		}
		c, err := d.Dial(ctx, ep)
		if err != nil {
			return "", err
		}
		defer c.Close(ctx)
		return c.Show(ctx, param)
	}
}

func cfgLock(a *app, cfg *config.Config) *remote.ClusterLock {
	if a.deps.Lock != nil {
		return a.deps.Lock
	}
	pool := remote.NewPool(remote.SSHDialer{}, cfg.SSH)
	return &remote.ClusterLock{Pool: pool, Cfg: cfg, Clock: a.deps.Clock, FS: a.deps.FS}
}

func fileSettingsFn(a *app, cfg *config.Config) func(context.Context) map[string]map[string]string {
	if a.deps.FileSettingsFn != nil {
		return a.deps.FileSettingsFn
	}
	return nil // built lazily by service when ConfigRemote exists
}

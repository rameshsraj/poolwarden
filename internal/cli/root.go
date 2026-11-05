// Package cli holds the cobra command tree, shared by one-shot mode and the
// interactive shell. All per-invocation state lives in an app struct carried
// on the command context; there are no package-level mutable globals.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/exitcode"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/render"
	"github.com/smarsh/poolwarden/internal/service"
	"github.com/smarsh/poolwarden/internal/toollog"
)

// Deps is everything the CLI needs injected so tests can fake it.
type Deps struct {
	FS      pwfs.FS
	Clock   clock.Clock
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	UID     int
	Version BuildInfo
	// Test seams: when set, newService uses these instead of real adapters.
	Source         service.SnapshotSource
	Diagnose       service.Diagnoser
	ProcInfo       service.ProcInfoer
	History        service.HistoryReader
	ErrorsColl     *errevents.Collector
	Env            audit.Env
	PCP            service.PCPRunner
	ScriptExists   func(context.Context, config.Host, string) (bool, error)
	ConfigRemote   service.RemoteOps
	ShowParam      service.ShowParamFn
	Lock           *remote.ClusterLock
	FileSettingsFn func(context.Context) map[string]map[string]string
	Flock          func(string) (func() error, func() error, error)
	Sessions       map[string]remote.Session
	// Loc overrides the display timezone (defaults to time.Local).
	Loc *time.Location
}

// DefaultDeps wires the real environment.
func DefaultDeps() *Deps {
	return &Deps{
		FS:      pwfs.OS{},
		Clock:   clock.Real{},
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		UID:     os.Getuid(),
		Version: defaultBuildInfo(),
	}
}

// app carries one invocation's state; it travels on the command context.
type app struct {
	deps      *Deps
	cfg       *config.Config
	tool      *toollog.Logger
	identity  audit.Identity
	sessionID string

	configPath string
	output     string
	nagios     bool
	readOnly   bool
	noColor    bool
	operator   string
	ticket     string
	yes        bool
	quiet      bool
	confirm    string
	force      bool
	format     render.Format
	cancel     context.CancelFunc
}

type ctxKey struct{}

func (a *app) finish() {
	if a.cancel != nil {
		a.cancel()
	}
}

func appFrom(cmd *cobra.Command) *app {
	if a, ok := cmd.Context().Value(ctxKey{}).(*app); ok {
		return a
	}
	return nil
}

// Commands that do not need the config file.
var noConfigLoad = map[string]bool{
	"version":    true,
	"help":       true,
	"completion": true,
	"init":       true,
}

// NewRoot builds the full command tree for one invocation.
func NewRoot(deps *Deps) *cobra.Command {
	return buildRoot(&app{deps: deps})
}

func buildRoot(a *app) *cobra.Command {
	deps := a.deps
	root := &cobra.Command{
		Use:           "poolwarden",
		Short:         "pgpool-II command center",
		Long:          "poolwarden is a terminal command center for one pgpool-II cluster:\nmonitoring, diagnosis, node control and configuration management.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			f, err := render.ParseFormat(a.output)
			if err != nil {
				return exitcode.New(exitcode.Usage, err.Error()).
					WithHint("use table, wide or json")
			}
			a.format = f
			if cmd == cmd.Root() && len(cmd.Flags().Args()) == 0 {
				return nil // shell stub; no config needed yet
			}
			for c := cmd; c != nil; c = c.Parent() {
				if noConfigLoad[c.Name()] {
					return nil
				}
			}
			cfg, err := config.LoadFor(deps.FS, a.configPath, deps.UID)
			if err != nil {
				return err
			}
			a.cfg = cfg
			exportPassfiles(a)
			tl, _ := toollog.Open(deps.FS, cfg.StateDir)
			a.tool = tl
			to, none := deadlineFor(cmd, cfg.Timeout)
			if none {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), to)
			a.cancel = cancel
			cmd.SetContext(ctx)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// no args on a TTY → the interactive shell (02 §1.4)
			tty := false
			if f, ok := deps.Stdin.(interface{ Fd() uintptr }); ok && term.IsTerminal(int(f.Fd())) {
				tty = true
			}
			if !tty {
				return exitcode.New(exitcode.Usage, "shell needs a TTY").
					WithHint("run a one-shot command instead")
			}
			return runShell(cmd, a)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&a.configPath, "config", "c", config.DefaultPath, "poolwarden config file")
	pf.StringVarP(&a.output, "output", "o", "table", "output format: table|wide|json")
	pf.BoolVar(&a.nagios, "nagios", false, "Nagios-style single-line output and exit codes")
	pf.BoolVar(&a.readOnly, "read-only", false, "disable all control commands")
	pf.BoolVar(&a.noColor, "no-color", false, "disable coloured output")
	pf.StringVar(&a.operator, "operator", "", "operator name (recorded in the audit log)")
	pf.StringVar(&a.ticket, "ticket", "", "change ticket id (audited when given)")
	pf.BoolVar(&a.yes, "yes", false, "skip the plain yes/no prompt (never a typed confirmation)")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "suppress non-essential output")
	pf.StringVar(&a.confirm, "confirm", "", "typed confirmation token for non-TTY use")
	pf.BoolVar(&a.force, "force", false, "override pre-flight WARN/NO-GO blocks")

	root.AddGroup(
		&cobra.Group{ID: "monitoring", Title: "Monitoring"},
		&cobra.Group{ID: "issues", Title: "Understanding issues"},
		&cobra.Group{ID: "control", Title: "Node control"},
		&cobra.Group{ID: "config", Title: "Configuration"},
		&cobra.Group{ID: "audit", Title: "Audit"},
		&cobra.Group{ID: "setup", Title: "Setup"},
	)
	root.SetContext(context.WithValue(context.Background(), ctxKey{}, a))
	addCommands(root)
	return root
}

// Execute runs args against deps, renders any error and returns the exit code.
func Execute(args []string, deps *Deps) int {
	a := &app{deps: deps}
	root := buildRoot(a)
	root.SetArgs(args)
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	root.SetIn(deps.Stdin)
	ctx := context.WithValue(context.Background(), ctxKey{}, a)
	err := root.ExecuteContext(ctx)
	a.finish()
	code := exitcode.OK
	if err != nil {
		// stdout must be fully flushed before the error block hits stderr
		flushOut(deps.Stdout)
		var e *exitcode.Error
		if errors.As(err, &e) && e.Msg == "" && e.Err == nil {
			code = e.Code // already rendered; just propagate the code
		} else if a.nagios {
			rr := render.New(deps.Stdout, render.FormatNagios, true)
			c2 := exitcode.Usage
			if errors.As(err, &e) {
				c2 = e.Code
			}
			code = rr.Nagios(c2, err.Error())
		} else {
			code = exitcode.Render(deps.Stderr, err)
		}
	}
	// M7: read-only commands audited only when cfg.Audit.ReadCommands
	if code != 0 || err == nil {
		var aerr error
		if err != nil {
			aerr = err
		}
		auditReadCommand(ctx, a, code, aerr)
	}
	return code
}

// deadlineFor resolves the command's budget: "deadline" annotation
// ("none" → no deadline), --follow disables, default is cfg.Timeout×4.
func deadlineFor(cmd *cobra.Command, cfgTimeout time.Duration) (time.Duration, bool) {
	to := cfgTimeout * 4
	if to <= 0 {
		to = 30 * time.Second
	}
	dl := cmd.Annotations["deadline"]
	for c := cmd; dl == "" && c != nil; c = c.Parent() {
		dl = c.Annotations["deadline"]
	}
	if f := cmd.Flags().Lookup("follow"); f != nil && f.Value.String() == "true" {
		dl = "none"
	}
	switch dl {
	case "none":
		return 0, true
	case "":
	default:
		if d, err := time.ParseDuration(dl); err == nil {
			to = d
		}
	}
	return to, false
}

// flushOut flushes deps.Stdout when it supports it (bufio writers);
// no-op for os.Stdout and buffers.
func flushOut(w interface{}) {
	if f, ok := w.(interface{ Flush() error }); ok {
		f.Flush()
	}
}

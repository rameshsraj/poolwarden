package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/render"
)

// renderView re-executes the named view command into a buffer.
func renderView(ctx context.Context, a *app, view string) string {
	if view == "errors" {
		return renderView(ctx, a, "errors")
	}
	if view == "top" {
		return renderView(ctx, a, "status") + "\n" + renderView(ctx, a, "lag")
	}
	var buf strBuf
	tmp := *a.deps
	tmp.Stdout = &buf
	na := &app{deps: &tmp, cfg: a.cfg, tool: a.tool, identity: a.identity,
		sessionID: a.sessionID, format: render.FormatTable, readOnly: true}
	root := buildRoot(na)
	root.SetArgs([]string{view})
	root.SetOut(&buf)
	root.SetErr(&buf)
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	root.SetContext(ctx2)
	_ = root.Execute()
	return buf.String()
}

func watchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:               "watch <view>",
		ValidArgsFunction: watchViewNames(), Short: "full-screen refresh of a view", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			tty := false
			if f, ok := a.deps.Stdout.(interface{ Fd() uintptr }); ok {
				tty = term.IsTerminal(int(f.Fd()))
			}
			if !tty {
				return exitcode.New(exitcode.Usage, "watch needs a TTY")
			}
			view := "status"
			if len(args) > 0 {
				view = args[0]
			}
			if _, ok := render.WatchViews[view]; !ok {
				return exitcode.New(exitcode.Usage, "unknown view "+view).
					WithHint("status nodes lag pools watchdog errors")
			}
			iv := 2 * time.Second
			if s, _ := cmd.Flags().GetString("interval"); s != "" {
				d, err := time.ParseDuration(s)
				if err != nil {
					return exitcode.New(exitcode.Usage, "bad --interval")
				}
				iv = render.ClampInterval(d, 1)
			}
			return runWatch(cmd, a, view, iv)
		},
	}
	c.Flags().String("interval", "", "refresh interval, e.g. 2s")
	c.Args = cobra.MaximumNArgs(1)
	return c
}

// runWatch drives the frame loop; key handling per render.MapKey.
func runWatch(cmd *cobra.Command, a *app, view string, iv time.Duration) error {
	enter, leave := render.TermCtl()
	out := a.deps.Stdout
	fmt.Fprint(out, enter)
	defer fmt.Fprint(out, leave)

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// raw mode for key reads
	keyCh := make(chan byte, 8)
	if f, ok := a.deps.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		old, err := term.MakeRaw(int(f.Fd()))
		if err == nil {
			defer term.Restore(int(f.Fd()), old)
		}
		go func() {
			buf := make([]byte, 1)
			for {
				if _, err := f.Read(buf); err != nil {
					return
				}
				select {
				case keyCh <- buf[0]:
				default:
				}
			}
		}()
	}
	paused := false
	curView := view
	draw := func() {
		body := renderView(ctx, a, curView)
		fmt.Fprint(out, render.Frame(curView, iv, paused, time.Now(), body))
	}
	draw()
	tk := time.NewTicker(iv)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case k := <-keyCh:
			if act := render.MapKey(k, render.WatchViews[curView]); act != nil {
				switch act.Kind {
				case "quit":
					return nil
				case "pause":
					paused = !paused
					draw()
				case "interval":
					iv = render.ClampInterval(iv, act.Arg)
					tk.Reset(iv)
					draw()
				case "view":
					for name, n := range render.WatchViews {
						if n == int(act.Arg) {
							curView = name
						}
					}
					draw()
				}
			}
		case <-tk.C:
			if !paused {
				draw()
			}
		}
	}
}

type strBuf struct{ b []byte }

func (s *strBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *strBuf) String() string              { return string(s.b) }

func topCmd() *cobra.Command {
	return &cobra.Command{
		Use: "top", Short: "live overview: status, lag and findings", GroupID: "monitoring",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			tty := false
			if f, ok := a.deps.Stdout.(interface{ Fd() uintptr }); ok {
				tty = term.IsTerminal(int(f.Fd()))
			}
			if !tty {
				return exitcode.New(exitcode.Usage, "top needs a TTY")
			}
			return runWatch(cmd, a, "top", 2*time.Second)
		},
	}
}

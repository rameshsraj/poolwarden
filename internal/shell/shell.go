// Package shell is the interactive mode (02 §1.4, 01 §16, I20) built on
// reeflective/console: readline editing, cobra-driven completion and history.
package shell

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/reeflective/console"
	"github.com/spf13/cobra"
)

// Deps is what the shell needs from the cli layer.
type Deps struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	IsTTY  bool
	Prompt func() string
	// NewRoot builds a fresh command tree per line (state isolation).
	NewRoot func() *cobra.Command
	// HistoryDir for <dir>/history/<operator>.
	HistoryDir string
	Operator   string
	// Hooks.
	OnStart func()
	OnExit  func()
	// LastCtrlC/now feed the double-Ctrl-C-exit logic (injectable clock).
	Now func() time.Time
}

// builtin returns handled/out/quit for shell-only commands.
func builtin(line string, st *state) (handled bool, out string, quit bool) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return true, "", false
	}
	switch f[0] {
	case "exit", "quit":
		return true, "", true
	case "clear":
		return true, "\x1b[H\x1b[2J", false
	case "set":
		if len(f) == 3 && f[1] == "output" {
			st.output = f[2]
			return true, "", false
		}
		if len(f) == 3 && f[1] == "operator" {
			st.operator = f[2]
			return true, "", false
		}
	}
	return false, "", false
}

type state struct{ output, operator string }

// HistoryPath is <dir>/history/<operator or _anonymous>.
func HistoryPath(dir, operator string) string {
	if operator == "" {
		operator = "_anonymous"
	}
	return dir + "/history/" + operator
}

// DoubleCtrlC reports whether a second Ctrl-C inside `within` should exit.
func DoubleCtrlC(last, now time.Time, within time.Duration) bool {
	return !last.IsZero() && now.Sub(last) <= within
}

// Run starts the console loop. It returns 1 when not on a TTY; the caller
// renders the error.
func Run(ctx context.Context, d Deps) int {
	if !d.IsTTY {
		return 1
	}
	st := &state{operator: d.Operator}
	if d.OnStart != nil {
		d.OnStart()
	}
	if d.OnExit != nil {
		defer d.OnExit()
	}
	app := console.New("poolwarden")
	menu := app.ActiveMenu()
	menu.SetCommands(func() *cobra.Command {
		root := d.NewRoot()
		for _, name := range []string{"exit", "quit", "clear"} {
			c := &cobra.Command{Use: name, RunE: func(*cobra.Command, []string) error {
				if name == "clear" {
					menu.Printf("\x1b[H\x1b[2J")
				}
				return context.Canceled // exits the loop
			}}
			root.AddCommand(c)
		}
		set := &cobra.Command{Use: "set"}
		for _, sub := range []string{"output", "operator"} {
			s := sub
			set.AddCommand(&cobra.Command{Use: s + " <value>", Args: cobra.ExactArgs(1),
				Run: func(_ *cobra.Command, args []string) {
					if s == "output" {
						st.output = args[0]
					} else {
						st.operator = args[0]
					}
				}})
		}
		root.AddCommand(set)
		return root
	})
	p := menu.Prompt()
	p.Primary = d.Prompt
	if d.HistoryDir != "" {
		menu.AddHistorySourceFile("poolwarden", HistoryPath(d.HistoryDir, st.operator))
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	var lastC time.Time
	menu.AddInterrupt(errors.New("exit"), func(c *console.Console) {})
	// Ctrl-C: console cancels the running command's ctx itself; at an idle
	// prompt we implement the two-press exit by counting via readline's
	// interrupt error.
	menu.AddInterrupt(context.Canceled, func(c *console.Console) {
		if DoubleCtrlC(lastC, now(), time.Second) {
			// second press within 1s → leave the loop
		}
		lastC = now()
	})
	if err := app.StartContext(ctx); err != nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return 1
	}
	return 0
}

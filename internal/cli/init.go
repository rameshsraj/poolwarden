package cli

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/service"
	"golang.org/x/term"
)

// stdinPrompter implements service.Prompter.
type stdinPrompter struct{ a *app }

func (p *stdinPrompter) Ask(text, def string) string {
	fmt.Fprintf(p.a.deps.Stdout, "%s [%s]: ", text, def)
	s := bufio.NewScanner(p.a.deps.Stdin)
	if s.Scan() && strings.TrimSpace(s.Text()) != "" {
		return strings.TrimSpace(s.Text())
	}
	return def
}

func (p *stdinPrompter) Choose(text string, options []string) int {
	fmt.Fprintln(p.a.deps.Stdout, text+":")
	for i, o := range options {
		fmt.Fprintf(p.a.deps.Stdout, "  %d) %s\n", i+1, o)
	}
	s := p.Ask("choose", "1")
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(options) {
		return n - 1
	}
	return 0
}

func (p *stdinPrompter) YesNo(text string) bool {
	s := p.Ask(text+" [y/N]", "")
	return s == "y" || s == "yes"
}

func initCmd() *cobra.Command {
	c := &cobra.Command{
		Annotations: map[string]string{"deadline": "10m"},
		Use:         "init", Short: "create or update /etc/poolwarden.yaml", GroupID: "setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			o := service.InitOpts{OutPath: "/etc/poolwarden.yaml"}
			if a.configPath != "" {
				o.OutPath = a.configPath
			}
			o.Host, _ = cmd.Flags().GetString("host")
			o.NonInteractive, _ = cmd.Flags().GetBool("non-interactive")
			o.FromFile, _ = cmd.Flags().GetString("from")
			var answers map[string]string
			if o.FromFile != "" {
				b, err := a.deps.FS.ReadFile(o.FromFile)
				if err != nil {
					return err
				}
				answers = map[string]string{}
				if err := yaml.Unmarshal(b, &answers); err != nil {
					return exitcode.New(exitcode.Usage, "bad answers file").Wrap(err)
				}
			}
			// non-TTY without --non-interactive → refuse (never prompt blindly)
			tty := false
			if f, ok := a.deps.Stdin.(interface{ Fd() uintptr }); ok {
				tty = term.IsTerminal(int(f.Fd()))
			}
			if !tty && !o.NonInteractive {
				return exitcode.New(exitcode.Usage, "init is interactive").
					WithHint("--non-interactive --from <answers.yaml>")
			}
			svc := newService(a)
			svc.FS = a.deps.FS
			cfg, err := svc.RunInit(cmd.Context(), o, &stdinPrompter{a}, answers)
			if err != nil {
				return err
			}
			if !o.NonInteractive {
				b, _ := yaml.Marshal(cfg)
				fmt.Fprintf(a.deps.Stdout, "\n%s\n", b)
			}
			svc.Confirmer = newConfirmer(a)
			res, err := svc.InitApply(cmd.Context(), cfg, o.OutPath,
				service.ControlOpts{Yes: o.NonInteractive, TTY: tty})
			if err != nil {
				return err
			}
			_ = res
			fmt.Fprintf(a.deps.Stdout, "wrote %s\n", o.OutPath)
			return done(0)
		},
	}
	c.Flags().String("host", "", "configure a single host")
	c.Flags().Bool("non-interactive", false, "no prompts; use --from")
	c.Flags().String("from", "", "seed values from a file")
	return c
}

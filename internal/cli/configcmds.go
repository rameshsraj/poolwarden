package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/pgconf"
	"github.com/smarsh/poolwarden/internal/render"
	"github.com/smarsh/poolwarden/internal/service"
)

func svcOf(cmd *cobra.Command) (*app, *service.Service) {
	a := appOf(cmd)
	svc := newService(a)
	svc.Confirmer = newConfirmer(a)
	return a, svc
}

func operatorOf(a *app) string { return a.identity.Operator() }

func configCmds() *cobra.Command {
	cfg := &cobra.Command{Use: "config", Short: "manage pgpool configuration without editing files", GroupID: "config"}

	get := &cobra.Command{
		Use: "get <param>", Args: cobra.ExactArgs(1),
		ValidArgsFunction: paramNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			row, err := svc.ConfigGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return presentPlain(a, row, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"FILE", "HOST", "VALUE", "LIVE", "DEFAULT", "DRIFT"}}
				var hs []string
				for h := range row.PerHost {
					hs = append(hs, h)
				}
				sort.Strings(hs)
				for _, h := range hs {
					t.Rows = append(t.Rows, []string{row.File, h, row.PerHost[h], orText(row.Live, "-"), row.Default, row.Drift})
				}
				r.WriteTable(t)
			})
		},
	}

	list := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			o := service.ConfigListOpts{}
			o.Changed, _ = cmd.Flags().GetBool("changed")
			o.Drift, _ = cmd.Flags().GetBool("drift")
			o.Grep, _ = cmd.Flags().GetString("grep")
			rows, err := svc.ConfigList(cmd.Context(), o)
			if err != nil {
				return err
			}
			return presentPlain(a, rows, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"PARAM", "FILE", "LIVE", "DEFAULT", "CONTEXT", "DRIFT"}}
				for _, row := range rows {
					t.Rows = append(t.Rows, []string{row.Param, row.File,
						orText(row.Live, "-"), row.Default, row.Context, row.Drift})
				}
				r.WriteTable(t)
			})
		},
	}
	list.Flags().Bool("changed", false, "only non-default values")
	list.Flags().Bool("drift", false, "only params where file != live")
	list.Flags().String("grep", "", "filter by substring")

	desc := &cobra.Command{
		Use: "describe <param>", Args: cobra.ExactArgs(1),
		ValidArgsFunction: paramNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			p, err := svc.ConfigDescribe(args[0])
			if err != nil {
				return err
			}
			return presentPlain(a, p, func(r *render.Renderer) {
				fmt.Fprintf(r.Out, "%s: %s (default %s, %s", p.Name, p.Type, p.Default, p.Context)
				if len(p.Enum) > 0 {
					fmt.Fprintf(r.Out, ", enum %s", strings.Join(p.Enum, "|"))
				}
				if p.Min != nil {
					fmt.Fprintf(r.Out, ", min %g", *p.Min)
				}
				if p.Max != nil {
					fmt.Fprintf(r.Out, ", max %g", *p.Max)
				}
				if p.Unverified {
					fmt.Fprint(r.Out, ", UNVERIFIED")
				}
				fmt.Fprintln(r.Out, ")")
			})
		},
	}

	set := &cobra.Command{
		Use: "set <param> <value>", Args: cobra.ExactArgs(2),
		ValidArgsFunction: paramNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			err := svc.ConfigSet(cmd.Context(), args[0], args[1], operatorOf(a))
			if err != nil {
				return err
			}
			fmt.Fprintf(a.deps.Stdout, "staged: %s = %s\n", args[0], args[1])
			return done(0)
		},
	}

	unset := &cobra.Command{
		Use: "unset <param>", Args: cobra.ExactArgs(1),
		ValidArgsFunction: paramNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			if err := svc.ConfigUnset(cmd.Context(), args[0], operatorOf(a)); err != nil {
				return err
			}
			fmt.Fprintf(a.deps.Stdout, "staged: revert %s to default\n", args[0])
			return done(0)
		},
	}

	diff := &cobra.Command{
		Use: "diff",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			d, err := svc.ConfigDiff(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprint(a.deps.Stdout, d)
			return done(0)
		},
	}

	validate := &cobra.Command{
		Use: "validate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			rows, fails, err := svc.ConfigValidate(cmd.Context())
			if err != nil {
				return err
			}
			_ = presentPlain(a, rows, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"NAME", "RESULT", "DETAIL"}}
				for _, row := range rows {
					t.Rows = append(t.Rows, []string{row.Name, row.Result, row.Detail})
				}
				r.WriteTable(t)
			})
			if fails > 0 {
				return done(4)
			}
			return done(0)
		},
	}

	apply := &cobra.Command{
		Use:         "apply",
		Annotations: map[string]string{"deadline": "15m"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Apply(cmd.Context(), controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}

	discard := &cobra.Command{
		Use: "discard",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				conf := newConfirmer(a)
				if tc, ok := conf.(*ttyConfirmer); !ok || !tc.isTTY {
					return exitcode.New(exitcode.Usage, "confirmation required").
						WithHint("--yes")
				}
				d, err := conf.Confirm(cmd.Context(), service.Prompt{
					Question: "Discard all staged changes? [y/N]", YesNo: true})
				if err != nil || !d.Confirmed {
					return exitcode.New(exitcode.Usage, "aborted")
				}
			}
			if err := svc.DiscardStaging(); err != nil {
				return err
			}
			fmt.Fprintln(a.deps.Stdout, "staged changes discarded")
			return done(0)
		},
	}

	history := &cobra.Command{
		Use: "history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			mans, err := svc.ConfigHistory()
			if err != nil {
				return err
			}
			return presentPlain(a, mans, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"VERSION", "TIME", "OPERATOR", "TICKET", "HOSTS", "SUMMARY"}}
				for _, m := range mans {
					t.Rows = append(t.Rows, []string{m.Version, r.TimeShort(m.Time),
						m.Operator, orText(m.Ticket, "-"), strings.Join(m.Hosts, ","), m.Summary})
				}
				r.WriteTable(t)
			})
		},
	}

	rollback := &cobra.Command{
		Use: "rollback <version>", Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"deadline": "15m"},
		RunE: func(cmd *cobra.Command, args []string) error {
			a := appOf(cmd)
			svc := newService(a)
			svc.Confirmer = newConfirmer(a)
			res, err := svc.Rollback(cmd.Context(), args[0], controlOpts(cmd, a))
			return finishControl(cmd, res, err)
		},
	}

	load := &cobra.Command{
		Use: "load <file>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			n, err := svc.ConfigLoad(cmd.Context(), b, operatorOf(a))
			if err != nil {
				return err
			}
			fmt.Fprintf(a.deps.Stdout, "staged %d param(s)\n", n)
			return done(0)
		},
	}

	export := &cobra.Command{
		Use: "export [file]", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			b, err := svc.ConfigExport(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return a.deps.FS.WriteFileAtomic(args[0], b, false)
			}
			fmt.Fprint(a.deps.Stdout, string(b))
			return done(0)
		},
	}

	cfg.AddCommand(get, list, desc, set, unset, diff, validate, apply, discard,
		history, rollback, load, export, catalogueCmd(),
		selfCheckCmd())
	return cfg
}

func backendCmds() *cobra.Command {
	b := &cobra.Command{Use: "backend", Short: "add, remove or change backend nodes", GroupID: "config"}
	mk := func(sub string) *cobra.Command {
		c := &cobra.Command{
			Use: sub + " <id>", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				a, svc := svcOf(cmd)
				id, err := argID(args)
				if err != nil {
					return err
				}
				switch sub {
				case "remove":
					if err := svc.BackendRemove(cmd.Context(), id, operatorOf(a)); err != nil {
						return err
					}
				default:
					bk := pgconf.Backend{ID: id}
					bk.Hostname, _ = cmd.Flags().GetString("host")
					bk.Port, _ = cmd.Flags().GetInt("port")
					bk.DataDirectory, _ = cmd.Flags().GetString("data-dir")
					bk.Flag, _ = cmd.Flags().GetString("flag")
					w, _ := cmd.Flags().GetString("weight")
					if w != "" {
						bk.Weight, _ = strconv.ParseFloat(w, 64)
					}
					if bk.Hostname == "" {
						return exitcode.New(exitcode.Usage, "--host required")
					}
					if err := stageBackend(svc, cmd, bk); err != nil {
						return err
					}
				}
				fmt.Fprintf(a.deps.Stdout, "staged: backend %s %d\n", sub, id)
				return done(0)
			},
		}
		c.Flags().String("host", "", "backend hostname")
		c.Flags().Int("port", 0, "backend port")
		c.Flags().String("weight", "", "load-balance weight")
		c.Flags().String("data-dir", "", "backend data directory")
		c.Flags().String("flag", "", "backend flag bits")
		return c
	}
	b.AddCommand(mk("add"), mk("remove"), mk("set"))
	return b
}

func stageBackend(svc *service.Service, cmd *cobra.Command, b pgconf.Backend) error {
	payload, _ := json.Marshal(b)
	return svc.StageChange(cmd.Context(), pgconf.Change{
		File: "pgpool.conf", Op: "backend_set", Param: fmt.Sprint(b.ID),
		Payload: payload, Operator: operatorOf(appOf(cmd))})
}

func hbaCmds() *cobra.Command {
	h := &cobra.Command{Use: "hba", Short: "manage pool_hba.conf", GroupID: "config"}
	list := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			rows, err := svc.HBAList(cmd.Context())
			if err != nil {
				return err
			}
			return presentPlain(a, rows, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"#", "TYPE", "DATABASE", "USER", "ADDRESS", "METHOD"}}
				for i, row := range rows {
					t.Rows = append(t.Rows, []string{fmt.Sprint(i), row.Type, row.Database,
						row.User, row.Address, row.Method})
				}
				r.WriteTable(t)
			})
		},
	}
	add := &cobra.Command{
		Use: "add",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			r := pgconf.HBARule{}
			r.Type, _ = cmd.Flags().GetString("type")
			r.Database, _ = cmd.Flags().GetString("db")
			r.User, _ = cmd.Flags().GetString("user")
			r.Address, _ = cmd.Flags().GetString("addr")
			r.Method, _ = cmd.Flags().GetString("method")
			if r.Type == "" || r.Database == "" || r.User == "" || r.Method == "" {
				return exitcode.New(exitcode.Usage, "--type --db --user --method required")
			}
			if err := svc.HBAAdd(cmd.Context(), r, operatorOf(a)); err != nil {
				return err
			}
			fmt.Fprintln(a.deps.Stdout, "staged: hba rule")
			return done(0)
		},
	}
	add.Flags().String("type", "", "local|host|hostssl|hostnossl")
	add.Flags().String("db", "", "database")
	add.Flags().String("user", "", "user")
	add.Flags().String("addr", "", "address/CIDR")
	add.Flags().String("method", "", "auth method")
	add.Flags().Int("position", 0, "insert position")
	rm := &cobra.Command{
		Use: "remove <n>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			if err := svc.HBARemove(cmd.Context(), args[0], operatorOf(a)); err != nil {
				return err
			}
			fmt.Fprintln(a.deps.Stdout, "staged: remove hba rule "+args[0])
			return done(0)
		},
	}
	h.AddCommand(list, add, rm)
	return h
}

func pcpuserCmds() *cobra.Command {
	c := &cobra.Command{Use: "pcpuser", Annotations: map[string]string{"deadline": "5m"}, Short: "manage pcp.conf users", GroupID: "config"}
	list := &cobra.Command{Use: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			us, err := svc.PCPUserList(cmd.Context())
			if err != nil {
				return err
			}
			for _, u := range us {
				fmt.Fprintln(a.deps.Stdout, u)
			}
			return done(0)
		}}
	mk := func(sub string) *cobra.Command {
		return &cobra.Command{
			Use: sub + " <user>", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				a, svc := svcOf(cmd)
				switch sub {
				case "remove":
					return svc.PCPUserRemove(cmd.Context(), args[0], controlOpts(cmd, a))
				default: // add | passwd
					return svc.PCPUserSet(cmd.Context(), args[0], "", operatorOf(a), controlOpts(cmd, a))
				}
			},
		}
	}
	c.AddCommand(list, mk("add"), mk("passwd"), mk("remove"))
	return c
}

func pooluserCmds() *cobra.Command {
	c := &cobra.Command{Use: "pooluser", Annotations: map[string]string{"deadline": "5m"}, Short: "manage pool_passwd users", GroupID: "config",
		Long: `Manage pool_passwd entries. md5 passwords are computed in-process
(md5 of password+username — pg_md5 -p needs a TTY) and the file is
rewritten atomically; AES mode calls pg_enc -i <tmpfile> with the
password in a 0600 scratch file, never on argv or in the audit argv.
AES is chosen when existing pool_passwd entries are AES-prefixed.`}
	list := &cobra.Command{Use: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			us, err := svc.PoolUserList(cmd.Context())
			if err != nil {
				return err
			}
			for _, u := range us {
				fmt.Fprintln(a.deps.Stdout, u)
			}
			return done(0)
		}}
	mk := func(sub string) *cobra.Command {
		return &cobra.Command{
			Use: sub + " <user>", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				a, svc := svcOf(cmd)
				if sub == "remove" {
					return svc.PoolUserRemove(cmd.Context(), args[0], controlOpts(cmd, a))
				}
				return svc.PoolUserSet(cmd.Context(), args[0], "", controlOpts(cmd, a))
			},
		}
	}
	c.AddCommand(list, mk("add"), mk("passwd"), mk("remove"))
	return c
}

func sqlCmd() *cobra.Command {
	c := &cobra.Command{
		Annotations: map[string]string{"deadline": "40s"},
		Use:         "sql <target> <statement>", Short: "read-only query on a backend", GroupID: "config",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: sqlTargetNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, svc := svcOf(cmd)
			limit, _ := cmd.Flags().GetInt("limit")
			res, err := svc.SQL(cmd.Context(), args[0], args[1], service.SQLOpts{Limit: limit})
			if err != nil {
				return err
			}
			return presentPlain(a, res, func(r *render.Renderer) {
				t := render.Table{Columns: res.Cols, Rows: res.Rows}
				r.WriteTable(t)
			})
		},
	}
	c.Flags().Int("limit", 1000, "maximum rows returned")
	return c
}

func selfCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use: "self-check", Short: "verify configured paths, SSH and binaries",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, svc := svcOf(cmd)
			rows := svc.SelfCheck(cmd.Context())
			warns := 0
			_ = presentPlain(a, rows, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"CHECK", "HOST", "RESULT", "DETAIL"}}
				for _, row := range rows {
					t.Rows = append(t.Rows, []string{row.Check, row.Host, row.Result, row.Detail})
				}
				r.WriteTable(t)
			})
			for _, row := range rows {
				if row.Result == "WARN" {
					warns++
				}
			}
			if warns > 0 {
				return done(10)
			}
			return done(0)
		},
	}
}

func sqlTargetNames() cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return []cobra.Completion{"primary", "standby"}, cobra.ShellCompDirectiveNoFileComp
	}
}

func catalogueCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "catalogue [--unverified]",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := appOf(cmd)
			only, _ := cmd.Flags().GetBool("unverified")
			var rows []pgconf.Param
			for _, p := range pgconf.All() {
				if only && !p.Unverified {
					continue
				}
				rows = append(rows, p)
			}
			return presentPlain(a, rows, func(r *render.Renderer) {
				t := render.Table{Columns: []string{"PARAM", "TYPE", "DEFAULT", "CONTEXT", "UNVERIFIED"}}
				for _, p := range rows {
					uv := ""
					if p.Unverified {
						uv = "yes"
					}
					t.Rows = append(t.Rows, []string{p.Name, p.Type, p.Default, p.Context, uv})
				}
				r.WriteTable(t)
			})
		},
	}
	c.Flags().Bool("unverified", false, "only params not confirmed against the docs")
	return c
}

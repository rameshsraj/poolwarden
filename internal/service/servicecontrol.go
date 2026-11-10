package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

// ServiceUnits are the service kinds poolwarden can manage on a host.
var serviceUnits = map[string]bool{"pgpool": true, "postgresql": true, "watchdog": true}

// unitServiceName resolves the configured unit name for a host+kind:
// pgpool → host's pgpool service name; postgresql → host's pg service name;
// watchdog → pgpool's watchdog (pgpool unit carries it, so start/stop is the
// same unit on hosts where pgpool runs).
func unitServiceName(h config.Host, unit string) string {
	switch unit {
	case "postgresql":
		if h.PG != nil && h.PG.Service != "" {
			return h.PG.Service
		}
		return "postgresql"
	default:
		return serviceName(h)
	}
}

// unitRoleOK reports whether the host actually runs that service kind.
func unitRoleOK(h config.Host, unit string) bool {
	for _, r := range h.Roles {
		if unit == "postgresql" && r == config.RolePG {
			return true
		}
		if unit != "postgresql" && r == config.RolePgpool {
			return true
		}
	}
	return false
}

// ServiceStatus execs `<systemctl> status <unit>` on the host and returns the
// raw output plus whether the unit reported active (exit 0).
func (s *Service) ServiceStatus(ctx context.Context, unit, hostName string) (string, bool, error) {
	h, name, err := s.serviceHostUnit(hostName, unit)
	if err != nil {
		return "", false, err
	}
	if s.ConfigRemote == nil {
		return "", false, exitcode.New(exitcode.Usage, "no remote adapter configured")
	}
	res, err := s.ConfigRemote.Exec(ctx, *h,
		[]string{s.Cfg.Bins.Systemctl, "status", name}, nil)
	out := string(res.Stdout) + string(res.Stderr)
	if err != nil {
		return out, false, nil // exec failure/unreachable: show output, report inactive
	}
	return out, res.Exit == 0, nil
}

func (s *Service) serviceHostUnit(hostName, unit string) (*config.Host, string, error) {
	if !serviceUnits[unit] {
		return nil, "", exitcode.New(exitcode.Usage, "unknown service "+strconv.Quote(unit)).
			WithHint("services: pgpool, postgresql, watchdog")
	}
	var h *config.Host
	for i := range s.Cfg.Hosts {
		hh := &s.Cfg.Hosts[i]
		if hh.Name == hostName || hh.Address == hostName {
			h = hh
			break
		}
	}
	if h == nil {
		return nil, "", exitcode.New(exitcode.Usage, "unknown host "+strconv.Quote(hostName)).
			WithHint("host must be a name or address in the cluster inventory")
	}
	return h, unitServiceName(*h, unit), nil
}

// ServiceControl runs start|stop|restart of pgpool/postgresql on one host as a
// full control action: preflight, typed confirmation, progress, audit, verify.
func (s *Service) ServiceControl(ctx context.Context, verb, unit, hostName string, o ControlOpts) (*ControlResult, error) {
	switch verb {
	case "start", "stop", "restart":
	default:
		return nil, exitcode.New(exitcode.Usage, "unknown verb "+strconv.Quote(verb)).
			WithHint("verbs: start, stop, restart")
	}
	h, name, err := s.serviceHostUnit(hostName, unit)
	if err != nil {
		return nil, err
	}
	if s.ConfigRemote == nil {
		return nil, exitcode.New(exitcode.Usage, "no remote adapter configured")
	}
	host := *h
	a := Action{
		Name:    "service " + verb,
		Target:  unit + "@" + host.Name,
		Command: fmt.Sprintf("service %s %s %s", verb, unit, host.Name),
		Token:   verb,
		Preflight: func(ctx context.Context) (*model.PreflightResult, error) {
			var checks []model.Check
			checks = append(checks, model.Check{Name: "host runs " + unit,
				Result: ternCheck(unitRoleOK(host, unit), "OK", "NO-GO"),
				Detail: "roles " + strings.Join(roleNames(host), ",")})
			if s.ConfigRemote != nil {
				if _, err := s.ConfigRemote.Exec(ctx, host, []string{"true"}, nil); err != nil {
					checks = append(checks, model.Check{Name: "ssh", Result: "NO-GO", Detail: err.Error()})
				} else {
					checks = append(checks, model.Check{Name: "ssh", Result: "OK", Detail: host.SSHUser + "@" + host.Address})
				}
			}
			if verb == "stop" {
				snap := s.Collector.Collect(ctx)
				switch unit {
				case "pgpool":
					// stopping the watchdog leader moves the VIP
					if snap.Watchdog.Available && snap.Watchdog.LeaderHost == host.Name {
						checks = append(checks, model.Check{Name: "watchdog leader",
							Result: "WARN", Detail: "this host holds the VIP — it will move to a peer"})
					}
					if snap.Watchdog.Available && snap.Watchdog.AliveRemote == 0 {
						checks = append(checks, model.Check{Name: "last pgpool",
							Result: "NO-GO", Detail: "no other pgpool node alive to serve clients"})
					}
				case "postgresql":
					for _, b := range snap.Backends {
						matched := b.Name == host.Name || b.Host == host.Address
						if matched && !b.InRecovery {
							checks = append(checks, model.Check{Name: "primary",
								Result: "WARN", Detail: "stopping the primary triggers failover"})
						}
					}
				}
			}
			verdict := "GO"
			for _, c := range checks {
				if c.Result == "NO-GO" {
					verdict = "NO-GO"
				}
			}
			return &model.PreflightResult{Action: "service " + verb + " " + unit + " on " + host.Name,
				Verdict: verdict, Checks: checks}, nil
		},
		Plan: func(pf *model.PreflightResult) string {
			return planText(pf, fmt.Sprintf("%s %s on %s via %s %s %s",
				strings.ToUpper(verb), unit, host.Name, s.Cfg.Bins.Systemctl, verb, name))
		},
	}
	a.Execute = func(ctx context.Context, rec *StepRecorder) error {
		return rec.Step(verb+" "+name, func() (string, error) {
			res, err := s.ConfigRemote.Exec(ctx, host,
				[]string{s.Cfg.Bins.Systemctl, verb, name}, nil)
			rec.Detail(string(res.Stdout) + string(res.Stderr))
			if err != nil {
				return "", err
			}
			return "ok", nil
		})
	}
	a.Verify = func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
		var vo VerifyOutcome
		err := rec.Step("verify", func() (string, error) {
			wantUp := verb != "stop"
			end := s.now().Add(60 * time.Second)
			for {
				res, err := s.ConfigRemote.Exec(ctx, host,
					[]string{s.Cfg.Bins.Systemctl, "status", name}, nil)
				up := err == nil && res.Exit == 0
				if up == wantUp {
					return tern(wantUp, "active", "inactive"), nil
				}
				if !s.now().Before(end) {
					if up {
						return "", fmt.Errorf("%s still reported active after %s", name, verb)
					}
					return "", fmt.Errorf("%s did not report active after %s", name, verb)
				}
				if err := s.sleep(ctx, 2*time.Second); err != nil {
					return "", err
				}
			}
		})
		if err != nil {
			return vo, err
		}
		vo.OK = true
		return vo, nil
	}
	return s.RunControl(ctx, &a, o)
}

func roleNames(h config.Host) []string {
	var out []string
	for _, r := range h.Roles {
		out = append(out, string(r))
	}
	return out
}

func ternCheck(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}

func tern(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}

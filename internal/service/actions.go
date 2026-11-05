package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/pcp"
	"github.com/smarsh/poolwarden/internal/preflight"
)

// PCPRunner is the control surface needed from the pcp adapter.
type PCPRunner interface {
	PromoteNode(ctx context.Context, id int, switchover, gracefully bool) (pcp.Result, error)
	DetachNode(ctx context.Context, id int, gracefully bool) (pcp.Result, error)
	AttachNode(ctx context.Context, id int) (pcp.Result, error)
	RecoveryNode(ctx context.Context, id int) (pcp.Result, error)
	ReloadConfig(ctx context.Context, scope string) (pcp.Result, error)
}

func planText(pf *model.PreflightResult, header string) string {
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n\n")
	}
	for _, c := range pf.Checks {
		fmt.Fprintf(&b, "  %-4s %s — %s\n", c.Result, c.Name, c.Detail)
	}
	fmt.Fprintf(&b, "\nVERDICT: %s\n", pf.Verdict)
	return b.String()
}

func (s *Service) preflightInput(ctx context.Context) (preflight.Input, *model.Snapshot, error) {
	snap := s.Collector.Collect(ctx)
	in := preflight.Input{Snap: snap, Cfg: s.Cfg, Now: s.now()}
	if s.Diagnoser != nil {
		in.Findings = s.Diagnoser.Run(ctx, s.buildInput(ctx, snap))
	}
	return in, snap, nil
}

func (s *Service) poll(ctx context.Context, every time.Duration, deadline time.Duration, fn func(*model.Snapshot) (bool, string)) error {
	end := s.now().Add(deadline)
	last := ""
	for {
		snap := s.Collector.Collect(ctx)
		ok, d := fn(snap)
		if d != "" {
			last = d
		}
		if ok {
			return nil
		}
		if !s.now().Before(end) {
			return &verifyTimeoutError{state: last}
		}
		if err := s.sleep(ctx, every); err != nil {
			return err
		}
	}
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.Clock != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.Clock.After(d):
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Promote builds the switchover action (02 §4.1).
func (s *Service) Promote(ctx context.Context, id int, gracefully, pgpoolOnly bool, o ControlOpts) (*ControlResult, error) {
	pcpR := s.PCP
	// the current primary is the "old primary" that must rejoin after switchover
	oldID, oldHost := -1, "?"
	if snap0 := s.Collector.Collect(ctx); snap0 != nil {
		for _, n := range snap0.Pgpool.Nodes {
			if n.Role == "primary" && n.ID != id {
				oldID, oldHost = n.ID, n.Hostname
			}
		}
	}
	a := Action{
		Name:       "promote",
		TargetID:   id,
		Token:      fmt.Sprint(id),
		ForceToken: fmt.Sprint(id),
		Command:    fmt.Sprintf("promote %d", id),
		Execute: func(ctx context.Context, rec *StepRecorder) error {
			return rec.Step("pcp_promote_node", func() (string, error) {
				pr, err := pcpR.PromoteNode(ctx, id, !pgpoolOnly, gracefully)
				rec.Detail(pr.Stdout + "\n" + pr.Stderr)
				if err != nil {
					return "", err
				}
				return "ok", nil
			})
		},
		Verify: func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
			var vo VerifyOutcome
			err := rec.Step("verify", func() (string, error) {
				err := s.poll(ctx, 2*time.Second, 120*time.Second, func(snap *model.Snapshot) (bool, string) {
					if pgpoolOnly {
						for _, n := range snap.Pgpool.Nodes {
							if n.ID == id && n.Role == "primary" {
								return true, ""
							}
						}
						return false, lastState(snap)
					}
					var primaries, streaming int
					targetPrimary := false
					var primary *model.Backend
					for i := range snap.Backends {
						if !snap.Backends[i].Reachable {
							continue
						}
						if !snap.Backends[i].InRecovery {
							primaries++
							primary = &snap.Backends[i]
							if snap.Backends[i].NodeID == id {
								targetPrimary = true
							}
						}
					}
					if !targetPrimary || primaries != 1 || primary == nil {
						return false, lastState(snap)
					}
					for _, n := range snap.Pgpool.Nodes {
						b := s.backendFor(snap, n)
						if b == nil || !b.Reachable {
							continue
						}
						real := "standby"
						if !b.InRecovery {
							real = "primary"
						}
						if n.Role != "" && n.Role != real {
							return false, lastState(snap)
						}
						if b.InRecovery {
							for _, st := range primary.Standbys {
								if model.StandbyMatches(st, *b) {
									streaming++
								}
							}
						}
					}
					standbys := 0
					for _, b := range snap.Backends {
						if b.Reachable && b.InRecovery {
							standbys++
						}
					}
					return streaming >= standbys && standbys > 0 || standbys == 0, ""
				})
				if err != nil {
					return "", err
				}
				vo.OK = true
				return "ok", nil
			})
			if err != nil || pgpoolOnly {
				return vo, err
			}
			// 02 §4.1 step 4: the old primary must rejoin as a streaming standby.
			var lastSnap *model.Snapshot
			if oldID >= 0 {
				werr := rec.Step("verify (old primary rejoin)", func() (string, error) {
					if perr := s.poll(ctx, 2*time.Second, 60*time.Second,
						func(snap *model.Snapshot) (bool, string) {
							lastSnap = snap
							var ob *model.Backend
							for i := range snap.Backends {
								if snap.Backends[i].NodeID == oldID {
									ob = &snap.Backends[i]
								}
							}
							if ob == nil || !ob.Reachable || ob.InRecovery == false {
								return false, lastState(snap)
							}
							// streaming from the new primary?
							var prim *model.Backend
							for i := range snap.Backends {
								if !snap.Backends[i].InRecovery && snap.Backends[i].Reachable {
									prim = &snap.Backends[i]
								}
							}
							for _, n := range snap.Pgpool.Nodes {
								if n.ID == oldID && n.Status != "up" {
									return false, lastState(snap)
								}
							}
							if prim != nil {
								for _, st := range prim.Standbys {
									if model.StandbyMatches(st, *ob) {
										return true, lastState(snap)
									}
								}
							}
							return false, lastState(snap)
						}); perr != nil {
						return "", perr
					}
					return "ok", nil
				})
				if werr != nil {
					vo.Warn = oldPrimaryWarn(lastSnap, oldID, oldHost)
				}
			}
			return vo, err
		},
	}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		in, snap, err := s.preflightInput(ctx)
		if err != nil {
			return nil, err
		}
		in.RecentFailover = false // error-source wiring: M6 sources available later
		_ = snap
		return preflight.Promote(in, id, pgpoolOnly), nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		hdr := ""
		if pgpoolOnly {
			hdr = "promote --pgpool-only: changes ONLY pgpool's view of the primary\n(UNVERIFIED T1: backend roles are not switched)"
		} else {
			hdr = fmt.Sprintf("switchover: promote node %d to primary; follow_primary_command runs on the old primary", id)
		}
		return planText(pf, hdr)
	}
	return s.RunControl(ctx, &a, o)
}

// Detach builds the detach action (02 §4.2).
func (s *Service) Detach(ctx context.Context, id int, gracefully bool, o ControlOpts) (*ControlResult, error) {
	pcpR := s.PCP
	token := fmt.Sprint(id)
	// detaching the primary needs the strong token; check its role now
	if snap0 := s.Collector.Collect(ctx); snap0 != nil {
		for _, n := range snap0.Pgpool.Nodes {
			if n.ID == id && n.Role == "primary" {
				token = fmt.Sprintf("detach-primary-%d", id)
			}
		}
	}
	a := Action{
		Name:       "detach",
		TargetID:   id,
		Token:      token,
		ForceToken: fmt.Sprintf("detach-primary-%d", id),
		Command:    fmt.Sprintf("detach %d", id),
		Execute: func(ctx context.Context, rec *StepRecorder) error {
			return rec.Step("pcp_detach_node", func() (string, error) {
				pr, err := pcpR.DetachNode(ctx, id, gracefully)
				rec.Detail(pr.Stdout + "\n" + pr.Stderr)
				if err != nil {
					return "", err
				}
				return "ok", nil
			})
		},
		Verify: func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
			var vo VerifyOutcome
			err := rec.Step("verify", func() (string, error) {
				err := s.poll(ctx, 2*time.Second, 60*time.Second, func(snap *model.Snapshot) (bool, string) {
					for _, n := range snap.Pgpool.Nodes {
						if n.ID == id {
							return n.Status == "down", ""
						}
					}
					return false, lastState(snap)
				})
				if err != nil {
					return "", err
				}
				vo.OK = true
				return "ok", nil
			})
			return vo, err
		},
	}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		in, _, err := s.preflightInput(ctx)
		if err != nil {
			return nil, err
		}
		return preflight.Detach(in, id, gracefully), nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, fmt.Sprintf("detach node %d from pgpool", id))
	}
	return s.RunControl(ctx, &a, o)
}

// Attach builds the attach action (02 §4.3).
func (s *Service) Attach(ctx context.Context, id int, o ControlOpts) (*ControlResult, error) {
	pcpR := s.PCP
	a := Action{
		Name:       "attach",
		TargetID:   id,
		Token:      fmt.Sprint(id),
		ForceToken: fmt.Sprint(id),
		Command:    fmt.Sprintf("attach %d", id),
		Execute: func(ctx context.Context, rec *StepRecorder) error {
			return rec.Step("pcp_attach_node", func() (string, error) {
				pr, err := pcpR.AttachNode(ctx, id)
				rec.Detail(pr.Stdout + "\n" + pr.Stderr)
				if err != nil {
					return "", err
				}
				return "ok", nil
			})
		},
		Verify: func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
			var vo VerifyOutcome
			err := rec.Step("verify", func() (string, error) {
				deadline := s.now().Add(60 * time.Second)
				for {
					snap := s.Collector.Collect(ctx)
					var n *model.PoolNode
					for i := range snap.Pgpool.Nodes {
						if snap.Pgpool.Nodes[i].ID == id {
							n = &snap.Pgpool.Nodes[i]
						}
					}
					if n != nil && n.Status != "up" {
						if !s.now().Before(deadline) {
							return "", fmt.Errorf("node did not come up")
						}
					} else if n != nil {
						b := s.backendFor(snap, *n)
						streaming, delayOK := false, false
						if b != nil {
							if b.ReplayDelaySec < float64(s.Cfg.Lag.WarnSeconds) {
								delayOK = true
							}
							if p := s.primaryBackend(snap); p != nil {
								for _, st := range p.Standbys {
									if model.StandbyMatches(st, *b) {
										streaming = true
									}
								}
							}
						}
						if streaming && delayOK {
							vo.OK = true
							return "ok", nil
						}
						if !s.now().Before(deadline) {
							vo.OK = true
							vo.Warn = "attached but not streaming yet (or delay above warn)"
							return "warn", nil
						}
					}
					if err := s.sleep(ctx, 2*time.Second); err != nil {
						return "", err
					}
				}
			})
			return vo, err
		},
	}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		in, _, err := s.preflightInput(ctx)
		if err != nil {
			return nil, err
		}
		return preflight.Attach(in, id), nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, fmt.Sprintf("attach node %d to pgpool", id))
	}
	return s.RunControl(ctx, &a, o)
}

// Recover builds the recover action (02 §4.4).
func (s *Service) Recover(ctx context.Context, id int, timeout time.Duration, o ControlOpts) (*ControlResult, error) {
	pcpR := s.PCP
	a := Action{
		Name:       "recover",
		TargetID:   id,
		Token:      fmt.Sprint(id),
		ForceToken: fmt.Sprint(id),
		Command:    fmt.Sprintf("recover %d", id),
		Execute: func(ctx context.Context, rec *StepRecorder) error {
			return rec.Step("pcp_recovery_node", func() (string, error) {
				// recovery can run long: no inner deadline unless asked
				pr, err := pcpR.RecoveryNode(ctx, id)
				rec.Detail(pr.Stdout + "\n" + pr.Stderr)
				if err != nil {
					return "", err
				}
				return "ok", nil
			})
		},
		Verify: func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
			var vo VerifyOutcome
			err := rec.Step("verify", func() (string, error) {
				err := s.poll(ctx, 2*time.Second, 60*time.Second, func(snap *model.Snapshot) (bool, string) {
					for _, n := range snap.Pgpool.Nodes {
						if n.ID == id && n.Status == "up" {
							b := s.backendFor(snap, n)
							if b == nil || b.WALReceiver {
								return true, ""
							}
						}
					}
					return false, lastState(snap)
				})
				if err != nil {
					return "", err
				}
				vo.OK = true
				return "ok", nil
			})
			return vo, err
		},
	}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		in, _, err := s.preflightInput(ctx)
		if err != nil {
			return nil, err
		}
		if s.ScriptExists != nil {
			in.ScriptExists = func(h config.Host, p string) (bool, error) {
				return s.ScriptExists(ctx, h, p)
			}
		}
		return preflight.Recover(in, id), nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		return planText(pf, fmt.Sprintf("online recovery of node %d (recovery_1st_stage_command runs on the primary)", id))
	}
	return s.RunControl(ctx, &a, o)
}

// Reload builds the reload action.
func (s *Service) Reload(ctx context.Context, local bool, o ControlOpts) (*ControlResult, error) {
	pcpR := s.PCP
	scope := "c"
	if local {
		scope = "l"
	}
	a := Action{
		Name:       "reload",
		Token:      "reload",
		ForceToken: "reload",
		Command:    "reload",
		Execute: func(ctx context.Context, rec *StepRecorder) error {
			return rec.Step("pcp_reload_config", func() (string, error) {
				pr, err := pcpR.ReloadConfig(ctx, scope)
				rec.Detail(pr.Stdout + "\n" + pr.Stderr)
				if err != nil {
					return "", err
				}
				return "ok", nil
			})
		},
		Verify: func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error) {
			var vo VerifyOutcome
			err := rec.Step("verify", func() (string, error) {
				snap := s.Collector.Collect(ctx)
				if !snap.Pgpool.Reachable {
					return "", fmt.Errorf("pgpool not answering after reload")
				}
				vo.OK = true
				return "ok", nil
			})
			return vo, err
		},
	}
	a.Preflight = func(ctx context.Context) (*model.PreflightResult, error) {
		in, _, err := s.preflightInput(ctx)
		if err != nil {
			return nil, err
		}
		return preflight.Reload(in), nil
	}
	a.Plan = func(pf *model.PreflightResult) string {
		var hosts []string
		for _, h := range s.Cfg.PgpoolHosts() {
			hosts = append(hosts, h.Name)
		}
		return planText(pf, "reload pgpool config on: "+strings.Join(hosts, ", "))
	}
	return s.RunControl(ctx, &a, o)
}

// lastState summarises the cluster for a verify-timeout Cause.
func lastState(snap *model.Snapshot) string {
	var parts []string
	for _, n := range snap.Pgpool.Nodes {
		parts = append(parts, fmt.Sprintf("node %d %s %s", n.ID, n.Status, n.Role))
	}
	for _, b := range snap.Backends {
		st := "up"
		if !b.Reachable {
			st = "UNREACHABLE"
		}
		role := "primary"
		if b.InRecovery {
			role = "standby"
		}
		parts = append(parts, fmt.Sprintf("backend %s %s %s", b.Name, st, role))
	}
	return strings.Join(parts, "; ")
}

// oldPrimaryWarn picks the operator action matching the old primary's
// last observed state (BUG P): attach when it is already streaming but
// pgpool still shows it down, diagnose on split-brain, recover otherwise.
func oldPrimaryWarn(snap *model.Snapshot, oldID int, oldHost string) string {
	base := fmt.Sprintf("old primary node %d (%s) is down after switchover: ", oldID, oldHost)
	if snap != nil {
		var ob *model.Backend
		for i := range snap.Backends {
			if snap.Backends[i].NodeID == oldID {
				ob = &snap.Backends[i]
			}
		}
		if ob != nil && ob.Reachable {
			if !ob.InRecovery {
				return base + "it still reports primary (split-brain risk) — run: poolwarden diagnose"
			}
			var prim *model.Backend
			for i := range snap.Backends {
				if !snap.Backends[i].InRecovery && snap.Backends[i].Reachable {
					prim = &snap.Backends[i]
				}
			}
			if prim != nil {
				for _, st := range prim.Standbys {
					if model.StandbyMatches(st, *ob) {
						return base + fmt.Sprintf("it is streaming but pgpool still shows it down — run: poolwarden attach %d", oldID)
					}
				}
			}
			return base + fmt.Sprintf("follow_primary_command did not re-attach it — check its log and run: poolwarden recover %d", oldID)
		}
	}
	return base + fmt.Sprintf("follow_primary_command did not re-attach it — check its log and run: poolwarden recover %d", oldID)
}

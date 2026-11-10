package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/remote"
)

// ErrAborted marks an operator abort.
var ErrAborted = errors.New("aborted")

// Prompt is what the cli prints before reading the confirmation.
type Prompt struct {
	Plan     string
	Token    string
	Question string
	YesNo    bool
}

// Decision is the confirm answer.
type Decision struct {
	Confirmed bool
	Aborted   bool
	At        time.Time
}

// Confirmer is supplied by the cli (TTY prompts).
type Confirmer interface {
	AskOperator(ctx context.Context) (string, error)
	AskTicket(ctx context.Context) (string, error)
	Confirm(ctx context.Context, p Prompt) (Decision, error)
}

// ControlOpts carries the cli flags into the runner.
type ControlOpts struct {
	Yes      bool
	Confirm  string
	Force    bool
	ReadOnly bool
	TTY      bool
	Ticket   string
	// PlanOnly stops after preflight+plan: no confirmation prompt, no
	// execution. ControlResult carries Plan, Preflight and Token so a
	// non-interactive caller can build a confirmation UI.
	PlanOnly bool
	Now      time.Time
}

// Step is one timeline row (02 §4.1 step 5).
type Step struct {
	Name     string        `json:"name"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
	Duration time.Duration `json:"duration"`
	Result   string        `json:"result"`
	Output   string        `json:"output,omitempty"`
	Remote   []remote.Step `json:"remote,omitempty"`
}

// StepRecorder collects timeline steps.
type StepRecorder struct {
	steps []Step
	cur   *Step
	clock func() time.Time
	// OnStep, when set, is called with "start" from Begin and "end" from End.
	OnStep func(phase string, st Step)
}

func (r *StepRecorder) Begin(name string) {
	r.cur = &Step{Name: name, Start: r.clock()}
	if r.OnStep != nil {
		r.OnStep("start", *r.cur)
	}
}

func (r *StepRecorder) End(result string) {
	if r.cur == nil {
		return
	}
	r.cur.End = r.clock()
	r.cur.Duration = r.cur.End.Sub(r.cur.Start)
	r.cur.Result = result
	r.steps = append(r.steps, *r.cur)
	if r.OnStep != nil {
		r.OnStep("end", *r.cur)
	}
	r.cur = nil
}

// Step runs fn as a named timeline step.
func (r *StepRecorder) Step(name string, fn func() (string, error)) error {
	r.Begin(name)
	res, err := fn()
	if err != nil {
		r.End("failed")
		return err
	}
	r.End(res)
	return nil
}

// Detail attaches tool output (stdout+stderr, ≤10 lines) to the current step.
func (r *StepRecorder) Detail(text string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	if r.cur != nil {
		r.cur.Output = strings.Join(lines, "\n")
	}
}

// AddRemote attaches ssh steps to the current/last step.
func (r *StepRecorder) AddRemote(s remote.Step) {
	if r.cur != nil {
		r.cur.Remote = append(r.cur.Remote, s)
	} else if len(r.steps) > 0 {
		r.steps[len(r.steps)-1].Remote = append(r.steps[len(r.steps)-1].Remote, s)
	}
}

// Steps returns the collected timeline.
func (r *StepRecorder) Steps() []Step { return r.steps }

// Action is one control command.
type Action struct {
	// Change/BackupVersion ride onto the finished audit entry.
	Change        *audit.Change
	BackupVersion string
	Name          string
	Target        string
	TargetID      int
	Command       string
	Plan          func(pf *model.PreflightResult) string
	Preflight     func(ctx context.Context) (*model.PreflightResult, error)
	Token         string
	ForceToken    string
	// YesNo asks a plain y/N question instead of a token (02 §0 --yes skips it).
	YesNo   bool
	Execute func(ctx context.Context, rec *StepRecorder) error
	Verify  func(ctx context.Context, rec *StepRecorder) (VerifyOutcome, error)
}

// VerifyOutcome is a verify loop's verdict.
type VerifyOutcome struct {
	OK     bool
	Warn   string
	Detail string
}

// ControlResult is the whole C-flow outcome.
type ControlResult struct {
	Plan      string                 `json:"plan"`
	Preflight *model.PreflightResult `json:"preflight"`
	Timeline  []Step                 `json:"timeline"`
	Outcome   string                 `json:"outcome"`
	Warn      string                 `json:"warn,omitempty"`
	EventID   string                 `json:"event_id"`
	// Token, YesNo and Question are set in PlanOnly mode so a
	// non-interactive caller can present the exact confirmation.
	Token    string `json:"token,omitempty"`
	YesNo    bool   `json:"yes_no,omitempty"`
	Question string `json:"question,omitempty"`
}

// RunControl implements the 02 §0 C-flow in order.
func (s *Service) RunControl(ctx context.Context, a *Action, o ControlOpts) (*ControlResult, error) {
	res := &ControlResult{}
	rec := s.Recorder

	// (2) audit required: a nil Recorder must never let an action run unaudited
	if rec == nil {
		return nil, exitcode.New(exitcode.Usage, "audit log unavailable").
			WithCause("no recorder: audit.file not configured or not writable").
			WithHint("check audit.file in /etc/poolwarden.yaml and that /var/lib/poolwarden is writable")
	}
	if err := audit.Allowed(s.Cfg, rec.Identity); err != nil {
		rec.Refused(ctx, audit.Action{Command: a.Command}, err.Error())
		return nil, err
	}
	if o.ReadOnly {
		e := exitcode.New(exitcode.Usage, "read-only mode")
		rec.Refused(ctx, audit.Action{Command: a.Command}, e.Msg)
		return nil, e
	}
	// (3) operator
	op := rec.Identity.Operator()
	if op == "" && s.Confirmer != nil && o.TTY {
		name, err := s.Confirmer.AskOperator(ctx)
		if err != nil {
			return nil, err
		}
		op = name
		if rec != nil {
			rec.Identity.Declared = name
		}
	}
	if op == "" && !o.TTY {
		return nil, exitcode.New(exitcode.Usage, "operator unknown").
			WithHint("pass --operator")
	}
	// (4) ticket
	ticket := o.Ticket
	if ticket == "" && s.Confirmer != nil && o.TTY {
		t, err := s.Confirmer.AskTicket(ctx)
		if err != nil {
			return nil, err
		}
		ticket = t
	}
	// (5) preflight
	sr := &StepRecorder{clock: s.now}
	sr.OnStep = func(phase string, st Step) {
		s.progress(ProgressEvent{Op: a.Name, Stage: "step_" + phase, Step: &st})
	}
	s.progress(ProgressEvent{Op: a.Name, Stage: "preflight"})
	var pf *model.PreflightResult
	if err := sr.Step("preflight", func() (string, error) {
		var err error
		pf, err = a.Preflight(ctx)
		if err != nil {
			return "", err
		}
		return "ok", nil
	}); err != nil {
		return nil, err
	}
	res.Preflight = pf
	// (6)+(7) plan & confirm
	plan := a.Plan(pf)
	res.Plan = plan
	token := a.Token
	question := fmt.Sprintf("Type %q to confirm, or Ctrl-C to abort:", token)
	if pf.Verdict == "NO-GO" {
		if !o.Force {
			rec.Refused(ctx, audit.Action{Command: a.Command, Preflight: pf}, "preflight NO-GO")
			cause := noGoCause(pf)
			msg := "preflight NO-GO"
			if len(cause) > 0 {
				msg = "preflight NO-GO (" + cause[0] + ")"
			}
			return res, exitcode.New(exitcode.PreflightNoGo, msg).
				WithCause(strings.Join(cause, "\n")).
				WithHint("re-run with --force and type the node id to override, or fix the NO-GO checks")
		}
		token = a.ForceToken
		if token == "" {
			token = a.Token
		}
		question = fmt.Sprintf("NO-GO overridden by --force. Type %q to confirm, or Ctrl-C to abort:", token)
	}
	if a.YesNo {
		question = fmt.Sprintf("%s [y/N]:", "Proceed?")
	}
	s.progress(ProgressEvent{Op: a.Name, Stage: "plan", Detail: pf.Verdict})
	if o.PlanOnly {
		res.Token = token
		res.YesNo = a.YesNo
		res.Question = question
		res.Outcome = "planned"
		return res, nil
	}
	s.progress(ProgressEvent{Op: a.Name, Stage: "confirm"})
	var confirmAt time.Time
	switch {
	case o.TTY && s.Confirmer != nil && !(a.YesNo && o.Yes):
		dec, err := s.Confirmer.Confirm(ctx, Prompt{Plan: plan, Token: token, Question: question, YesNo: a.YesNo})
		if err != nil || dec.Aborted {
			rec.Aborted(ctx, audit.Action{Command: a.Command, Preflight: pf}, "confirm")
			return res, exitcode.New(exitcode.Usage, ErrAborted.Error()).
				WithCause(strings.Join(noGoCause(pf), "\n"))
		}
		if !dec.Confirmed {
			rec.Aborted(ctx, audit.Action{Command: a.Command, Preflight: pf}, "confirm")
			return res, exitcode.New(exitcode.Usage, ErrAborted.Error()).
				WithCause(strings.Join(noGoCause(pf), "\n"))
		}
		confirmAt = dec.At
	default:
		// non-TTY or TTY with --yes skipping the YesNo:
		// require --yes --confirm <token> (YesNo: --yes alone)
		if a.YesNo {
			if !o.Yes {
				rec.Refused(ctx, audit.Action{Command: a.Command, Preflight: pf},
					"non-TTY without --yes")
				return res, exitcode.New(exitcode.Usage, "confirmation required").
					WithCause(strings.Join(noGoCause(pf), "\n")).
					WithHint("--yes")
			}
		} else if !o.Yes || o.Confirm != token {
			rec.Refused(ctx, audit.Action{Command: a.Command, Preflight: pf},
				"non-TTY without --yes --confirm")
			return res, exitcode.New(exitcode.Usage, "confirmation required").
				WithCause(strings.Join(noGoCause(pf), "\n")).
				WithHint("--yes --confirm " + token)
		}
	}
	// (8) requested + confirmed
	pend, aerr := rec.Begin(ctx, audit.Action{
		Command: a.Command, Target: a.Target, Ticket: ticket,
		Preflight: pf, Force: o.Force,
	})
	if aerr != nil {
		if s.Tool != nil {
			s.Tool.Warn("audit write failed", "err", aerr)
		}
		return nil, exitcode.New(exitcode.Connection, "cannot write audit entry").Wrap(aerr)
	}
	res.EventID = pend.EventID()
	if confirmAt.IsZero() {
		confirmAt = s.now()
	}
	pend.Confirmed(confirmAt)
	// (9) started + execute
	pend.Started(s.now())
	s.progress(ProgressEvent{Op: a.Name, Stage: "execute"})
	execErr := a.Execute(ctx, sr)
	// (10) verify
	var vo VerifyOutcome
	var verr error
	vo.OK = true
	if execErr == nil && a.Verify != nil {
		vo, verr = a.Verify(ctx, sr)
	}
	res.Timeline = sr.Steps()
	// (11) finish — an *exitcode.Error carries its own code (4/5/6 apply codes)
	codeOf := func(e error) int {
		var ee *exitcode.Error
		if errors.As(e, &ee) {
			return ee.Code
		}
		return 2
	}
	outcome := "success"
	code := 0
	var ferr error
	switch {
	case ctx.Err() != nil && (execErr != nil || verr != nil):
		outcome, code = "failed", 2
		ferr = exitcode.New(exitcode.Connection, "interrupted while the operation was running").
			WithCause("the command was cancelled or hit its deadline mid-flight; the cluster may still be recovering").
			WithHint("poolwarden nodes; poolwarden logs --classify --since 10m")
	case execErr != nil:
		code = codeOf(execErr)
		ferr = execErr
		outcome = "failed"
		if code == 5 {
			outcome = "rolled_back"
		}
	case verr != nil:
		outcome, code = "failed", 2
		ferr = verr
	case !vo.OK:
		outcome, code = "failed", 2
		ferr = fmt.Errorf("verification failed: %s", vo.Detail)
	default:
		res.Warn = vo.Warn
	}
	if pend != nil {
		if res.Warn != "" {
			pend.Warn(res.Warn)
		}
		for _, st := range res.Timeline {
			pend.AddStep(remote.Step{Action: st.Name, Start: st.Start, End: st.End,
				Result: st.Result, Output: st.Output})
		}
		pend.Finish(ctx, outcome, code, ferr, a.Change, a.BackupVersion)
	}
	res.Outcome = outcome
	detail := outcome
	if ferr != nil {
		detail = outcome + ": " + ferr.Error()
	}
	s.progress(ProgressEvent{Op: a.Name, Stage: "outcome", Detail: detail, Code: code})
	if ferr != nil {
		hint := "poolwarden nodes; poolwarden logs --classify --since 10m"
		msg := ferr.Error()
		if errors.Is(verr, errVerifyTimeout) && a.Name == "promote" {
			msg = "promotion state unclear"
		}
		var ee *exitcode.Error
		if errors.As(ferr, &ee) {
			if ee.Hint == "" {
				ee.Hint = hint
			}
			return res, ee
		}
		e := exitcode.New(code, msg).WithHint(hint)
		var vte *verifyTimeoutError
		if errors.As(verr, &vte) && vte.state != "" {
			e.WithCause("last observed state: " + vte.state)
		}
		return res, e
	}
	return res, nil
}

var errVerifyTimeout = errors.New("verify timeout")

// verifyTimeoutError carries the last observed cluster state.
type verifyTimeoutError struct{ state string }

func (e *verifyTimeoutError) Error() string { return "verify timeout" }
func (e *verifyTimeoutError) Unwrap() error { return errVerifyTimeout }

// noGoCause lists each NO-GO check as "name: result (detail)".
func noGoCause(pf *model.PreflightResult) []string {
	var out []string
	if pf == nil {
		return out
	}
	for _, c := range pf.Checks {
		if c.Result == "NO-GO" {
			if c.Detail != "" {
				out = append(out, fmt.Sprintf("%s: NO-GO (%s)", c.Name, c.Detail))
			} else {
				out = append(out, c.Name+": NO-GO")
			}
		}
	}
	return out
}

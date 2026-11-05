package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/remote"
)

// Action describes the audited command.
type Action struct {
	Command   string
	Target    string
	Ticket    string
	Preflight *model.PreflightResult
	Force     bool
	Change    *Change
}

// Recorder writes requested/finished pairs around commands.
type Recorder struct {
	W         *Writer
	Identity  Identity
	SessionID string
	Cluster   string
	Clock     clock.Clock
}

func (r *Recorder) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now()
}

func jt(t time.Time) *model.JSONTime {
	j := model.JSONTime(t)
	return &j
}

func newEventID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Pending is a command in flight.
type Pending struct {
	rec   *Recorder
	entry *Entry
}

// Begin writes the requested entry.
func (r *Recorder) Begin(ctx context.Context, a Action) (*Pending, error) {
	e := &Entry{
		EventID:   newEventID(),
		SessionID: r.SessionID,
		Phase:     "requested",
		Identity:  r.Identity,
		Operator:  r.Identity.Operator(),
		Command:   Mask(a.Command),
		Cluster:   r.Cluster,
		Target:    a.Target,
		Ticket:    a.Ticket,
		Preflight: a.Preflight,
		Force:     a.Force,
	}
	e.RequestedAt = jt(r.now())
	if a.Change != nil {
		e.Change = maskChange(a.Change)
	}
	if err := r.append(ctx, e); err != nil {
		return nil, err
	}
	return &Pending{rec: r, entry: e}, nil
}

func maskChange(c *Change) *Change {
	out := &Change{Diff: Mask(c.Diff)}
	maskMap := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := map[string]string{}
		for k, v := range in {
			if containsFold(k, "passw") {
				out[k] = "***"
			} else {
				out[k] = v
			}
		}
		return out
	}
	out.Before = maskMap(c.Before)
	out.After = maskMap(c.After)
	return out
}

func containsFold(s, sub string) bool {
	l := len(s)
	for i := 0; i+len(sub) <= l; i++ {
		eq := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				eq = false
				break
			}
		}
		if eq {
			return true
		}
	}
	return false
}

// EventID returns the pending event id.
func (p *Pending) EventID() string { return p.entry.EventID }

// Confirmed records the confirmation time.
func (p *Pending) Confirmed(t time.Time) { p.entry.ConfirmedAt = jt(t) }

// Started records the start time.
func (p *Pending) Started(t time.Time) { p.entry.StartedAt = jt(t) }

// Warn records a post-success warning on the finished entry.
func (p *Pending) Warn(w string) { p.entry.Warn = w }

// AddStep appends a step to the finished entry.
func (p *Pending) AddStep(s remote.Step) { p.entry.Steps = append(p.entry.Steps, s) }

// Finish writes the finished entry for the same event.
func (p *Pending) Finish(ctx context.Context, outcome string, exitCode int, err error, change *Change, backupVersion string) error {
	fin := &Entry{
		EventID:       p.entry.EventID,
		SessionID:     p.entry.SessionID,
		Phase:         "finished",
		Identity:      p.entry.Identity,
		Operator:      p.entry.Operator,
		Command:       p.entry.Command,
		Cluster:       p.entry.Cluster,
		Target:        p.entry.Target,
		Ticket:        p.entry.Ticket,
		RequestedAt:   p.entry.RequestedAt,
		ConfirmedAt:   p.entry.ConfirmedAt,
		StartedAt:     p.entry.StartedAt,
		Steps:         p.entry.Steps,
		Preflight:     p.entry.Preflight,
		Force:         p.entry.Force,
		Outcome:       outcome,
		Warn:          p.entry.Warn,
		ExitCode:      exitCode,
		BackupVersion: backupVersion,
	}
	if err != nil {
		fin.Error = Mask(err.Error())
	}
	fin.FinishedAt = jt(p.rec.now())
	start := p.entry.StartedAt
	if start == nil {
		start = p.entry.RequestedAt
	}
	if start != nil {
		fin.DurationMS = time.Time(*fin.FinishedAt).Sub(time.Time(*start)).Milliseconds()
	}
	if change != nil {
		fin.Change = maskChange(change)
	}
	return p.rec.append(ctx, fin)
}

// Refused writes a refused pair immediately.
func (r *Recorder) Refused(ctx context.Context, a Action, reason string) error {
	p, err := r.Begin(ctx, a)
	if err != nil {
		return err
	}
	return p.Finish(ctx, "refused", 1, fmt.Errorf("%s", reason), nil, "")
}

// Aborted writes an aborted pair immediately.
func (r *Recorder) Aborted(ctx context.Context, a Action, atStep string) error {
	p, err := r.Begin(ctx, a)
	if err != nil {
		return err
	}
	return p.Finish(ctx, "aborted", 1, fmt.Errorf("aborted at step %s", atStep), nil, "")
}

// SessionStart writes a session-start entry.
func (r *Recorder) SessionStart(ctx context.Context) {
	e := &Entry{EventID: newEventID(), SessionID: r.SessionID, Phase: "finished",
		Identity: r.Identity, Operator: r.Identity.Operator(), Command: "shell start",
		Cluster: r.Cluster, Outcome: "success"}
	e.RequestedAt = jt(r.now())
	e.FinishedAt = e.RequestedAt
	r.append(ctx, e)
}

// SessionEnd writes a session-end entry.
func (r *Recorder) SessionEnd(ctx context.Context) {
	e := &Entry{EventID: newEventID(), SessionID: r.SessionID, Phase: "finished",
		Identity: r.Identity, Operator: r.Identity.Operator(), Command: "shell end",
		Cluster: r.Cluster, Outcome: "success"}
	e.RequestedAt = jt(r.now())
	e.FinishedAt = e.RequestedAt
	r.append(ctx, e)
}

// append bounds an audit write to 5s under the caller's ctx. When the
// caller's ctx is already cancelled (Ctrl-C during a control command) the
// entry must still land — this is the one intentional detachment.
func (r *Recorder) append(parent context.Context, e *Entry) error {
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		parent = context.WithoutCancel(parent)
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return r.W.Append(ctx, e)
}

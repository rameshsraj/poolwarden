package service

import (
	"context"
	"fmt"
	"time"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

// AuditRow is one `audit` line (one per finished entry).
type AuditRow struct {
	EventID    string    `json:"event_id"`
	TS         time.Time `json:"ts"`
	Operator   string    `json:"operator"`
	Command    string    `json:"command"`
	Target     string    `json:"target"`
	Ticket     string    `json:"ticket"`
	Outcome    string    `json:"outcome"`
	DurationMS int64     `json:"duration_ms"`
}

type AuditListResult struct {
	Rows []AuditRow `json:"rows"`
}

func (s *Service) auditPath() string { return s.Cfg.Audit.File }

func (s *Service) AuditList(ctx context.Context, f audit.Filter) (*AuditListResult, error) {
	if f.Since <= 0 {
		f.Since = 24 * time.Hour
	}
	entries := audit.List(s.FS, s.auditPath(), f, s.now())
	// index finished by event id; pending-requested without finished → in progress
	fin := map[string]audit.Entry{}
	var order []audit.Entry
	for _, e := range entries {
		if e.Phase == "finished" {
			fin[e.EventID] = e
			order = append(order, e)
		}
	}
	res := &AuditListResult{}
	for _, e := range order {
		res.Rows = append(res.Rows, AuditRow{
			EventID: e.EventID, TS: auditEntryTime(e), Operator: e.Operator,
			Command: e.Command, Target: e.Target, Ticket: e.Ticket,
			Outcome: e.Outcome, DurationMS: e.DurationMS,
		})
	}
	for _, e := range entries {
		if e.Phase == "requested" {
			if _, ok := fin[e.EventID]; !ok {
				res.Rows = append(res.Rows, AuditRow{
					EventID: e.EventID, TS: auditEntryTime(e), Operator: e.Operator,
					Command: e.Command, Target: e.Target, Ticket: e.Ticket,
					Outcome: "in progress",
				})
			}
		}
	}
	return res, nil
}

func auditEntryTime(e audit.Entry) time.Time {
	for _, t := range []*model.JSONTime{e.FinishedAt, e.RequestedAt, e.StartedAt, e.ConfirmedAt} {
		if t != nil {
			return time.Time(*t)
		}
	}
	return time.Time{}
}

type AuditShowResult struct {
	Requested *audit.Entry `json:"requested"`
	Finished  *audit.Entry `json:"finished"`
}

func (s *Service) AuditShow(ctx context.Context, id string) (*AuditShowResult, error) {
	req, fin := audit.Get(s.FS, s.auditPath(), id, 90*24*time.Hour, s.now())
	if req == nil && fin == nil {
		return nil, exitcode.New(exitcode.Usage, "no audit entry "+id).
			WithHint("poolwarden audit")
	}
	return &AuditShowResult{Requested: req, Finished: fin}, nil
}

func (s *Service) AuditVerify(ctx context.Context, since time.Duration) (audit.Report, error) {
	return audit.Verify(s.FS, s.auditPath(), since, s.now()), nil
}

func (s *Service) AuditSessions(ctx context.Context, since time.Duration) ([]audit.Session, error) {
	return audit.Sessions(s.FS, s.auditPath(), since, s.now()), nil
}

// AuditReadCommand writes a single audited pair for read-only commands when
// cfg.Audit.ReadCommands is on.
func (s *Service) AuditReadCommand(ctx context.Context, command string, exitCode int, err error) {
	if s.Recorder == nil || !s.Cfg.Audit.ReadCommands {
		return
	}
	outcome := "success"
	if err != nil || exitCode != 0 {
		outcome = "failed"
	}
	p, aerr := s.Recorder.Begin(ctx, audit.Action{Command: command})
	if aerr != nil {
		if s.Tool != nil {
			s.Tool.Warn("audit write failed", "err", aerr)
		}
		return
	}
	p.Started(s.now())
	p.Finish(ctx, outcome, exitCode, err, nil, "")
}

var _ = fmt.Sprint

package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

// DiagnoseRow is one line of `diagnose` (02 §3.1).
type DiagnoseRow struct {
	Severity string `json:"severity"`
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	Summary  string `json:"summary"`
}

type DiagnoseResult struct {
	Header   Header          `json:"header"`
	Rows     []DiagnoseRow   `json:"rows"`
	Next     string          `json:"next"`
	Findings []model.Finding `json:"findings"`
}

// ErrorSource finds recent error events for a subject (M6 supplies it).
type ErrorSource interface {
	Recent(ctx context.Context, subject string, n int) []model.ErrorEvent
}

func (s *Service) Diagnose(ctx context.Context, minSeverity, ruleID string) (*DiagnoseResult, error) {
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	res := &DiagnoseResult{Header: r.header, Findings: r.findings}
	rank := map[string]int{model.SevCrit: 2, model.SevWarn: 1, model.SevInfo: 0}
	min := 0
	switch minSeverity {
	case "crit":
		min = 2
	case "warn":
		min = 1
	case "", "info":
		min = 0
	default:
		return nil, exitcode.New(exitcode.Usage, "unknown severity "+minSeverity).
			WithHint("use info, warn or crit")
	}
	first := ""
	for _, f := range r.findings {
		if ruleID != "" && f.Rule != ruleID {
			continue
		}
		if rank[f.Severity] < min {
			continue
		}
		sum := ""
		if len(f.Evidence) > 0 {
			sum = f.Evidence[0]
		} else {
			sum = f.Cause
		}
		if len(sum) > 80 {
			sum = sum[:77] + "..."
		}
		res.Rows = append(res.Rows, DiagnoseRow{
			Severity: f.Severity, ID: f.ID, Subject: f.Subject, Summary: sum,
		})
		if first == "" && (f.Severity == model.SevCrit || f.Severity == model.SevWarn) {
			first = f.ID
		}
	}
	if first != "" {
		res.Next = "poolwarden explain " + first
	}
	return res, nil
}

// Explain returns the §3.2 block for a finding id (RULE:SUBJECT).
func (s *Service) Explain(ctx context.Context, id string) (*diagnose.ExplainResult, error) {
	ruleID, _, _ := strings.Cut(id, ":")
	var rule *diagnose.Rule
	for i := range diagnose.Catalogue {
		if diagnose.Catalogue[i].ID == ruleID {
			rule = &diagnose.Catalogue[i]
		}
	}
	if rule == nil {
		return nil, exitcode.New(exitcode.Usage, "unknown finding or rule "+id).
			WithHint("poolwarden diagnose")
	}
	r, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range r.findings {
		if f.ID == id {
			var related []model.ErrorEvent
			if s.ErrSrc != nil {
				related = s.ErrSrc.Recent(ctx, f.Subject, 5)
			}
			res := diagnose.Explain(f, *rule, related, true)
			return &res, nil
		}
	}
	// Rule exists but not currently firing.
	res := diagnose.Explain(model.Finding{ID: id, Subject: subjectOf(id)},
		*rule, nil, false)
	return &res, nil
}

func subjectOf(id string) string {
	_, sub, ok := strings.Cut(id, ":")
	if !ok {
		return ""
	}
	return sub
}

var _ = fmt.Sprint

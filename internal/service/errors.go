package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/errevents"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/logsrc"
	"github.com/smarsh/poolwarden/internal/model"
)

// ErrorsResult feeds the `errors` table.
type ErrorsResult struct {
	Header Header             `json:"header"`
	Events []model.ErrorEvent `json:"events"`
}

type SummaryResult struct {
	Header Header                 `json:"header"`
	Rows   []errevents.SummaryRow `json:"rows"`
}

type ShowFullResult struct {
	Header  Header                  `json:"header"`
	Event   model.ErrorEvent        `json:"event"`
	Context []string                `json:"context"`
	Explain *diagnose.ExplainResult `json:"explain,omitempty"`
}

func (s *Service) Errors(ctx context.Context, f errevents.Filter) (*ErrorsResult, error) {
	res := &ErrorsResult{}
	if s.ErrorsColl == nil {
		return res, nil
	}
	ev, err := s.ErrorsColl.Events(ctx, f)
	if err != nil {
		return nil, err
	}
	res.Events = ev
	return res, nil
}

func (s *Service) ErrorsSummary(ctx context.Context, f errevents.Filter) (*SummaryResult, error) {
	if s.ErrorsColl == nil {
		return &SummaryResult{}, nil
	}
	ev, err := s.ErrorsColl.Events(ctx, f)
	if err != nil {
		return nil, err
	}
	return &SummaryResult{Rows: errevents.Summary(ev)}, nil
}

func (s *Service) ErrorsShow(ctx context.Context, id string, since time.Duration) (*ShowFullResult, error) {
	if s.ErrorsColl == nil {
		return nil, exitcode.New(exitcode.Usage, "no error sources configured")
	}
	r, err := s.ErrorsColl.Show(ctx, id, since)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, err.Error()).WithHint("poolwarden errors")
	}
	res := &ShowFullResult{Event: r.Event, Context: r.Context}
	// when the event references a finding id, attach its explain block
	if res.Event.Ref != "" && s.Diagnoser != nil {
		ruleID, _, _ := strings.Cut(res.Event.Ref, ":")
		for _, r := range diagnose.Catalogue {
			if r.ID == ruleID {
				if ex, err := s.Explain(ctx, res.Event.Ref); err == nil && ex != nil {
					res.Explain = ex
				}
				break
			}
		}
	}
	return res, nil
}

func (s *Service) ErrorsFollow(ctx context.Context, f errevents.Filter, out chan<- model.ErrorEvent) error {
	if s.ErrorsColl == nil {
		return nil
	}
	return s.ErrorsColl.Follow(ctx, f, out)
}

// LogsOpts drives `logs`.
type LogsOpts struct {
	Host, Component string
	Since           time.Duration
	Grep            *regexp.Regexp
}

type LogLinesResult struct {
	Lines []logsrc.Line `json:"lines"`
	Tags  []string      `json:"tags,omitempty"` // parallel classify tags when --classify
}

func (s *Service) Logs(ctx context.Context, o LogsOpts) (*LogLinesResult, error) {
	res := &LogLinesResult{}
	if s.ErrorsColl == nil {
		return res, nil
	}
	since := o.Since
	if since <= 0 {
		since = time.Hour
	}
	for _, rd := range s.ErrorsColl.Logs {
		if fs, ok := rd.(*logsrc.FileSource); ok {
			if o.Host != "" && fs.Host != o.Host {
				continue
			}
			if o.Component != "" && fs.Component != o.Component {
				continue
			}
		}
		lines, err := rd.Read(ctx, logsrc.ReadOpts{Since: since, Grep: o.Grep, Now: s.now()})
		if err != nil {
			continue
		}
		res.Lines = append(res.Lines, lines...)
	}
	return res, nil
}

// LogsFollow streams raw lines; only --classify is applied in cli.
func (s *Service) LogsFollow(ctx context.Context, o LogsOpts, out chan<- logsrc.Line) error {
	if s.ErrorsColl == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	inner := make(chan logsrc.Line, 64)
	done := make(chan error, len(s.ErrorsColl.Logs))
	n := 0
	for _, rd := range s.ErrorsColl.Logs {
		if fs, ok := rd.(*logsrc.FileSource); ok {
			if o.Host != "" && fs.Host != o.Host {
				continue
			}
			if o.Component != "" && fs.Component != o.Component {
				continue
			}
		}
		rd := rd
		go func() { done <- rd.Follow(ctx, logsrc.ReadOpts{}, inner) }()
		n++
	}
	if n == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	for n > 0 {
		select {
		case <-ctx.Done():
			for i := 0; i < n; i++ {
				<-done
			}
			return ctx.Err()
		case err := <-done:
			n--
			if err != nil {
				return err
			}
		case l := <-inner:
			select {
			case out <- l:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

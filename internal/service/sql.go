package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/smarsh/poolwarden/internal/audit"
	"github.com/smarsh/poolwarden/internal/exitcode"
	"github.com/smarsh/poolwarden/internal/model"
)

// SingleStatement reports whether stmt is exactly one SQL statement: any
// trailing ';' must be followed only by whitespace/comments; ';' inside
// '...' and "-- ..." comments doesn't count.
func SingleStatement(stmt string) bool {
	var inQ, inLineCmt bool
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		if inLineCmt {
			if c == '\n' {
				inLineCmt = false
			}
			continue
		}
		if inQ {
			if c == '\'' {
				if i+1 < len(stmt) && stmt[i+1] == '\'' {
					i++
				} else {
					inQ = false
				}
			}
			continue
		}
		switch {
		case c == '\'':
			inQ = true
		case c == '-' && i+1 < len(stmt) && stmt[i+1] == '-':
			inLineCmt = true
		case c == ';':
			// must be the last statement
			if strings.TrimSpace(stmt[i+1:]) != "" {
				return false
			}
			return true
		}
	}
	return true
}

// SQLRunner is the pgx-shaped surface the service needs.
type SQLRunner interface {
	Exec(ctx context.Context, addr string, port int, sql string) error
	Query(ctx context.Context, addr string, port int, sql string) ([]string, [][]string, error)
}

// SQLOpts for the sql command.
type SQLOpts struct {
	Limit int
}

// SQLResult rows+cols.
type SQLResult struct {
	Cols []string   `json:"cols"`
	Rows [][]string `json:"rows"`
}

// SQL runs one read-only statement on a resolved backend (01 §17).
func (s *Service) SQL(ctx context.Context, target, stmt string, o SQLOpts) (*SQLResult, error) {
	if !SingleStatement(stmt) {
		return nil, exitcode.New(exitcode.Usage, "one statement only").
			WithHint("run them one at a time")
	}
	// allowed_operators applies to sql too (it's read-only but gated)
	if s.Recorder != nil {
		if err := audit.Allowed(s.Cfg, s.Recorder.Identity); err != nil {
			return nil, err
		}
	}
	snap := s.Collector.Collect(ctx)
	if snap == nil {
		return nil, exitcode.New(exitcode.Usage, "no snapshot")
	}
	var b *model.Backend
	target = strings.ToLower(strings.TrimSpace(target))
	switch {
	case target == "primary":
		b = s.primaryBackend(snap)
	case target == "standby":
		for i := range snap.Backends {
			if snap.Backends[i].Reachable && snap.Backends[i].InRecovery {
				b = &snap.Backends[i]
			}
		}
	case strings.HasPrefix(target, "node "):
		var id int
		fmt.Sscanf(target[5:], "%d", &id)
		for i := range snap.Backends {
			if snap.Backends[i].NodeID == id {
				b = &snap.Backends[i]
			}
		}
	case strings.HasPrefix(target, "host "):
		for i := range snap.Backends {
			if snap.Backends[i].Host == target[5:] || snap.Backends[i].Name == target[5:] {
				b = &snap.Backends[i]
			}
		}
	}
	if b == nil {
		return nil, exitcode.New(exitcode.Usage, "no such target "+target).
			WithHint("poolwarden nodes")
	}
	limit := o.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	pend, err := s.Recorder.Begin(ctx, audit.Action{
		Command: "sql " + target + " " + audit.Mask(stmt), Target: b.Name})
	if err != nil {
		return nil, exitcode.New(exitcode.Connection, "cannot write audit entry").Wrap(err)
	}
	pend.Started(s.now())
	res := &SQLResult{}
	var ferr error
	if err := s.SQLRunner.Exec(ctx, b.Host, b.Port, "BEGIN READ ONLY"); err == nil {
		if err := s.SQLRunner.Exec(ctx, b.Host, b.Port,
			"SET LOCAL statement_timeout = '30s'"); err == nil {
			cols, rows, qerr := s.SQLRunner.Query(ctx, b.Host, b.Port, stmt)
			if qerr != nil {
				ferr = qerr
				if strings.Contains(qerr.Error(), "multiple commands") {
					ferr = exitcode.New(exitcode.Usage, "one statement only").
						WithHint("run them one at a time")
				}
			} else {
				res.Cols = cols
				if len(rows) > limit {
					rows = rows[:limit]
				}
				res.Rows = rows
			}
		} else {
			ferr = err
		}
		s.SQLRunner.Exec(ctx, b.Host, b.Port, "ROLLBACK")
	} else {
		ferr = err
	}
	outcome, code := "success", 0
	if ferr != nil {
		outcome, code = "failed", 2
	}
	pend.Finish(ctx, outcome, code, ferr, nil, "")
	if ferr != nil {
		return nil, ferr
	}
	return res, nil
}

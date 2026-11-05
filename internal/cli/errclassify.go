package cli

import (
	"errors"
	"strings"
	"syscall"

	"github.com/smarsh/poolwarden/internal/exitcode"
)

// ClassifyConnError maps a connection error to a short Msg (02 §0 errors
// must be one line; full text goes to Cause).
func ClassifyConnError(addr string, err error) *exitcode.Error {
	var reason string
	switch {
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused"):
		reason = "connection refused"
	case errors.Is(err, syscall.ETIMEDOUT) || strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "deadline exceeded"):
		reason = "timeout"
	case errors.Is(err, syscall.EHOSTUNREACH) || strings.Contains(err.Error(), "no route to host"):
		reason = "no route to host"
	case strings.Contains(err.Error(), "authentication failed") ||
		strings.Contains(err.Error(), "password authentication failed"):
		reason = "authentication failed"
	case strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "name resolution"):
		reason = "unknown host"
	default:
		// last non-empty line of the error, trimmed
		reason = "connection failed"
		lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if s := strings.TrimSpace(lines[i]); s != "" {
				reason = s
				break
			}
		}
	}
	return exitcode.New(exitcode.Connection,
		"cannot reach pgpool at "+addr+" ("+reason+")").
		WithCause(err.Error())
}

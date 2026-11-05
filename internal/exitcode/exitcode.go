// Package exitcode defines poolwarden's process exit codes (design §15) and
// the Error type every user-facing failure is rendered through.
package exitcode

import (
	"errors"
	"fmt"
	"io"
)

const (
	OK                  = 0
	Usage               = 1
	Connection          = 2
	PreflightNoGo       = 3
	Validation          = 4
	ApplyRolledBack     = 5
	ApplyRollbackFailed = 6
	FindingsWarn        = 10
	FindingsCrit        = 11
)

// Error is a user-facing failure: a message, an optional cause and the exact
// next command to run, mapped to a process exit code.
type Error struct {
	Code   int
	Msg    string
	Cause  string
	Hint   string
	Detail string
	Err    error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

func New(code int, msg string) *Error { return &Error{Code: code, Msg: msg} }

func (e *Error) WithCause(cause string) *Error { e.Cause = cause; return e }

func (e *Error) WithHint(hint string) *Error { e.Hint = hint; return e }

// WithDetail sets per-host / extra state shown in the failure table.
func (e *Error) WithDetail(d string) *Error { e.Detail = d; return e }

// Wrap keeps err as the wrapped cause.
func (e *Error) Wrap(err error) *Error { e.Err = err; return e }

// Render writes an error in the spec's error:/cause:/next: form and returns
// the exit code to use.
func Render(w io.Writer, err error) int {
	var e *Error
	if !errors.As(err, &e) {
		fmt.Fprintf(w, "error: %s\n", err)
		return Usage
	}
	fmt.Fprintf(w, "error: %s\n", e.Msg)
	switch {
	case e.Cause != "":
		fmt.Fprintf(w, "cause: %s\n", e.Cause)
	case e.Err != nil:
		fmt.Fprintf(w, "cause: %s\n", e.Err)
	}
	if e.Hint != "" {
		fmt.Fprintf(w, "next:  %s\n", e.Hint)
	}
	return e.Code
}

// Nagios maps an exit code to a Nagios status and label (docs/04 I15).
func Nagios(code int) (status int, label string) {
	switch code {
	case OK:
		return 0, "OK"
	case FindingsWarn:
		return 1, "WARNING"
	case FindingsCrit:
		return 2, "CRITICAL"
	default:
		return 3, "UNKNOWN"
	}
}

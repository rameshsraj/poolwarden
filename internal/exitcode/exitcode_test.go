package exitcode

import (
	"errors"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
		code int
	}{
		{"plain", New(Usage, "bad flag"), "error: bad flag\n", Usage},
		{"cause", New(Usage, "bad flag").WithCause("unknown value"), "error: bad flag\ncause: unknown value\n", Usage},
		{"hint", New(Usage, "bad flag").WithHint("poolwarden --help"), "error: bad flag\nnext:  poolwarden --help\n", Usage},
		{"all", New(Validation, "invalid config").WithCause("cluster: must not be empty").WithHint("poolwarden init"),
			"error: invalid config\ncause: cluster: must not be empty\nnext:  poolwarden init\n", Validation},
		{"wrapped cause", New(Connection, "cannot reach pgpool").Wrap(errors.New("connection refused")),
			"error: cannot reach pgpool\ncause: connection refused\n", Connection},
		{"cause beats wrapped", New(Connection, "down").WithCause("service stopped").Wrap(errors.New("refused")),
			"error: down\ncause: service stopped\n", Connection},
		{"not an Error", errors.New("boom"), "error: boom\n", Usage},
		{"wrapped Error", fmt_wrap(New(FindingsCrit, "crit findings")),
			"error: crit findings\n", FindingsCrit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sb strings.Builder
			if got := Render(&sb, tt.err); got != tt.code {
				t.Errorf("code = %d, want %d", got, tt.code)
			}
			if sb.String() != tt.want {
				t.Errorf("output = %q, want %q", sb.String(), tt.want)
			}
		})
	}
}

func fmt_wrap(err error) error { return fmtWrap(err) }

func TestNagios(t *testing.T) {
	tests := []struct {
		code   int
		status int
		label  string
	}{
		{OK, 0, "OK"},
		{FindingsWarn, 1, "WARNING"},
		{FindingsCrit, 2, "CRITICAL"},
		{Usage, 3, "UNKNOWN"},
		{Connection, 3, "UNKNOWN"},
		{PreflightNoGo, 3, "UNKNOWN"},
		{99, 3, "UNKNOWN"},
	}
	for _, tt := range tests {
		s, l := Nagios(tt.code)
		if s != tt.status || l != tt.label {
			t.Errorf("Nagios(%d) = %d,%q; want %d,%q", tt.code, s, l, tt.status, tt.label)
		}
	}
}

func fmtWrap(err error) error {
	return errors.Join(err)
}

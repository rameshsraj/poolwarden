package cli

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

func TestClassifyConnError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{syscall.ECONNREFUSED, "connection refused"},
		{fmt.Errorf("dial tcp: i/o timeout"), "timeout"},
		{syscall.EHOSTUNREACH, "no route to host"},
		{fmt.Errorf("pgx: password authentication failed for user x"), "authentication failed"},
		{fmt.Errorf("lookup db1: no such host"), "unknown host"},
		{fmt.Errorf("line1\nsomething went wrong"), "something went wrong"},
	}
	for _, c := range cases {
		e := ClassifyConnError("127.0.0.1:9999", c.err)
		if !strings.Contains(e.Msg, c.want) {
			t.Errorf("%v → %q", c.err, e.Msg)
		}
		if !strings.HasPrefix(e.Msg, "cannot reach pgpool at 127.0.0.1:9999") {
			t.Errorf("msg %q", e.Msg)
		}
	}
}

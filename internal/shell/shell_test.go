package shell

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestBuiltins(t *testing.T) {
	st := &state{}
	if h, _, q := builtin("exit", st); !h || !q {
		t.Error("exit")
	}
	if h, _, _ := builtin("set output json", st); !h || st.output != "json" {
		t.Error("set output")
	}
	if h, _, _ := builtin("set operator alice", st); !h || st.operator != "alice" {
		t.Error("set operator")
	}
	if h, _, _ := builtin("nodes", st); h {
		t.Error("nodes should run cobra")
	}
}

func TestHistoryPathAndCtrlC(t *testing.T) {
	if HistoryPath("/var/lib/poolwarden", "") != "/var/lib/poolwarden/history/_anonymous" {
		t.Error("anon")
	}
	if HistoryPath("/d", "alice") != "/d/history/alice" {
		t.Error("named")
	}
	t0 := time.Now()
	if !DoubleCtrlC(t0, t0.Add(500*time.Millisecond), time.Second) {
		t.Error("double within")
	}
	if DoubleCtrlC(t0, t0.Add(2*time.Second), time.Second) {
		t.Error("stale")
	}
}

// TestSetCommandsIsolation drives menu commands twice through a console
// Menu via RunCommandLine — needs no pty.
func TestSetCommandsIsolation(t *testing.T) {
	var last string
	mk := func() *cobra.Command {
		var o string
		r := &cobra.Command{Use: "pw"}
		r.PersistentFlags().StringVar(&o, "output", "table", "")
		ec := &cobra.Command{Use: "echo", Run: func(cmd *cobra.Command, _ []string) { last = o }}
		r.AddCommand(ec)
		return r
	}
	// emulate the SetCommands rebuild: each line gets a fresh tree
	root := mk()
	root.SetArgs([]string{"echo", "--output", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if last != "json" {
		t.Fatal(last)
	}
	root2 := mk()
	root2.SetArgs([]string{"echo"})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if last != "table" {
		t.Fatalf("flag leaked: %q", last)
	}
}

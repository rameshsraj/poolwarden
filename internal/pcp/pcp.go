// Package pcp is the adapter for pgpool-II's pcp_* binaries. Commands run
// through exec.CommandContext with absolute paths from the config; no shell,
// no secrets on argv (PCPPASSFILE only, -w disables the prompt).
package pcp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
)

// Runner executes one external binary; fakeable via pcptest.
type Runner interface {
	Run(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// InputRunner is a Runner that can also feed stdin (pg_md5/pg_enc -p: the
// password goes through stdin and never on argv — docs/04 I26).
type InputRunner interface {
	Runner
	RunInput(ctx context.Context, bin string, stdin []byte, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// OSRunner is the real Runner.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, []byte, int, error) {
	cmd := newCmd(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil
		} else {
			code = -1
		}
	}
	return out.Bytes(), errb.Bytes(), code, err
}

// RunInput runs bin with stdin fed from a byte slice.
func (OSRunner) RunInput(ctx context.Context, bin string, stdin []byte, args ...string) ([]byte, []byte, int, error) {
	cmd := newCmd(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Stdin = bytes.NewReader(stdin)
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			err = nil
		} else {
			code = -1
		}
	}
	return out.Bytes(), errb.Bytes(), code, err
}

// Result is the audited record of one pcp call. There are never secrets on
// argv, so Args is safe to log.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	Exit     int
	Started  time.Time
	Finished time.Time
}

// PCP runs pcp_* commands for one configured endpoint.
type PCP struct {
	R       Runner
	Cfg     config.PCP
	Now     func() time.Time
	mu      sync.Mutex
	results []Result
}

// Results returns the last 100 results recorded by this PCP (docs/03 M6).
func (p *PCP) Results() []Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Result, len(p.results))
	copy(out, p.results)
	return out
}

func (p *PCP) record(res Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results = append(p.results, res)
	if len(p.results) > 100 {
		p.results = p.results[len(p.results)-100:]
	}
}

func (p *PCP) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *PCP) base() []string {
	return []string{"-h", p.Cfg.Host, "-p", strconv.Itoa(p.Cfg.Port), "-U", p.Cfg.User, "-w"}
}

func (p *PCP) run(ctx context.Context, name string, extra ...string) (Result, error) {
	args := append(p.base(), extra...)
	bin := filepath.Join(p.Cfg.BinDir, name)
	res := Result{Args: args, Started: p.now()}
	out, errb, code, err := p.R.Run(ctx, bin, args...)
	res.Finished = p.now()
	res.Stdout = string(out)
	res.Stderr = string(errb)
	res.Exit = code
	p.record(res)
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, fmt.Errorf("%s exited %d: %s", name, code, res.Stderr)
	}
	return res, nil
}

// NodeInfo runs pcp_node_info -v <id>.
func (p *PCP) NodeInfo(ctx context.Context, id int) (model.PoolNode, Result, error) {
	res, err := p.run(ctx, "pcp_node_info", "-v", strconv.Itoa(id))
	if err != nil {
		return model.PoolNode{}, res, err
	}
	n, err := ParseNodeInfo(res.Stdout)
	n.ID = id
	return n, res, err
}

// NodeInfoAll runs pcp_node_info -v --all (docs/04 I24).
func (p *PCP) NodeInfoAll(ctx context.Context) ([]model.PoolNode, Result, error) {
	res, err := p.run(ctx, "pcp_node_info", "-v", "--all")
	if err != nil {
		return nil, res, err
	}
	nodes, err := ParseNodeInfoAll(res.Stdout)
	return nodes, res, err
}

// ProcInfo runs pcp_proc_info -v -P <pid> (or -a for all).
func (p *PCP) ProcInfo(ctx context.Context, pid int) (Result, error) {
	return p.run(ctx, "pcp_proc_info", "-v", "-P", strconv.Itoa(pid))
}

// ProcInfoAll runs pcp_proc_info -v -a.
func (p *PCP) ProcInfoAll(ctx context.Context) (Result, error) {
	return p.run(ctx, "pcp_proc_info", "-v", "-a")
}

// WatchdogInfo runs pcp_watchdog_info -v -w already implied by base args;
// the -v gives the verbose cluster/node blocks.
func (p *PCP) WatchdogInfo(ctx context.Context) (model.Watchdog, Result, error) {
	res, err := p.run(ctx, "pcp_watchdog_info", "-v")
	if err != nil {
		return model.Watchdog{Err: err.Error(), Raw: res.Stdout}, res, err
	}
	w, err := ParseWatchdogInfo(res.Stdout)
	w.Raw = res.Stdout
	return w, res, err
}

func (p *PCP) AttachNode(ctx context.Context, id int) (Result, error) {
	return p.run(ctx, "pcp_attach_node", "-n", strconv.Itoa(id))
}

func (p *PCP) DetachNode(ctx context.Context, id int, gracefully bool) (Result, error) {
	args := []string{}
	if gracefully {
		args = append(args, "-g")
	}
	args = append(args, "-n", strconv.Itoa(id))
	return p.run(ctx, "pcp_detach_node", args...)
}

// PromoteNode runs pcp_promote_node; -s only for a switchover (design §10),
// -g when gracefully.
func (p *PCP) PromoteNode(ctx context.Context, id int, switchover, gracefully bool) (Result, error) {
	args := []string{}
	if switchover {
		args = append(args, "-s")
	}
	if gracefully {
		args = append(args, "-g")
	}
	args = append(args, "-n", strconv.Itoa(id))
	return p.run(ctx, "pcp_promote_node", args...)
}

func (p *PCP) RecoveryNode(ctx context.Context, id int) (Result, error) {
	return p.run(ctx, "pcp_recovery_node", "-n", strconv.Itoa(id))
}

// ReloadConfig runs pcp_reload_config -s c (cluster) or -s l (local).
func (p *PCP) ReloadConfig(ctx context.Context, scope string) (Result, error) {
	return p.run(ctx, "pcp_reload_config", "-s", scope)
}

// newCmd cancels children with SIGTERM and a 10s grace before SIGKILL —
// pcp_* tools run remote actions (recovery, promote) that must not be
// hard-killed mid-flight.
func newCmd(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Op is one administrative action running through the CLI. It survives
// browser disconnects and is journaled to <state>/console-ops/<id>.json.
type Op struct {
	ID       string            `json:"id"`
	Action   string            `json:"action"`
	Argv     []string          `json:"argv"` // redacted display argv
	Operator string            `json:"operator"`
	Status   string            `json:"status"` // running|success|failed|interrupted
	Started  time.Time         `json:"started"`
	Finished *time.Time        `json:"finished,omitempty"`
	ExitCode int               `json:"exit_code"`
	Events   []json.RawMessage `json:"events"`
	Result   json.RawMessage   `json:"result,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type ops struct {
	srv   *Server
	dir   string
	mu    sync.Mutex
	procs map[string]*Op
	subs  map[chan []byte]struct{}
	seq   uint64
}

func newOps(s *Server, stateDir string) *ops {
	o := &ops{
		srv:   s,
		dir:   filepath.Join(stateDir, "console-ops"),
		procs: map[string]*Op{},
		subs:  map[chan []byte]struct{}{},
	}
	o.load()
	return o
}

// load restores journaled ops; anything left "running" when the gateway
// stopped is marked interrupted (outcome unknown — never auto-retried).
func (o *ops) load() {
	entries, err := os.ReadDir(o.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(o.dir, e.Name()))
		if err != nil {
			continue
		}
		var op Op
		if json.Unmarshal(b, &op) != nil {
			continue
		}
		if op.Status == "running" {
			op.Status = "interrupted"
			op.Error = "gateway restarted while the operation was running; " +
				"check `poolwarden audit show` and cluster state before retrying"
		}
		o.procs[op.ID] = &op
	}
}

func (o *ops) save(op *Op) {
	_ = os.MkdirAll(o.dir, 0700)
	b, err := json.Marshal(op)
	if err != nil {
		return
	}
	tmp := filepath.Join(o.dir, op.ID+".tmp")
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(o.dir, op.ID+".json"))
}

func (o *ops) subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 64)
	o.mu.Lock()
	o.subs[ch] = struct{}{}
	o.mu.Unlock()
	return ch, func() {
		o.mu.Lock()
		delete(o.subs, ch)
		o.mu.Unlock()
		close(ch)
	}
}

func (o *ops) emit(op *Op, data json.RawMessage) {
	o.seq++
	ev := map[string]any{"op_id": op.ID, "action": op.Action, "status": op.Status}
	if data != nil {
		ev["data"] = data
	}
	raw, _ := json.Marshal(ev)
	sse := []byte(fmt.Sprintf("id: op-%d\nevent: op\ndata: %s\n\n", o.seq, raw))
	o.mu.Lock()
	for ch := range o.subs {
		select {
		case ch <- sse:
		default:
		}
	}
	o.mu.Unlock()
	o.save(op)
}

// --- action allowlist --------------------------------------------------------

// actionSpec describes one allowlisted mutation and how to build its argv.
type actionSpec struct {
	build func(req actionReq) ([]string, error)
	// tokenConfirm: the action runs through RunControl with a typed
	// confirmation token, so --plan is meaningful and execution needs
	// --yes --confirm <token>. Non-RunControl actions (staging edits)
	// take only --yes and reject /api/plan.
	tokenConfirm bool
}

var actionAllow = map[string]actionSpec{
	"attach": {build: nodeAction("attach"), tokenConfirm: true},
	"detach": {build: nodeAction("detach"), tokenConfirm: true},
	"promote": {build: func(r actionReq) ([]string, error) {
		a, err := nodeAction("promote")(r)
		if err != nil {
			return nil, err
		}
		if r.Gracefully {
			a = append(a, "--gracefully")
		}
		if r.PgpoolOnly {
			a = append(a, "--pgpool-only")
		}
		return a, nil
	}, tokenConfirm: true},
	"recover": {build: func(r actionReq) ([]string, error) {
		a, err := nodeAction("recover")(r)
		if err != nil {
			return nil, err
		}
		if r.Timeout != "" {
			a = append(a, "--timeout", r.Timeout)
		}
		return a, nil
	}, tokenConfirm: true},
	"reload": {build: func(r actionReq) ([]string, error) {
		a := []string{"reload"}
		if r.Local {
			a = append(a, "--local")
		}
		return a, nil
	}, tokenConfirm: true},
	"config-set": {build: func(r actionReq) ([]string, error) {
		if r.Param == "" || r.Value == "" {
			return nil, fmt.Errorf("param and value required")
		}
		return []string{"config", "set", r.Param, r.Value}, nil
	}},
	"config-unset": {build: func(r actionReq) ([]string, error) {
		if r.Param == "" {
			return nil, fmt.Errorf("param required")
		}
		return []string{"config", "unset", r.Param}, nil
	}},
	"config-discard": {build: func(actionReq) ([]string, error) {
		return []string{"config", "discard"}, nil
	}},
	"config-apply": {build: func(actionReq) ([]string, error) {
		return []string{"config", "apply"}, nil
	}, tokenConfirm: true},
	"config-rollback": {build: func(r actionReq) ([]string, error) {
		if r.Value == "" {
			return nil, fmt.Errorf("version required")
		}
		return []string{"config", "rollback", r.Value}, nil
	}, tokenConfirm: true},
	"service": {build: func(r actionReq) ([]string, error) {
		switch r.Verb {
		case "start", "stop", "restart":
		default:
			return nil, fmt.Errorf("verb required: start|stop|restart")
		}
		switch r.Unit {
		case "pgpool", "postgresql", "watchdog":
		default:
			return nil, fmt.Errorf("unit required: pgpool|postgresql|watchdog")
		}
		if r.Host == "" {
			return nil, fmt.Errorf("host required")
		}
		return []string{"service", r.Verb, r.Unit, r.Host}, nil
	}, tokenConfirm: true},
}

func nodeAction(name string) func(actionReq) ([]string, error) {
	return func(r actionReq) ([]string, error) {
		if r.ID < 0 {
			return nil, fmt.Errorf("node id required")
		}
		return []string{name, strconv.Itoa(r.ID)}, nil
	}
}

type actionReq struct {
	Action         string `json:"action"`
	ID             int    `json:"id"`
	Confirm        string `json:"confirm"`
	Force          bool   `json:"force"`
	Gracefully     bool   `json:"gracefully"`
	PgpoolOnly     bool   `json:"pgpool_only"`
	Local          bool   `json:"local"`
	Timeout        string `json:"timeout"`
	Param          string `json:"param"`
	Value          string `json:"value"`
	Verb           string `json:"verb"`
	Unit           string `json:"unit"`
	Host           string `json:"host"`
	Ticket         string `json:"ticket"`
	IdempotencyKey string `json:"idempotency_key"`
}

// actionArgv builds the validated CLI argv for a mutation. No shell, no
// free-form command strings — the allowlist above is the only surface.
func (o *ops) actionArgv(req actionReq) ([]string, error) {
	spec, ok := actionAllow[req.Action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
	argv, err := spec.build(req)
	if err != nil {
		return nil, err
	}
	argv = append(argv, "-o", "json", "--progress")
	if spec.tokenConfirm {
		if req.Confirm == "" {
			return nil, fmt.Errorf("confirm token required (run /api/plan first)")
		}
		argv = append(argv, "--yes", "--confirm", req.Confirm)
	} else {
		argv = append(argv, "--yes")
	}
	if req.Force {
		argv = append(argv, "--force")
	}
	if req.Ticket != "" {
		argv = append(argv, "--ticket", req.Ticket)
	}
	return argv, nil
}

// run spawns the CLI and streams real progress. Stdout carries the final
// JSON result; stderr carries NDJSON progress events + human diagnostics.
func (o *ops) run(op *Op, argv []string) {
	ctx := context.Background()
	full := append([]string{o.srv.o.CLIBin, "-c", o.srv.o.Config, "--operator", op.Operator}, argv...)
	display := append([]string{"poolwarden"}, argv...)
	op.Argv = display
	op.Status = "running"
	op.Started = time.Now()
	o.emit(op, mustJSON(map[string]any{"stage": "spawn", "argv": display}))

	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		op.Status, op.Error = "failed", err.Error()
		o.finish(op)
		return
	}
	if err := cmd.Start(); err != nil {
		op.Status, op.Error = "failed", "cannot start poolwarden: "+err.Error()
		o.finish(op)
		return
	}
	// stderr: NDJSON progress events + plain diagnostics
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			var ev struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Type == "progress" {
				op.Events = append(op.Events, ev.Data)
				o.emit(op, ev.Data)
			}
		}
	}()
	err = cmd.Wait()
	op.ExitCode = 0
	if ee, ok := err.(*exec.ExitError); ok {
		op.ExitCode = ee.ExitCode()
	} else if err != nil {
		op.ExitCode = -1
		op.Error = err.Error()
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) > 0 {
		op.Result = json.RawMessage(out)
	}
	switch {
	case op.ExitCode == 0:
		op.Status = "success"
	default:
		op.Status = "failed"
		if op.Error == "" {
			op.Error = fmt.Sprintf("poolwarden exited %d", op.ExitCode)
		}
	}
	o.finish(op)
}

func (o *ops) finish(op *Op) {
	now := time.Now()
	op.Finished = &now
	o.emit(op, mustJSON(map[string]any{
		"stage": "done", "status": op.Status, "exit_code": op.ExitCode,
		"error": op.Error,
	}))
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// --- HTTP handlers -----------------------------------------------------------

// plan runs the CLI in --plan mode: real preflight + plan + confirm token,
// no execution.
func (s *Server) plan(w http.ResponseWriter, r *http.Request, sess *session) {
	var req actionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("bad request"))
		return
	}
	spec, ok := actionAllow[req.Action]
	if !ok {
		writeJSON(w, 400, errBody("unknown action "+strconv.Quote(req.Action)))
		return
	}
	if !spec.tokenConfirm {
		// Non-RunControl actions have no preflight/plan phase; --plan is
		// ignored by the CLI and would EXECUTE — refuse it here.
		writeJSON(w, 400, errBody(req.Action+" does not support planning"))
		return
	}
	argv, err := spec.build(req)
	if err != nil {
		writeJSON(w, 400, errBody(err.Error()))
		return
	}
	argv = append(argv, "--plan", "-o", "json")
	if req.Force {
		argv = append(argv, "--force")
	}
	full := append([]string{"-c", s.o.Config, "--operator", sess.Operator}, argv...)
	out, stderr, code := s.runCLI(r.Context(), full)
	if len(out) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		env := map[string]any{"exit_code": code, "argv": append([]string{"poolwarden"}, argv...)}
		raw := json.RawMessage(out)
		env["result"] = raw
		if code != 0 {
			env["error"] = strings.TrimSpace(string(stderr))
		}
		writeJSON(w, 200, env)
		return
	}
	writeJSON(w, 502, cliErr(stderr, nil))
}

// action starts a supervised CLI mutation; idempotent on IdempotencyKey.
func (s *Server) action(w http.ResponseWriter, r *http.Request, sess *session) {
	var req actionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("bad request"))
		return
	}
	if req.IdempotencyKey != "" {
		s.ops.mu.Lock()
		for _, op := range s.ops.procs {
			// idempotency: same key returns the existing op
			if strings.HasSuffix(op.ID, "|"+req.IdempotencyKey) {
				s.ops.mu.Unlock()
				writeJSON(w, 200, op)
				return
			}
		}
		s.ops.mu.Unlock()
	}
	argv, err := s.ops.actionArgv(req)
	if err != nil {
		writeJSON(w, 400, errBody(err.Error()))
		return
	}
	id := rnd(16)
	if req.IdempotencyKey != "" {
		id += "|" + req.IdempotencyKey
	}
	op := &Op{ID: id, Action: req.Action, Operator: sess.Operator, Status: "queued"}
	s.ops.mu.Lock()
	s.ops.procs[id] = op
	s.ops.mu.Unlock()
	go s.ops.run(op, argv)
	writeJSON(w, 202, op)
}

func (s *Server) listOps(w http.ResponseWriter, _ *http.Request, _ *session) {
	s.ops.mu.Lock()
	var out []*Op
	for _, op := range s.ops.procs {
		out = append(out, op)
	}
	s.ops.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	writeJSON(w, 200, map[string]any{"ops": out})
}

func (s *Server) getOp(w http.ResponseWriter, r *http.Request, _ *session) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ops/")
	s.ops.mu.Lock()
	op := s.ops.procs[id]
	s.ops.mu.Unlock()
	if op == nil {
		writeJSON(w, 404, errBody("op not found"))
		return
	}
	writeJSON(w, 200, op)
}

// --- one-shot read views through the CLI -------------------------------------

// viewAllow maps API view names to poolwarden argv (all read-only).
var viewAllow = map[string][]string{
	"status":         {"status"},
	"nodes":          {"nodes"},
	"lag":            {"lag", "--history", "1h"},
	"slots":          {"slots"},
	"pools":          {"pools"},
	"procs":          {"procs"},
	"conns":          {"conns"},
	"watchdog":       {"watchdog"},
	"health":         {"health"},
	"topology":       {"topology", "show"},
	"diagnose":       {"diagnose"},
	"forecast":       {"forecast"},
	"errors":         {"errors", "--since", "24h"},
	"errors-summary": {"errors", "summary", "--since", "24h"},
	"logs":           {"logs", "--since", "1h", "--classify"},
	"audit-list":     {"audit", "list", "--since", "24h"},
	"audit-sessions": {"audit", "sessions", "--since", "24h"},
	"audit-verify":   {"audit", "verify"},
	"config-list":    {"config", "list"},
	"config-diff":    {"config", "diff"},
	"config-history": {"config", "history"},
	"hba-list":       {"hba", "list"},
	"pooluser-list":  {"pooluser", "list"},
	"pcpuser-list":   {"pcpuser", "list"},
	"selfcheck":      {"config", "self-check"},
	"version":        {"version"},
}

// parametric views: /api/view/node?id=2 etc.
var paramViews = map[string]func(q map[string]string) ([]string, error){
	"node": func(q map[string]string) ([]string, error) {
		return []string{"nodes", q["id"]}, nil
	},
	"proc": func(q map[string]string) ([]string, error) {
		return []string{"procs", q["id"]}, nil
	},
	"explain": func(q map[string]string) ([]string, error) {
		return []string{"explain", q["id"]}, nil
	},
	"service-status": func(q map[string]string) ([]string, error) {
		if q["unit"] == "" || q["host"] == "" {
			return nil, fmt.Errorf("unit and host required")
		}
		return []string{"service", "status", q["unit"], q["host"]}, nil
	},
	"errors-show": func(q map[string]string) ([]string, error) {
		return []string{"errors", "show", q["id"]}, nil
	},
	"audit-show": func(q map[string]string) ([]string, error) {
		return []string{"audit", "show", q["id"]}, nil
	},
	"config-get": func(q map[string]string) ([]string, error) {
		return []string{"config", "get", q["name"]}, nil
	},
	"config-describe": func(q map[string]string) ([]string, error) {
		return []string{"config", "describe", q["name"]}, nil
	},
}

func (s *Server) view(w http.ResponseWriter, r *http.Request, _ *session) {
	name := strings.TrimPrefix(r.URL.Path, "/api/view/")
	name = strings.TrimSuffix(name, "/")
	q := map[string]string{}
	for k := range r.URL.Query() {
		q[k] = r.URL.Query().Get(k)
	}
	var argv []string
	if argv0, ok := viewAllow[name]; ok {
		argv = argv0
	} else if fn, ok := paramViews[name]; ok {
		var err error
		argv, err = fn(q)
		if err != nil || argv[len(argv)-1] == "" {
			writeJSON(w, 400, errBody("missing parameter"))
			return
		}
	} else {
		writeJSON(w, 404, errBody("unknown view"))
		return
	}
	full := append([]string{"-c", s.o.Config, "-o", "json"}, argv...)
	out, stderr, code := s.runCLI(r.Context(), full)
	if len(out) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(out)
		return
	}
	writeJSON(w, 502, cliErr(stderr, fmt.Errorf("exit %d", code)))
}

// runCLI executes poolwarden with validated argv (no shell) and returns
// stdout, stderr and the exit code. Read views exit 10/11 on WARN/CRIT
// findings — that is data, not failure, so output is returned regardless.
func (s *Server) runCLI(ctx context.Context, args []string) (stdout, stderr []byte, code int) {
	cmd := exec.CommandContext(ctx, s.o.CLIBin, args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return so.Bytes(), se.Bytes(), code
}

// --- connection (setup wizard) ------------------------------------------------

// getConnection returns the installed poolwarden.yaml verbatim — it holds no
// secrets (passwords live in passfiles referenced by path).
func (s *Server) getConnection(w http.ResponseWriter, _ *http.Request, _ *session) {
	b, err := os.ReadFile(s.o.Config)
	if err != nil {
		writeJSON(w, 200, map[string]any{"exists": false, "yaml": ""})
		return
	}
	writeJSON(w, 200, map[string]any{"exists": true, "yaml": string(b)})
}

// testConnection writes the candidate YAML to a 0600 temp file and runs
// `poolwarden -c <cand> probe -o json` — real per-endpoint results.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request, _ *session) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("bad request"))
		return
	}
	path, cleanup, err := writeCandidate(s.o.StateDir, req.YAML)
	if err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	defer cleanup()
	out, stderr, code := s.runCLI(r.Context(),
		[]string{"-c", path, "-o", "json", "probe"})
	if len(out) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
		return
	}
	writeJSON(w, 502, cliErr(stderr, fmt.Errorf("exit %d", code)))
}

// installConnection validates the candidate with `config install` (audited,
// atomic, backup) then restarts the monitor on the new config.
func (s *Server) installConnection(w http.ResponseWriter, r *http.Request, sess *session) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("bad request"))
		return
	}
	path, cleanup, err := writeCandidate(s.o.StateDir, req.YAML)
	if err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	defer cleanup()
	full := []string{"-c", s.o.Config, "-o", "json", "--operator", sess.Operator,
		"config", "install", path, "--yes"}
	out, stderr, code := s.runCLI(r.Context(), full)
	if code != 0 {
		writeJSON(w, 502, cliErr(stderr, fmt.Errorf("install exited %d", code)))
		return
	}
	s.mon.restart() // reload on the new endpoints
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// writeCandidate persists wizard YAML to a 0600 file under the console state
// dir; cleanup removes it after the probe/install.
func writeCandidate(stateDir, data string) (path string, cleanup func(), err error) {
	dir := filepath.Join(stateDir, "console-candidates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "cand-*.yaml")
	if err != nil {
		return "", nil, err
	}
	p := f.Name()
	if _, err := f.WriteString(data); err == nil {
		err = f.Sync()
	}
	_ = f.Close()
	_ = os.Chmod(p, 0600)
	if err != nil {
		_ = os.Remove(p)
		return "", nil, err
	}
	return p, func() { _ = os.Remove(p) }, nil
}

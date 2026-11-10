package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// monitor supervises ONE `poolwarden monitor` process and fans its NDJSON
// events out to every SSE subscriber. All browser clients share this single
// collection stream; a process exit triggers a bounded backoff restart.
type monitor struct {
	cli, cfg string

	mu      sync.Mutex
	subs    map[chan []byte]struct{}
	ring    [][]byte // last events for Last-Event-ID replay
	seq     uint64
	last    []byte // last snapshot/collect_error event (sent on subscribe)
	running bool
	cur     *exec.Cmd // live monitor process, for restart()
}

const ringSize = 512

func newMonitor(cli, cfg string) *monitor {
	return &monitor{cli: cli, cfg: cfg, subs: map[chan []byte]struct{}{}}
}

// start launches the supervisor loop; it keeps restarting the CLI monitor
// with backoff until ctx ends.
func (m *monitor) start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *monitor) loop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := m.run(ctx)
		m.setRunning(false)
		m.broadcast([]byte(fmt.Sprintf("event: monitor\ndata: {\"state\":\"down\",\"reason\":%q}\n\n",
			errString(err))))
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "monitor exited"
	}
	return err.Error()
}

func (m *monitor) setRunning(v bool) {
	m.mu.Lock()
	m.running = v
	if !v {
		m.cur = nil
	}
	m.mu.Unlock()
}

// restart kills the live monitor; the supervisor loop relaunches it on the
// (possibly updated) config. Called after `config install`.
func (m *monitor) restart() {
	m.mu.Lock()
	cmd := m.cur
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// run executes `poolwarden -c <cfg> monitor` and streams each NDJSON line to
// subscribers as one SSE event. Returns when the process exits or ctx ends.
func (m *monitor) run(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, m.cli, "-c", m.cfg, "monitor")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start poolwarden monitor: %w", err)
	}
	m.mu.Lock()
	m.cur = cmd
	m.running = true
	m.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev struct {
			Seq  uint64          `json:"seq"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		m.push(ev.Type, ev.Seq, line)
	}
	select {
	case err := <-done:
		if err != nil && stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

// push assigns an SSE id and broadcasts; snapshot/collect_error also update
// the "latest" frame sent to new subscribers.
func (m *monitor) push(typ string, seq uint64, line []byte) {
	m.mu.Lock()
	m.seq = seq
	sse := []byte(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", seq, typ, line))
	m.ring = append(m.ring, sse)
	if len(m.ring) > ringSize {
		m.ring = m.ring[len(m.ring)-ringSize:]
	}
	if typ == "snapshot" || typ == "collect_error" {
		m.last = sse
	}
	m.mu.Unlock()
	m.broadcast(sse)
}

func (m *monitor) broadcast(sse []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- sse:
		default: // slow subscriber: drop, ring buffer covers reconnect
		}
	}
}

func (m *monitor) subscribe() (ch chan []byte, last []byte, after uint64, cancel func()) {
	ch = make(chan []byte, 64)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	last = m.last
	after = m.seq
	m.mu.Unlock()
	return ch, last, after, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
		close(ch)
	}
}

// replay returns buffered events with id > afterID (Last-Event-ID).
func (m *monitor) replay(afterID uint64) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][]byte
	for _, e := range m.ring {
		var id uint64
		fmt.Sscanf(string(e), "id: %d", &id)
		if id > afterID {
			out = append(out, e)
		}
	}
	return out
}

// stream is GET /api/stream: SSE fan-out of the shared monitor.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, _ *session) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ch, last, _, cancel := s.mon.subscribe()
	defer cancel()
	och, ocancel := s.ops.subscribe()
	defer ocancel()

	// replay or send latest snapshot so the client has state immediately
	if lid := r.Header.Get("Last-Event-ID"); lid != "" {
		var after uint64
		fmt.Sscanf(lid, "%d", &after)
		for _, e := range s.mon.replay(after) {
			_, _ = w.Write(e)
		}
	} else if last != nil {
		_, _ = w.Write(last)
	}
	fl.Flush()

	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			_, _ = w.Write(e)
			fl.Flush()
		case e := <-och:
			_, _ = w.Write(e)
			fl.Flush()
		case <-hb.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

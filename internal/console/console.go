// Package console is the optional web gateway for poolwarden. It holds no
// cluster logic of its own: every read and every mutation is executed by the
// poolwarden CLI (validated argv, no shell), so behaviour, safeguards and the
// audit trail are identical to terminal use.
package console

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Options configures Server.
type Options struct {
	Addr     string // listen address, e.g. 127.0.0.1:8080
	Token    string // access token exchanged for a session
	CLIBin   string // path to the poolwarden binary
	Config   string // poolwarden.yaml path (-c)
	StateDir string // console state: ops journal lives here
	Assets   fs.FS  // embedded SPA (may be nil for API-only use)
	TLSCert  string // optional TLS cert
	TLSKey   string // optional TLS key
}

type session struct {
	ID       string
	Operator string
	CSRF     string
	Created  time.Time
}

// Server is the console HTTP gateway.
type Server struct {
	o        Options
	mon      *monitor
	ops      *ops
	mu       sync.Mutex
	sessions map[string]*session
}

// NewServer builds the gateway; assets may be nil (API-only).
func NewServer(o Options) *Server {
	if o.CLIBin == "" {
		o.CLIBin = "poolwarden"
	}
	if o.Config == "" {
		o.Config = "/etc/poolwarden.yaml"
	}
	s := &Server{
		o:        o,
		sessions: map[string]*session{},
	}
	s.mon = newMonitor(o.CLIBin, o.Config)
	s.ops = newOps(s, o.StateDir)
	return s
}

// Run starts the monitor subprocess and serves HTTP until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	s.mon.start(ctx)
	mux := http.NewServeMux()
	s.routes(mux)
	srv := &http.Server{Addr: s.o.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	var err error
	if s.o.TLSCert != "" {
		err = srv.ListenAndServeTLS(s.o.TLSCert, s.o.TLSKey)
	} else {
		err = srv.ListenAndServe()
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.requireAuth(func(w http.ResponseWriter, _ *http.Request, sess *session) {
		writeJSON(w, 200, map[string]any{"operator": sess.Operator, "csrf": sess.CSRF})
	}))
	mux.HandleFunc("GET /api/stream", s.requireAuth(s.stream))
	mux.HandleFunc("GET /api/view/", s.requireAuth(s.view))
	mux.HandleFunc("POST /api/plan", s.requireAuth(s.plan))
	mux.HandleFunc("POST /api/action", s.requireAuth(s.action))
	mux.HandleFunc("GET /api/ops", s.requireAuth(s.listOps))
	mux.HandleFunc("GET /api/ops/", s.requireAuth(s.getOp))
	mux.HandleFunc("GET /api/connection", s.requireAuth(s.getConnection))
	mux.HandleFunc("POST /api/connection/test", s.requireAuth(s.testConnection))
	mux.HandleFunc("POST /api/connection/install", s.requireAuth(s.installConnection))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	if s.o.Assets != nil {
		mux.HandleFunc("GET /", s.spa)
	}
}

// --- auth -------------------------------------------------------------------

func rnd(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type loginReq struct {
	Token    string `json:"token"`
	Operator string `json:"operator"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, 400, errBody("bad request"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.o.Token)) != 1 {
		writeJSON(w, 401, errBody("invalid token"))
		return
	}
	op := strings.TrimSpace(req.Operator)
	if op == "" {
		op = "console"
	}
	sess := &session{ID: rnd(32), Operator: op, CSRF: rnd(32), Created: time.Now()}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "pwsess", Value: sess.ID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "pwcsrf", Value: sess.CSRF, Path: "/",
		SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600,
	})
	writeJSON(w, 200, map[string]any{"operator": op, "csrf": sess.CSRF})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("pwsess"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// sessionOf validates the session cookie and, for mutating methods, the
// Origin and the double-submit CSRF token.
func (s *Server) sessionOf(w http.ResponseWriter, r *http.Request) *session {
	c, err := r.Cookie("pwsess")
	if err != nil {
		return nil
	}
	s.mu.Lock()
	sess := s.sessions[c.Value]
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if og := r.Header.Get("Origin"); og != "" {
			host := r.Host
			if !strings.Contains(og, "://"+host) {
				return nil
			}
		}
		cc, err := r.Cookie("pwcsrf")
		if err != nil || r.Header.Get("X-CSRF") != sess.CSRF || cc.Value != sess.CSRF {
			return nil
		}
	}
	return sess
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.sessionOf(w, r)
		if sess == nil {
			writeJSON(w, 401, errBody("unauthenticated"))
			return
		}
		next(w, r, sess)
	}
}

// --- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]any { return map[string]any{"error": msg} }

func cliErr(stderr []byte, err error) map[string]any {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if i := strings.LastIndex(msg, "error: "); i >= 0 {
		msg = msg[i:]
	}
	return map[string]any{"error": msg}
}

const noFrontend = `<!doctype html><title>poolwarden console</title>
<body style="background:#0b0f14;color:#8b98a9;font-family:monospace;padding:3em">
<h2>poolwarden console</h2>
<p>The API is running but the web frontend is not embedded in this binary.</p>
<p>Build it with <code>make web</code> (or use the console Docker image).</p>
</body>`

// spa serves the embedded frontend with index.html fallback for client routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	f, err := s.o.Assets.Open(p)
	if err != nil {
		// SPA fallback
		f, err = s.o.Assets.Open("index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(noFrontend))
			return
		}
	}
	defer f.Close()
	st, _ := f.Stat()
	name := p
	if strings.HasSuffix(r.URL.Path, "/") || !strings.Contains(name, ".") {
		name = "index.html"
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), bytes.NewReader(data))
}

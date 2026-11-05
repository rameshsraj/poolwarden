// Package audit is the tamper-evident audit trail (design 01 §11): chained
// JSONL entries, daily rotation, retention, identity resolution and masking.
// Field order in Entry is the canonical hash order — do not reorder.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/user"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/remote"
)

// Env abstracts OS lookups so identity resolution is testable.
type Env interface {
	Getenv(string) string
	LookupUser(uid int) (string, error)
	Hostname() string
	Pid() int
	TTY() string
}

type realEnv struct{}

func (realEnv) Getenv(k string) string { return getenv(k) }
func (realEnv) LookupUser(uid int) (string, error) {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
func (realEnv) Hostname() string { h, _ := hostname(); return h }
func (realEnv) Pid() int         { return pid() }
func (realEnv) TTY() string      { return ttyName() }

// RealEnv returns the production Env.
func RealEnv() Env { return realEnv{} }

// Identity is who ran the command (01 §11 signals).
type Identity struct {
	SSHKeyFingerprint string `json:"ssh_key_fingerprint,omitempty"`
	SSHKeyComment     string `json:"ssh_key_comment,omitempty"`
	KeyOwner          string `json:"key_owner,omitempty"`
	LoginUID          int    `json:"login_uid"`
	LoginUser         string `json:"login_user,omitempty"`
	SudoUser          string `json:"sudo_user,omitempty"`
	SSHClient         string `json:"ssh_client,omitempty"`
	Declared          string `json:"declared,omitempty"`
	TTY               string `json:"tty,omitempty"`
	Hostname          string `json:"hostname,omitempty"`
	PID               int    `json:"pid"`
	OSUser            string `json:"os_user,omitempty"`
	Mismatch          bool   `json:"identity_mismatch,omitempty"`
}

// Operator is the resolved operator name (order per 01 §11).
func (i Identity) Operator() string {
	for _, c := range []string{i.KeyOwner, i.Declared, i.SudoUser, i.LoginUser, i.OSUser} {
		if c != "" {
			return c
		}
	}
	return ""
}

// Resolve builds the Identity from environment signals.
func Resolve(env Env, fsys pwfs.FS, cfg *config.Config, declared string) Identity {
	id := Identity{
		Declared: declared, TTY: env.TTY(), Hostname: env.Hostname(),
		PID: env.Pid(), LoginUID: -1,
	}
	if sudo := env.Getenv("SUDO_USER"); sudo != "" {
		id.SudoUser = sudo
	}
	if sc := env.Getenv("SSH_CONNECTION"); sc != "" {
		parts := strings.Fields(sc)
		if len(parts) >= 2 {
			id.SSHClient = parts[0] + ":" + parts[1]
		}
	}
	if f := env.Getenv("SSH_USER_AUTH"); f != "" {
		if data, err := fsys.ReadFile(f); err == nil {
			var last []byte
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "publickey ") {
					last = []byte(line)
				}
			}
			if last != nil {
				if key, _, _, _, err := ssh.ParseAuthorizedKey(last); err == nil {
					id.SSHKeyFingerprint = ssh.FingerprintSHA256(key)
					id.SSHKeyComment = strings.Join(strings.Fields(string(last))[3:], " ")
				}
			}
		}
	}
	if cfg != nil && id.SSHKeyFingerprint != "" {
		id.KeyOwner = cfg.OperatorFor(id.SSHKeyFingerprint)
	}
	if lu := readLoginUID(fsys); lu >= 0 {
		id.LoginUID = lu
		if name, err := env.LookupUser(lu); err == nil {
			id.LoginUser = name
		}
	}
	if u, err := env.LookupUser(id.LoginUID); err == nil && id.OSUser == "" && id.LoginUID >= 0 {
		_ = u
	}
	if id.OSUser == "" {
		if name, err := env.LookupUser(-2); err == nil { // -2 = euid in fake; real: current
			id.OSUser = name
		}
	}
	id.Mismatch = id.KeyOwner != "" && id.Declared != "" && id.KeyOwner != id.Declared
	return id
}

func readLoginUID(fsys pwfs.FS) int {
	data, err := fsys.ReadFile("/proc/self/loginuid")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n == 4294967295 {
		return -1
	}
	return n
}

var secretPats = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(--password[ =])(\S+)`),
	regexp.MustCompile(`(?i)(password=)(\S+)`),
	regexp.MustCompile(`(PGPASSWORD=)(\S+)`),
	regexp.MustCompile(`(-W )(\S+)`),
	regexp.MustCompile(`scram-sha-256\$\S+`),
	regexp.MustCompile(`(?i)\bmd5[0-9a-f]{32}\b`),
}

// Mask redacts secrets in a command/diff line.
func Mask(s string) string {
	for _, p := range secretPats {
		s = p.ReplaceAllString(s, "${1}***")
	}
	return s
}

// Change is a before/after diff recorded for mutating commands.
type Change struct {
	Diff   string            `json:"diff,omitempty"`
	Before map[string]string `json:"before,omitempty"`
	After  map[string]string `json:"after,omitempty"`
}

// Entry is one audit line. Field order is canonical — do not reorder.
type Entry struct {
	EventID           string                 `json:"event_id"`
	SessionID         string                 `json:"session_id"`
	Seq               int64                  `json:"seq"`
	Phase             string                 `json:"phase"` // requested|finished
	RequestedAt       *model.JSONTime        `json:"requested_at,omitempty"`
	ConfirmedAt       *model.JSONTime        `json:"confirmed_at,omitempty"`
	StartedAt         *model.JSONTime        `json:"started_at,omitempty"`
	FinishedAt        *model.JSONTime        `json:"finished_at,omitempty"`
	DurationMS        int64                  `json:"duration_ms"`
	Steps             []remote.Step          `json:"steps,omitempty"`
	Identity          Identity               `json:"identity"`
	Operator          string                 `json:"operator"`
	Command           string                 `json:"command"`
	Cluster           string                 `json:"cluster"`
	Target            string                 `json:"target,omitempty"`
	Ticket            string                 `json:"ticket,omitempty"`
	Preflight         *model.PreflightResult `json:"preflight,omitempty"`
	Force             bool                   `json:"force,omitempty"`
	Change            *Change                `json:"change,omitempty"`
	Outcome           string                 `json:"outcome,omitempty"` // success|failed|rolled_back|aborted|refused
	ExitCode          int                    `json:"exit_code,omitempty"`
	Error             string                 `json:"error,omitempty"`
	Warn              string                 `json:"warn,omitempty"`
	BackupVersion     string                 `json:"backup_version,omitempty"`
	PoolwardenVersion string                 `json:"poolwarden_version"`
	Host              string                 `json:"host"`
	PrevHash          string                 `json:"prev_hash"`
	Hash              string                 `json:"hash"`
}

// canonical returns the entry as JSON with Hash cleared.
func (e *Entry) canonical() []byte {
	c := *e
	c.Hash = ""
	b, _ := json.Marshal(c)
	return b
}

func hashOf(prev string, canon []byte) string {
	h := sha256.Sum256(append([]byte(prev+"\n"), canon...))
	return hex.EncodeToString(h[:])
}

// SyslogSink receives each entry (authpriv.notice).
type SyslogSink interface {
	Write(priority, msg string) error
}

// Writer appends chained entries.
type Writer struct {
	FS            pwfs.FS
	Clock         clock.Clock
	Path          string
	Syslog        SyslogSink
	Version, Host string
	RetentionDays int
	// Flock overrides the lock impl in tests (nil → syscall flock).
	Flock func(path string) (lock, unlock func() error, err error)
}

func (w *Writer) now() time.Time {
	if w.Clock != nil {
		return w.Clock.Now()
	}
	return time.Now()
}

// Append locks, rotates if needed, chains, appends and syslogs.
func (w *Writer) Append(ctx context.Context, e *Entry) error {
	if w.FS == nil {
		w.FS = pwfs.OS{}
	}
	if w.RetentionDays <= 0 {
		w.RetentionDays = 30
	}
	lockPath := w.Path + ".lock"
	fl := w.Flock
	if fl == nil {
		fl = flockPath
	}
	lock, unlock, err := fl(lockPath)
	if err != nil {
		return err
	}
	if err := lock(); err != nil {
		return err
	}
	defer unlock()
	dir := w.Path[:strings.LastIndex(w.Path, "/")]
	if err := w.FS.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := w.rotateLocked(); err != nil {
		return err
	}
	w.expireLocked(w.now())
	prevHash, seq := w.tailLocked()
	e.PrevHash = prevHash
	e.Seq = seq + 1
	e.PoolwardenVersion = w.Version
	e.Host = w.Host
	e.Hash = hashOf(e.PrevHash, e.canonical())
	line, _ := json.Marshal(e)
	f, err := w.FS.OpenAppend(w.Path, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if w.Syslog != nil {
		w.Syslog.Write("info", string(line))
	}
	return nil
}

// rotateLocked renames the active file to .YYYYMMDD when a new UTC day began.
func (w *Writer) rotateLocked() error {
	fi, err := w.FS.Stat(w.Path)
	if err != nil || fi.Size() == 0 {
		return nil
	}
	// read first line's requested/finished ts to decide the file's day
	data, err := w.FS.ReadFile(w.Path)
	if err != nil {
		return err
	}
	var first Entry
	for _, l := range strings.Split(string(data), "\n") {
		if l == "" {
			continue
		}
		if json.Unmarshal([]byte(l), &first) != nil {
			return nil
		}
		break
	}
	var day time.Time
	if first.RequestedAt != nil {
		day = time.Time(*first.RequestedAt)
	} else if first.FinishedAt != nil {
		day = time.Time(*first.FinishedAt)
	}
	if day.IsZero() {
		return nil
	}
	now := w.now()
	if day.UTC().Format("2006-01-02") == now.UTC().Format("2006-01-02") {
		return nil
	}
	rotated := fmt.Sprintf("%s.%s", w.Path, day.UTC().Format("20060102"))
	if err := w.FS.Rename(w.Path, rotated); err != nil {
		return err
	}
	w.expireLocked(now)
	return nil
}

func (w *Writer) expireLocked(now time.Time) {
	// remove rotated files older than RetentionDays; the active file is kept
	prefix := w.Path + "."
	for _, p := range listDir(w.FS, w.Path[:strings.LastIndex(w.Path, "/")]) {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		suf := strings.TrimPrefix(p, prefix)
		day, err := time.Parse("20060102", suf)
		if err != nil {
			continue
		}
		if now.Sub(day) > time.Duration(w.RetentionDays)*24*time.Hour {
			w.FS.Remove(p)
		}
	}
}

// tailLocked returns (prevHash, lastSeq) — across rotation, from the newest
// rotated file when the active file is empty/missing.
func (w *Writer) tailLocked() (string, int64) {
	if e, ok := lastEntry(w.FS, w.Path); ok {
		return e.Hash, e.Seq
	}
	dir := w.Path[:strings.LastIndex(w.Path, "/")]
	var best string
	var bestDay time.Time
	for _, p := range listDir(w.FS, dir) {
		if !strings.HasPrefix(p, w.Path+".") {
			continue
		}
		suf := strings.TrimPrefix(p, w.Path+".")
		day, err := time.Parse("20060102", suf)
		if err != nil {
			continue
		}
		if day.After(bestDay) {
			bestDay, best = day, p
		}
	}
	if best == "" {
		return "", 0
	}
	if e, ok := lastEntry(w.FS, best); ok {
		return e.Hash, e.Seq
	}
	return "", 0
}

// listDir returns full paths of entries in dir ([]string{} on error).
func listDir(fsys pwfs.FS, dir string) []string {
	des, err := fsys.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range des {
		out = append(out, dir+"/"+de.Name())
	}
	return out
}

func lastEntry(fsys pwfs.FS, path string) (Entry, bool) {
	data, err := fsys.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}
	var last string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			last = l
		}
	}
	if last == "" {
		return Entry{}, false
	}
	var e Entry
	if json.Unmarshal([]byte(last), &e) != nil {
		return Entry{}, false
	}
	return e, true
}

// Problem is a verify finding.
type Problem struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Kind   string `json:"kind"` // hash_mismatch|chain_break|seq_gap|parse_error|reordered
	Detail string `json:"detail"`
}

// Report is the `audit verify` result.
type Report struct {
	Entries  int       `json:"entries"`
	Files    []string  `json:"files"`
	Problems []Problem `json:"problems"`
}

// Verify walks rotated files oldest→newest, then the active file.
func Verify(fsys pwfs.FS, path string, since time.Duration, now time.Time) Report {
	files := chainFiles(fsys, path)
	rep := Report{Files: files}
	prevHash := ""
	var prevSeq int64
	var prevTS time.Time
	cutoff := now.Add(-since)
	for _, f := range files {
		data, err := fsys.ReadFile(f)
		if err != nil {
			rep.Problems = append(rep.Problems, Problem{File: f, Kind: "parse_error", Detail: err.Error()})
			continue
		}
		lineNo := 0
		for _, l := range strings.Split(string(data), "\n") {
			if l == "" {
				continue
			}
			lineNo++
			var e Entry
			if json.Unmarshal([]byte(l), &e) != nil {
				rep.Problems = append(rep.Problems, Problem{File: f, Line: lineNo, Kind: "parse_error", Detail: "bad json"})
				continue
			}
			ts := entryTS(&e)
			if !ts.IsZero() && ts.Before(cutoff) {
				continue
			}
			rep.Entries++
			if e.Hash != hashOf(e.PrevHash, e.canonical()) {
				rep.Problems = append(rep.Problems, Problem{File: f, Line: lineNo, Kind: "hash_mismatch",
					Detail: "entry " + e.EventID})
			}
			if prevHash != "" && e.PrevHash != prevHash {
				rep.Problems = append(rep.Problems, Problem{File: f, Line: lineNo, Kind: "chain_break",
					Detail: "prev_hash mismatch at " + e.EventID})
			}
			if prevSeq != 0 && e.Seq != prevSeq+1 {
				rep.Problems = append(rep.Problems, Problem{File: f, Line: lineNo, Kind: "seq_gap",
					Detail: fmt.Sprintf("seq %d after %d", e.Seq, prevSeq)})
			}
			if !ts.IsZero() && !prevTS.IsZero() && ts.Before(prevTS) {
				rep.Problems = append(rep.Problems, Problem{File: f, Line: lineNo, Kind: "reordered",
					Detail: "timestamp went backwards at " + e.EventID})
			}
			prevHash, prevSeq = e.Hash, e.Seq
			if !ts.IsZero() {
				prevTS = ts
			}
		}
	}
	return rep
}

func entryTS(e *Entry) time.Time {
	for _, t := range []*model.JSONTime{e.FinishedAt, e.RequestedAt, e.StartedAt, e.ConfirmedAt} {
		if t != nil && !time.Time(*t).IsZero() {
			return time.Time(*t)
		}
	}
	return time.Time{}
}

// chainFiles returns rotated files oldest→newest + the active file.
func chainFiles(fsys pwfs.FS, path string) []string {
	dir := path[:strings.LastIndex(path, "/")]
	var rotated []string
	for _, p := range listDir(fsys, dir) {
		if strings.HasPrefix(p, path+".") {
			if _, err := time.Parse("20060102", strings.TrimPrefix(p, path+".")); err == nil {
				rotated = append(rotated, p)
			}
		}
	}
	sort.Strings(rotated)
	return append(rotated, path)
}

// Filter selects entries for List.
type Filter struct {
	Since                                   time.Duration
	Operator, Action, Node, Ticket, Outcome string
}

// List merges rotated + active files in range.
func List(fsys pwfs.FS, path string, f Filter, now time.Time) []Entry {
	var out []Entry
	cutoff := now.Add(-f.Since)
	for _, file := range chainFiles(fsys, path) {
		data, err := fsys.ReadFile(file)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(data), "\n") {
			if l == "" {
				continue
			}
			var e Entry
			if json.Unmarshal([]byte(l), &e) != nil {
				continue
			}
			ts := entryTS(&e)
			if !ts.IsZero() && ts.Before(cutoff) {
				continue
			}
			if f.Operator != "" && e.Operator != f.Operator {
				continue
			}
			if f.Action != "" && !strings.HasPrefix(e.Command, f.Action) {
				continue
			}
			if f.Ticket != "" && e.Ticket != f.Ticket {
				continue
			}
			if f.Outcome != "" && e.Outcome != f.Outcome {
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

// Get returns the requested+finished pair for an event id.
func Get(fsys pwfs.FS, path string, eventID string, since time.Duration, now time.Time) (*Entry, *Entry) {
	var req, fin *Entry
	for _, e := range List(fsys, path, Filter{Since: since}, now) {
		if e.EventID != eventID {
			continue
		}
		c := e
		switch e.Phase {
		case "requested":
			req = &c
		case "finished":
			fin = &c
		}
	}
	return req, fin
}

// Session groups entries by session id.
type Session struct {
	seen      map[string]bool
	SessionID string
	Start     time.Time
	End       time.Time
	Operator  string
	From      string
	KeyFP     string
	Commands  int
}

// Sessions derives sessions from shell start/end entries.
func Sessions(fsys pwfs.FS, path string, since time.Duration, now time.Time) []Session {
	m := map[string]*Session{}
	var order []string
	for _, e := range List(fsys, path, Filter{Since: since}, now) {
		s, ok := m[e.SessionID]
		if !ok {
			s = &Session{SessionID: e.SessionID, Operator: e.Operator,
				From: e.Identity.SSHClient, KeyFP: e.Identity.SSHKeyFingerprint}
			m[e.SessionID] = s
			order = append(order, e.SessionID)
		}
		ts := entryTS(&e)
		if s.Start.IsZero() || ts.Before(s.Start) {
			s.Start = ts
		}
		if ts.After(s.End) {
			s.End = ts
		}
		if e.Command != "shell start" && e.Command != "shell end" {
			if s.seen == nil {
				s.seen = map[string]bool{}
			}
			if !s.seen[e.EventID] {
				s.seen[e.EventID] = true
				s.Commands++
			}
		}
	}
	var out []Session
	for _, id := range order {
		out = append(out, *m[id])
	}
	return out
}

var (
	_ = context.Background
	_ = exitcode.Usage
	_ = sort.Strings
	_ = fmt.Sprint
	_ = remote.Step{}
)

// Allowed checks cfg.AllowedOperators.
func Allowed(cfg *config.Config, id Identity) error {
	op := id.Operator()
	for _, o := range cfg.AllowedOperators {
		if o == op {
			return nil
		}
	}
	if len(cfg.AllowedOperators) == 0 {
		return nil
	}
	return exitcode.New(exitcode.Usage, "operator "+op+" is not in allowed_operators").
		WithHint("ask an administrator to add your key fingerprint to operators:/allowed_operators in /etc/poolwarden.yaml")
}

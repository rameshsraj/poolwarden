// Package logsrc reads and parses pgpool/PostgreSQL log lines on hosts,
// driven by log_line_prefix (01 §5, 02 §3.4).
package logsrc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line is one parsed log line.
type Line struct {
	Host         string
	Component    string // pgpool | pg
	File         string
	LineNo       int64
	TS           time.Time
	Fields       map[string]string
	Severity     string
	Message      string
	Raw          string
	Continuation []string
}

// Prefix is a compiled log_line_prefix.
type Prefix struct {
	re       *regexp.Regexp
	groups   []string // capture group names in order
	tsField  string
	tsLayout string
	Unknown  []string
}

var knownFields = map[string]string{
	"a": "app", "u": "user", "d": "db", "r": "remote", "h": "host",
	"b": "backend_type", "p": "pid", "P": "pgpool_pid", "i": "command",
	"e": "sqlstate", "c": "session_id", "l": "line", "s": "session_start",
	"v": "vxid", "x": "txid",
}

var sevRe = regexp.MustCompile(`^\s*(?P<sev>DEBUG[1-5]?|LOG|INFO|NOTICE|WARNING|ERROR|FATAL|PANIC):\s*(?P<msg>.*)$`)

var contRe = regexp.MustCompile(`^\s*(DETAIL|HINT|CONTEXT|STATEMENT|QUERY|LOCATION|LINE|POSITION):`)

// pgpoolEscapes is pgpool-II's documented escape set (4.5 runtime-config-
// logging, Table 5-6). %P is the process NAME, not a pid.
var pgpoolEscapes = map[string]bool{
	"a": true, "p": true, "t": true, "m": true, "d": true, "u": true, "l": true, "%": true,
}

// ParsePrefix compiles log_line_prefix into a Prefix for the given component
// ("pg" or "pgpool"). Unknown escapes become non-greedy wildcards and are
// reported in the returned slice.
func ParsePrefix(component, prefix string) (*Prefix, []string) {
	var b strings.Builder
	var groups []string
	var tsField, tsLayout string
	var unknown []string
	i := 0
	for i < len(prefix) {
		c := prefix[i]
		if c != '%' || i+1 >= len(prefix) {
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
			continue
		}
		e := string(prefix[i+1])
		i += 2
		switch e {
		case "%":
			b.WriteString(regexp.QuoteMeta("%"))
		case "m":
			b.WriteString(`(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+(?: [A-Z+-][^ :]*)?)`)
			groups = append(groups, "ts")
			tsField, tsLayout = "ts", "%m"
		case "t":
			b.WriteString(`(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?: [A-Z+-][^ :]*)?)`)
			groups = append(groups, "ts")
			tsField, tsLayout = "ts", "%t"
		case "n":
			b.WriteString(`(?P<tsn>\d+\.\d+|\d+)`)
			groups = append(groups, "tsn")
			tsField, tsLayout = "tsn", "%n"
		case "q":
			b.WriteString(``) // stop marker — no output
		case "P":
			if component == "pgpool" {
				b.WriteString(`(?P<f_process>\S+)`)
				groups = append(groups, "process")
			} else {
				name := "f_" + knownFields["P"]
				b.WriteString(`(?P<` + name + `>.*?)`)
				groups = append(groups, knownFields["P"])
			}
		default:
			if component == "pgpool" && !pgpoolEscapes[e] {
				b.WriteString(`.*?`)
				unknown = append(unknown, "%"+e)
			} else if f, ok := knownFields[e]; ok && e != "P" {
				name := "f_" + f
				b.WriteString(`(?P<` + name + `>.*?)`)
				groups = append(groups, f)
			} else {
				b.WriteString(`.*?`)
				unknown = append(unknown, "%"+e)
			}
		}
	}
	re, err := regexp.Compile("^" + b.String())
	if err != nil {
		return &Prefix{}, append(unknown, "compile:"+err.Error())
	}
	return &Prefix{re: re, groups: groups, tsField: tsField, tsLayout: tsLayout, Unknown: unknown}, unknown
}

var tsLayouts = []string{
	"2006-01-02 15:04:05.000 MST", "2006-01-02 15:04:05.000 -07", "2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -07", "2006-01-02 15:04:05",
}

func parseTSVal(s string) time.Time {
	for _, l := range tsLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Parse parses one raw line. ok=false for non-matching lines (continuations
// are detected by the caller via IsContinuation).
func (p *Prefix) Parse(raw string) (Line, bool) {
	if p == nil || p.re == nil {
		return Line{}, false
	}
	m := p.re.FindStringSubmatch(raw)
	if m == nil {
		return Line{}, false
	}
	l := Line{Raw: raw, Fields: map[string]string{}}
	names := p.re.SubexpNames()
	prefixLen := 0
	for i, g := range m {
		if i == 0 || g == "" {
			continue
		}
		if names[i] == "ts" || names[i] == "tsn" {
			if names[i] == "tsn" {
				l.TS = parseEpoch(g)
			} else {
				l.TS = parseTSVal(strings.TrimSpace(g))
			}
		} else if strings.HasPrefix(names[i], "f_") {
			l.Fields[names[i][2:]] = g
		}
	}
	prefixLen = len(m[0])
	rest := raw[prefixLen:]
	if sm := sevRe.FindStringSubmatch(rest); sm != nil {
		l.Severity = sm[1]
		l.Message = sm[2]
	} else {
		l.Severity = "LOG"
		l.Message = strings.TrimSpace(rest)
	}
	return l, true
}

// IsContinuation reports whether raw continues the previous log line.
func IsContinuation(raw string) bool {
	if strings.HasPrefix(raw, "\t") {
		return true
	}
	return contRe.MatchString(raw)
}

func parseEpoch(s string) time.Time {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		sec, _ := strconv.ParseInt(s[:i], 10, 64)
		ms, _ := strconv.ParseInt(s[i+1:], 10, 64)
		return time.Unix(sec, ms*int64(time.Millisecond)/1000).Local()
	}
	sec, _ := strconv.ParseInt(s, 10, 64)
	return time.Unix(sec, 0).Local()
}

// Classify maps a message to a tag (02 §3.5 classifier).
var classRules = []struct {
	tag string
	re  *regexp.Regexp
}{
	{"FAILOVER", regexp.MustCompile(`(?i)failover|failover_command|follow_primary|promot|degenerat`)},
	{"HEALTHCHECK", regexp.MustCompile(`(?i)health check|health_check`)},
	{"CHILD_EXIT", regexp.MustCompile(`(?i)child process .* exit|child .* was terminated|reaper`)},
	{"AUTH_FAIL", regexp.MustCompile(`(?i)authentication failed|password authentication|no pg_hba|pool_hba|md5 authentication|scram`)},
	{"WD_ELECTION", regexp.MustCompile(`(?i)watchdog.*(election|leader|master|quorum|escalat|standby)|wd_`)},
	{"CONN_FAIL", regexp.MustCompile(`(?i)connection to .* failed|could not connect|too many clients|remaining connection slots|connection refused|failed to connect`)},
}

// Classify returns the error tag for a parsed line, or OTHER.
func Classify(l Line) string {
	for _, r := range classRules {
		if r.re.MatchString(l.Message) {
			return r.tag
		}
	}
	return "OTHER"
}

var _ = fmt.Sprint

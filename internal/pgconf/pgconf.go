// Package pgconf is a round-trip parser for pgpool.conf / pool_hba.conf /
// pcp.conf plus the parameter catalogue (design 01 §9, 02 §5).
package pgconf

import (
	"fmt"
	"strconv"
	"strings"
)

// LineKind classifies a config line.
type LineKind int

const (
	KindBlank LineKind = iota
	KindComment
	KindParam
	KindManagedMarker
	KindOther
)

// Line is one physical line, round-trippable via Raw.
type Line struct {
	Raw       string
	Kind      LineKind
	Name      string
	Value     string // unquoted
	Quote     byte   // 0 or '\''
	Commented bool   // "#name = value" default line
	Inline    string // trailing comment incl. whitespace+'#'
}

// File is a parsed config file.
type File struct {
	Lines []Line
	Path  string
}

const (
	markerBegin = "# --- managed by poolwarden ---"
	markerEnd   = "# --- end managed by poolwarden ---"
)

// Parse parses config text tolerating all pgpool syntax variants.
func Parse(data []byte) (*File, error) {
	f := &File{}
	for _, raw := range strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\n")
		f.Lines = append(f.Lines, parseLine(raw))
	}
	return f, nil
}

func parseLine(raw string) Line {
	l := Line{Raw: raw}
	trim := strings.TrimSpace(raw)
	switch {
	case trim == "":
		l.Kind = KindBlank
		return l
	case trim == markerBegin || trim == markerEnd:
		l.Kind = KindManagedMarker
		return l
	case strings.HasPrefix(trim, "#"):
		body := strings.TrimSpace(trim[1:])
		if name, val, _, ok := parseParam(body); ok && isParamName(name) {
			l.Kind = KindParam
			l.Commented = true
			l.Name = name
			l.Value = val
			// trailing comment after value
			l.Inline = findInline(body)
			return l
		}
		l.Kind = KindComment
		return l
	}
	name, val, quote, ok := parseParam(trim)
	if !ok || !isParamName(name) {
		l.Kind = KindOther
		return l
	}
	l.Kind = KindParam
	l.Name = name
	l.Value = val
	l.Quote = quote
	l.Inline = findInline(trim)
	return l
}

func isParamName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// parseParam returns name, unquoted value, quote char, ok.
func parseParam(s string) (string, string, byte, bool) {
	// find '=' outside quotes
	eq := -1
	inQ := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			inQ = !inQ
		}
		if c == '#' && !inQ {
			s = s[:i]
			break
		}
		if c == '=' && !inQ && eq < 0 {
			eq = i
		}
	}
	if eq < 0 {
		return "", "", 0, false
	}
	name := strings.TrimSpace(s[:eq])
	rest := strings.TrimSpace(s[eq+1:])
	// strip inline comment
	val := rest
	if i := indexComment(rest); i >= 0 {
		val = strings.TrimSpace(rest[:i])
	}
	var q byte
	if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
		q = '\''
		val = unquote(val)
	}
	return name, val, q, true
}

func indexComment(s string) int {
	inQ := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			inQ = !inQ
		}
		if s[i] == '#' && !inQ {
			return i
		}
	}
	return -1
}

func findInline(s string) string {
	// returns from the '#' that isn't inside quotes or part of name/value
	eq := strings.Index(s, "=")
	if eq < 0 {
		return ""
	}
	rest := s[eq+1:]
	inQ := false
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\'' {
			inQ = !inQ
		}
		if rest[i] == '#' && !inQ {
			// keep preceding whitespace
			j := i
			for j > 0 && (rest[j-1] == ' ' || rest[j-1] == '\t') {
				j--
			}
			return rest[j:]
		}
	}
	return ""
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Bytes reproduces the file byte-identically when unmodified.
func (f *File) Bytes() []byte {
	var b strings.Builder
	for _, l := range f.Lines {
		b.WriteString(l.Raw)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Get returns the last active value for a param.
func (f *File) Get(name string) (value string, found bool, commented bool) {
	for i := len(f.Lines) - 1; i >= 0; i-- {
		l := &f.Lines[i]
		if l.Kind == KindParam && l.Name == name {
			if !l.Commented {
				return l.Value, true, false
			}
			value, found, commented = l.Value, true, true
		}
	}
	return value, found, commented
}

// Set writes a value: last active line → in place; commented default →
// uncomment; else append inside the managed block.
func (f *File) Set(name, value string) {
	// comment out any earlier active duplicates, edit the last one
	last := -1
	for i, l := range f.Lines {
		if l.Kind == KindParam && l.Name == name && !l.Commented {
			last = i
		}
	}
	if last >= 0 {
		for i, l := range f.Lines {
			if l.Kind == KindParam && l.Name == name && !l.Commented && i != last {
				f.Lines[i].Raw = "#" + l.Raw
				f.Lines[i].Commented = true
			}
		}
		f.Lines[last] = rebuild(f.Lines[last], value)
		return
	}
	for i, l := range f.Lines {
		if l.Kind == KindParam && l.Name == name && l.Commented {
			nl := rebuild(l, value)
			nl.Commented = false
			f.Lines[i] = nl
			return
		}
	}
	f.appendManaged(name, value)
}

func rebuild(l Line, value string) Line {
	indent := l.Raw[:len(l.Raw)-len(strings.TrimLeft(l.Raw, " \t"))]
	v := value
	q := byte(0)
	if needsQuote(v) || l.Quote != 0 {
		v = quote(v)
		q = '\''
	}
	raw := fmt.Sprintf("%s%s = %s%s", indent, l.Name, v, l.Inline)
	return Line{Raw: raw, Kind: KindParam, Name: l.Name, Value: value, Quote: q}
}

func needsQuote(v string) bool {
	if v == "" {
		return true
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.' || c == '-' || c == '/' || c == ':' || c == '*') {
			return true
		}
	}
	return false
}

func (f *File) appendManaged(name, value string) {
	bi, ei := -1, -1
	for i, l := range f.Lines {
		if l.Kind == KindManagedMarker {
			if l.Raw == markerBegin || strings.TrimSpace(l.Raw) == markerBegin {
				bi = i
			} else {
				ei = i
			}
		}
	}
	nl := paramLine(name, value)
	if bi < 0 {
		f.Lines = append(f.Lines,
			Line{Raw: "", Kind: KindBlank},
			Line{Raw: markerBegin, Kind: KindManagedMarker},
			nl,
			Line{Raw: markerEnd, Kind: KindManagedMarker})
		return
	}
	// insert before end marker
	out := append([]Line{}, f.Lines[:ei]...)
	out = append(out, nl)
	f.Lines = append(out, f.Lines[ei:]...)
}

func paramLine(name, value string) Line {
	v := value
	var q byte
	if needsQuote(v) {
		v = quote(v)
		q = '\''
	}
	return Line{Raw: fmt.Sprintf("%s = %s", name, v), Kind: KindParam,
		Name: name, Value: value, Quote: q}
}

// Unset comments the active line(s) out; managed-block lines are removed.
func (f *File) Unset(name string) {
	var out []Line
	managed := false
	for _, l := range f.Lines {
		if l.Kind == KindManagedMarker {
			managed = !strings.Contains(l.Raw, "end managed")
		}
		if l.Kind == KindParam && l.Name == name && !l.Commented {
			if managed {
				continue // remove managed lines
			}
			l.Raw = "#" + l.Raw
			l.Commented = true
		}
		out = append(out, l)
	}
	f.Lines = out
}

// Backend is one backend_*N block.
type Backend struct {
	ID                  int
	Hostname            string
	Port                int
	Weight              float64
	DataDirectory, Flag string
	ApplicationName     string
}

// Backends collects all backend_*N groups.
func (f *File) Backends() []Backend {
	m := map[int]*Backend{}
	var order []int
	get := func(id int) *Backend {
		if b, ok := m[id]; ok {
			return b
		}
		b := &Backend{ID: id}
		m[id] = b
		order = append(order, id)
		return b
	}
	for _, l := range f.Lines {
		if l.Kind != KindParam || l.Commented {
			continue
		}
		var id int
		var suffix string
		for _, p := range []string{"backend_hostname", "backend_port", "backend_weight",
			"backend_data_directory", "backend_flag", "backend_application_name"} {
			if strings.HasPrefix(l.Name, p) {
				rest := l.Name[len(p):]
				if n, err := strconv.Atoi(rest); err == nil {
					id, suffix = n, p
				}
			}
		}
		if suffix == "" {
			continue
		}
		b := get(id)
		switch suffix {
		case "backend_hostname":
			b.Hostname = l.Value
		case "backend_port":
			b.Port, _ = strconv.Atoi(l.Value)
		case "backend_weight":
			b.Weight, _ = strconv.ParseFloat(l.Value, 64)
		case "backend_data_directory":
			b.DataDirectory = l.Value
		case "backend_flag":
			b.Flag = l.Value
		case "backend_application_name":
			b.ApplicationName = l.Value
		}
	}
	var out []Backend
	for _, i := range order {
		out = append(out, *m[i])
	}
	return out
}

// SetBackend writes all keys for one backend as a unit (managed block or in
// place when an active group exists).
func (f *File) SetBackend(b Backend) {
	// remove existing group then append fresh in the managed block
	f.RemoveBackend(b.ID)
	f.appendManaged("backend_hostname"+itoa(b.ID), b.Hostname)
	f.appendManaged("backend_port"+itoa(b.ID), itoa(b.Port))
	f.appendManaged("backend_weight"+itoa(b.ID), fmt.Sprintf("%g", b.Weight))
	if b.DataDirectory != "" {
		f.appendManaged("backend_data_directory"+itoa(b.ID), b.DataDirectory)
	}
	if b.Flag != "" {
		f.appendManaged("backend_flag"+itoa(b.ID), b.Flag)
	}
	if b.ApplicationName != "" {
		f.appendManaged("backend_application_name"+itoa(b.ID), b.ApplicationName)
	}
}

// RemoveBackend deletes all keys of one backend id.
func (f *File) RemoveBackend(id int) {
	suffix := itoa(id)
	var out []Line
	for _, l := range f.Lines {
		if l.Kind == KindParam && strings.HasSuffix(l.Name, suffix) &&
			strings.HasPrefix(l.Name, "backend_") {
			continue
		}
		out = append(out, l)
	}
	f.Lines = out
}

func itoa(i int) string { return strconv.Itoa(i) }

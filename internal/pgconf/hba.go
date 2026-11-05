package pgconf

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// HBARule is one pool_hba.conf row.
type HBARule struct {
	Type, Database, User, Address, Method, Options string
	LineNo                                         int
}

// HBAFile is a parsed pool_hba.conf.
type HBAFile struct {
	Lines []Line
}

// ParseHBA parses pool_hba.conf.
func ParseHBA(data []byte) (*HBAFile, error) {
	f := &HBAFile{}
	for _, raw := range strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\n")
		l := parseLine(raw)
		// hba lines aren't name=value; classify active non-comment as Other
		f.Lines = append(f.Lines, l)
	}
	return f, nil
}

// Bytes round-trips.
func (f *HBAFile) Bytes() []byte {
	var b strings.Builder
	for _, l := range f.Lines {
		b.WriteString(l.Raw)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Rules returns the active (non-commented) rules in order.
func (f *HBAFile) Rules() []HBARule {
	var out []HBARule
	for i, l := range f.Lines {
		trim := strings.TrimSpace(l.Raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		parts := strings.Fields(trim)
		if len(parts) < 4 {
			continue
		}
		r := HBARule{LineNo: i}
		r.Type = parts[0]
		r.Database = parts[1]
		r.User = parts[2]
		// local rows have no address: type local, db, user, method
		if r.Type == "local" {
			r.Method = parts[3]
			if len(parts) > 4 {
				r.Options = strings.Join(parts[4:], " ")
			}
		} else {
			r.Address = parts[3]
			if len(parts) > 4 {
				r.Method = parts[4]
			}
			if len(parts) > 5 {
				r.Options = strings.Join(parts[5:], " ")
			}
		}
		out = append(out, r)
	}
	return out
}

func (r HBARule) line() string {
	if r.Type == "local" {
		s := fmt.Sprintf("%-8s%-10s%-10s%-20s", r.Type, r.Database, r.User, r.Method)
		if r.Options != "" {
			s += "  " + r.Options
		}
		return strings.TrimRight(s, " ")
	}
	s := fmt.Sprintf("%-8s%-10s%-10s%-14s%-18s", r.Type, r.Database, r.User, r.Address, r.Method)
	if r.Options != "" {
		s += "  " + r.Options
	}
	return strings.TrimRight(s, " ")
}

// AddRule inserts a rule after the last active rule.
func (f *HBAFile) AddRule(r HBARule) {
	last := -1
	for i, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if t != "" && !strings.HasPrefix(t, "#") {
			last = i
		}
	}
	nl := Line{Raw: r.line(), Kind: KindOther}
	f.Lines = append(append(append([]Line{}, f.Lines[:last+1]...), nl), f.Lines[last+1:]...)
}

// RemoveRule removes the rule at index i (into Rules()).
func (f *HBAFile) RemoveRule(idx int) {
	seen := -1
	var out []Line
	for _, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if t != "" && !strings.HasPrefix(t, "#") && len(strings.Fields(t)) >= 4 {
			seen++
			if seen == idx {
				continue
			}
		}
		out = append(out, l)
	}
	f.Lines = out
}

// PCPFile is a parsed pcp.conf (user:md5hex lines).
type PCPFile struct {
	Lines []Line
}

// ParsePCP parses pcp.conf.
func ParsePCP(data []byte) (*PCPFile, error) {
	f := &PCPFile{}
	for _, raw := range strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\n")
		f.Lines = append(f.Lines, parseLine(raw))
	}
	return f, nil
}

// Bytes round-trips.
func (f *PCPFile) Bytes() []byte {
	var b strings.Builder
	for _, l := range f.Lines {
		b.WriteString(l.Raw)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// MD5Hex returns hex(md5(password)) — the pcp.conf password format.
func MD5Hex(password string) string {
	sum := md5.Sum([]byte(password))
	return hex.EncodeToString(sum[:])
}

// Users returns active user names.
func (f *PCPFile) Users() []string {
	var out []string
	for _, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if i := strings.Index(t, ":"); i > 0 {
			out = append(out, t[:i])
		}
	}
	return out
}

// SetUser adds or replaces a user line.
func (f *PCPFile) SetUser(user, md5hex string) {
	for i, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if t != "" && !strings.HasPrefix(t, "#") && strings.HasPrefix(t, user+":") {
			f.Lines[i].Raw = user + ":" + md5hex
			return
		}
	}
	f.Lines = append(f.Lines, Line{Raw: user + ":" + md5hex, Kind: KindOther})
}

// RemoveUser drops a user.
func (f *PCPFile) RemoveUser(user string) {
	var out []Line
	for _, l := range f.Lines {
		t := strings.TrimSpace(l.Raw)
		if t != "" && !strings.HasPrefix(t, "#") && strings.HasPrefix(t, user+":") {
			continue
		}
		out = append(out, l)
	}
	f.Lines = out
}

// PoolPasswdUser is a pool_passwd entry (read-only).
type PoolPasswdUser struct {
	User, Hash string
}

// ParsePoolPasswd reads user:hash lines.
func ParsePoolPasswd(data []byte) []PoolPasswdUser {
	var out []PoolPasswdUser
	for _, raw := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if i := strings.Index(t, ":"); i > 0 {
			out = append(out, PoolPasswdUser{User: t[:i], Hash: t[i+1:]})
		}
	}
	return out
}

// Package config loads, validates and marshals poolwarden's own YAML config.
// Passwords are never stored here: they come from PGPASSFILE/PCPPASSFILE or
// no-echo prompts only.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/smarsh/poolwarden/internal/exitcode"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// DefaultPath is the only config location; -c overrides it.
const DefaultPath = "/etc/poolwarden.yaml"

type Role string

const (
	RolePgpool Role = "pgpool"
	RolePG     Role = "pg"
)

// ByteSize is a byte count that marshals as "100GB"-style values.
type ByteSize int64

type Endpoint struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	DBName   string `yaml:"dbname,omitempty"`
	Passfile string `yaml:"passfile,omitempty"` // I28
}

type PCP struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	BinDir   string `yaml:"bin_dir"`
	Passfile string `yaml:"passfile,omitempty"` // I28
}

type Bins struct {
	Systemctl string `yaml:"systemctl"`
	IP        string `yaml:"ip"`
}

type SSH struct {
	Key        string        `yaml:"key"`
	KnownHosts string        `yaml:"known_hosts"`
	Timeout    time.Duration `yaml:"timeout"`
}

type Lag struct {
	WarnSeconds int `yaml:"warn_seconds"`
	CritSeconds int `yaml:"crit_seconds"`
}

type Audit struct {
	File          string `yaml:"file"`
	Syslog        bool   `yaml:"syslog"`
	ReadCommands  bool   `yaml:"read_commands"`
	RetentionDays int    `yaml:"retention_days"`
}

// PgpoolPaths are the on-host paths for a host carrying the pgpool role.
type PgpoolPaths struct {
	Conf          string `yaml:"conf,omitempty"`
	HBA           string `yaml:"hba,omitempty"`
	PCPConf       string `yaml:"pcp_conf,omitempty"`
	PoolPasswd    string `yaml:"pool_passwd,omitempty"`
	LogDir        string `yaml:"log_dir,omitempty"`
	LogLinePrefix string `yaml:"log_line_prefix,omitempty"`
	Service       string `yaml:"service,omitempty"`
}

// PGPaths are the on-host PostgreSQL settings for a host carrying the pg role.
type PGPaths struct {
	Port           int    `yaml:"port,omitempty"`
	DataDir        string `yaml:"data_dir,omitempty"`
	LogDir         string `yaml:"log_dir,omitempty"`
	LogFilename    string `yaml:"log_filename,omitempty"`
	LogDestination string `yaml:"log_destination,omitempty"`
	LogLinePrefix  string `yaml:"log_line_prefix,omitempty"`
}

type Host struct {
	Name    string       `yaml:"name"`
	Address string       `yaml:"address"`
	Roles   []Role       `yaml:"roles"`
	SSHUser string       `yaml:"ssh_user"`
	Pgpool  *PgpoolPaths `yaml:"pgpool,omitempty"`
	PG      *PGPaths     `yaml:"pg,omitempty"`
}

// OperatorFor resolves an SSH key fingerprint to an operator name
// (operators: name -> SHA256 fingerprint, or fingerprint -> name).
func (c *Config) OperatorFor(fp string) string {
	for name, fpr := range c.Operators {
		if fpr == fp {
			return name
		}
		if name == fp {
			return fpr
		}
	}
	return ""
}

func (h Host) Has(r Role) bool {
	for _, x := range h.Roles {
		if x == r {
			return true
		}
	}
	return false
}

type Config struct {
	Cluster          string            `yaml:"cluster"`
	StateDir         string            `yaml:"state_dir"`
	Operators        map[string]string `yaml:"operators,omitempty"`
	AllowedOperators []string          `yaml:"allowed_operators"`
	Pgpool           Endpoint          `yaml:"pgpool"`
	PCP              PCP               `yaml:"pcp"`
	Bins             Bins              `yaml:"bins"`
	SSH              SSH               `yaml:"ssh"`
	Interval         time.Duration     `yaml:"interval"`
	Timeout          time.Duration     `yaml:"timeout"`
	Lag              Lag               `yaml:"lag"`
	WALVolumeBytes   ByteSize          `yaml:"wal_volume_bytes"`
	Audit            Audit             `yaml:"audit"`
	Hosts            []Host            `yaml:"hosts"`
}

// Defaults is the full default configuration; user YAML decodes over it.
func Defaults() Config {
	return Config{
		StateDir: "/var/lib/poolwarden",
		Pgpool:   Endpoint{Host: "127.0.0.1", Port: 9999, User: "postgres", DBName: "postgres"},
		PCP:      PCP{Host: "127.0.0.1", Port: 9898, User: "postgres", BinDir: "/usr/bin"},
		Bins:     Bins{Systemctl: "/usr/bin/systemctl", IP: "/usr/sbin/ip"},
		SSH: SSH{
			Key:        "/root/.ssh/id_ed25519",
			KnownHosts: "/root/.ssh/known_hosts",
			Timeout:    5 * time.Second,
		},
		Interval: 2 * time.Second,
		Timeout:  3 * time.Second,
		Lag:      Lag{WarnSeconds: 1800, CritSeconds: 3600},
		Audit: Audit{
			File:          "/var/lib/poolwarden/audit.jsonl",
			Syslog:        true,
			ReadCommands:  false,
			RetentionDays: 31,
		},
		AllowedOperators: []string{},
	}
}

// ApplyDefaults fills per-host fields that depend on the host's roles.
func (c *Config) ApplyDefaults() {
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.SSHUser == "" {
			h.SSHUser = "root"
		}
		if h.Has(RolePgpool) {
			if h.Pgpool == nil {
				h.Pgpool = &PgpoolPaths{}
			}
			if h.Pgpool.Service == "" {
				h.Pgpool.Service = "pgpool-II"
			}
		}
		if h.Has(RolePG) {
			if h.PG == nil {
				h.PG = &PGPaths{}
			}
			if h.PG.Port == 0 {
				h.PG.Port = 5432
			}
		}
	}
}

// CheckPermissions enforces the config-file ownership/mode rule (docs/04 I19):
// running as root the file must be owned by root; otherwise by the caller's
// uid. Group/other write bits are always refused.
func CheckPermissions(fsys pwfs.FS, path string, uid int) error {
	fi, err := fsys.Stat(path)
	if err != nil {
		return err
	}
	owner, ok := pwfs.OwnerOf(fi)
	if !ok {
		return exitcode.New(exitcode.Usage, "config file ownership cannot be determined").
			WithCause(path).
			WithHint("chown root:root " + path + " && chmod 0600 " + path)
	}
	wantUID := 0
	if uid != 0 {
		wantUID = uid
	}
	var problems []string
	if owner.UID != wantUID {
		problems = append(problems, fmt.Sprintf("%s: owned by uid %d, must be uid %d", path, owner.UID, wantUID))
	}
	if m := fi.Mode().Perm(); m&0o022 != 0 {
		problems = append(problems, fmt.Sprintf("%s: mode %04o is group/other-writable", path, m))
	}
	if len(problems) > 0 {
		return exitcode.New(exitcode.Usage, "config file has unsafe permissions").
			WithCause(strings.Join(problems, "\n")).
			WithHint("chown root:root " + path + " && chmod 0600 " + path)
	}
	return nil
}

// Load reads path through fsys for the real process uid.
func Load(fsys pwfs.FS, path string) (*Config, error) {
	return LoadFor(fsys, path, os.Getuid())
}

// LoadFor is Load with an explicit uid for the ownership check.
func LoadFor(fsys pwfs.FS, path string, uid int) (*Config, error) {
	if err := CheckPermissions(fsys, path, uid); err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return nil, exitcode.New(exitcode.Usage, "config file not found").
				WithCause(path).
				WithHint("poolwarden init")
		}
		return nil, err
	}
	data, err := fsys.ReadFile(path)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, "cannot read config file").
			WithCause(err.Error()).
			WithHint("poolwarden init")
	}
	cfg := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err != io.EOF {
		return nil, exitcode.New(exitcode.Usage, "invalid config file").
			WithCause(err.Error()).
			WithHint(path)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks every rule; all problems are returned in one error, one per
// line, each prefixed with its field path.
func (c *Config) Validate() error {
	var p []string
	bad := func(s string) { p = append(p, s) }
	badf := func(format string, args ...any) { bad(fmt.Sprintf(format, args...)) }

	if c.Cluster == "" {
		bad("cluster: must not be empty")
	}
	if len(c.Hosts) == 0 {
		bad("hosts: must define at least one host")
	}
	seen := map[string]bool{}
	for i, h := range c.Hosts {
		base := fmt.Sprintf("hosts[%d]", i)
		if h.Name == "" {
			bad(base + ".name: must not be empty")
		} else if seen[h.Name] {
			badf(base+".name: %q is duplicated", h.Name)
		}
		seen[h.Name] = true
		if len(h.Roles) == 0 {
			bad(base + ".roles: must have at least one role")
		}
		for j, r := range h.Roles {
			if r != RolePgpool && r != RolePG {
				badf("%s.roles[%d]: %q must be pgpool or pg", base, j, string(r))
			}
		}
		if h.SSHUser != "root" && h.SSHUser != "postgres" {
			badf(base+".ssh_user: %q must be root or postgres", h.SSHUser)
		}
		if h.Has(RolePgpool) {
			if h.Pgpool == nil || h.Pgpool.Conf == "" {
				bad(base + ".pgpool.conf: must not be empty on a pgpool host")
			}
			if h.Pgpool != nil {
				for _, f := range [][2]string{
					{".pgpool.conf", h.Pgpool.Conf},
					{".pgpool.hba", h.Pgpool.HBA},
					{".pgpool.pcp_conf", h.Pgpool.PCPConf},
					{".pgpool.pool_passwd", h.Pgpool.PoolPasswd},
					{".pgpool.log_dir", h.Pgpool.LogDir},
				} {
					if f[1] != "" && !filepath.IsAbs(f[1]) {
						bad(base + f[0] + ": must be absolute")
					}
				}
			}
		}
		if h.Has(RolePG) {
			if h.PG == nil || h.PG.LogDir == "" {
				bad(base + ".pg.log_dir: must not be empty on a pg host")
			}
			if h.PG != nil {
				for _, f := range [][2]string{
					{".pg.data_dir", h.PG.DataDir},
					{".pg.log_dir", h.PG.LogDir},
				} {
					if f[1] != "" && !filepath.IsAbs(f[1]) {
						bad(base + f[0] + ": must be absolute")
					}
				}
			}
		}
	}
	if c.Lag.WarnSeconds <= 0 || c.Lag.CritSeconds <= 0 {
		bad("lag: warn_seconds and crit_seconds must be > 0")
	} else if c.Lag.WarnSeconds >= c.Lag.CritSeconds {
		bad("lag: warn_seconds must be < crit_seconds")
	}
	if c.Interval <= 0 {
		bad("interval: must be > 0")
	}
	if c.Timeout <= 0 {
		bad("timeout: must be > 0")
	}
	if c.Audit.RetentionDays < 1 {
		bad("audit.retention_days: must be >= 1")
	}
	for _, f := range [][2]string{
		{"state_dir", c.StateDir},
		{"pcp.bin_dir", c.PCP.BinDir},
		{"bins.systemctl", c.Bins.Systemctl},
		{"bins.ip", c.Bins.IP},
		{"ssh.key", c.SSH.Key},
		{"ssh.known_hosts", c.SSH.KnownHosts},
		{"audit.file", c.Audit.File},
	} {
		if f[1] != "" && !filepath.IsAbs(f[1]) {
			bad(f[0] + ": must be absolute")
		}
	}
	if len(p) > 0 {
		return exitcode.New(exitcode.Usage, "invalid config").
			WithCause(strings.Join(p, "\n")).
			WithHint(firstField(p[0]))
	}
	return nil
}

// firstField extracts the "path:" prefix of a validation problem line.
func firstField(problem string) string {
	if i := strings.Index(problem, ":"); i > 0 {
		return problem[:i]
	}
	return problem
}

func (c *Config) HostsWith(r Role) []Host {
	var out []Host
	for _, h := range c.Hosts {
		if h.Has(r) {
			out = append(out, h)
		}
	}
	return out
}

func (c *Config) PgpoolHosts() []Host { return c.HostsWith(RolePgpool) }
func (c *Config) PGHosts() []Host     { return c.HostsWith(RolePG) }

func (c *Config) HostByName(name string) (Host, bool) {
	for _, h := range c.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}

// Marshal renders the config back to YAML.
func (c *Config) Marshal() ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}

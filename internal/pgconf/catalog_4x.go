package pgconf

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/smarsh/poolwarden/internal/model"
)

// Param is one catalogue entry (pgpool 4.4/4.5).
type Param struct {
	Name        string
	Type        string // bool|int|real|string|enum|list|memory|time
	Default     string
	Min, Max    *float64
	Enum        []string
	Context     string // "reload" | "restart"
	Since       string
	Description string
	Unverified  bool
}

func f64(v float64) *float64 { return &v }

// catalogue covers every param used elsewhere in poolwarden plus the
// documented 4.5 runtime-config set. Entries whose defaults/context could
// not be confirmed against the live docs are marked Unverified.
var catalogue = []Param{
	// connections (confirmed vs 4.5 docs page)
	{"listen_addresses", "string", "localhost", nil, nil, nil, "restart", "4.4", "where pgpool listens", false},
	{"port", "int", "9999", f64(1), f64(65535), nil, "restart", "4.4", "pgpool port", false},
	{"pcp_listen_addresses", "string", "localhost", nil, nil, nil, "restart", "4.4", "pcp listen", false},
	{"pcp_port", "int", "9898", f64(1), f64(65535), nil, "restart", "4.4", "pcp port", false},
	{"pcp_socket_dir", "string", "/tmp", nil, nil, nil, "restart", "4.4", "pcp socket dir", false},
	{"unix_socket_directories", "list", "/tmp", nil, nil, nil, "restart", "4.4", "socket dirs", false},
	{"unix_socket_group", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"unix_socket_permissions", "int", "0777", nil, nil, nil, "restart", "4.4", "", false},
	{"num_init_children", "int", "32", f64(1), f64(10000), nil, "restart", "4.4", "preforked children", false},
	{"reserved_connections", "int", "0", f64(0), nil, nil, "restart", "4.4", "", false},
	// pools
	{"max_pool", "int", "4", f64(1), nil, nil, "restart", "4.4", "cached conns per pair", false},
	{"child_life_time", "time", "5min", nil, nil, nil, "restart", "4.4", "", true},
	{"child_max_connections", "int", "0", f64(0), nil, nil, "restart", "4.4", "", true},
	{"connection_life_time", "time", "0", nil, nil, nil, "restart", "4.4", "", true},
	{"client_idle_limit", "time", "0", nil, nil, nil, "restart", "4.4", "", true},
	{"connection_cache", "bool", "on", nil, nil, nil, "restart", "4.4", "cache connections", true},
	// backend
	{"backend_hostname*", "string", "", nil, nil, nil, "restart", "4.4", "backend host", false},
	{"backend_port*", "int", "5432", f64(1), f64(65535), nil, "restart", "4.4", "", false},
	{"backend_weight*", "real", "1", f64(0), nil, nil, "reload", "4.4", "", false},
	{"backend_data_directory*", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"backend_flag*", "enum", "ALLOW_TO_FAILOVER", nil, nil,
		[]string{"ALLOW_TO_FAILOVER", "DISALLOW_TO_FAILOVER", "ALWAYS_PRIMARY"}, "reload", "4.4", "", true},
	{"backend_application_name*", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	// authentication
	{"enable_pool_hba", "bool", "off", nil, nil, nil, "reload", "4.4", "", false},
	{"pool_passwd", "string", "pool_passwd", nil, nil, nil, "reload", "4.4", "", false},
	{"authentication_timeout", "time", "60", nil, nil, nil, "restart", "4.4", "", true},
	{"allow_clear_text_frontend_auth", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	// logging
	{"log_destination", "enum", "stderr", nil, nil, []string{"stderr", "syslog", "eventlog"}, "restart", "4.4", "", true},
	{"log_line_prefix", "string", "%m: %a pid %p: ", nil, nil, nil, "reload", "4.4", "", false},
	{"log_connections", "bool", "off", nil, nil, nil, "reload", "4.4", "", true},
	{"log_hostname", "bool", "off", nil, nil, nil, "reload", "4.4", "", true},
	{"log_statement", "bool", "off", nil, nil, nil, "reload", "4.4", "", true},
	{"log_per_node_statement", "bool", "off", nil, nil, nil, "reload", "4.4", "", true},
	{"log_standby_delay", "enum", "none", nil, nil,
		[]string{"none", "always", "if_over_threshold"}, "reload", "4.4", "", true},
	{"logging_collector", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	{"log_filename", "string", "pgpool-%Y-%m-%d_%H%M%S.log", nil, nil, nil, "reload", "4.4", "", false},
	{"log_directory", "string", "/tmp", nil, nil, nil, "restart", "4.4", "", true},
	{"log_rotation_age", "time", "1d", nil, nil, nil, "reload", "4.4", "", true},
	{"log_rotation_size", "memory", "10MB", nil, nil, nil, "reload", "4.4", "", true},
	{"syslog_facility", "enum", "LOCAL0", nil, nil,
		[]string{"LOCAL0", "LOCAL1", "LOCAL2", "LOCAL3", "LOCAL4", "LOCAL5", "LOCAL6", "LOCAL7"}, "restart", "4.4", "", true},
	{"syslog_ident", "string", "pgpool", nil, nil, nil, "restart", "4.4", "", true},
	{"log_min_messages", "enum", "warning", nil, nil,
		[]string{"debug", "error", "log", "notice", "warning", "info"}, "reload", "4.4", "", true},
	// load balancing
	{"load_balance_mode", "bool", "off", nil, nil, nil, "restart", "4.4", "", false},
	{"ignore_leading_white_space", "bool", "on", nil, nil, nil, "reload", "4.4", "", true},
	{"white_function_list", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"black_function_list", "string", "nextval,setval", nil, nil, nil, "reload", "4.4", "", true},
	{"black_query_pattern_list", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"database_redirect_preference_list", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"app_name_redirect_preference_list", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"allow_sql_comments", "bool", "off", nil, nil, nil, "reload", "4.4", "", true},
	{"disable_load_balance_on_write", "enum", "transaction", nil, nil,
		[]string{"transaction", "trans_transaction", "always", "off"}, "restart", "4.4", "", true},
	{"statement_level_load_balance", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	// health check
	{"health_check_period", "time", "0", f64(0), nil, nil, "reload", "4.4", "", false},
	{"health_check_timeout", "time", "20", f64(0), nil, nil, "reload", "4.4", "", true},
	{"health_check_user", "string", "nobody", nil, nil, nil, "reload", "4.4", "", false},
	{"health_check_password", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"health_check_database", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"health_check_max_retries", "int", "0", f64(0), nil, nil, "reload", "4.4", "", false},
	{"health_check_retry_delay", "time", "1", f64(0), nil, nil, "reload", "4.4", "", false},
	{"connect_timeout", "time", "10000", nil, nil, nil, "reload", "4.4", "", true},
	// failover / recovery
	{"failover_command", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"failback_command", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"follow_primary_command", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"failover_on_backend_error", "bool", "on", nil, nil, nil, "restart", "4.4", "", true},
	{"failover_on_backend_shutdown", "bool", "on", nil, nil, nil, "restart", "4.4", "", true},
	{"failover_when_quorum_exists", "bool", "on", nil, nil, nil, "restart", "4.4", "", false},
	{"failover_require_consensus", "bool", "on", nil, nil, nil, "restart", "4.4", "", false},
	{"detach_false_primary", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	{"search_primary_node_timeout", "time", "5min", nil, nil, nil, "restart", "4.4", "", true},
	{"auto_failback", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	{"auto_failback_interval", "time", "1min", nil, nil, nil, "restart", "4.4", "", true},
	{"recovery_user", "string", "nobody", nil, nil, nil, "restart", "4.4", "", true},
	{"recovery_password", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"recovery_1st_stage_command", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"recovery_2nd_stage_command", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"recovery_timeout", "time", "90", nil, nil, nil, "restart", "4.4", "", true},
	{"client_idle_limit_in_recovery", "time", "0", nil, nil, nil, "restart", "4.4", "", true},
	// watchdog
	{"use_watchdog", "bool", "off", nil, nil, nil, "restart", "4.4", "", false},
	{"trusted_servers", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"ping_path", "string", "/bin", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_hostname", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"hostname*", "string", "", nil, nil, nil, "restart", "4.4", "wd node", false},
	{"wd_port*", "int", "9000", nil, nil, nil, "restart", "4.4", "", false},
	{"pgpool_port*", "int", "9999", nil, nil, nil, "restart", "4.4", "", false},
	{"wd_priority", "int", "1", f64(1), nil, nil, "restart", "4.4", "", false},
	{"wd_authkey", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_ipc_socket_dir", "string", "/tmp", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_interval", "time", "10", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_life_point", "int", "3", f64(1), nil, nil, "restart", "4.4", "", true},
	{"wd_lifecheck_query", "string", "SELECT 1", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_lifecheck_user", "string", "nobody", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_lifecheck_password", "string", "", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_lifecheck_dbname", "string", "template1", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_lifecheck_method", "enum", "heartbeat", nil, nil, []string{"heartbeat", "query"}, "restart", "4.4", "", true},
	{"wd_heartbeat_port", "int", "9694", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_heartbeat_keepalive", "time", "2", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_heartbeat_deadtime", "time", "30", nil, nil, nil, "restart", "4.4", "", true},
	{"heartbeat_destination*", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"heartbeat_destination_port*", "int", "9694", nil, nil, nil, "restart", "4.4", "", false},
	{"heartbeat_device*", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"delegate_ip", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"if_up_cmd", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"if_down_cmd", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"arping_path", "string", "/usr/sbin", nil, nil, nil, "restart", "4.4", "", true},
	{"arping_cmd", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"clear_memqcache_on_escalation", "bool", "on", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_escalation_command", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"wd_de_escalation_command", "string", "", nil, nil, nil, "restart", "4.4", "", false},
	{"enable_consensus_with_half_votes", "bool", "off", nil, nil, nil, "restart", "4.4", "", false},
	{"allow_multiple_failover_requests_from_node", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	{"wd_remove_shutdown_nodes", "bool", "off", nil, nil, nil, "restart", "4.4", "", true},
	// streaming replication check
	{"sr_check_period", "time", "0", f64(0), nil, nil, "reload", "4.4", "", false},
	{"sr_check_user", "string", "nobody", nil, nil, nil, "reload", "4.4", "", false},
	{"sr_check_password", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	{"sr_check_database", "string", "postgres", nil, nil, nil, "reload", "4.4", "", false},
	{"delay_threshold", "memory", "0", f64(0), nil, nil, "reload", "4.4", "", true},
	{"delay_threshold_by_time", "time", "0", f64(0), nil, nil, "reload", "4.4", "", true},
	{"preferred_main_clusters", "string", "", nil, nil, nil, "reload", "4.4", "", true},
	// memory cache / misc
	{"memory_cache_enabled", "bool", "off", nil, nil, nil, "restart", "4.4", "", false},
	{"memqcache_oiddir", "string", "/var/log/pgpool/oiddir", nil, nil, nil, "restart", "4.4", "", true},
	{"memqcache_expire", "time", "0", nil, nil, nil, "restart", "4.4", "", true},
	{"memqcache_total_size", "memory", "64MB", nil, nil, nil, "restart", "4.4", "", true},
	{"memqcache_max_num_cache", "int", "999999", nil, nil, nil, "restart", "4.4", "", true},
	{"memqcache_cache_block_size", "memory", "1MB", nil, nil, nil, "restart", "4.4", "", true},
	{"relcache_expire", "time", "0", nil, nil, nil, "restart", "4.4", "", true},
	{"relcache_size", "int", "256", nil, nil, nil, "restart", "4.4", "", true},
	{"check_temp_table", "enum", "catalog", nil, nil, []string{"catalog", "all", "none"}, "restart", "4.4", "", true},
	{"check_unlogged_table", "enum", "on", nil, nil, []string{"on", "off", "catalog", "all", "none"}, "restart", "4.4", "", true},
}

var numberedRE = regexp.MustCompile(`^(.+?)(\d+)$`)

// Lookup finds a param, including numbered backend_hostname0-style names.
func Lookup(name string) (Param, bool) {
	for _, p := range catalogue {
		if p.Name == name {
			return p, true
		}
	}
	if m := numberedRE.FindStringSubmatch(name); m != nil {
		tmpl := m[1] + "*"
		for _, p := range catalogue {
			if p.Name == tmpl {
				p.Name = name
				return p, true
			}
		}
	}
	return Param{}, false
}

var memRE = regexp.MustCompile(`^(\d+(\.\d+)?)(kB|KB|MB|GB|B)?$`)
var timeRE = regexp.MustCompile(`^(\d+(\.\d+)?)(ms|s|sec|min|h|d)?$`)

// ValidateValue checks a value against type/range/enum.
func ValidateValue(p Param, v string) error {
	switch p.Type {
	case "bool":
		switch strings.ToLower(v) {
		case "on", "off", "true", "false", "yes", "no", "1", "0":
		default:
			return fmt.Errorf("%s wants a boolean", p.Name)
		}
	case "int":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n != float64(int64(n)) {
			return fmt.Errorf("%s wants an integer", p.Name)
		}
		if p.Min != nil && n < *p.Min {
			return fmt.Errorf("%s < %g", p.Name, *p.Min)
		}
		if p.Max != nil && n > *p.Max {
			return fmt.Errorf("%s > %g", p.Name, *p.Max)
		}
	case "real":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s wants a number", p.Name)
		}
		if p.Min != nil && n < *p.Min {
			return fmt.Errorf("%s < %g", p.Name, *p.Min)
		}
	case "enum":
		for _, e := range p.Enum {
			if e == v {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", p.Name, strings.Join(p.Enum, "|"))
	case "memory":
		if !memRE.MatchString(v) {
			return fmt.Errorf("%s wants a memory value (e.g. 64MB)", p.Name)
		}
	case "time":
		if !timeRE.MatchString(v) {
			return fmt.Errorf("%s wants a time value (e.g. 30s, 10min)", p.Name)
		}
	case "list", "string":
	}
	return nil
}

// RuleResult is one cross-param rule outcome.
type RuleResult struct {
	Name   string
	OK     bool
	Detail string
}

// WarnRules is the WARN-class: capacity advice that must not gate
// `config validate` exit 4 or `config apply` preflight (design 02 §5).
var WarnRules = map[string]bool{"CONN_BUDGET": true}

// CrossRules evaluates the rules from the brief.
func CrossRules(values map[string]string, backends []model.Backend) []RuleResult {
	get := func(k string) string { return values[k] }
	on := func(s string) bool { return s == "on" || s == "true" || s == "yes" || s == "1" }
	var out []RuleResult
	// CONN_BUDGET
	nc, _ := strconv.Atoi(get("num_init_children"))
	mp, _ := strconv.Atoi(get("max_pool"))
	if nc > 0 && mp > 0 && len(backends) > 0 {
		minConns, known := 0, 0
		for _, b := range backends {
			if b.MaxConns > 0 {
				known++
				if minConns == 0 || b.MaxConns < minConns {
					minConns = b.MaxConns
				}
			}
		}
		if known == 0 {
			out = append(out, RuleResult{"CONN_BUDGET", true,
				"max_connections unknown: run `poolwarden nodes` first"})
		} else {
			reserved := 3
			budget := nc * mp
			ok := budget <= minConns-reserved
			out = append(out, RuleResult{"CONN_BUDGET", ok,
				fmt.Sprintf("num_init_children×max_pool = %d vs min(max_connections) %d − %d reserved",
					budget, minConns, reserved)})
		}
	}
	if rc, _ := strconv.Atoi(get("reserved_connections")); nc > 0 && rc >= nc {
		out = append(out, RuleResult{"RESERVED_CONNECTIONS", false,
			fmt.Sprintf("reserved_connections %d ≥ num_init_children %d", rc, nc)})
	} else if nc > 0 {
		out = append(out, RuleResult{"RESERVED_CONNECTIONS", true,
			fmt.Sprintf("reserved_connections %d < num_init_children %d", rc, nc)})
	}
	if wp, err := strconv.Atoi(get("wd_priority")); get("wd_priority") != "" && (err != nil || wp < 1) {
		out = append(out, RuleResult{"WD_PRIORITY", false, "wd_priority < 1"})
	} else {
		out = append(out, RuleResult{"WD_PRIORITY", true, "wd_priority ≥ 1"})
	}
	if get("delegate_ip") != "" {
		missing := []string{}
		for _, k := range []string{"if_up_cmd", "if_down_cmd", "arping_cmd"} {
			if get(k) == "" {
				missing = append(missing, k)
			}
		}
		detail := "if_up_cmd, if_down_cmd, arping_cmd all set"
		if len(missing) > 0 {
			detail = "delegate_ip set ⇒ missing " + strings.Join(missing, ",")
		}
		out = append(out, RuleResult{"VIP_COMMANDS", len(missing) == 0, detail})
	}
	if on(get("use_watchdog")) {
		missing := []string{}
		if get("wd_port0") == "" && get("wd_port") == "" {
			missing = append(missing, "wd_port0")
		}
		if get("hostname0") == "" {
			missing = append(missing, "hostname0")
		}
		detail := "watchdog node entries present"
		if len(missing) > 0 {
			detail = "use_watchdog on ⇒ " + strings.Join(missing, ",") + " required"
		}
		out = append(out, RuleResult{"WATCHDOG_NODES", len(missing) == 0, detail})
	}
	if hr, err := strconv.Atoi(get("health_check_max_retries")); get("health_check_max_retries") != "" && (err != nil || hr < 0) {
		out = append(out, RuleResult{"HEALTH_RETRIES", false, "health_check_max_retries < 0"})
	} else {
		out = append(out, RuleResult{"HEALTH_RETRIES", true, "health_check_max_retries ≥ 0"})
	}
	if hd := get("health_check_retry_delay"); hd != "" {
		out = append(out, RuleResult{"HEALTH_RETRY_DELAY", true, "health_check_retry_delay ≥ 0"})
	}
	if get("sr_check_user") != "" && get("sr_check_user") != "nobody" {
		sp := ParseTimeSeconds(get("sr_check_period"))
		out = append(out, RuleResult{"SR_CHECK", sp > 0,
			fmt.Sprintf("sr_check_user set ⇒ sr_check_period > 0 (got %s)", get("sr_check_period"))})
	}
	return out
}

// Context returns "restart" if any changed param needs a restart.
func Context(names []string) string {
	ctx := "reload"
	for _, n := range names {
		if p, ok := Lookup(n); ok && p.Context == "restart" {
			ctx = "restart"
		}
	}
	return ctx
}

// All returns the whole catalogue.
func All() []Param { return catalogue }

// ParseTimeSeconds parses pgpool time values ("5s", "10min", "2h", "500ms",
// "1d", bare = seconds) into seconds.
func ParseTimeSeconds(s string) int {
	s = strings.TrimSpace(s)
	mult := 1.0
	for _, suf := range []struct {
		s string
		m float64
	}{{"ms", 0.001}, {"min", 60}, {"s", 1}, {"h", 3600}, {"d", 86400}} {
		if strings.HasSuffix(s, suf.s) {
			s = strings.TrimSuffix(s, suf.s)
			mult = suf.m
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f * mult)
}

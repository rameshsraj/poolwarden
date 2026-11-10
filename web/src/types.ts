// Types mirroring the CLI's JSON output (service.*Result) and console API.

export interface Header {
  cluster: string;
  pgpool_version: string;
  local_host: string;
  local_wd_role: string;
  vip_host: string;
  collected: string;
  duration_ms: number;
  degraded: boolean;
}

export interface NodeRow {
  id: number;
  host_port: string;
  status: string;
  pgpool_role: string;
  real_role: string;
  lb_weight: number;
  selects: number;
  delay_sec: number;
  repl_state: string;
  sync: string;
  last_change: string;
  score: number;
  note: string;
  timeline?: number;
  current_lsn?: string;
  receive_lsn?: string;
  conns: number;
  max_conns: number;
  oldest_xact_sec: number;
  conflicts: number;
}

export interface LagRow {
  standby: string;
  state: string;
  sync: string;
  lag_bytes: number;
  write_lag_sec: number;
  flush_lag_sec: number;
  replay_lag_sec: number;
  delay_sec: number;
  delay_severity: string;
  trend: string;
  eta_sec: number;
  sent_lsn?: string;
  write_lsn?: string;
  flush_lsn?: string;
  replay_lsn?: string;
}

export interface LagResult {
  header: Header;
  primary: string;
  primary_lsn: string;
  standbys: LagRow[];
  findings: Finding[];
}

export interface Finding {
  ID: string;
  Rule: string;
  Severity: "INFO" | "WARN" | "CRIT";
  Subject: string;
  Evidence: string[];
  Cause: string;
  Causes: string[];
  NextCommands: string[];
  DocRef: string;
  FirstSeen: string;
}

export interface PoolGroup {
  database: string;
  user: string;
  backends: number;
  pools: number;
  connected: number;
  load_balance_nodes: number;
}

export interface PoolsResult {
  header: Header;
  children_busy: number;
  children_max: number;  // num_init_children
  pool_max: number;      // max_pool: backend conns pooled per child
  clients: number;       // cumulative client conns served (lifetime)
  groups: PoolGroup[];
  findings: Finding[];
}

export interface ConnRow {
  where: string;
  database: string;
  user: string;
  application: string;
  state: string; // pg_stat_activity.state; "-" for pgpool-side rows
  conns: number;
  limit: number;
}

export interface WatchdogNodeRow {
  node: number;
  host: string;
  status: string;
  priority: number;
  vip_present: boolean;
}

export interface WatchdogResult {
  header: Header;
  available: boolean;
  quorum: string;
  leader: string;
  alive: number;
  total: number;
  params: Record<string, string>;
  nodes: WatchdogNodeRow[];
  vip_banner: string;
  findings: Finding[];
}

export interface TopologyRow {
  host: string;
  address: string;
  roles: string[];
  ssh_user: string;
  watchdog: string;
  vip: boolean;
  backend_role: string;
}

export interface StatusResult {
  header: Header;
  nodes: NodeRow[];
  watchdog_available: boolean;
  quorum: string;
  leader: string;
  alive: number;
  total: number;
  half_votes: boolean;
  children_busy: number;
  children_max: number;
  clients: number;
  crit: number;
  warn: number;
  info: number;
  findings: Finding[];
}

export interface MonitorBundle {
  header: Header;
  status: StatusResult;
  nodes: { header: Header; nodes: NodeRow[]; findings: Finding[] };
  lag?: LagResult;
  lag_err?: string;
  pools: PoolsResult;
  conns: { header: Header; conns: ConnRow[]; findings: Finding[] };
  slots: {
    header: Header;
    slots: {
      name: string; type: string; active: boolean;
      wal_status: string; retained_bytes: number; eta_sec: number;
    }[];
    findings: Finding[];
  };
  watchdog: WatchdogResult;
  health: {
    header: Header;
    nodes: {
      node: number; host: string; status: string; total: number;
      success: number; fail: number; skip: number; retry: number;
      avg_ms: number; max_ms: number; last_fail: string; delta_fail: number | null;
    }[];
    findings: Finding[];
  };
  topology: { header: Header; hosts: TopologyRow[]; findings: Finding[] };
  wal?: {
    primary: string;
    primary_lsn: string;
    wal_dir_bytes: number;   // -1 unknown
    wal_files: number;       // -1 unknown
    slot_retained: number;   // -1 unknown
  };
  rates?: {
    interval_ms: number;
    wal_write_bps: number;                    // primary WAL generation, -1 unknown
    nodes?: Record<number, number>;           // node id -> selects/sec
    standbys?: Record<string, number>;        // standby name -> replay bytes/sec
  };
  findings: Finding[];
}

export interface ErrorEvent {
  ID: string;
  TS: string;
  Severity: string;
  Source: string;
  Host: string;
  Code: string;
  Message: string;
  Ref: string;
}

export interface LogLine {
  TS: string;
  Host: string;
  Component: string;
  Severity: string;
  Message: string;
}

export interface CollectError {
  message: string;
  cause?: string;
  hint?: string;
}

export interface Op {
  id: string;
  action: string;
  argv: string[];
  operator: string;
  status: "queued" | "running" | "success" | "failed" | "interrupted";
  started: string;
  finished?: string;
  exit_code: number;
  events: any[];
  result?: any;
  error?: string;
}

export interface PlanResult {
  exit_code: number;
  argv: string[];
  result?: {
    plan: string;
    preflight?: {
      Action: string;
      Verdict: string;
      Checks: { Name: string; Result: string; Detail: string }[];
    };
    token?: string;
    yes_no?: boolean;
    question?: string;
    outcome?: string;
  };
  error?: string;
}

export interface ProbeResult {
  cluster: string;
  duration_ms: number;
  checks: { name: string; host: string; result: string; detail: string }[];
  ok: boolean;
}

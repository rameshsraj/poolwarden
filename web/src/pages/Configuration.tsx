// Configuration: Connection Settings (setup wizard → probe → install via
// `poolwarden config install`) and pgpool Configuration (staging transaction
// via `poolwarden config set/diff/apply/rollback`).
import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { parse as parseYAML, stringify as toYAML } from "yaml";
import { api } from "../api";
import { Badge, Button, Card, Dialog, Field, Input, Table } from "../ui";
import type { ProbeResult } from "../types";

// ---------- connection settings model ----------

interface HostForm {
  name: string; address: string; roles: string; ssh_user: string;
  pg_port: string; pg_data_dir: string; pg_log_dir: string; pg_log_filename: string; pg_log_line_prefix: string;
  pp_conf: string; pp_hba: string; pp_pcp_conf: string; pp_pool_passwd: string;
  pp_log_dir: string; pp_log_line_prefix: string; pp_service: string;
}

interface ConnForm {
  cluster: string; state_dir: string;
  pp_host: string; pp_port: string; pp_user: string; pp_dbname: string; pp_passfile: string;
  pcp_host: string; pcp_port: string; pcp_user: string; pcp_bin_dir: string; pcp_passfile: string;
  delegate_ip: string; // expected VIP (pgpool delegate_ip)
  ssh_key: string; ssh_known_hosts: string; ssh_timeout: string;
  interval: string; timeout: string;
  lag_warn: string; lag_crit: string; wal_volume: string;
  audit_file: string; audit_syslog: boolean; audit_read_cmds: boolean; audit_retention: string;
  hosts: HostForm[];
}

const emptyHost = (): HostForm => ({
  name: "", address: "", roles: "pgpool,pg", ssh_user: "root",
  pg_port: "5432", pg_data_dir: "", pg_log_dir: "", pg_log_filename: "", pg_log_line_prefix: "",
  pp_conf: "/etc/pgpool-II/pgpool.conf", pp_hba: "/etc/pgpool-II/pool_hba.conf",
  pp_pcp_conf: "/etc/pgpool-II/pcp.conf", pp_pool_passwd: "/etc/pgpool-II/pool_passwd",
  pp_log_dir: "/var/log/pgpool-II", pp_log_line_prefix: "%m: %a pid %p: ", pp_service: "pgpool-II",
});

const emptyForm = (): ConnForm => ({
  cluster: "prod-a", state_dir: "/var/lib/poolwarden",
  pp_host: "127.0.0.1", pp_port: "9999", pp_user: "postgres", pp_dbname: "postgres", pp_passfile: "",
  pcp_host: "127.0.0.1", pcp_port: "9898", pcp_user: "postgres", pcp_bin_dir: "/usr/bin", pcp_passfile: "",
  delegate_ip: "",
  ssh_key: "/root/.ssh/id_ed25519", ssh_known_hosts: "/root/.ssh/known_hosts", ssh_timeout: "5s",
  interval: "2s", timeout: "3s",
  lag_warn: "1800", lag_crit: "3600", wal_volume: "0",
  audit_file: "/var/lib/poolwarden/audit.jsonl", audit_syslog: true, audit_read_cmds: false, audit_retention: "31",
  hosts: [emptyHost()],
});

function formToCfg(f: ConnForm): any {
  const cfg: any = {
    cluster: f.cluster,
    state_dir: f.state_dir,
    pgpool: {
      host: f.pp_host, port: +f.pp_port || 9999, user: f.pp_user, dbname: f.pp_dbname,
      ...(f.pp_passfile ? { passfile: f.pp_passfile } : {}),
    },
    pcp: {
      host: f.pcp_host, port: +f.pcp_port || 9898, user: f.pcp_user, bin_dir: f.pcp_bin_dir,
      ...(f.pcp_passfile ? { passfile: f.pcp_passfile } : {}),
    },
    bins: { systemctl: "/usr/bin/systemctl", ip: "/usr/sbin/ip" },
    ssh: { key: f.ssh_key, known_hosts: f.ssh_known_hosts, timeout: f.ssh_timeout },
    interval: f.interval,
    timeout: f.timeout,
    lag: { warn_seconds: +f.lag_warn || 0, crit_seconds: +f.lag_crit || 0 },
    wal_volume_bytes: f.wal_volume,
    audit: {
      file: f.audit_file, syslog: f.audit_syslog,
      read_commands: f.audit_read_cmds, retention_days: +f.audit_retention || 31,
    },
    hosts: f.hosts.map((h) => {
      const roles = h.roles.split(",").map((s) => s.trim()).filter(Boolean);
      const host: any = { name: h.name, address: h.address, roles, ssh_user: h.ssh_user };
      if (roles.includes("pgpool")) {
        host.pgpool = {
          conf: h.pp_conf, hba: h.pp_hba, pcp_conf: h.pp_pcp_conf,
          pool_passwd: h.pp_pool_passwd, log_dir: h.pp_log_dir,
          log_line_prefix: h.pp_log_line_prefix, service: h.pp_service,
        };
      }
      if (roles.includes("pg")) {
        host.pg = {
          port: +h.pg_port || 5432,
          ...(h.pg_data_dir ? { data_dir: h.pg_data_dir } : {}),
          ...(h.pg_log_dir ? { log_dir: h.pg_log_dir } : {}),
          ...(h.pg_log_filename ? { log_filename: h.pg_log_filename } : {}),
          ...(h.pg_log_line_prefix ? { log_line_prefix: h.pg_log_line_prefix } : {}),
        };
      }
      return host;
    }),
  };
  return cfg;
}

function cfgToForm(c: any): ConnForm {
  const f = emptyForm();
  if (!c || typeof c !== "object") return f;
  f.cluster = c.cluster ?? f.cluster;
  f.state_dir = c.state_dir ?? f.state_dir;
  if (c.pgpool) {
    f.pp_host = c.pgpool.host ?? f.pp_host;
    f.pp_port = String(c.pgpool.port ?? f.pp_port);
    f.pp_user = c.pgpool.user ?? f.pp_user;
    f.pp_dbname = c.pgpool.dbname ?? f.pp_dbname;
    f.pp_passfile = c.pgpool.passfile ?? "";
  }
  if (c.pcp) {
    f.pcp_host = c.pcp.host ?? f.pcp_host;
    f.pcp_port = String(c.pcp.port ?? f.pcp_port);
    f.pcp_user = c.pcp.user ?? f.pcp_user;
    f.pcp_bin_dir = c.pcp.bin_dir ?? f.pcp_bin_dir;
    f.pcp_passfile = c.pcp.passfile ?? "";
  }
  if (c.ssh) {
    f.ssh_key = c.ssh.key ?? f.ssh_key;
    f.ssh_known_hosts = c.ssh.known_hosts ?? f.ssh_known_hosts;
    f.ssh_timeout = c.ssh.timeout ?? f.ssh_timeout;
  }
  f.interval = c.interval ?? f.interval;
  f.timeout = c.timeout ?? f.timeout;
  if (c.lag) {
    f.lag_warn = String(c.lag.warn_seconds ?? f.lag_warn);
    f.lag_crit = String(c.lag.crit_seconds ?? f.lag_crit);
  }
  f.wal_volume = String(c.wal_volume_bytes ?? f.wal_volume);
  if (c.audit) {
    f.audit_file = c.audit.file ?? f.audit_file;
    f.audit_syslog = !!c.audit.syslog;
    f.audit_read_cmds = !!c.audit.read_commands;
    f.audit_retention = String(c.audit.retention_days ?? f.audit_retention);
  }
  if (Array.isArray(c.hosts)) {
    f.hosts = c.hosts.map((h: any) => ({
      ...emptyHost(),
      name: h.name ?? "", address: h.address ?? "",
      roles: (h.roles ?? []).join(","), ssh_user: h.ssh_user ?? "root",
      pg_port: String(h.pg?.port ?? "5432"),
      pg_data_dir: h.pg?.data_dir ?? "", pg_log_dir: h.pg?.log_dir ?? "",
      pg_log_filename: h.pg?.log_filename ?? "", pg_log_line_prefix: h.pg?.log_line_prefix ?? "",
      pp_conf: h.pgpool?.conf ?? emptyHost().pp_conf,
      pp_hba: h.pgpool?.hba ?? emptyHost().pp_hba,
      pp_pcp_conf: h.pgpool?.pcp_conf ?? emptyHost().pp_pcp_conf,
      pp_pool_passwd: h.pgpool?.pool_passwd ?? emptyHost().pp_pool_passwd,
      pp_log_dir: h.pgpool?.log_dir ?? emptyHost().pp_log_dir,
      pp_log_line_prefix: h.pgpool?.log_line_prefix ?? emptyHost().pp_log_line_prefix,
      pp_service: h.pgpool?.service ?? emptyHost().pp_service,
    }));
  }
  return f;
}

// ---------- connection settings tab ----------

function ConnectionSettings() {
  const qc = useQueryClient();
  const conn = useQuery({ queryKey: ["connection"], queryFn: api.connection });
  const [form, setForm] = useState<ConnForm | null>(null);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [probeErr, setProbeErr] = useState("");
  const [installMsg, setInstallMsg] = useState("");
  const [busy, setBusy] = useState("");
  const [showYaml, setShowYaml] = useState(false);

  useEffect(() => {
    if (conn.data && form === null) {
      try {
        setForm(conn.data.exists ? cfgToForm(parseYAML(conn.data.yaml)) : emptyForm());
      } catch {
        setForm(emptyForm());
      }
    }
  }, [conn.data]);

  const yaml = useMemo(() => (form ? toYAML(formToCfg(form)) : ""), [form]);
  if (!form) return <div className="text-mut">loading…</div>;
  const set = <K extends keyof ConnForm>(k: K, v: ConnForm[K]) => setForm({ ...form, [k]: v });
  const setHost = (i: number, k: keyof HostForm, v: string) => {
    const hosts = form.hosts.slice();
    hosts[i] = { ...hosts[i], [k]: v };
    setForm({ ...form, hosts });
  };

  const test = async () => {
    setBusy("test");
    setProbeErr("");
    setProbe(null);
    try {
      setProbe((await api.testConnection(yaml)) as ProbeResult);
    } catch (e: any) {
      setProbeErr(e.message);
    } finally {
      setBusy("");
    }
  };
  const install = async () => {
    setBusy("install");
    setInstallMsg("");
    setProbeErr("");
    try {
      await api.installConnection(yaml);
      setInstallMsg("installed — monitor restarted on the new configuration");
      qc.invalidateQueries({ queryKey: ["connection"] });
    } catch (e: any) {
      setProbeErr(e.message);
    } finally {
      setBusy("");
    }
  };

  const sev = (r: string) => (r === "OK" ? "OK" : r === "FAIL" ? "CRIT" : r === "WARN" ? "WARN" : "INFO");

  return (
    <div className="space-y-4">
      <Card title="cluster & endpoints" right={<Badge text={conn.data?.exists ? "installed" : "not installed"} sev={conn.data?.exists ? "OK" : "WARN"} />}>
        <div className="grid gap-3 md:grid-cols-3">
          <Field label="cluster name"><Input value={form.cluster} onChange={(v) => set("cluster", v)} /></Field>
          <Field label="state dir"><Input value={form.state_dir} onChange={(v) => set("state_dir", v)} /></Field>
          <Field label="expected VIP (pgpool delegate_ip)" hint="expected holder is monitored; changing the VIP itself is a pgpool config change">
            <Input value={form.delegate_ip} onChange={(v) => set("delegate_ip", v)} placeholder="10.0.0.100" />
          </Field>
        </div>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="pgpool SQL endpoint" >
          <div className="grid grid-cols-2 gap-3">
            <Field label="host"><Input value={form.pp_host} onChange={(v) => set("pp_host", v)} /></Field>
            <Field label="port"><Input value={form.pp_port} onChange={(v) => set("pp_port", v)} /></Field>
            <Field label="user"><Input value={form.pp_user} onChange={(v) => set("pp_user", v)} /></Field>
            <Field label="dbname"><Input value={form.pp_dbname} onChange={(v) => set("pp_dbname", v)} /></Field>
            <Field label="passfile" hint="PGPASSFILE path — passwords are never stored in this config">
              <Input value={form.pp_passfile} onChange={(v) => set("pp_passfile", v)} placeholder="/root/.pgpass" />
            </Field>
          </div>
        </Card>
        <Card title="pgpool admin (PCP)">
          <div className="grid grid-cols-2 gap-3">
            <Field label="host"><Input value={form.pcp_host} onChange={(v) => set("pcp_host", v)} /></Field>
            <Field label="port"><Input value={form.pcp_port} onChange={(v) => set("pcp_port", v)} /></Field>
            <Field label="user"><Input value={form.pcp_user} onChange={(v) => set("pcp_user", v)} /></Field>
            <Field label="pcp bin dir"><Input value={form.pcp_bin_dir} onChange={(v) => set("pcp_bin_dir", v)} /></Field>
            <Field label="passfile" hint="PCPPASSFILE path">
              <Input value={form.pcp_passfile} onChange={(v) => set("pcp_passfile", v)} placeholder="/root/.pcppass" />
            </Field>
          </div>
        </Card>
      </div>

      <Card title="ssh & monitoring">
        <div className="grid gap-3 md:grid-cols-4">
          <Field label="ssh key"><Input value={form.ssh_key} onChange={(v) => set("ssh_key", v)} /></Field>
          <Field label="known_hosts"><Input value={form.ssh_known_hosts} onChange={(v) => set("ssh_known_hosts", v)} /></Field>
          <Field label="ssh timeout"><Input value={form.ssh_timeout} onChange={(v) => set("ssh_timeout", v)} /></Field>
          <Field label="collect interval"><Input value={form.interval} onChange={(v) => set("interval", v)} /></Field>
          <Field label="command timeout"><Input value={form.timeout} onChange={(v) => set("timeout", v)} /></Field>
          <Field label="lag warn (s)"><Input value={form.lag_warn} onChange={(v) => set("lag_warn", v)} /></Field>
          <Field label="lag crit (s)"><Input value={form.lag_crit} onChange={(v) => set("lag_crit", v)} /></Field>
          <Field label="wal volume bytes"><Input value={form.wal_volume} onChange={(v) => set("wal_volume", v)} /></Field>
          <Field label="audit file"><Input value={form.audit_file} onChange={(v) => set("audit_file", v)} /></Field>
          <Field label="audit retention (d)"><Input value={form.audit_retention} onChange={(v) => set("audit_retention", v)} /></Field>
          <Field label="audit syslog">
            <input type="checkbox" checked={form.audit_syslog} onChange={(e) => set("audit_syslog", e.target.checked)} className="mt-2" />
          </Field>
        </div>
      </Card>

      {form.hosts.map((h, i) => (
        <Card
          key={i}
          title={`host ${i + 1}: ${h.name || "(unnamed)"}`}
          right={
            <Button small variant="ghost" onClick={() => set("hosts", form.hosts.filter((_, j) => j !== i))}>
              remove
            </Button>
          }
        >
          <div className="grid gap-3 md:grid-cols-4">
            <Field label="name"><Input value={h.name} onChange={(v) => setHost(i, "name", v)} /></Field>
            <Field label="address"><Input value={h.address} onChange={(v) => setHost(i, "address", v)} /></Field>
            <Field label="roles" hint="pgpool,pg"><Input value={h.roles} onChange={(v) => setHost(i, "roles", v)} /></Field>
            <Field label="ssh user"><Input value={h.ssh_user} onChange={(v) => setHost(i, "ssh_user", v)} /></Field>
          </div>
          {h.roles.includes("pg") && (
            <div className="mt-3 grid gap-3 rounded border border-edge/60 p-3 md:grid-cols-4">
              <div className="col-span-full text-[10px] font-semibold uppercase text-mut">postgresql</div>
              <Field label="port"><Input value={h.pg_port} onChange={(v) => setHost(i, "pg_port", v)} /></Field>
              <Field label="data dir"><Input value={h.pg_data_dir} onChange={(v) => setHost(i, "pg_data_dir", v)} /></Field>
              <Field label="log dir"><Input value={h.pg_log_dir} onChange={(v) => setHost(i, "pg_log_dir", v)} /></Field>
              <Field label="log filename"><Input value={h.pg_log_filename} onChange={(v) => setHost(i, "pg_log_filename", v)} /></Field>
              <Field label="log_line_prefix"><Input value={h.pg_log_line_prefix} onChange={(v) => setHost(i, "pg_log_line_prefix", v)} /></Field>
            </div>
          )}
          {h.roles.includes("pgpool") && (
            <div className="mt-3 grid gap-3 rounded border border-edge/60 p-3 md:grid-cols-4">
              <div className="col-span-full text-[10px] font-semibold uppercase text-mut">pgpool files</div>
              <Field label="pgpool.conf"><Input value={h.pp_conf} onChange={(v) => setHost(i, "pp_conf", v)} /></Field>
              <Field label="pool_hba.conf"><Input value={h.pp_hba} onChange={(v) => setHost(i, "pp_hba", v)} /></Field>
              <Field label="pcp.conf"><Input value={h.pp_pcp_conf} onChange={(v) => setHost(i, "pp_pcp_conf", v)} /></Field>
              <Field label="pool_passwd"><Input value={h.pp_pool_passwd} onChange={(v) => setHost(i, "pp_pool_passwd", v)} /></Field>
              <Field label="log dir"><Input value={h.pp_log_dir} onChange={(v) => setHost(i, "pp_log_dir", v)} /></Field>
              <Field label="log_line_prefix"><Input value={h.pp_log_line_prefix} onChange={(v) => setHost(i, "pp_log_line_prefix", v)} /></Field>
              <Field label="service name"><Input value={h.pp_service} onChange={(v) => setHost(i, "pp_service", v)} /></Field>
            </div>
          )}
        </Card>
      ))}
      <Button variant="ghost" small onClick={() => set("hosts", [...form.hosts, emptyHost()])}>+ add host</Button>

      <Card title="review & apply" right={<Button small variant="ghost" onClick={() => setShowYaml(!showYaml)}>{showYaml ? "hide" : "show"} yaml</Button>}>
        {showYaml && (
          <pre className="mb-3 max-h-64 overflow-auto rounded border border-edge bg-bg p-2 font-mono text-[10px] text-mut">{yaml}</pre>
        )}
        <div className="flex items-center gap-3">
          <Button onClick={test} disabled={!!busy}>{busy === "test" ? "testing…" : "test connections"}</Button>
          <Button variant="danger" onClick={install} disabled={!!busy}>
            {busy === "install" ? "installing…" : "install config"}
          </Button>
          {probe && <Badge text={probe.ok ? "all reachable" : "failures"} sev={probe.ok ? "OK" : "CRIT"} />}
          {installMsg && <span className="text-xs text-ok">{installMsg}</span>}
        </div>
        {probeErr && <div className="mt-2 text-xs text-crit">{probeErr}</div>}
        {probe && (
          <div className="mt-3">
            <Table
              dense
              cols={["CHECK", "HOST", "RESULT", "DETAIL"]}
              rows={probe.checks.map((c) => [
                <span className="font-mono">{c.name}</span>,
                <span className="font-mono">{c.host || "-"}</span>,
                <Badge text={c.result} sev={sev(c.result)} />,
                <span className="text-mut">{c.detail}</span>,
              ])}
            />
          </div>
        )}
      </Card>
    </div>
  );
}

// ---------- pgpool configuration tab ----------

function PgpoolConfig() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["config-list"], queryFn: () => api.view<any[]>("config-list"), refetchInterval: 15000 });
  const diff = useQuery({ queryKey: ["config-diff"], queryFn: () => api.view<string>("config-diff") });
  const history = useQuery({ queryKey: ["config-history"], queryFn: () => api.view<any[]>("config-history") });
  const [grep, setGrep] = useState("");
  const [setParam, setSetParam] = useState<{ name: string; value: string } | null>(null);
  const [msg, setMsg] = useState("");
  const [confirmApply, setConfirmApply] = useState(false);

  const rows = (list.data ?? []).filter(
    (r: any) => !grep || r.Param?.toLowerCase().includes(grep.toLowerCase()),
  );

  const run = async (body: object, note: string) => {
    try {
      await api.action(body);
      setMsg(note);
      qc.invalidateQueries({ queryKey: ["config-list"] });
      qc.invalidateQueries({ queryKey: ["config-diff"] });
    } catch (e: any) {
      setMsg(e.message);
    }
  };

  return (
    <div className="space-y-4">
      <Card
        title="staged changes (transaction)"
        right={
          <span className="flex gap-2">
            <Button small variant="ghost" onClick={() => run({ action: "config-discard" }, "staged changes discarded")}>discard</Button>
            <Button small variant="danger" onClick={() => setConfirmApply(true)}>apply to cluster</Button>
          </span>
        }
      >
        <pre className="max-h-56 overflow-auto rounded border border-edge bg-bg p-2 font-mono text-[11px] text-mut">
          {typeof diff.data === "string" ? diff.data : JSON.stringify(diff.data, null, 2) || "(no staged changes)"}
        </pre>
        {msg && <div className="mt-2 text-xs text-info">{msg}</div>}
      </Card>

      <Card title="parameters" right={<div className="w-56"><Input value={grep} onChange={setGrep} placeholder="filter params…" /></div>}>
        <div className="max-h-[52vh] overflow-auto">
          <Table
            dense
            cols={["PARAM", "FILE", "LIVE", "DEFAULT", "CONTEXT", "DRIFT", ""]}
            rows={rows.map((r: any) => [
              <span className="font-mono text-[11px]">{r.Param}</span>,
              r.File,
              <span className="font-mono">{r.Live || "-"}</span>,
              <span className="font-mono text-mut">{r.Default}</span>,
              r.Context,
              r.Drift ? <Badge text={r.Drift} sev="WARN" /> : "-",
              <Button small variant="ghost" onClick={() => setSetParam({ name: r.Param, value: r.Live || r.Default || "" })}>set</Button>,
            ])}
          />
        </div>
      </Card>

      <Card title="apply history">
        <Table
          dense
          cols={["VERSION", "TIME", "OPERATOR", "TICKET", "HOSTS", "SUMMARY", ""]}
          rows={(history.data ?? []).map((m: any) => [
            <span className="font-mono">{m.Version}</span>,
            m.Time ? new Date(m.Time).toLocaleString() : "-",
            m.Operator, m.Ticket || "-", (m.Hosts ?? []).join(","), m.Summary,
            <Button small variant="ghost" onClick={() => run({ action: "config-rollback", value: m.Version }, `rolling back to ${m.Version}`)}>rollback</Button>,
          ])}
        />
      </Card>

      <Dialog open={!!setParam} onClose={() => setSetParam(null)} title={`set ${setParam?.name}`}>
        {setParam && (
          <div className="space-y-3">
            <Field label={`${setParam.name} =`}>
              <Input value={setParam.value} onChange={(v) => setSetParam({ ...setParam, value: v })} />
            </Field>
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setSetParam(null)}>cancel</Button>
              <Button onClick={async () => {
                await run({ action: "config-set", param: setParam.name, value: setParam.value }, `staged ${setParam.name}`);
                setSetParam(null);
              }}>stage</Button>
            </div>
          </div>
        )}
      </Dialog>

      <Dialog open={confirmApply} onClose={() => setConfirmApply(false)} title="apply staged changes to all pgpool hosts">
        <div className="space-y-3 text-sm">
          <p className="text-mut">
            Runs <code className="font-mono">poolwarden config apply</code>: validates the staged
            transaction, writes files atomically on every pgpool host, reloads or performs an
            ordered restart, and rolls back all-or-nothing on failure.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmApply(false)}>cancel</Button>
            <Button variant="danger" onClick={async () => {
              await run({ action: "config-apply" }, "config apply submitted");
              setConfirmApply(false);
            }}>apply</Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}

export default function Configuration() {
  const [tab, setTab] = useState<"conn" | "pgpool">("conn");
  return (
    <div className="space-y-4">
      <div className="flex rounded border border-edge">
        {([["conn", "Connection Settings"], ["pgpool", "pgpool Configuration"]] as const).map(([k, l]) => (
          <button
            key={k}
            onClick={() => setTab(k)}
            className={`px-4 py-1.5 text-xs font-medium ${tab === k ? "bg-info/20 text-info" : "text-mut"}`}
          >
            {l}
          </button>
        ))}
      </div>
      {tab === "conn" ? <ConnectionSettings /> : <PgpoolConfig />}
    </div>
  );
}

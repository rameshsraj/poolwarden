import { useLive } from "../store";
import { Badge, Card, fmtBytes, Table } from "../ui";
import MiniTopology from "../components/MiniTopology";

export default function TopologyPage() {
  const b = useLive().snapshot;
  if (!b) return <div className="text-mut">waiting for snapshot…</div>;
  const t = b.topology;
  return (
    <div className="space-y-4">
      <Card title="cluster topology & data flow">
        <MiniTopology height={480} />
        <div className="mt-2 flex flex-wrap gap-4 text-[10px] text-mut">
          <span><span className="text-info">━▶</span> client traffic: VIP → pgpool (then routed to the PG primary)</span>
          <span><span className="text-ok">━▶</span> WAL replication: PG primary → standby (observed)</span>
          <span><span className="text-mut">┄┄</span> watchdog peer link</span>
          <span>edge animation = actively streaming</span>
          <span>note: watchdog LEADER owns the VIP; it need not be the PG primary</span>
        </div>
      </Card>
      {b.wal && (
        <Card title={`wal & storage — primary ${b.wal.primary}`}>
          <div className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4">
            <div>
              <div className="text-[10px] uppercase text-mut">WAL rate (generation)</div>
              <div className="font-mono text-lg text-ok">
                {b.rates && b.rates.wal_write_bps >= 0 ? `${fmtBytes(b.rates.wal_write_bps)}/s` : "-"}
              </div>
            </div>
            <div>
              <div className="text-[10px] uppercase text-mut">WAL dir size</div>
              <div className="font-mono text-lg">{b.wal.wal_dir_bytes >= 0 ? fmtBytes(b.wal.wal_dir_bytes) : "-"}</div>
            </div>
            <div>
              <div className="text-[10px] uppercase text-mut">WAL files retained</div>
              <div className="font-mono text-lg">{b.wal.wal_files >= 0 ? b.wal.wal_files : "-"}</div>
            </div>
            <div>
              <div className="text-[10px] uppercase text-mut">slot-retained WAL</div>
              <div className="font-mono text-lg">{b.wal.slot_retained >= 0 ? fmtBytes(b.wal.slot_retained) : "-"}</div>
            </div>
          </div>
          {b.lag && b.lag.standbys.length > 0 && (
            <Table
              cols={["STANDBY", "STATE", "REPLAY RATE", "LAG BACKLOG", "DELAY"]}
              rows={b.lag.standbys.map((s) => [
                <span className="font-mono">{s.standby}</span>,
                <Badge text={s.state} sev={s.state === "streaming" ? "OK" : "CRIT"} />,
                <span className="font-mono">
                  {b.rates?.standbys?.[s.standby] != null && b.rates.standbys[s.standby] >= 0
                    ? `${fmtBytes(b.rates.standbys[s.standby])}/s` : "-"}
                </span>,
                <span className="font-mono">{s.lag_bytes >= 0 ? fmtBytes(s.lag_bytes) : "-"}</span>,
                s.delay_sec >= 0 ? `${s.delay_sec.toFixed(0)}s` : "-",
              ])}
            />
          )}
          <div className="mt-2 text-[10px] text-mut">
            WAL dir size / file count from pg_ls_waldir on the primary · rates are
            deltas between collection ticks (every {b.rates ? (b.rates.interval_ms / 1000).toFixed(0) : "?"}s)
            · LSN {b.wal.primary_lsn}
          </div>
        </Card>
      )}
      <Card title="host inventory">
        <Table
          cols={["HOST", "ADDRESS", "ROLES", "SSH", "WATCHDOG", "VIP", "BACKEND-ROLE"]}
          rows={t.hosts.map((h) => [
            <span className="font-mono font-bold">{h.host}</span>,
            <span className="font-mono">{h.address}</span>,
            h.roles.join(","),
            <span className="font-mono">{h.ssh_user}</span>,
            h.watchdog,
            h.vip ? <Badge text="yes" sev="WARN" /> : "no",
            <Badge
              text={h.backend_role}
              sev={
                h.backend_role === "primary" ? "INFO"
                : h.backend_role === "UNREACHABLE" ? "CRIT"
                : h.backend_role === "standby" ? "OK" : undefined
              }
            />,
          ])}
        />
      </Card>
      <Card title="watchdog">
        {b.watchdog.available ? (
          <>
            <div className="mb-2 text-sm">
              quorum <b>{b.watchdog.quorum}</b> · leader <b>{b.watchdog.leader}</b> ·{" "}
              {b.watchdog.alive}/{b.watchdog.total} alive
            </div>
            <Table
              cols={["NODE", "HOST", "STATUS", "PRIORITY", "VIP"]}
              rows={b.watchdog.nodes.map((n) => [
                n.node, <span className="font-mono">{n.host}</span>, n.status, n.priority,
                n.vip_present ? <Badge text="yes" sev="WARN" /> : "no",
              ])}
            />
            <div className="mt-3 grid grid-cols-2 gap-2 text-[11px] md:grid-cols-3">
              {Object.entries(b.watchdog.params).map(([k, v]) => (
                <div key={k} className="rounded border border-edge bg-bg px-2 py-1">
                  <span className="text-mut">{k}</span>{" "}
                  <span className="font-mono">{v}</span>
                </div>
              ))}
            </div>
          </>
        ) : (
          <div className="text-mut">watchdog unavailable</div>
        )}
      </Card>
    </div>
  );
}

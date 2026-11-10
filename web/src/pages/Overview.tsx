import { Link } from "react-router-dom";
import { useLive } from "../store";
import { Badge, Card, fmtSecs, timeAgo } from "../ui";
import MiniTopology from "../components/MiniTopology";

function Stat(p: { label: string; value: React.ReactNode; sub?: string; sev?: string }) {
  return (
    <div className="rounded-lg border border-edge bg-panel px-3 py-2">
      <div className="text-[10px] uppercase tracking-wider text-mut">{p.label}</div>
      <div className="mt-0.5 text-lg font-bold leading-tight">{p.value}</div>
      {p.sub && <div className="text-[10px] text-mut">{p.sub}</div>}
      {p.sev && <Badge text={p.sev} sev={p.sev} className="mt-1" />}
    </div>
  );
}

export default function Overview() {
  const live = useLive();
  const b = live.snapshot;
  if (!b) {
    return (
      <div className="flex h-64 items-center justify-center text-mut">
        {live.collectErr
          ? `cluster unreachable: ${live.collectErr.message}`
          : "waiting for first snapshot…"}
      </div>
    );
  }
  const st = b.status;
  const wd = b.watchdog;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Stat label="cluster" value={b.header.cluster} sub={`pgpool ${b.header.pgpool_version}`} />
        <Stat
          label="primary"
          value={b.lag?.primary ?? "-"}
          sub={b.lag?.primary_lsn ?? ""}
        />
        <Stat
          label="pool nodes"
          value={`${st.nodes.filter((n) => n.status === "up").length}/${st.nodes.length} up`}
          sub={`${st.clients} conns served`}
        />
        <Stat
          label="watchdog"
          value={wd.available ? `${wd.alive}/${wd.total}` : "down"}
          sub={wd.available ? `${wd.quorum} · leader ${wd.leader}` : "unavailable"}
        />
        <Stat
          label="pool children"
          value={`${st.children_busy}/${st.children_max}`}
          sub="busy / max"
        />
        <Stat
          label="findings"
          value={
            <span>
              <span className="text-crit">{st.crit}</span>
              <span className="text-mut"> / </span>
              <span className="text-warn">{st.warn}</span>
              <span className="text-mut"> / </span>
              <span className="text-info">{st.info}</span>
            </span>
          }
          sub="crit / warn / info"
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-3">
        <Card title="topology & data flow" className="xl:col-span-2">
          <MiniTopology height={300} />
          <div className="mt-2 flex gap-4 text-[10px] text-mut">
            <span><span className="text-info">—</span> client traffic: VIP → pgpool</span>
            <span><span className="text-ok">⇢</span> WAL: PG primary → standby</span>
            <span><span className="text-mut">··</span> watchdog peers</span>
          </div>
        </Card>

        <div className="space-y-4">
          <Card title="active findings" right={<Link to="/logs" className="text-[10px] text-info">all →</Link>}>
            <div className="max-h-56 space-y-1.5 overflow-auto">
              {b.findings.length === 0 && <div className="text-xs text-ok">cluster healthy — no findings</div>}
              {b.findings.map((f) => (
                <div key={f.ID} className="flex items-start gap-2 text-xs">
                  <Badge text={f.Severity} sev={f.Severity} className="mt-0.5 w-11 shrink-0 text-center" />
                  <div className="min-w-0">
                    <div className="font-mono">{f.ID}</div>
                    <div className="truncate text-mut">{f.Cause}</div>
                  </div>
                </div>
              ))}
            </div>
          </Card>

          <Card title="node scores">
            <div className="space-y-1">
              {st.nodes.map((n) => (
                <div key={n.id} className="flex items-center gap-2 text-xs">
                  <span className="w-5 font-mono text-mut">#{n.id}</span>
                  <span className="w-36 truncate font-mono">{n.host_port}</span>
                  <Badge
                    text={n.real_role}
                    sev={n.real_role === "primary" ? "INFO" : n.real_role === "UNREACHABLE" ? "CRIT" : undefined}
                    className="w-20 text-center"
                  />
                  <Badge
                    text={n.status}
                    sev={n.status === "up" ? "OK" : "CRIT"}
                    className="w-14 text-center"
                  />
                  {n.real_role === "standby" && (
                    <span className="text-mut">delay {fmtSecs(n.delay_sec)}</span>
                  )}
                  <span className="ml-auto font-mono text-mut">score {n.score}</span>
                </div>
              ))}
            </div>
          </Card>
        </div>
      </div>

      <Card title="recent errors" right={<Link to="/logs" className="text-[10px] text-info">logs & errors →</Link>}>
        <div className="max-h-40 space-y-1 overflow-auto font-mono text-[11px]">
          {live.errors.slice(-8).reverse().map((e, i) => (
            <div key={i} className="flex gap-2">
              <span className="text-mut">{timeAgo(e.TS)}</span>
              <Badge text={e.Severity} sev={e.Severity} className="w-11 text-center" />
              <span className="text-mut">{e.Host}</span>
              <span className="truncate">{e.Message}</span>
            </div>
          ))}
          {live.errors.length === 0 && <div className="text-mut">no recent error events</div>}
        </div>
      </Card>
    </div>
  );
}

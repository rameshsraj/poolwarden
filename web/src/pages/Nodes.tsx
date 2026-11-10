import { useState } from "react";
import { useLive } from "../store";
import { Badge, Button, Card, fmtSecs, Table, timeAgo } from "../ui";
import ActionDialog, { ActionSpec } from "../components/ActionDialog";

export default function Nodes() {
  const b = useLive().snapshot;
  const [spec, setSpec] = useState<ActionSpec | null>(null);
  const [sel, setSel] = useState<number | null>(null);

  if (!b) return <div className="text-mut">waiting for snapshot…</div>;
  const nodes = b.nodes.nodes;
  const detail = sel != null ? nodes.find((n) => n.id === sel) : undefined;

  const act = (action: string, id: number, title: string, extra: Record<string, any> = {}) =>
    setSpec({ action, title, body: { id, ...extra } });

  return (
    <div className="space-y-4">
      <Card
        title="pool nodes"
        right={
          <Button small variant="ghost" onClick={() => act("reload", -1, "reload pgpool configuration", {})}>
            reload pgpool
          </Button>
        }
      >
        <Table
          cols={["ID", "HOST:PORT", "STATUS", "PGPOOL-ROLE", "REAL-ROLE", "LB-W", "SELECTS", "DELAY", "REPL", "SCORE", "NOTE", "ACTIONS"]}
          rows={nodes.map((n) => [
            <button className="font-mono font-bold text-info" onClick={() => setSel(n.id)}>{n.id}</button>,
            <span className="font-mono">{n.host_port}</span>,
            <Badge text={n.status} sev={n.status === "up" ? "OK" : "CRIT"} />,
            n.pgpool_role || "-",
            <Badge
              text={n.real_role}
              sev={n.real_role === "primary" ? "INFO" : n.real_role === "UNREACHABLE" ? "CRIT" : "OK"}
            />,
            n.lb_weight.toFixed(2),
            n.selects,
            n.real_role === "standby" ? fmtSecs(n.delay_sec) : "-",
            `${n.repl_state} ${n.sync}`,
            n.score,
            <span className="text-warn">{n.note}</span>,
            <span className="flex gap-1">
              {n.status === "up" && (
                <Button small variant="ghost" onClick={() => act("detach", n.id, `detach node ${n.id}`)}>detach</Button>
              )}
              {n.status === "down" && (
                <>
                  <Button small variant="ghost" onClick={() => act("attach", n.id, `attach node ${n.id}`)}>attach</Button>
                  <Button small variant="ghost" onClick={() => act("recover", n.id, `online recovery of node ${n.id}`)}>recover</Button>
                </>
              )}
              {n.real_role === "standby" && n.status === "up" && (
                <Button small variant="ghost" onClick={() => act("promote", n.id, `switchover: promote node ${n.id}`)}>
                  promote
                </Button>
              )}
            </span>,
          ])}
        />
      </Card>

      {detail && (
        <Card
          title={`node ${detail.id} — ${detail.host_port}`}
          right={<button className="text-mut" onClick={() => setSel(null)}>close ✕</button>}
        >
          <div className="grid grid-cols-2 gap-2 text-xs md:grid-cols-4">
            {[
              ["status", detail.status], ["pgpool role", detail.pgpool_role || "-"],
              ["real role", detail.real_role], ["timeline", detail.timeline ?? "-"],
              ["current lsn", detail.current_lsn ?? "-"], ["receive lsn", detail.receive_lsn ?? "-"],
              ["delay", fmtSecs(detail.delay_sec)], ["repl", `${detail.repl_state} ${detail.sync}`],
              ["lb weight", detail.lb_weight.toFixed(2)], ["selects", detail.selects],
              ["conns", `${Math.max(0, detail.conns)}/${Math.max(0, detail.max_conns)}`],
              ["oldest txn", fmtSecs(detail.oldest_xact_sec)],
              ["conflicts", Math.max(0, detail.conflicts)],
              ["score", detail.score],
              ["last change", timeAgo(detail.last_change)],
              ["note", detail.note || "-"],
            ].map(([k, v]) => (
              <div key={k as string} className="rounded border border-edge bg-bg px-2 py-1.5">
                <div className="text-[10px] uppercase text-mut">{k}</div>
                <div className="font-mono">{v}</div>
              </div>
            ))}
          </div>
          <div className="mt-3">
            {b.nodes.findings
              .filter((f) => f.Subject === detail.host_port.split(":")[0] || f.Subject === `node${detail.id}`)
              .map((f) => (
                <div key={f.ID} className="mb-1 flex gap-2 text-xs">
                  <Badge text={f.Severity} sev={f.Severity} className="w-11 text-center" />
                  <span className="font-mono">{f.ID}</span>
                  <span className="text-mut">{f.Cause}</span>
                </div>
              ))}
          </div>
        </Card>
      )}

      <ActionDialog spec={spec} onClose={() => setSpec(null)} />
    </div>
  );
}

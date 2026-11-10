import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { useLive } from "../store";
import { Badge, Button, Card, Dialog, Table, timeAgo } from "../ui";
import ActionDialog, { type ActionSpec } from "../components/ActionDialog";
import type { Op } from "../types";

const sev: Record<string, string> = {
  success: "OK", running: "INFO", queued: "INFO",
  failed: "CRIT", interrupted: "WARN",
};

export default function Operations() {
  const live = useLive();
  const [sel, setSel] = useState<Op | null>(null);
  const [spec, setSpec] = useState<ActionSpec | null>(null);
  const ops = useQuery({
    queryKey: ["ops"],
    queryFn: api.ops,
    refetchInterval: 5000,
  });

  // live SSE updates fold into the list
  const items = (ops.data?.ops ?? []).map((o) => {
    const l = live.ops[o.id];
    return l ? { ...o, status: (l.status as Op["status"]) ?? o.status } : o;
  });

  return (
    <div className="space-y-4">
      <Card title="services (start / stop / restart — observed state from live snapshots)">
        <Table
          cols={["HOST", "SERVICE", "OBSERVED", "", "", ""]}
          rows={(live.snapshot?.topology.hosts ?? []).flatMap((h) => {
            const wd = live.snapshot?.watchdog.nodes?.find((n) => n.host === h.host);
            const pool = live.snapshot?.status.nodes?.find((n) => n.host_port.split(":")[0] === h.host);
            const rows: any[][] = [];
            const svcRow = (unit: string, observed: string, sevv: string | undefined) => [
              <span className="font-mono font-bold">{h.host}</span>,
              unit,
              <Badge text={observed} sev={sevv} />,
              ...(["start", "stop", "restart"] as const).map((v) => (
                <Button
                  key={v}
                  small
                  variant={v === "stop" ? "danger" : "ghost"}
                  onClick={() =>
                    setSpec({
                      action: "service",
                      title: `${v} ${unit} on ${h.host}`,
                      body: { verb: v, unit, host: h.host },
                      tokenLabel: `"${v}"`,
                    })
                  }
                >
                  {v}
                </Button>
              )),
            ];
            if (h.roles.includes("pgpool")) {
              const st = wd?.status ?? "unknown";
              rows.push(svcRow("pgpool", st,
                st === "LEADER" ? "WARN" : st === "STANDBY" ? "OK" : "CRIT"));
            }
            if (h.roles.includes("pg")) {
              const st = pool?.real_role ?? (pool?.status === "down" ? "unreachable" : "unknown");
              rows.push(svcRow("postgresql", st,
                st === "primary" ? "INFO" : st === "standby" ? "OK" : "CRIT"));
            }
            return rows;
          })}
        />
        <div className="mt-2 text-[10px] text-mut">
          OBSERVED is the live cluster view: watchdog node status for pgpool,
          real PG role/reachability for postgresql. Buttons run
          <span className="font-mono"> poolwarden service &lt;verb&gt; &lt;unit&gt; &lt;host&gt; </span>
          through the CLI control flow — plan, typed confirm, verify, audit.
        </div>
      </Card>
      <Card title="operations (all mutations run through the poolwarden CLI)">
        <Table
          cols={["STARTED", "ACTION", "ARGV", "OPERATOR", "STATUS", "EXIT", ""]}
          rows={items.map((o) => [
            timeAgo(o.started),
            o.action,
            <span className="font-mono text-[11px]">{(o.argv ?? []).join(" ")}</span>,
            o.operator,
            <Badge text={o.status} sev={sev[o.status]} />,
            o.exit_code,
            <Button small variant="ghost" onClick={() => setSel(o)}>detail</Button>,
          ])}
        />
      </Card>

      <Dialog open={!!sel} onClose={() => setSel(null)} title={`operation ${sel?.id ?? ""}`} wide>
        {sel && (
          <div className="space-y-3">
            <div className="flex items-center gap-3 text-sm">
              <Badge text={sel.status} sev={sev[sel.status]} />
              <span className="font-mono text-xs">$ {(sel.argv ?? []).join(" ")}</span>
              <span className="ml-auto text-mut">exit {sel.exit_code}</span>
            </div>
            <div className="max-h-64 space-y-1 overflow-auto rounded border border-edge bg-bg p-2">
              {(sel.events ?? []).map((e: any, i: number) => (
                <div key={i} className="flex gap-2 font-mono text-[11px]">
                  <span className="w-24 shrink-0 text-info">{e.stage}</span>
                  <span className="text-slate-300">
                    {e.step ? `${e.step.name} → ${e.step.result ?? ""}` : e.detail ?? ""}
                  </span>
                </div>
              ))}
            </div>
            {sel.error && <div className="text-xs text-crit">{sel.error}</div>}
            {sel.result?.timeline && (
              <Table
                dense
                cols={["STEP", "DURATION", "RESULT"]}
                rows={sel.result.timeline.map((s: any) => [s.name, `${(s.duration / 1e9).toFixed(1)}s`, s.result])}
              />
            )}
          </div>
        )}
      </Dialog>
      <ActionDialog spec={spec} onClose={() => setSpec(null)} />
    </div>
  );
}

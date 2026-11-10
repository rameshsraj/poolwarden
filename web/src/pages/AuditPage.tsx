import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";
import { Badge, Button, Card, Dialog, Table, timeAgo } from "../ui";

export default function AuditPage() {
  const list = useQuery({ queryKey: ["audit"], queryFn: () => api.view<any>("audit-list"), refetchInterval: 15000 });
  const sessions = useQuery({ queryKey: ["audit-sess"], queryFn: () => api.view<any>("audit-sessions") });
  const verify = useQuery({ queryKey: ["audit-verify"], queryFn: () => api.view<any>("audit-verify") });
  const [sel, setSel] = useState<string | null>(null);
  const detail = useQuery({
    queryKey: ["audit-show", sel],
    queryFn: () => api.view<any>("audit-show", { id: sel! }),
    enabled: !!sel,
  });

  // audit list JSON shape: rows or events — handle both defensively
  const rows: any[] = Array.isArray(list.data) ? list.data : list.data?.rows ?? list.data?.events ?? [];

  return (
    <div className="space-y-4">
      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="audit log (hash-chained)">
          <div className="max-h-[60vh] overflow-auto">
            <Table
              dense
              cols={["TIME", "COMMAND", "OPERATOR", "OUTCOME", "CODE", ""]}
              rows={rows.map((r: any) => [
                timeAgo(r.FinishedAt ?? r.RequestedAt ?? r.ts ?? r.TS),
                <span className="font-mono text-[11px]">{r.Command ?? r.command}</span>,
                r.Operator ?? r.operator ?? "-",
                <Badge
                  text={r.Outcome ?? r.outcome ?? "-"}
                  sev={(r.Outcome ?? r.outcome) === "success" ? "OK" : "CRIT"}
                />,
                r.ExitCode ?? r.exit_code ?? "-",
                <Button small variant="ghost" onClick={() => setSel(r.EventID ?? r.event_id ?? r.ID)}>show</Button>,
              ])}
            />
          </div>
        </Card>
        <div className="space-y-4">
          <Card title="sessions">
            <Table
              dense
              cols={["START", "END", "OPERATOR", "FROM", "KEY-FP", "CMDS"]}
              rows={(Array.isArray(sessions.data) ? sessions.data : sessions.data?.sessions ?? []).map((s: any) => [
                timeAgo(s.Start), timeAgo(s.End), s.Operator, s.From ?? "-",
                <span className="font-mono text-[10px]">{(s.KeyFP ?? "").slice(0, 18)}</span>, s.Commands,
              ])}
            />
          </Card>
          <Card title="chain verification">
            <pre className="overflow-auto text-[11px] text-mut">
              {verify.data ? JSON.stringify(verify.data, null, 2) : "…"}
            </pre>
          </Card>
        </div>
      </div>

      <Dialog open={!!sel} onClose={() => setSel(null)} title={`audit ${sel}`} wide>
        <pre className="max-h-[70vh] overflow-auto text-[11px] text-mut">
          {detail.data ? JSON.stringify(detail.data, null, 2) : "loading…"}
        </pre>
      </Dialog>
    </div>
  );
}

// ActionDialog: plan (real CLI preflight) → typed confirmation → live
// progress streamed from the supervised CLI subprocess.
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { getLive, useLive } from "../store";
import { Badge, Button, Dialog, Input } from "../ui";
import type { Op, PlanResult } from "../types";

export interface ActionSpec {
  action: string;
  title: string;
  body: Record<string, any>; // merged into the POST body (id, flags, ...)
  tokenLabel?: string;       // hint for what to type, e.g. "the node id"
}

export default function ActionDialog(p: {
  spec: ActionSpec | null;
  onClose: () => void;
}) {
  const [plan, setPlan] = useState<PlanResult | null>(null);
  const [planErr, setPlanErr] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [op, setOp] = useState<Op | null>(null);
  const live = useLive();
  const opId = useRef<string | null>(null);

  // reset + run the plan when the dialog opens
  useEffect(() => {
    setPlan(null);
    setPlanErr("");
    setConfirm("");
    setOp(null);
    opId.current = null;
    if (!p.spec) return;
    setLoading(true);
    api
      .plan({ action: p.spec.action, ...p.spec.body })
      .then(setPlan)
      .catch((e) => setPlanErr(e.message))
      .finally(() => setLoading(false));
  }, [p.spec]);

  // live op updates from the SSE op channel
  const cur = opId.current ? live.ops[opId.current] : undefined;
  useEffect(() => {
    if (opId.current && cur?.status && op && op.status !== cur.status) {
      api.op(opId.current).then(setOp).catch(() => {});
    }
  }, [cur?.status]);

  const pf = plan?.result?.preflight;
  const verdict = pf?.Verdict;
  const needsToken = !!(plan?.result?.token && !plan?.result?.yes_no);
  const question = plan?.result?.question;
  const canRun =
    !!plan &&
    plan.exit_code === 0 &&
    !!plan.result &&
    (plan.result.yes_no || !needsToken || confirm.trim() === plan.result.token) &&
    !op;

  const run = async () => {
    if (!p.spec) return;
    setLoading(true);
    try {
      const body: Record<string, any> = { action: p.spec.action, ...p.spec.body };
      if (needsToken) body.confirm = confirm.trim();
      const o = await api.action(body);
      setOp(o);
      opId.current = o.id;
    } catch (e: any) {
      setPlanErr(e.message);
    } finally {
      setLoading(false);
    }
  };

  const evs = op?.events ?? [];
  const liveStatus = opId.current ? getLive().ops[opId.current]?.status : undefined;

  return (
    <Dialog open={!!p.spec} onClose={p.onClose} title={p.spec?.title ?? ""} wide>
      {loading && !plan && <div className="py-8 text-center text-mut">running preflight…</div>}
      {planErr && <div className="mb-3 rounded border border-crit/40 bg-crit/10 p-2 text-sm text-crit">{planErr}</div>}

      {plan && !op && (
        <>
          {verdict && (
            <div className="mb-3 flex items-center gap-2">
              <span className="text-sm text-mut">preflight verdict</span>
              <Badge text={verdict} sev={verdict === "GO" ? "OK" : verdict === "NO-GO" ? "CRIT" : "WARN"} />
            </div>
          )}
          {pf && (
            <div className="mb-3 space-y-1">
              {pf.Checks.map((c, i) => (
                <div key={i} className="flex items-start gap-2 text-xs">
                  <Badge
                    text={c.Result}
                    sev={c.Result === "OK" ? "OK" : c.Result === "NO-GO" ? "CRIT" : "WARN"}
                    className="mt-0.5 w-14 text-center"
                  />
                  <div>
                    <span className="font-mono">{c.Name}</span>
                    {c.Detail && <span className="ml-2 text-mut">{c.Detail}</span>}
                  </div>
                </div>
              ))}
            </div>
          )}
          {plan.result?.plan && (
            <pre className="mb-3 max-h-48 overflow-auto rounded border border-edge bg-bg p-2 text-[11px] text-mut">
              {plan.result.plan}
            </pre>
          )}
          {plan.error && (
            <div className="mb-3 rounded border border-warn/40 bg-warn/10 p-2 text-xs text-warn">{plan.error}</div>
          )}
          <div className="mb-2 font-mono text-[11px] text-mut">
            $ {(plan.argv ?? []).join(" ")}
          </div>
          {needsToken && (
            <div className="mt-4">
              <div className="mb-1 text-sm">
                Type <code className="rounded bg-edge px-1.5 py-0.5 font-mono text-warn">{plan.result?.token}</code>{" "}
                to confirm
              </div>
              <Input value={confirm} onChange={setConfirm} placeholder={question ?? "confirmation token"} />
            </div>
          )}
          {plan.result?.yes_no && (
            <div className="mt-4 text-sm text-mut">This action asks for a plain confirmation.</div>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" onClick={p.onClose}>cancel</Button>
            <Button variant="danger" disabled={!canRun || loading} onClick={run}>
              {loading ? "starting…" : "execute"}
            </Button>
          </div>
        </>
      )}

      {op && (
        <div>
          <div className="mb-3 flex items-center gap-3">
            <Badge
              text={liveStatus ?? op.status}
              sev={
                (liveStatus ?? op.status) === "success"
                  ? "OK"
                  : (liveStatus ?? op.status) === "running" || (liveStatus ?? op.status) === "queued"
                    ? "INFO"
                    : "CRIT"
              }
            />
            <span className="font-mono text-xs text-mut">$ {op.argv.join(" ")}</span>
          </div>
          <div className="max-h-72 space-y-1 overflow-auto rounded border border-edge bg-bg p-2">
            {evs.map((e, i) => (
              <div key={i} className="flex gap-2 font-mono text-[11px]">
                <span className="w-20 shrink-0 text-info">{e.stage}</span>
                <span className="text-slate-300">
                  {e.step ? `${e.step.name} ${e.step.result ?? ""}` : e.detail ?? ""}
                </span>
              </div>
            ))}
            {evs.length === 0 && <div className="text-[11px] text-mut">waiting for progress…</div>}
          </div>
          {op.error && <div className="mt-2 text-xs text-crit">{op.error}</div>}
          {(op.status === "success" || op.status === "failed" || op.status === "interrupted") && (
            <div className="mt-4 flex justify-end">
              <Button onClick={p.onClose}>close</Button>
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}

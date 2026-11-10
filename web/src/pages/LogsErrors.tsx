import { useMemo, useState } from "react";
import { useLive } from "../store";
import { Badge, Card, Input } from "../ui";

// Live error events + raw log tail, both fed by the monitor's follow streams.
export default function LogsErrors() {
  const live = useLive();
  const [tab, setTab] = useState<"logs" | "errors">("logs");
  const [grep, setGrep] = useState("");
  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState<{ logs: typeof live.logs; errors: typeof live.errors } | null>(null);

  const togglePause = () => {
    if (paused) {
      setFrozen(null);
      setPaused(false);
    } else {
      setFrozen({ logs: live.logs, errors: live.errors });
      setPaused(true);
    }
  };

  const logs = frozen?.logs ?? live.logs;
  const errors = frozen?.errors ?? live.errors;

  const flogs = useMemo(
    () => logs.filter((l) => !grep || JSON.stringify(l).toLowerCase().includes(grep.toLowerCase())),
    [logs, grep],
  );
  const ferrs = useMemo(
    () => errors.filter((e) => !grep || JSON.stringify(e).toLowerCase().includes(grep.toLowerCase())),
    [errors, grep],
  );

  return (
    <div className="flex h-full flex-col space-y-3">
      <div className="flex items-center gap-3">
        <div className="flex rounded border border-edge">
          {(["logs", "errors"] as const).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`px-3 py-1 text-xs font-medium ${tab === t ? "bg-info/20 text-info" : "text-mut"}`}
            >
              {t}
            </button>
          ))}
        </div>
        <div className="w-64"><Input value={grep} onChange={setGrep} placeholder="filter…" /></div>
        <button
          onClick={togglePause}
          className={`rounded border px-3 py-1 text-xs ${paused ? "border-warn/50 text-warn" : "border-edge text-mut"}`}
        >
          {paused ? "paused ⏸" : "live"}
        </button>
        <span className="text-[10px] text-mut">
          {tab === "logs" ? flogs.length : ferrs.length} shown · live tail via monitor stream
        </span>
      </div>

      <Card className="min-h-0 flex-1">
        <div className="max-h-[70vh] space-y-0.5 overflow-auto font-mono text-[11px]">
          {tab === "logs" &&
            flogs.slice(-400).map((l, i) => (
              <div key={i} className="flex gap-2 whitespace-nowrap">
                <span className="w-20 shrink-0 text-mut">{new Date(l.TS).toLocaleTimeString()}</span>
                <span className="w-14 shrink-0 text-mut">{l.Host}</span>
                <span className="w-14 shrink-0 text-info">{l.Component}</span>
                <Badge text={l.Severity} sev={l.Severity} className="w-12 shrink-0 text-center" />
                <span className="whitespace-pre-wrap">{l.Message}</span>
              </div>
            ))}
          {tab === "errors" &&
            ferrs.slice(-300).reverse().map((e, i) => (
              <div key={i} className="flex gap-2 whitespace-nowrap">
                <span className="w-20 shrink-0 text-mut">{new Date(e.TS).toLocaleTimeString()}</span>
                <span className="w-14 shrink-0 text-mut">{e.Host}</span>
                <span className="w-16 shrink-0 text-info">{e.Source}</span>
                <Badge text={e.Severity} sev={e.Severity} className="w-12 shrink-0 text-center" />
                <span className="whitespace-pre-wrap">{e.Code} {e.Message}</span>
              </div>
            ))}
          {((tab === "logs" && flogs.length === 0) || (tab === "errors" && ferrs.length === 0)) && (
            <div className="py-8 text-center text-mut">no events yet — the tail follows new entries as they arrive</div>
          )}
        </div>
      </Card>
    </div>
  );
}

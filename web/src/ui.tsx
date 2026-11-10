// Small UI primitives (shadcn-style) keeping the bundle lean.
import { ReactNode } from "react";

export function Card(p: { title?: ReactNode; right?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-lg border border-edge bg-panel ${p.className ?? ""}`}>
      {(p.title || p.right) && (
        <div className="flex items-center justify-between border-b border-edge px-3 py-2">
          <div className="text-xs font-semibold uppercase tracking-wide text-mut">{p.title}</div>
          <div>{p.right}</div>
        </div>
      )}
      <div className="p-3">{p.children}</div>
    </div>
  );
}

const sevColor: Record<string, string> = {
  CRIT: "bg-crit/15 text-crit border-crit/40",
  WARN: "bg-warn/15 text-warn border-warn/40",
  INFO: "bg-info/15 text-info border-info/40",
  OK: "bg-ok/15 text-ok border-ok/40",
  FAIL: "bg-crit/15 text-crit border-crit/40",
};

export function Badge(p: { text: string; sev?: string; className?: string }) {
  const c = sevColor[p.sev ?? ""] ?? "bg-edge/50 text-mut border-edge";
  return (
    <span className={`inline-block rounded border px-1.5 py-0.5 text-[10px] font-bold ${c} ${p.className ?? ""}`}>
      {p.text}
    </span>
  );
}

export function Button(p: {
  children: ReactNode; onClick?: () => void; variant?: "primary" | "danger" | "ghost";
  disabled?: boolean; small?: boolean;
}) {
  const v = p.variant ?? "primary";
  const cls =
    v === "danger"
      ? "bg-crit/20 text-crit border-crit/50 hover:bg-crit/30"
      : v === "ghost"
        ? "bg-transparent text-slate-300 border-edge hover:bg-edge/40"
        : "bg-info/20 text-info border-info/50 hover:bg-info/30";
  return (
    <button
      onClick={p.onClick}
      disabled={p.disabled}
      className={`rounded border font-medium transition disabled:opacity-40 ${
        p.small ? "px-2 py-0.5 text-xs" : "px-3 py-1.5 text-sm"
      } ${cls}`}
    >
      {p.children}
    </button>
  );
}

export function Table(p: { cols: string[]; rows: ReactNode[][]; dense?: boolean }) {
  return (
    <table className="w-full text-left text-xs">
      <thead>
        <tr className="border-b border-edge text-[10px] uppercase tracking-wider text-mut">
          {p.cols.map((c, i) => (
            <th key={i} className={`${p.dense ? "px-2 py-1" : "px-3 py-2"} font-medium`}>{c}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {p.rows.map((r, i) => (
          <tr key={i} className="border-b border-edge/50 hover:bg-edge/20">
            {r.map((c, j) => (
              <td key={j} className={`${p.dense ? "px-2 py-1" : "px-3 py-2"} align-top`}>{c}</td>
            ))}
          </tr>
        ))}
        {p.rows.length === 0 && (
          <tr><td colSpan={p.cols.length} className="px-3 py-6 text-center text-mut">no data</td></tr>
        )}
      </tbody>
    </table>
  );
}

export function Dialog(p: { open: boolean; onClose: () => void; title: string; children: ReactNode; wide?: boolean }) {
  if (!p.open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={p.onClose}>
      <div
        className={`max-h-[90vh] overflow-auto rounded-lg border border-edge bg-panel shadow-2xl ${p.wide ? "w-[900px]" : "w-[560px]"}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-edge px-4 py-3">
          <h3 className="font-semibold">{p.title}</h3>
          <button onClick={p.onClose} className="text-mut hover:text-slate-200">✕</button>
        </div>
        <div className="p-4">{p.children}</div>
      </div>
    </div>
  );
}

export function Input(p: {
  value: string; onChange: (v: string) => void; placeholder?: string; type?: string; className?: string;
}) {
  return (
    <input
      value={p.value}
      type={p.type ?? "text"}
      placeholder={p.placeholder}
      onChange={(e) => p.onChange(e.target.value)}
      className={`w-full rounded border border-edge bg-bg px-2 py-1.5 text-sm outline-none focus:border-info/50 ${p.className ?? ""}`}
    />
  );
}

export function Field(p: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block">
      <div className="mb-1 text-xs font-medium text-mut">{p.label}</div>
      {p.children}
      {p.hint && <div className="mt-1 text-[10px] text-mut/70">{p.hint}</div>}
    </label>
  );
}

export function fmtBytes(n: number): string {
  if (n < 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i ? 1 : 0)}${u[i]}`;
}

export function fmtSecs(s: number): string {
  if (s < 0) return "-";
  if (s < 1) return `${s.toFixed(1)}s`;
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m${Math.round(s % 60)}s`;
  return `${Math.floor(s / 3600)}h${Math.floor((s % 3600) / 60)}m`;
}

export function timeAgo(ts?: string | number): string {
  if (!ts) return "-";
  const t = typeof ts === "string" ? new Date(ts).getTime() : ts;
  const d = Math.max(0, Date.now() - t);
  if (d < 2000) return "now";
  if (d < 60000) return `${Math.floor(d / 1000)}s ago`;
  if (d < 3600000) return `${Math.floor(d / 60000)}m ago`;
  return `${Math.floor(d / 3600000)}h ago`;
}

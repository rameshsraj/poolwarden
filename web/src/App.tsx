import { useEffect, useState } from "react";
import { HashRouter, NavLink, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { api, setCSRF } from "./api";
import { startStream, useLive } from "./store";
import { Badge, timeAgo } from "./ui";
import Login from "./pages/Login";
import Overview from "./pages/Overview";
import TopologyPage from "./pages/TopologyPage";
import Replication from "./pages/Replication";
import Nodes from "./pages/Nodes";
import PoolsPage from "./pages/PoolsPage";
import LogsErrors from "./pages/LogsErrors";
import Operations from "./pages/Operations";
import Configuration from "./pages/Configuration";
import AuditPage from "./pages/AuditPage";

const NAV = [
  ["", "Overview"],
  ["topology", "Topology"],
  ["replication", "Replication"],
  ["nodes", "Nodes"],
  ["pools", "Pools & Connections"],
  ["logs", "Logs & Errors"],
  ["ops", "Operations"],
  ["config", "Configuration"],
  ["audit", "Audit"],
];

function Shell() {
  const live = useLive();
  const loc = useLocation();
  const snap = live.snapshot;
  const hdr = snap?.header;
  const connColor =
    live.conn === "live" ? "text-ok" : live.conn === "stale" ? "text-warn" : "text-crit";
  return (
    <div className="flex h-screen">
      <aside className="flex w-44 shrink-0 flex-col border-r border-edge bg-panel">
        <div className="border-b border-edge px-3 py-3">
          <div className="font-mono text-sm font-bold text-info">poolwarden</div>
          <div className="text-[10px] uppercase tracking-wider text-mut">console</div>
        </div>
        <nav className="flex-1 space-y-0.5 overflow-auto p-2">
          {NAV.map(([to, label]) => (
            <NavLink
              key={to}
              to={"/" + to}
              end={to === ""}
              className={({ isActive }) =>
                `block rounded px-2 py-1.5 text-xs font-medium ${
                  isActive ? "bg-info/15 text-info" : "text-slate-400 hover:bg-edge/40 hover:text-slate-200"
                }`
              }
            >
              {label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-edge p-2 text-[10px] text-mut">
          {hdr && <div>cluster {hdr.cluster}</div>}
          <div className={connColor}>● {live.conn}</div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-4 border-b border-edge bg-panel px-4 py-2 text-xs">
          <span className={`font-bold ${connColor}`}>
            {live.conn === "live" ? "LIVE" : live.conn.toUpperCase()}
          </span>
          {hdr && (
            <>
              <span className="text-mut">pgpool {hdr.pgpool_version}</span>
              <span className="text-mut">
                PG primary <b className="text-info">{snap?.status.nodes.find((n) => n.real_role === "primary")?.host_port ?? "-"}</b>
              </span>
              <span className="text-mut">VIP <b className="text-slate-300">{hdr.vip_host || "-"}</b></span>
              <span className="text-mut">
                watchdog <b className="text-slate-300">{snap?.watchdog?.available ? `${snap.watchdog.alive}/${snap.watchdog.total} ${snap.watchdog.quorum}` : "unavailable"}</b>
              </span>
              {hdr.degraded && <Badge text="DEGRADED" sev="WARN" />}
              <span className="ml-auto text-mut" title={hdr.collected}>
                last collect {timeAgo(hdr.collected)} · {hdr.duration_ms}ms
              </span>
            </>
          )}
          {live.collectErr && (
            <span className="text-crit">{live.collectErr.message}</span>
          )}
          <span className="text-mut/60">{loc.pathname}</span>
        </header>
        <main className="min-h-0 flex-1 overflow-auto p-4">
          <Routes>
            <Route path="/" element={<Overview />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/replication" element={<Replication />} />
            <Route path="/nodes" element={<Nodes />} />
            <Route path="/pools" element={<PoolsPage />} />
            <Route path="/logs" element={<LogsErrors />} />
            <Route path="/ops" element={<Operations />} />
            <Route path="/config" element={<Configuration />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}

export default function App() {
  const [auth, setAuth] = useState<"checking" | "ok" | "login">("checking");
  useEffect(() => {
    // Session probe first: if the cookie is valid, (re)issue CSRF and start
    // the live stream; otherwise route to the login screen. EventSource
    // failures can't redirect on their own (no status code is exposed).
    setCSRF(getCookie("pwcsrf") ?? "");
    fetch("/api/me", { credentials: "same-origin" })
      .then(async (r) => {
        if (r.ok) {
          const me = await r.json();
          if (me.csrf) setCSRF(me.csrf);
          startStream();
          setAuth("ok");
        } else {
          setAuth("login");
        }
      })
      .catch(() => setAuth("login"));
  }, []);
  if (auth === "checking") return null;
  return (
    <HashRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/*" element={auth === "ok" ? <Shell /> : <Navigate to="/login" replace />} />
      </Routes>
    </HashRouter>
  );
}

function getCookie(name: string): string | undefined {
  return document.cookie
    .split("; ")
    .find((c) => c.startsWith(name + "="))
    ?.split("=")[1];
}

export { api };

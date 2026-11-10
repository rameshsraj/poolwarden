// Shared live state fed by /api/stream (SSE). One EventSource drives all
// widgets; React reads via useSyncExternalStore.
import { useSyncExternalStore } from "react";
import type { CollectError, ErrorEvent, LogLine, MonitorBundle } from "./types";

export type ConnState = "live" | "connecting" | "stale" | "down";

interface LiveState {
  conn: ConnState;
  monitorUp: boolean;
  snapshot?: MonitorBundle;
  collectErr?: CollectError;
  lastSeq: number;
  lastAt?: number;
  errors: ErrorEvent[]; // recent error events (ring)
  logs: LogLine[];      // recent log lines (ring)
  ops: Record<string, { status: string; last?: any }>;
}

const state: LiveState = {
  conn: "connecting",
  monitorUp: false,
  lastSeq: 0,
  errors: [],
  logs: [],
  ops: {},
};

const listeners = new Set<() => void>();
let version = 0;

function notify() {
  version++;
  listeners.forEach((l) => l());
}

function set(patch: Partial<LiveState>) {
  Object.assign(state, patch);
  notify();
}

let es: EventSource | null = null;
let staleTimer: ReturnType<typeof setInterval> | null = null;

export function startStream() {
  if (es) return;
  es = new EventSource("/api/stream");

  es.addEventListener("snapshot", (e) => {
    const env = JSON.parse((e as MessageEvent).data);
    set({
      snapshot: env.data as MonitorBundle,
      collectErr: undefined,
      conn: "live",
      monitorUp: true,
      lastSeq: env.seq,
      lastAt: Date.now(),
    });
  });
  es.addEventListener("collect_error", (e) => {
    const env = JSON.parse((e as MessageEvent).data);
    set({
      collectErr: env.data as CollectError,
      conn: "live",
      monitorUp: true,
      lastSeq: env.seq,
      lastAt: Date.now(),
    });
  });
  es.addEventListener("error_event", (e) => {
    const env = JSON.parse((e as MessageEvent).data);
    state.errors = [...state.errors.slice(-199), env.data as ErrorEvent];
    set({ errors: state.errors, lastAt: Date.now() });
  });
  es.addEventListener("log", (e) => {
    const env = JSON.parse((e as MessageEvent).data);
    state.logs = [...state.logs.slice(-499), env.data as LogLine];
    set({ logs: state.logs, lastAt: Date.now() });
  });
  es.addEventListener("op", (e) => {
    const env = JSON.parse((e as MessageEvent).data);
    state.ops = { ...state.ops, [env.op_id]: { status: env.status, last: env } };
    set({ ops: state.ops });
  });
  es.addEventListener("monitor", (e) => {
    const d = JSON.parse((e as MessageEvent).data);
    if (d.state === "down") set({ monitorUp: false, conn: "down" });
  });
  es.addEventListener("hello", () => set({ monitorUp: true }));
  es.onerror = () => {
    if (state.conn === "live") set({ conn: "stale" });
  };

  staleTimer = setInterval(() => {
    if (state.conn === "live" && state.lastAt && Date.now() - state.lastAt > 8000) {
      set({ conn: "stale" });
    }
    // a persistently-dead stream usually means the session expired — probe
    // /api/me and send the user back to login
    if (state.conn !== "live" && state.lastAt && Date.now() - state.lastAt > 30000) {
      fetch("/api/me", { credentials: "same-origin" }).then((r) => {
        if (r.status === 401) {
          stopStream();
          window.location.hash = "#/login";
          window.location.reload();
        }
      }).catch(() => {});
    }
  }, 2000);
}

export function stopStream() {
  es?.close();
  es = null;
  if (staleTimer) clearInterval(staleTimer);
  staleTimer = null;
}

export function useLive(): LiveState {
  useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
    () => version,
    () => version,
  );
  return state;
}

export function getLive(): LiveState {
  return state;
}

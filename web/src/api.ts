// Thin REST/SSE client for the poolwconsole gateway.
import type { Op, PlanResult } from "./types";

let csrf = "";

export function setCSRF(t: string) {
  csrf = t;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(csrf ? { "X-CSRF": csrf } : {}),
    },
    ...init,
  });
  if (r.status === 401) {
    window.location.hash = "#/login";
    throw new Error("unauthenticated");
  }
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
  return body as T;
}

export const api = {
  login: (token: string, operator: string) =>
    req<{ operator: string; csrf: string }>("/api/login", {
      method: "POST",
      body: JSON.stringify({ token, operator }),
    }),
  logout: () => req("/api/logout", { method: "POST" }),
  view: <T = any>(name: string, params?: Record<string, string>) => {
    const qs = params ? "?" + new URLSearchParams(params).toString() : "";
    return req<T>(`/api/view/${name}${qs}`);
  },
  plan: (body: object) => req<PlanResult>("/api/plan", { method: "POST", body: JSON.stringify(body) }),
  action: (body: object) => req<Op>("/api/action", { method: "POST", body: JSON.stringify(body) }),
  ops: () => req<{ ops: Op[] }>("/api/ops"),
  op: (id: string) => req<Op>(`/api/ops/${id}`),
  connection: () => req<{ exists: boolean; yaml: string }>("/api/connection"),
  testConnection: (yaml: string) =>
    req("/api/connection/test", { method: "POST", body: JSON.stringify({ yaml }) }),
  installConnection: (yaml: string) =>
    req("/api/connection/install", { method: "POST", body: JSON.stringify({ yaml }) }),
};

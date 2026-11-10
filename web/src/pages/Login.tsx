import { useState } from "react";
import { api, setCSRF } from "../api";
import { startStream, stopStream } from "../store";
import { Button, Field, Input } from "../ui";

export default function Login() {
  const [token, setToken] = useState("");
  const [operator, setOperator] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const r = await api.login(token, operator);
      setCSRF(r.csrf);
      stopStream(); // drop any stale-cookie EventSource before reopening
      startStream();
      // App's auth state was resolved before login — reload so the session
      // probe re-runs and lands on the shell.
      window.location.hash = "#/";
      window.location.reload();
    } catch (e2: any) {
      setErr(e2.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-screen items-center justify-center">
      <form onSubmit={submit} className="w-80 space-y-4 rounded-lg border border-edge bg-panel p-6">
        <div>
          <div className="font-mono text-lg font-bold text-info">poolwarden console</div>
          <div className="mt-1 text-xs text-mut">sign in with the console access token</div>
        </div>
        <Field label="access token">
          <Input type="password" value={token} onChange={setToken} placeholder="PWCONSOLE_TOKEN" />
        </Field>
        <Field label="operator name" hint="recorded in the poolwarden audit log">
          <Input value={operator} onChange={setOperator} placeholder="jane.dba" />
        </Field>
        {err && <div className="text-xs text-crit">{err}</div>}
        <Button disabled={busy || !token}>{busy ? "…" : "sign in"}</Button>
      </form>
    </div>
  );
}

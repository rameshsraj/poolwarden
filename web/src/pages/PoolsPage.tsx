import { useLive } from "../store";
import { Badge, Card, Table } from "../ui";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

export default function PoolsPage() {
  const b = useLive().snapshot;
  // procs are not in the monitor bundle — pull on demand through the CLI.
  const procs = useQuery({
    queryKey: ["procs"],
    queryFn: () => api.view<{ procs: any[] }>("procs"),
    refetchInterval: 10000,
  });

  if (!b) return <div className="text-mut">waiting for snapshot…</div>;
  const pools = b.pools;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Card title="pool children">
          <div className="text-lg font-bold">{pools.children_busy}/{pools.children_max}</div>
          <div className="text-xs text-mut">busy / num_init_children</div>
        </Card>
        <Card title="max_pool">
          <div className="text-lg font-bold">{pools.pool_max || "-"}</div>
          <div className="text-xs text-mut">backend conns per child</div>
        </Card>
        <Card title="client conns served">
          <div className="text-lg font-bold">{pools.clients}</div>
          <div className="text-xs text-mut">lifetime (cumulative)</div>
        </Card>
        <Card title="pool groups">
          <div className="text-lg font-bold">{(pools.groups ?? []).length}</div>
          <div className="text-xs text-mut">database+user</div>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="pools (SHOW POOL_POOLS)">
          <Table
            dense
            cols={["DATABASE", "USER", "BACKENDS", "POOLS", "CONNECTED", "LB-NODES"]}
            rows={(pools.groups ?? []).map((g) => [
              <span className="font-mono">{g.database}</span>,
              <span className="font-mono">{g.user}</span>,
              g.backends, g.pools, g.connected, g.load_balance_nodes,
            ])}
          />
        </Card>
        <Card title="connections (pgpool + direct)">
          <Table
            dense
            cols={["WHERE", "DATABASE", "USER", "APPLICATION", "STATE", "CONNS", "LIMIT"]}
            rows={(b.conns?.conns ?? []).map((c) => [
              <span className="font-mono">{c.where}</span>,
              c.database, c.user, c.application,
              <Badge
                text={c.state}
                sev={c.state === "active" ? "INFO" : c.state === "idle in transaction" ? "WARN" : undefined}
              />,
              c.conns, c.limit,
            ])}
          />
          <div className="mt-1 text-[10px] text-mut">
            STATE is pg_stat_activity.state per backend: active = running a query,
            idle = live connection doing nothing, "idle in transaction" = holds an open
            txn (watch out). pgpool-side rows are pooled backend conns ("-").
          </div>
        </Card>
      </div>

      <Card title="processes (SHOW POOL_PROCESSES)" >
        <Table
          dense
          cols={["PID", "START", "CLIENT-CONNS", "DATABASE", "USER", "BACKEND-CONN", "COUNTER", "STATUS"]}
          rows={(procs.data?.procs ?? []).map((p: any) => [
            p.pid, p.start_time, p.client_conns, p.database, p.user,
            p.backend_conn_time, p.pool_counter, p.status,
          ])}
        />
      </Card>
    </div>
  );
}

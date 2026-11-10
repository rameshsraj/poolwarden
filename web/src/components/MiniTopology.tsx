// Cluster topology + data flow built from the live snapshot.
// Edges: clients→VIP→pgpool (client traffic, configured/inferred),
// primary→standby (observed WAL replication), pgpool↔pgpool (watchdog).
import { useMemo } from "react";
import ReactFlow, {
  Background,
  Edge,
  Handle,
  MarkerType,
  Node,
  NodeProps,
  Position,
} from "reactflow";
import "reactflow/dist/style.css";
import { useLive } from "../store";
import { fmtBytes } from "../ui";
import { Badge } from "../ui";

const roleColor: Record<string, string> = {
  primary: "#58a6ff",
  standby: "#3fb950",
  UNREACHABLE: "#f85149",
  "?": "#8b98a9",
};

function ClusterNode({ data }: NodeProps) {
  const c = roleColor[data.role] ?? roleColor["?"];
  return (
    <div
      className="rounded-md border bg-panel px-3 py-2 text-xs shadow-lg"
      style={{ borderColor: c, minWidth: 170 }}
    >
      <Handle type="target" position={Position.Left} style={{ opacity: 0 }} />
      <Handle type="source" position={Position.Right} style={{ opacity: 0 }} />
      <Handle type="target" position={Position.Top} style={{ opacity: 0 }} />
      <Handle type="source" position={Position.Bottom} style={{ opacity: 0 }} />
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono font-bold">{data.label}</span>
        {data.vip && <span className="rounded bg-warn/20 px-1 text-[9px] font-bold text-warn">VIP</span>}
      </div>
      <div className="mt-1.5 space-y-1">
        {data.pg && (
          <div className="flex items-center gap-1.5">
            <span className="w-7 text-[9px] uppercase text-mut">PG</span>
            <Badge
              text={data.role === "primary" ? "PRIMARY" : data.role === "standby" ? "STANDBY" : data.role}
              sev={data.role === "primary" ? "INFO" : data.role === "UNREACHABLE" ? "CRIT" : "OK"}
            />
          </div>
        )}
        {data.pgpool && (
          <div className="flex items-center gap-1.5">
            <span className="w-7 text-[9px] uppercase text-mut">WD</span>
            <Badge
              text={data.wdLeader ? "LEADER" : data.wd ?? "-"}
              sev={data.wdLeader ? "WARN" : undefined}
            />
            {data.status && <span className="text-[10px] text-mut">pool {data.status}</span>}
          </div>
        )}
      </div>
      {data.detail && <div className="mt-1.5 max-w-[190px] truncate text-[10px] text-mut" title={data.detail}>{data.detail}</div>}
    </div>
  );
}

const nodeTypes = { cluster: ClusterNode };

export default function MiniTopology(p: { height?: number }) {
  const live = useLive();
  const b = live.snapshot;

  const { nodes, edges } = useMemo(() => {
    const nodes: Node[] = [];
    const edges: Edge[] = [];
    if (!b) return { nodes, edges };

    const topo = b.topology.hosts;
    const poolByHost = new Map(b.status.nodes.map((n) => [n.host_port.split(":")[0], n]));
    const wdByHost = new Map(b.watchdog.nodes.map((w) => [w.host, w]));
    const vipHost = b.header.vip_host && b.header.vip_host !== "none" ? b.header.vip_host : "";

    // clients + VIP pseudo-nodes on the left, hosts in a column on the right
    const n = topo.length;
    const midY = ((n - 1) * 150) / 2;
    nodes.push({
      id: "clients", type: "cluster", position: { x: 0, y: midY - 60 },
      data: { label: "clients", role: "?", detail: "app connections" },
    });
    if (b.header.vip_host) {
      nodes.push({
        id: "vip", type: "cluster", position: { x: 0, y: midY + 60 },
        data: { label: "VIP", role: "?", detail: b.header.vip_host === "none" ? "not present" : `on ${vipHost}` },
      });
    }

    topo.forEach((h, i) => {
      const y = i * 150;
      const pool = poolByHost.get(h.host) ?? poolByHost.get(h.address);
      const wd = wdByHost.get(h.host);
      const role = h.backend_role !== "-" ? h.backend_role : pool?.real_role ?? "?";
      const hasVip = h.vip || vipHost === h.host;
      const wdLeader = b.watchdog.leader === h.host;
      nodes.push({
        id: h.host, type: "cluster",
        position: { x: 430, y },
        data: {
          label: h.host,
          role,
          pg: h.roles.includes("pg"),
          pgpool: h.roles.includes("pgpool"),
          vip: hasVip,
          status: pool?.status,
          wd: wd?.status ?? h.watchdog,
          wdLeader,
          detail: `${h.address} · ${pool?.conns ?? "?"}/${pool?.max_conns ?? "?"} conns`,
        },
      });
      // client traffic: clients → VIP → pgpool host holding the VIP
      if (hasVip && h.roles.includes("pgpool")) {
        const qps = b.rates?.nodes ? Object.values(b.rates.nodes).reduce((a, x) => a + x, 0) : -1;
        edges.push({
          id: `e-vip-${h.host}`, source: "vip", target: h.host,
          style: { stroke: "#58a6ff", strokeWidth: 1.5 },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
          label: `client traffic${qps >= 0 ? ` · ${qps.toFixed(1)} qps` : ""} · ${b.pools.children_busy}/${b.pools.children_max} children`,
          labelStyle: { fill: "#8b98a9", fontSize: 9 },
        });
      } else if (h.roles.includes("pgpool") && !b.header.vip_host) {
        edges.push({
          id: `e-cli-${h.host}`, source: "clients", target: h.host,
          style: { stroke: "#58a6ff", strokeWidth: 1, strokeDasharray: "4 4" },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
        });
      }
      // watchdog mesh between pgpool hosts (heartbeats/quorum coordination)
      topo.forEach((o) => {
        if (o.host > h.host && o.roles.includes("pgpool") && h.roles.includes("pgpool")) {
          edges.push({
            id: `e-wd-${h.host}-${o.host}`, source: h.host, target: o.host,
            style: { stroke: "#8b98a9", strokeWidth: 1, strokeDasharray: "2 4" },
            label: "watchdog",
            labelStyle: { fill: "#8b98a9", fontSize: 9 },
            labelBgStyle: { fill: "#11161d" },
          });
        }
      });
    });
    edges.push({
      id: "e-cli-vip", source: "clients", target: b.header.vip_host ? "vip" : topo[0]?.host ?? "clients",
      style: { stroke: "#58a6ff", strokeWidth: 1.5 },
      markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
    });

    // WAL replication: primary → standbys (observed pg_stat_replication)
    const lag = b.lag;
    if (lag) {
      const primHost = lag.primary.split(":")[0];
      const primTopo = topo.find((h) => h.host === primHost || h.address === primHost) ??
        topo.find((h) => h.backend_role === "primary");
      for (const s of lag.standbys) {
        const dst = topo.find((h) => h.host === s.standby || h.address === s.standby || h.host === s.standby.split(":")[0]);
        if (primTopo && dst) {
          const color = s.delay_severity === "CRIT" ? "#f85149" : s.delay_severity === "WARN" ? "#d29922" : "#3fb950";
          const rate = b.rates?.standbys?.[s.standby];
          edges.push({
            id: `e-wal-${primTopo.host}-${dst.host}`,
            source: primTopo.host, target: dst.host,
            animated: s.state === "streaming",
            style: { stroke: color, strokeWidth: 2 },
            markerEnd: { type: MarkerType.ArrowClosed, color },
            label: `WAL ${s.state}${rate != null && rate >= 0 ? ` · ${fmtBytes(rate)}/s` : ""}${s.lag_bytes >= 0 ? ` · ${fmtBytes(s.lag_bytes)} lag` : ""}`,
            labelStyle: { fill: color, fontSize: 9 },
            labelBgStyle: { fill: "#11161d" },
          });
        }
      }
    }
    return { nodes, edges };
  }, [b]);

  return (
    <div style={{ height: p.height ?? 320 }} className="rounded border border-edge/60 bg-bg">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.4}
        maxZoom={1.6}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="#1f2937" gap={20} size={1} />
      </ReactFlow>
    </div>
  );
}

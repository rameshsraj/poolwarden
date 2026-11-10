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
      {/* fanned-out handles so every edge gets its own slot — no overlap
          regardless of node/edge count */}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`l-s-${i}`} id={`l-s-${i}`} type="source" position={Position.Left}
          style={{ opacity: 0, top: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`l-t-${i}`} id={`l-t-${i}`} type="target" position={Position.Left}
          style={{ opacity: 0, top: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`r-s-${i}`} id={`r-s-${i}`} type="source" position={Position.Right}
          style={{ opacity: 0, top: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`r-t-${i}`} id={`r-t-${i}`} type="target" position={Position.Right}
          style={{ opacity: 0, top: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`t-s-${i}`} id={`t-s-${i}`} type="source" position={Position.Top}
          style={{ opacity: 0, left: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`t-t-${i}`} id={`t-t-${i}`} type="target" position={Position.Top}
          style={{ opacity: 0, left: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`b-s-${i}`} id={`b-s-${i}`} type="source" position={Position.Bottom}
          style={{ opacity: 0, left: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
      {Array.from({ length: 8 }, (_, i) => (
        <Handle key={`b-t-${i}`} id={`b-t-${i}`} type="target" position={Position.Bottom}
          style={{ opacity: 0, left: `${14 + i * 10}%`, width: 1, height: 1 }} />
      ))}
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

    const topo = b.topology.hosts ?? [];
    const poolByHost = new Map((b.status.nodes ?? []).map((n) => [n.host_port.split(":")[0], n]));
    const wdByHost = new Map((b.watchdog.nodes ?? []).map((w) => [w.host, w]));
    const vipHost = b.header.vip_host && b.header.vip_host !== "none" ? b.header.vip_host : "";

    // Two-tier data-flow layout — every edge family owns its own region:
    //   pgpool row on top (watchdog arcs above it), PG row below (WAL arcs
    //   beneath it), routing edges live in the gap between the two rows.
    const pwHosts = topo.filter((h) => h.roles.includes("pgpool"));
    const pgHosts = topo.filter((h) => h.roles.includes("pg"));
    const X0 = 250, COL = 285, PW_Y = 90, PG_Y = 430;

    // order the PG row to line up under same-named pgpool cards first
    const pgOrder = [
      ...pwHosts.map((h) => h.host).filter((x) => pgHosts.some((g) => g.host === x)),
      ...pgHosts.map((h) => h.host).filter((x) => !pwHosts.some((w) => w.host === x)),
    ];
    const pgX = new Map(pgOrder.map((h, j) => [h, X0 + j * COL]));

    nodes.push({
      id: "clients", type: "cluster", position: { x: 0, y: 150 },
      data: { label: "clients", role: "?", detail: "app connections" },
    });
    if (b.header.vip_host) {
      nodes.push({
        id: "vip", type: "cluster", position: { x: 0, y: 270 },
        data: { label: "VIP", role: "?", detail: b.header.vip_host === "none" ? "not present" : `on ${vipHost}` },
      });
    }
    edges.push({
      id: "e-cli-vip", source: "clients", target: b.header.vip_host ? "vip" : `pw-${pwHosts[0]?.host ?? "clients"}`,
      sourceHandle: "b-s-0", targetHandle: b.header.vip_host ? "t-t-0" : "l-t-0",
      style: { stroke: "#58a6ff", strokeWidth: 1.5 },
      markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
    });

    // ---- pgpool tier ------------------------------------------------------
    const pwX = new Map(pwHosts.map((h, i) => [h.host, X0 + i * COL]));
    let vipPw = "";
    pwHosts.forEach((h) => {
      const wd = wdByHost.get(h.host);
      const hasVip = h.vip || vipHost === h.host;
      const wdLeader = b.watchdog.leader === h.host;
      if (hasVip) vipPw = h.host;
      nodes.push({
        id: `pw-${h.host}`, type: "cluster",
        position: { x: pwX.get(h.host)!, y: PW_Y },
        data: {
          label: `${h.host} · pgpool`,
          role: hasVip ? "primary" : "?",
          pg: false, pgpool: true,
          vip: hasVip,
          status: undefined,
          wd: wd?.status ?? h.watchdog,
          wdLeader,
          detail: `${h.address}`,
        },
      });
      // client traffic: clients → VIP → pgpool host holding the VIP
      if (hasVip) {
        const qps = b.rates?.nodes ? Object.values(b.rates.nodes).reduce((a, x) => a + x, 0) : -1;
        edges.push({
          id: `e-vip-${h.host}`, source: "vip", target: `pw-${h.host}`,
          sourceHandle: "r-s-0", targetHandle: "l-t-0",
          style: { stroke: "#58a6ff", strokeWidth: 1.5 },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
          label: `client traffic${qps >= 0 ? ` · ${qps.toFixed(1)} qps` : ""} · ${b.pools.children_busy}/${b.pools.children_max} children`,
          labelStyle: { fill: "#8b98a9", fontSize: 9 },
        });
      } else if (!b.header.vip_host) {
        edges.push({
          id: `e-cli-${h.host}`, source: "clients", target: `pw-${h.host}`,
          sourceHandle: "r-s-0", targetHandle: "l-t-0",
          style: { stroke: "#58a6ff", strokeWidth: 1, strokeDasharray: "4 4" },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#58a6ff" },
        });
      }
    });
    // watchdog mesh — arcs above the pgpool row, each on its own slot
    {
      let k = 0;
      pwHosts.forEach((h, i) => {
        pwHosts.slice(i + 1).forEach((o) => {
          const slot = k++;
          edges.push({
            id: `e-wd-${h.host}-${o.host}`, source: `pw-${h.host}`, target: `pw-${o.host}`,
            sourceHandle: `t-s-${slot}`, targetHandle: `t-t-${slot}`,
            style: { stroke: "#8b98a9", strokeWidth: 1, strokeDasharray: "2 4" },
            label: "watchdog",
            labelStyle: { fill: "#8b98a9", fontSize: 9 },
            labelBgStyle: { fill: "#11161d" },
          });
        });
      });
    }

    // ---- pg tier ----------------------------------------------------------
    const pgNodeRole = (h: (typeof topo)[number]) => {
      const pool = poolByHost.get(h.host) ?? poolByHost.get(h.address);
      return { pool, role: h.backend_role !== "-" ? h.backend_role : pool?.real_role ?? "?" };
    };
    pgOrder.forEach((host) => {
      const h = pgHosts.find((g) => g.host === host)!;
      const { pool, role } = pgNodeRole(h);
      nodes.push({
        id: `pg-${h.host}`, type: "cluster",
        position: { x: pgX.get(host)!, y: PG_Y },
        data: {
          label: `${h.host} · pg`,
          role,
          pg: true, pgpool: false,
          vip: false,
          status: pool?.status,
          wd: undefined, wdLeader: false,
          detail: `${h.address} · ${pool?.conns ?? "?"}/${pool?.max_conns ?? "?"} conns`,
        },
      });
    });

    // routing edges: VIP-holder pgpool → every backend (writes to primary,
    // reads load-balanced) — they live in the gap between the two rows
    if (vipPw) {
      let k = 0;
      for (const host of pgOrder) {
        const { role } = pgNodeRole(pgHosts.find((g) => g.host === host)!);
        const primary = role === "primary";
        const slot = k++;
        edges.push({
          id: `e-route-${host}`, source: `pw-${vipPw}`, target: `pg-${host}`,
          sourceHandle: `b-s-${slot}`, targetHandle: `t-t-${slot}`,
          style: {
            stroke: primary ? "#58a6ff" : "#8b98a9",
            strokeWidth: primary ? 1.8 : 1,
            strokeDasharray: primary ? undefined : "3 3",
          },
          markerEnd: { type: MarkerType.ArrowClosed, color: primary ? "#58a6ff" : "#8b98a9" },
          label: primary ? "writes" : "reads",
          labelStyle: { fill: primary ? "#58a6ff" : "#8b98a9", fontSize: 8 },
          labelBgStyle: { fill: "#11161d" },
        });
      }
    }

    // WAL replication: primary → standbys — arcs below the PG row
    const lag = b.lag;
    if (lag) {
      // lag rows may name nodes by hostname, address or application_name
      const pgRowFor = (name: string) =>
        pgOrder.find((h) => {
          const g = pgHosts.find((x) => x.host === h);
          return h === name || g?.address === name;
        });
      const primHost = lag.primary.split(":")[0];
      const primPg = pgRowFor(primHost) ??
        pgOrder.find((h) => pgNodeRole(pgHosts.find((g) => g.host === h)!).role === "primary");
      let walIdx = 0;
      for (const s of lag.standbys ?? []) {
        walIdx++;
        const dst = pgRowFor(s.standby.split(":")[0]);
        if (primPg && dst) {
          const color = s.delay_severity === "CRIT" ? "#f85149" : s.delay_severity === "WARN" ? "#d29922" : "#3fb950";
          const rate = b.rates?.standbys?.[s.standby];
          edges.push({
            id: `e-wal-${primPg}-${dst}`,
            source: `pg-${primPg}`, target: `pg-${dst}`,
            sourceHandle: `b-s-${walIdx}`, targetHandle: `b-t-${walIdx}`,
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

  const autoH = Math.max(600, ((b?.topology.hosts?.length ?? 2) + 2) * 170);
  return (
    <div style={{ height: p.height ?? autoH }} className="rounded border border-edge/60 bg-bg">
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

import { useMemo } from "react";
import { useLive } from "../store";
import { Badge, Card, fmtBytes, fmtSecs, Table } from "../ui";
import Chart, { chartTheme } from "../components/Chart";
import type { EChartsOption } from "echarts";

// Replication lag + WAL data flow. Series come from the CLI's history samples
// (built by `lag --history`), which persist across console restarts.
export default function Replication() {
  const live = useLive();
  const b = live.snapshot;
  const lag = b?.lag;

  const chartOpt = useMemo<EChartsOption | null>(() => {
    if (!lag || !lag.standbys || lag.standbys.length === 0) return null;
    return {
      ...chartTheme,
      tooltip: { trigger: "axis", textStyle: { fontSize: 10 } },
      legend: { top: 0, textStyle: { color: "#8b98a9", fontSize: 10 } },
      xAxis: { type: "category", data: lag.standbys.map((s) => s.standby), axisLabel: { color: "#8b98a9" } },
      yAxis: { type: "value", name: "lag (s)", nameTextStyle: { color: "#8b98a9" }, axisLabel: { color: "#8b98a9" }, splitLine: { lineStyle: { color: "#1f2937" } } },
      series: [
        { name: "write", type: "bar", stack: "lag", data: lag.standbys.map((s) => Math.max(0, s.write_lag_sec)), itemStyle: { color: "#58a6ff" } },
        { name: "flush", type: "bar", stack: "lag", data: lag.standbys.map((s) => Math.max(0, s.flush_lag_sec)), itemStyle: { color: "#d29922" } },
        { name: "replay", type: "bar", stack: "lag", data: lag.standbys.map((s) => Math.max(0, s.replay_lag_sec)), itemStyle: { color: "#3fb950" } },
        { name: "delay", type: "bar", data: lag.standbys.map((s) => Math.max(0, s.delay_sec)), itemStyle: { color: "#a371f7" } },
      ],
    };
  }, [lag]);

  if (!b) return <div className="text-mut">waiting for snapshot…</div>;
  if (!lag) {
    return (
      <Card title="replication">
        <div className="text-mut">{b.lag_err ?? "no reachable primary"}</div>
      </Card>
    );
  }
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Card title="primary" className="md:col-span-2">
          <div className="font-mono text-lg font-bold text-info">{lag.primary}</div>
          <div className="mt-1 font-mono text-xs text-mut">wal {lag.primary_lsn}</div>
        </Card>
        <Card title="standbys">
          <div className="text-lg font-bold">{lag.standbys.length}</div>
          <div className="text-xs text-mut">
            {lag.standbys.filter((s) => s.state === "streaming").length} streaming
          </div>
        </Card>
        <Card title="worst delay">
          <div className="text-lg font-bold">
            {fmtSecs(Math.max(-1, ...lag.standbys.map((s) => s.delay_sec)))}
          </div>
          <div className="text-xs text-mut">replay delay</div>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card title="lag breakdown (write / flush / replay)">
          {chartOpt ? <Chart option={chartOpt} height={240} /> : <div className="text-mut">no standbys</div>}
        </Card>
        <Card title="replication slots">
          <Table
            dense
            cols={["SLOT", "TYPE", "ACTIVE", "WAL", "RETAINED", "ETA-DISK-FULL"]}
            rows={(b.slots?.slots ?? []).map((s) => [
              <span className="font-mono">{s.name}</span>,
              s.type,
              s.active ? <Badge text="yes" sev="OK" /> : <Badge text="no" sev="WARN" />,
              s.wal_status,
              fmtBytes(s.retained_bytes),
              s.eta_sec >= 0 ? fmtSecs(s.eta_sec) : "-",
            ])}
          />
        </Card>
      </div>

      <Card title="per-standby replication detail">
        <Table
          cols={["STANDBY", "STATE", "SYNC", "LAG", "WRITE", "FLUSH", "REPLAY", "DELAY", "TREND", "ETA→CRIT", "SENT-LSN", "REPLAY-LSN"]}
          rows={lag.standbys.map((s) => [
            <span className="font-mono font-bold">{s.standby}</span>,
            <Badge text={s.state} sev={s.state === "streaming" ? "OK" : "WARN"} />,
            s.sync,
            s.lag_bytes >= 0 ? fmtBytes(s.lag_bytes) : "-",
            fmtSecs(s.write_lag_sec),
            fmtSecs(s.flush_lag_sec),
            fmtSecs(s.replay_lag_sec),
            <Badge text={fmtSecs(s.delay_sec)} sev={s.delay_severity} />,
            s.trend,
            s.eta_sec >= 0 ? fmtSecs(s.eta_sec) : "-",
            <span className="font-mono text-[10px]">{s.sent_lsn ?? "-"}</span>,
            <span className="font-mono text-[10px]">{s.replay_lsn ?? "-"}</span>,
          ])}
        />
      </Card>

      <Card title="health checks">
        <Table
          dense
          cols={["NODE", "HOST", "PGPOOL", "PG ROLE", "PG REPL", "TOTAL", "FAIL", "SKIP", "RETRY", "AVG ms", "MAX ms", "LAST-FAIL", "ΔFAIL"]}
          rows={(b.health?.nodes ?? []).map((h) => {
            const pg = (b.status.nodes ?? []).find((n) => n.id === h.node);
            return [
              h.node, <span className="font-mono">{h.host}</span>,
              <Badge text={h.status} sev={h.status === "up" ? "OK" : "CRIT"} />,
              pg
                ? <Badge text={pg.real_role} sev={pg.real_role === "primary" ? "INFO" : pg.real_role === "UNREACHABLE" ? "CRIT" : "OK"} />
                : "-",
              pg ? `${pg.repl_state}${pg.sync !== "-" ? `/${pg.sync}` : ""}` : "-",
              h.total, h.fail, h.skip, h.retry, h.avg_ms.toFixed(1), h.max_ms,
              h.last_fail, h.delta_fail != null ? `+${h.delta_fail}` : "-",
            ];
          })}
        />
        <div className="mt-1 text-[10px] text-mut">
          PGPOOL = pgpool's health-check verdict · PG ROLE / PG REPL = the actual PostgreSQL state (pg_is_in_recovery, pg_stat_wal_receiver / pg_stat_replication)
        </div>
      </Card>
    </div>
  );
}

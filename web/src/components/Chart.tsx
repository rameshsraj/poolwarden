import { useEffect, useRef } from "react";
import * as echarts from "echarts";

// Thin ECharts wrapper: builds/updates one chart instance per option object.
export default function Chart(p: { option: echarts.EChartsOption; height?: number; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const chart = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    chart.current = echarts.init(ref.current, undefined, { renderer: "canvas" });
    const ro = new ResizeObserver(() => chart.current?.resize());
    ro.observe(ref.current);
    return () => {
      ro.disconnect();
      chart.current?.dispose();
      chart.current = null;
    };
  }, []);

  useEffect(() => {
    chart.current?.setOption(p.option, { notMerge: true });
  }, [p.option]);

  return <div ref={ref} style={{ height: p.height ?? 220 }} className={p.className} />;
}

export const chartTheme = {
  text: { color: "#8b98a9", fontSize: 10 },
  grid: { left: 44, right: 12, top: 24, bottom: 22 },
};

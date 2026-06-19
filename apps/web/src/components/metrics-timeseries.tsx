"use client";

import { useEffect, useRef, useState } from "react";

import type { PodMetrics } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Sparkline } from "@/components/sparkline";

// A single retained sample of the aggregate CPU/memory readings. We only ever
// keep values the backend actually reported — there is NO interpolation or
// synthetic fill (invariant #6). When metrics are unavailable we keep nothing.
export interface MetricSample {
  /** Wall-clock time the sample was observed in the client, ms epoch. */
  t: number;
  cpuMillicores: number;
  memoryBytes: number;
}

// How many samples to retain in the rolling window. At the parent's 10s poll
// cadence this is ~5 minutes of trend, enough to read direction without
// pretending to be a long-term metrics store (that's Prometheus' job).
const MAX_SAMPLES = 30;

// Minimum points before we draw a line. With a single point a "trend" is
// meaningless, so we show the live number and wait for the next poll.
const MIN_POINTS_FOR_CHART = 2;

interface MetricsTimeseriesProps {
  /**
   * The latest live snapshot from the parent's polling hook. The parent is
   * expected to hand us a NEW object reference on every successful poll (which
   * `useResource` does via `setData`); we dedupe on that reference so a plain
   * re-render never double-counts a sample.
   */
  snapshot: PodMetrics;
  formatMillicores: (m: number) => string;
  formatBytes: (b: number) => string;
}

/**
 * Renders a short, client-side rolling time-series of aggregate pod CPU/memory
 * as small line charts (reusing {@link Sparkline}), so users see a trend rather
 * than a single point-in-time number.
 *
 * The window lives only in this component's state — it is intentionally NOT
 * persisted and resets when the component unmounts or when metrics become
 * unavailable. We never fabricate points: each entry is a value the metrics
 * backend reported (invariant #6).
 */
export function MetricsTimeseries({
  snapshot,
  formatMillicores,
  formatBytes,
}: MetricsTimeseriesProps) {
  const [samples, setSamples] = useState<MetricSample[]>([]);

  // Track the last snapshot object we recorded so re-renders that don't carry a
  // fresh poll result are ignored. Using reference identity (not value) keeps
  // genuinely-equal back-to-back readings as distinct, honest samples.
  const lastSnapshotRef = useRef<PodMetrics | null>(null);

  useEffect(() => {
    // Honest gap: while metrics are unavailable we record nothing and drop any
    // retained window, so the chart can't imply continuity across an outage.
    if (!snapshot.available) {
      lastSnapshotRef.current = null;
      setSamples((prev) => (prev.length === 0 ? prev : []));
      return;
    }

    // Only append once per distinct poll result.
    if (lastSnapshotRef.current === snapshot) return;
    lastSnapshotRef.current = snapshot;

    const sample: MetricSample = {
      t: Date.now(),
      cpuMillicores: snapshot.cpuMillicores,
      memoryBytes: snapshot.memoryBytes,
    };

    setSamples((prev) => {
      const next = [...prev, sample];
      return next.length > MAX_SAMPLES ? next.slice(next.length - MAX_SAMPLES) : next;
    });
  }, [snapshot]);

  const cpuSeries = samples.map((s) => s.cpuMillicores);
  const memSeries = samples.map((s) => s.memoryBytes);
  const haveTrend = samples.length >= MIN_POINTS_FOR_CHART;

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <TrendCard
        label="CPU (aggregate)"
        value={formatMillicores(snapshot.cpuMillicores)}
        series={cpuSeries}
        haveTrend={haveTrend}
        stroke="hsl(var(--primary))"
        peakLabel={
          cpuSeries.length > 0
            ? `peak ${formatMillicores(Math.max(...cpuSeries))}`
            : undefined
        }
      />
      <TrendCard
        label="Memory (aggregate)"
        value={formatBytes(snapshot.memoryBytes)}
        series={memSeries}
        haveTrend={haveTrend}
        stroke="hsl(var(--info))"
        peakLabel={
          memSeries.length > 0
            ? `peak ${formatBytes(Math.max(...memSeries))}`
            : undefined
        }
      />
    </div>
  );
}

interface TrendCardProps {
  label: string;
  value: string;
  series: number[];
  haveTrend: boolean;
  stroke: string;
  peakLabel?: string;
}

function TrendCard({
  label,
  value,
  series,
  haveTrend,
  stroke,
  peakLabel,
}: TrendCardProps) {
  return (
    <Card className="flex flex-col gap-3 p-5">
      <div className="flex items-baseline justify-between gap-2">
        <p className="text-sm text-muted-foreground">{label}</p>
        <p className="text-2xl font-semibold tracking-tight tabular-nums">
          {value}
        </p>
      </div>
      {haveTrend ? (
        <Sparkline data={series} stroke={stroke} height={48} />
      ) : (
        // First poll only: we have the live number but not enough history to
        // honestly draw a trend yet. Don't render a misleading flat line.
        <div className="flex h-12 items-center text-xs text-muted-foreground">
          Collecting trend…
        </div>
      )}
      <div className="flex items-center justify-between text-xs text-muted-foreground tabular-nums">
        <span>last {series.length} samples</span>
        {peakLabel ? <span>{peakLabel}</span> : null}
      </div>
    </Card>
  );
}

"use client";

import { useMemo } from "react";
import { Activity, Cpu, MemoryStick, Network } from "lucide-react";
import { isDemoMode } from "@/lib/demo";
import { BRAND_MAGENTA } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Sparkline } from "@/components/sparkline";
import { StatCard } from "@/components/stat-card";

function series(seed: number, n = 32): number[] {
  const out: number[] = [];
  let v = 50;
  for (let i = 0; i < n; i++) {
    v += Math.sin(i / 3 + seed) * 8 + ((seed * (i + 3)) % 11) - 5;
    out.push(Math.max(2, Math.min(98, Math.round(v))));
  }
  return out;
}

export default function MetricsPage() {
  // There is no org-wide aggregate metrics endpoint yet, so the charts below
  // are sample data shown only in demo mode. Production renders an explicit
  // placeholder rather than presenting invented numbers as live.
  const demo = isDemoMode();

  const cpu = useMemo(() => (demo ? series(2) : []), [demo]);
  const mem = useMemo(() => (demo ? series(5) : []), [demo]);
  const net = useMemo(() => (demo ? series(9) : []), [demo]);
  const req = useMemo(() => (demo ? series(13) : []), [demo]);

  if (!demo) {
    return (
      <div className="space-y-6">
        <PageHeader
          title="Metrics"
          description="Resource usage across your machines. Org-wide aggregation isn't available yet."
        />
        <Card className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-sm text-muted-foreground">
            Aggregate metrics aren&apos;t available yet. View per-app metrics
            from an app&apos;s Metrics tab.
          </p>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Metrics"
        description="Sample resource usage (a static snapshot, not live data)."
        actions={<Badge variant="outline">Demo</Badge>}
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Avg CPU" value={`${cpu[cpu.length - 1]}%`} icon={Cpu} />
        <StatCard
          label="Avg Memory"
          value={`${mem[mem.length - 1]}%`}
          icon={MemoryStick}
        />
        <StatCard
          label="Egress"
          value={`${net[net.length - 1]} MB/s`}
          icon={Network}
        />
        <StatCard
          label="Requests"
          value={`${req[req.length - 1]}0/s`}
          icon={Activity}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <ChartCard title="CPU utilization" data={cpu} color="hsl(var(--primary))" />
        <ChartCard title="Memory utilization" data={mem} color={BRAND_MAGENTA} />
        <ChartCard title="Network egress" data={net} color="hsl(var(--info))" />
        <ChartCard
          title="Requests per second"
          data={req}
          color="hsl(var(--success))"
        />
      </div>
    </div>
  );
}

function ChartCard({
  title,
  data,
  color,
}: {
  title: string;
  data: number[];
  color: string;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <Sparkline data={data} stroke={color} height={120} />
      </CardContent>
    </Card>
  );
}

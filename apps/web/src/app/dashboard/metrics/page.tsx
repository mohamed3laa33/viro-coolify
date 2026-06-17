"use client";

import { useMemo } from "react";
import { Activity, Cpu, MemoryStick, Network } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
  const cpu = useMemo(() => series(2), []);
  const mem = useMemo(() => series(5), []);
  const net = useMemo(() => series(9), []);
  const req = useMemo(() => series(13), []);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Metrics"
        description="Real-time resource usage across all your machines."
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
        <ChartCard title="Memory utilization" data={mem} color="#E0218A" />
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

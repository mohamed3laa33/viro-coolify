"use client";

import { useMemo, useRef } from "react";
import Link from "next/link";
import {
  Activity,
  ChevronRight,
  Cpu,
  GitBranch,
  MemoryStick,
  Network,
  Package,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { BRAND_MAGENTA } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Notice } from "@/components/ui/notice";
import { StatusDot } from "@/components/ui/status-dot";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
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
  // are sample data shown only in demo mode. Production renders the real list
  // of apps, each linking to its own (real) Metrics tab, rather than presenting
  // invented aggregate numbers as live.
  const demo = isDemoMode();

  const cpu = useMemo(() => (demo ? series(2) : []), [demo]);
  const mem = useMemo(() => (demo ? series(5) : []), [demo]);
  const net = useMemo(() => (demo ? series(9) : []), [demo]);
  const req = useMemo(() => (demo ? series(13) : []), [demo]);

  if (!demo) {
    return <MetricsAppList />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Metrics"
        description="Sample resource usage (a static snapshot, not live data)."
        actions={<Badge variant="outline">Demo</Badge>}
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Avg CPU"
          value={`${cpu[cpu.length - 1]}%`}
          icon={Cpu}
        />
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
        <ChartCard
          title="CPU utilization"
          data={cpu}
          color="hsl(var(--primary))"
        />
        <ChartCard
          title="Memory utilization"
          data={mem}
          color={BRAND_MAGENTA}
        />
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

// Production view: org-wide aggregation isn't available yet, so instead of a
// dead placeholder we surface the org's apps and link each to its detail page
// (where the real per-app Metrics tab lives). The `?tab=metrics` hint is
// forward-compatible if the detail page starts honoring it. No fabricated
// numbers — the list comes from the API.
function MetricsAppList() {
  const { activeOrgId, authedCall } = useAuth();

  // useResource exposes a boolean `error`; capture the actual failure so the
  // banner shows the real backend message rather than a generic string.
  const fetchErrorRef = useRef<string | null>(null);

  // Demo fallback never ships to prod; harmless here (this branch is prod-only)
  // but kept consistent with the apps list so behaviour matches in preview.
  const demoApps = useDemoData((m) => m.mockApps, [] as App[]);

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listApps(activeOrgId, token, on, { signal })
                .then((res) => {
                  fetchErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  fetchErrorRef.current = errorMessage(
                    err,
                    "Failed to load apps.",
                  );
                  throw err;
                }),
            signal,
          )
      : null,
    { data: demoApps },
    [activeOrgId, demoApps],
    // Share the apps-list cache with the sidebar/apps page so this view dedupes
    // the request instead of issuing its own.
    { cacheKey: activeOrgId ? `apps:${activeOrgId}` : undefined },
  );

  const apps = data.data;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Metrics"
        description="Resource usage across your apps. Open an app to see its live metrics."
      />

      <Notice variant="info">
        <span className="text-primary-bright">
          Org-wide aggregation isn&apos;t available yet — open an app below to
          see its live metrics on the Metrics tab.
        </span>
      </Notice>

      {error && (
        <Notice variant="error">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span>{fetchErrorRef.current ?? "Failed to load apps."}</span>
            <Button size="sm" variant="secondary" onClick={refetch}>
              Retry
            </Button>
          </div>
        </Notice>
      )}

      {apps.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => (
            <Link key={app.id} href={`/dashboard/apps/${app.id}?tab=metrics`}>
              <Card className="h-full p-5 transition-colors hover:border-primary/40">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate font-medium">{app.name}</p>
                    <p className="truncate font-mono text-xs text-muted-foreground">
                      {app.image || app.gitRepository}
                    </p>
                  </div>
                  <StatusDot status={app.status} />
                </div>

                <div className="mt-5 flex items-center justify-between gap-2 text-xs text-muted-foreground">
                  {app.image ? (
                    <span className="inline-flex items-center gap-1">
                      <Package className="h-3.5 w-3.5" />
                      image
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1">
                      <GitBranch className="h-3.5 w-3.5" />
                      {app.gitBranch}
                    </span>
                  )}
                  <span className="inline-flex items-center gap-1 text-primary-bright">
                    Open app
                    <ChevronRight className="h-3.5 w-3.5" />
                  </span>
                </div>
              </Card>
            </Link>
          ))}
        </div>
      )}

      {/* Skeletons on first load so the empty state doesn't flash. */}
      {loading && apps.length === 0 && !error && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Card key={i} className="h-full space-y-5 p-5">
              <div className="flex items-start justify-between">
                <div className="min-w-0 space-y-2">
                  <Skeleton className="h-4 w-32" />
                  <Skeleton className="h-3 w-40" />
                </div>
                <Skeleton className="h-2.5 w-2.5 rounded-full" />
              </div>
              <Skeleton className="h-3 w-24" />
            </Card>
          ))}
        </div>
      )}

      {!loading && apps.length === 0 && !error && (
        <EmptyState
          icon={Activity}
          title="No apps to show metrics for"
          description="Deploy an app to start collecting per-app metrics."
        />
      )}
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

"use client";

import { use, useMemo, useState } from "react";
import Link from "next/link";
import {
  ArrowLeft,
  Rocket,
  Square,
  RotateCw,
  GitBranch,
  Package,
  Globe,
  Loader2,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App } from "@/lib/api";
import { mockApps } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";
import { Sparkline } from "@/components/sparkline";

const TABS = [
  "Overview",
  "Logs",
  "Metrics",
  "Environment",
  "Settings",
] as const;
type Tab = (typeof TABS)[number];

type ActionKind = "deploy" | "stop" | "restart";

function makeSeries(seed: number, n = 24): number[] {
  const out: number[] = [];
  let v = 40 + (seed % 20);
  for (let i = 0; i < n; i++) {
    v += Math.sin(i / 2 + seed) * 6 + ((seed * (i + 1)) % 9) - 4;
    out.push(Math.max(4, Math.min(96, Math.round(v))));
  }
  return out;
}

const MOCK_LOGS = [
  "2026-06-17T09:01:02Z [info]  Starting machine 4d891 in iad",
  "2026-06-17T09:01:03Z [info]  Pulling image registry.viro.app/app:v42",
  "2026-06-17T09:01:08Z [info]  Image pulled in 5.1s",
  "2026-06-17T09:01:09Z [info]  Running health checks on :8080/healthz",
  "2026-06-17T09:01:11Z [info]  ✓ Health check passed",
  "2026-06-17T09:01:11Z [info]  Listening on http://0.0.0.0:8080",
  "2026-06-17T09:02:34Z [info]  GET /  200  12ms",
  "2026-06-17T09:02:35Z [info]  GET /api/users  200  41ms",
  "2026-06-17T09:03:02Z [warn]  Upstream latency 320ms (db pool saturated)",
  "2026-06-17T09:03:40Z [info]  Autoscaled to 3 machines (lhr, sin)",
];

const MOCK_ENV: Array<[string, string]> = [
  ["NODE_ENV", "production"],
  ["PORT", "8080"],
  ["DATABASE_URL", "postgres://•••••••••@primary-postgres.internal:5432/app"],
  ["REDIS_URL", "redis://•••••••••@session-cache.internal:6379"],
  ["LOG_LEVEL", "info"],
];

export default function AppDetailPage({
  params,
}: {
  params: Promise<{ uuid: string }>;
}) {
  // The route folder is named [uuid] for historical reasons; the param is the
  // app id.
  const { uuid: appId } = use(params);
  const { activeOrgId, authedCall } = useAuth();

  const fallback = useMemo<App>(
    () => mockApps.find((a) => a.id === appId) ?? mockApps[0],
    [appId],
  );

  const { data: fetched, loading } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.getApp(activeOrgId, appId, token, on))
      : null,
    fallback,
    [activeOrgId, appId],
  );

  // Local override so action results (which return the updated App) reflect
  // immediately without a refetch.
  const [override, setOverride] = useState<App | null>(null);
  const app = override ?? fetched;

  const [tab, setTab] = useState<Tab>("Overview");
  const [pending, setPending] = useState<ActionKind | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const cpu = useMemo(() => makeSeries(appId.length + 3), [appId]);
  const mem = useMemo(() => makeSeries(appId.length + 11), [appId]);

  async function runAction(kind: ActionKind) {
    if (!activeOrgId) {
      setNotice(
        `${capitalize(kind)} unavailable — no active organization (demo mode).`,
      );
      return;
    }
    setPending(kind);
    setNotice(null);
    try {
      const updated = await authedCall((token, on) =>
        kind === "deploy"
          ? api.deployApp(activeOrgId, appId, token, on)
          : kind === "stop"
            ? api.stopApp(activeOrgId, appId, token, on)
            : api.restartApp(activeOrgId, appId, token, on),
      );
      setOverride(updated);
      setNotice(`${capitalize(kind)} requested — status: ${updated.status}`);
    } catch {
      setNotice(
        `${capitalize(kind)} queued locally (API unreachable — running in demo mode).`,
      );
    } finally {
      setPending(null);
    }
  }

  return (
    <div className="space-y-6">
      <Link
        href="/dashboard/apps"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to apps
      </Link>

      {/* Header */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex items-center gap-3">
          <StatusDot status={app.status} />
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">
              {app.name}
            </h1>
            <span className="inline-flex items-center gap-1 font-mono text-sm text-muted-foreground">
              <Globe className="h-3.5 w-3.5" />
              {app.gitRepository}
            </span>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            onClick={() => runAction("deploy")}
            disabled={pending !== null}
          >
            {pending === "deploy" ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Rocket className="h-4 w-4" />
            )}
            Deploy
          </Button>
          <Button
            variant="secondary"
            onClick={() => runAction("restart")}
            disabled={pending !== null}
          >
            {pending === "restart" ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RotateCw className="h-4 w-4" />
            )}
            Restart
          </Button>
          <Button
            variant="destructive"
            onClick={() => runAction("stop")}
            disabled={pending !== null}
          >
            {pending === "stop" ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Square className="h-4 w-4" />
            )}
            Stop
          </Button>
        </div>
      </div>

      {notice && (
        <div className="rounded-md border border-primary/30 bg-primary/10 px-4 py-2 text-sm text-primary">
          {notice}
        </div>
      )}

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="-mb-px flex gap-6 overflow-x-auto">
          {TABS.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => setTab(t)}
              className={cn(
                "whitespace-nowrap border-b-2 px-1 pb-3 text-sm font-medium transition-colors",
                tab === t
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t}
            </button>
          ))}
        </nav>
      </div>

      {loading && (
        <p className="text-sm text-muted-foreground">Loading app…</p>
      )}

      {/* Tab content */}
      {tab === "Overview" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <InfoCard title="Status">
            <StatusDot status={app.status} showLabel />
          </InfoCard>
          <InfoCard title="Repository">
            <span className="font-mono text-sm">{app.gitRepository}</span>
          </InfoCard>
          <InfoCard title="Branch">
            <span className="inline-flex items-center gap-1.5 font-mono text-sm">
              <GitBranch className="h-3.5 w-3.5 text-muted-foreground" />
              {app.gitBranch}
            </span>
          </InfoCard>
          <InfoCard title="Build pack">
            <span className="inline-flex items-center gap-1.5 font-mono text-sm">
              <Package className="h-3.5 w-3.5 text-muted-foreground" />
              {app.buildPack}
            </span>
          </InfoCard>
        </div>
      )}

      {tab === "Logs" && (
        <Card className="overflow-hidden">
          <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
            <span className="font-mono text-xs text-muted-foreground">
              live tail — {app.name}
            </span>
            <StatusDot status="running" showLabel />
          </div>
          <div className="scrollbar-thin max-h-[420px] overflow-y-auto bg-[#0c0c0f] p-4 font-mono text-xs leading-relaxed">
            {MOCK_LOGS.map((line, i) => {
              const level = line.includes("[warn]")
                ? "text-warning"
                : line.includes("[error]")
                  ? "text-destructive"
                  : "text-muted-foreground";
              return (
                <div key={i} className={cn("whitespace-pre-wrap", level)}>
                  {line}
                </div>
              );
            })}
          </div>
        </Card>
      )}

      {tab === "Metrics" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <MetricCard
            title="CPU"
            value={`${cpu[cpu.length - 1]}%`}
            data={cpu}
            color="hsl(var(--primary))"
          />
          <MetricCard
            title="Memory"
            value={`${mem[mem.length - 1]}%`}
            data={mem}
            color="#E0218A"
          />
        </div>
      )}

      {tab === "Environment" && (
        <Card>
          <CardHeader>
            <CardTitle>Environment variables</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <ul className="divide-y divide-border">
              {MOCK_ENV.map(([key, value]) => (
                <li
                  key={key}
                  className="grid grid-cols-1 gap-1 px-6 py-3 sm:grid-cols-[200px_1fr] sm:gap-4"
                >
                  <span className="font-mono text-sm text-foreground">
                    {key}
                  </span>
                  <span className="truncate font-mono text-sm text-muted-foreground">
                    {value}
                  </span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {tab === "Settings" && (
        <Card>
          <CardHeader>
            <CardTitle>Danger zone</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between rounded-lg border border-border p-4">
              <div>
                <p className="text-sm font-medium">Transfer app</p>
                <p className="text-sm text-muted-foreground">
                  Move this app to another organization.
                </p>
              </div>
              <Button variant="secondary" size="sm">
                Transfer
              </Button>
            </div>
            <div className="flex items-center justify-between rounded-lg border border-destructive/40 p-4">
              <div>
                <p className="text-sm font-medium text-destructive">
                  Delete app
                </p>
                <p className="text-sm text-muted-foreground">
                  Permanently remove this app and all of its machines.
                </p>
              </div>
              <Button variant="destructive" size="sm">
                Delete
              </Button>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function InfoCard({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Card className="p-5">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {title}
      </p>
      <div className="mt-2">{children}</div>
    </Card>
  );
}

function MetricCard({
  title,
  value,
  data,
  color,
}: {
  title: string;
  value: string;
  data: number[];
  color: string;
}) {
  return (
    <Card className="p-5">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">{title}</p>
        <p className="text-2xl font-semibold tracking-tight">{value}</p>
      </div>
      <div className="mt-4">
        <Sparkline data={data} stroke={color} />
      </div>
    </Card>
  );
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

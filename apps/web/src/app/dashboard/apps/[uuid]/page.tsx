"use client";

import { use, useMemo, useState, type FormEvent } from "react";
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
  Plus,
  Trash2,
  Eye,
  EyeOff,
  ShieldCheck,
  ShieldAlert,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  type App,
  type AppMetrics,
  type Domain,
  type EnvVar,
} from "@/lib/api";
import {
  mockApps,
  mockDomains,
  mockEnv,
  mockMetrics,
} from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { StatusDot } from "@/components/ui/status-dot";
import { Sparkline } from "@/components/sparkline";

const TABS = [
  "Overview",
  "Logs",
  "Metrics",
  "Environment",
  "Domains",
  "Settings",
] as const;
type Tab = (typeof TABS)[number];

type ActionKind = "deploy" | "stop" | "restart";

const MOCK_LOGS = [
  "2026-06-17T09:01:02Z [info]  Starting machine 4d891 in iad",
  "2026-06-17T09:01:03Z [info]  Pulling image registry.vortex.v60ai.com/app:v42",
  "2026-06-17T09:01:08Z [info]  Image pulled in 5.1s",
  "2026-06-17T09:01:09Z [info]  Running health checks on :8080/healthz",
  "2026-06-17T09:01:11Z [info]  ✓ Health check passed",
  "2026-06-17T09:01:11Z [info]  Listening on http://0.0.0.0:8080",
  "2026-06-17T09:02:34Z [info]  GET /  200  12ms",
  "2026-06-17T09:02:35Z [info]  GET /api/users  200  41ms",
  "2026-06-17T09:03:02Z [warn]  Upstream latency 320ms (db pool saturated)",
  "2026-06-17T09:03:40Z [info]  Autoscaled to 3 machines (lhr, sin)",
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

      {tab === "Metrics" && <MetricsTab appId={appId} />}

      {tab === "Environment" && <EnvironmentTab appId={appId} />}

      {tab === "Domains" && <DomainsTab appId={appId} appName={app.name} />}

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

// ---------------------------------------------------------------------------
// Metrics tab
// ---------------------------------------------------------------------------

function MetricsTab({ appId }: { appId: string }) {
  const { activeOrgId, authedCall } = useAuth();

  const { data } = useResource<AppMetrics>(
    activeOrgId
      ? () =>
          authedCall((token, on) =>
            api.getMetrics(activeOrgId, appId, token, on),
          )
      : null,
    mockMetrics,
    [activeOrgId, appId],
  );

  // Fall back to client mock when the endpoint returns empty series.
  const metrics: AppMetrics = {
    cpu: data.cpu.length ? data.cpu : mockMetrics.cpu,
    memory: data.memory.length ? data.memory : mockMetrics.memory,
    requests: data.requests.length ? data.requests : mockMetrics.requests,
  };

  const cpu = metrics.cpu.map((p) => p.v);
  const mem = metrics.memory.map((p) => p.v);
  const req = metrics.requests.map((p) => p.v);

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <MetricCard
        title="CPU"
        value={`${last(cpu)}%`}
        data={cpu}
        color="hsl(var(--primary))"
      />
      <MetricCard
        title="Memory"
        value={`${last(mem)}%`}
        data={mem}
        color="#E0218A"
      />
      <MetricCard
        title="Requests"
        value={`${last(req)}/s`}
        data={req}
        color="hsl(var(--success))"
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Environment tab
// ---------------------------------------------------------------------------

function EnvironmentTab({ appId }: { appId: string }) {
  const { activeOrgId, authedCall } = useAuth();

  const { data, refetch } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listEnv(activeOrgId, appId, token, on))
      : null,
    { data: mockEnv },
    [activeOrgId, appId],
  );

  const vars = data.data;

  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [pending, setPending] = useState(false);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function onAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const k = key.trim();
    if (!k) return;
    if (!activeOrgId) {
      setNotice("Set unavailable — no active organization (demo mode).");
      return;
    }
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.setEnv(activeOrgId, appId, { key: k, value }, token, on),
      );
      setKey("");
      setValue("");
      refetch();
    } catch {
      setNotice("Variable queued locally (API unreachable — demo mode).");
    } finally {
      setPending(false);
    }
  }

  async function onDelete(k: string) {
    if (!activeOrgId) {
      setNotice("Delete unavailable — no active organization (demo mode).");
      return;
    }
    setBusyKey(k);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteEnv(activeOrgId, appId, k, token, on),
      );
      refetch();
    } catch {
      setNotice("Delete queued locally (API unreachable — demo mode).");
    } finally {
      setBusyKey(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && (
        <div className="rounded-md border border-primary/30 bg-primary/10 px-4 py-2 text-sm text-primary">
          {notice}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Environment variables</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {vars.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              No environment variables yet.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {vars.map((v: EnvVar) => {
                const show = revealed[v.key];
                return (
                  <li
                    key={v.key}
                    className="flex items-center gap-4 px-6 py-3"
                  >
                    <span className="w-[200px] shrink-0 truncate font-mono text-sm text-foreground">
                      {v.key}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono text-sm text-muted-foreground">
                      {show ? v.value : "•".repeat(Math.min(24, v.value.length || 8))}
                    </span>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() =>
                        setRevealed((r) => ({ ...r, [v.key]: !r[v.key] }))
                      }
                      aria-label={show ? "Hide value" : "Reveal value"}
                    >
                      {show ? (
                        <EyeOff className="h-4 w-4" />
                      ) : (
                        <Eye className="h-4 w-4" />
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => onDelete(v.key)}
                      disabled={busyKey === v.key}
                      aria-label="Delete variable"
                    >
                      {busyKey === v.key ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Trash2 className="h-4 w-4 text-destructive" />
                      )}
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Add variable</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            onSubmit={onAdd}
            className="grid gap-4 sm:grid-cols-[200px_1fr_auto] sm:items-end"
          >
            <div className="space-y-2">
              <Label htmlFor="env-key">Key</Label>
              <Input
                id="env-key"
                className="font-mono"
                placeholder="API_KEY"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="env-value">Value</Label>
              <Input
                id="env-value"
                className="font-mono"
                placeholder="value"
                value={value}
                onChange={(e) => setValue(e.target.value)}
              />
            </div>
            <Button type="submit" disabled={pending}>
              {pending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Plus className="h-4 w-4" />
              )}
              Save
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Domains tab
// ---------------------------------------------------------------------------

// Base domain for platform-issued app hostnames.
const VORTEX_BASE_DOMAIN = "vortex.v60ai.com";

function slugify(value: string): string {
  return (
    value
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "app"
  );
}

function DomainsTab({ appId, appName }: { appId: string; appName: string }) {
  const { activeOrgId, authedCall, orgs } = useAuth();

  // Build the default hostname as <app>.<project>.<org>.vortex.v60ai.com.
  // The project segment defaults to "default" until apps expose their project.
  const orgSlug = slugify(
    orgs.find((o) => o.id === activeOrgId)?.slug ?? "personal",
  );

  const { data, refetch } = useResource(
    activeOrgId
      ? () =>
          authedCall((token, on) =>
            api.listDomains(activeOrgId, appId, token, on),
          )
      : null,
    { data: mockDomains },
    [activeOrgId, appId],
  );

  const domains = data.data;
  const fqdn = `${slugify(appName)}.default.${orgSlug}.${VORTEX_BASE_DOMAIN}`;

  const [domain, setDomain] = useState("");
  const [pending, setPending] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function onAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = domain.trim();
    if (!d) return;
    if (!activeOrgId) {
      setNotice("Add unavailable — no active organization (demo mode).");
      return;
    }
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.addDomain(activeOrgId, appId, d, token, on),
      );
      setDomain("");
      refetch();
    } catch {
      setNotice("Domain queued locally (API unreachable — demo mode).");
    } finally {
      setPending(false);
    }
  }

  async function onDelete(id: string) {
    if (!activeOrgId) {
      setNotice("Delete unavailable — no active organization (demo mode).");
      return;
    }
    setBusyId(id);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteDomain(activeOrgId, appId, id, token, on),
      );
      refetch();
    } catch {
      setNotice("Delete queued locally (API unreachable — demo mode).");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && (
        <div className="rounded-md border border-primary/30 bg-primary/10 px-4 py-2 text-sm text-primary">
          {notice}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Default domain</CardTitle>
        </CardHeader>
        <CardContent>
          <span className="inline-flex items-center gap-2 font-mono text-sm">
            <Globe className="h-4 w-4 text-muted-foreground" />
            {fqdn}
            <Badge variant="success">
              <ShieldCheck className="mr-1 h-3 w-3" />
              TLS
            </Badge>
          </span>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Custom domains</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {domains.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              No custom domains yet.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {domains.map((d: Domain) => (
                <li
                  key={d.id}
                  className="flex items-center justify-between px-6 py-3"
                >
                  <span className="inline-flex items-center gap-2 font-mono text-sm">
                    <Globe className="h-4 w-4 text-muted-foreground" />
                    {d.domain}
                  </span>
                  <div className="flex items-center gap-3">
                    {d.verified ? (
                      <Badge variant="success">
                        <ShieldCheck className="mr-1 h-3 w-3" />
                        Verified
                      </Badge>
                    ) : (
                      <Badge variant="warning">
                        <ShieldAlert className="mr-1 h-3 w-3" />
                        Pending
                      </Badge>
                    )}
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => onDelete(d.id)}
                      disabled={busyId === d.id}
                      aria-label="Delete domain"
                    >
                      {busyId === d.id ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Trash2 className="h-4 w-4 text-destructive" />
                      )}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Add domain</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            onSubmit={onAdd}
            className="flex flex-col gap-4 sm:flex-row sm:items-end"
          >
            <div className="flex-1 space-y-2">
              <Label htmlFor="domain-host">Domain</Label>
              <Input
                id="domain-host"
                className="font-mono"
                placeholder="app.acme.com"
                value={domain}
                onChange={(e) => setDomain(e.target.value)}
                required
              />
            </div>
            <Button type="submit" disabled={pending}>
              {pending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Plus className="h-4 w-4" />
              )}
              Add domain
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared
// ---------------------------------------------------------------------------

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

function last(data: number[]): number {
  return data.length ? data[data.length - 1] : 0;
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

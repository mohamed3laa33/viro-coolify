"use client";

import {
  use,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import Link from "next/link";
import {
  ArrowLeft,
  Rocket,
  Square,
  RotateCw,
  GitBranch,
  Package,
  Globe,
  Cpu,
  MemoryStick,
  Server,
  RefreshCw,
  Loader2,
  Plus,
  Trash2,
  Eye,
  EyeOff,
  ShieldCheck,
  ShieldAlert,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import {
  api,
  type App,
  type AppMetrics,
  type Domain,
  type EnvVar,
} from "@/lib/api";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { cn, buildAppFqdn, slugify, BRAND_MAGENTA } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Notice } from "@/components/ui/notice";
import { Tabs } from "@/components/ui/tabs";
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

// Sample log lines shown only in demo mode (when the API is unreachable). In
// production the Logs tab fetches the real tail from the API.
const DEMO_LOGS = [
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
  params: Promise<{ appId: string }>;
}) {
  const { appId } = use(params);
  const router = useRouter();
  const { activeOrgId, authedCall } = useAuth();

  // In demo mode show a mock app as the fallback; in production there is no
  // fabricated app, so a failed/absent fetch renders an explicit empty state.
  // The mock module loads lazily (demo mode only) so it never ships to prod.
  const fallback = useDemoData<App | null>(
    (m) => m.mockApps.find((a) => a.id === appId) ?? m.mockApps[0] ?? null,
    null,
  );

  const {
    data: fetched,
    loading,
    refetch,
  } = useResource<App | null>(
    activeOrgId
      ? () =>
          authedCall((token, on) => api.getApp(activeOrgId, appId, token, on))
      : null,
    fallback,
    [activeOrgId, appId, fallback],
  );

  // Brief optimistic status while an action is in flight; once the action
  // resolves we refetch() so the displayed status reflects the backend rather
  // than an optimistic guess that can desync.
  const [optimisticStatus, setOptimisticStatus] = useState<
    App["status"] | null
  >(null);
  const app = useMemo<App | null>(
    () =>
      fetched && optimisticStatus
        ? { ...fetched, status: optimisticStatus }
        : fetched,
    [fetched, optimisticStatus],
  );

  const [tab, setTab] = useState<Tab>("Overview");
  const [pending, setPending] = useState<ActionKind | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Optimistic status hint per action so the StatusDot reacts instantly.
  const optimisticFor: Record<ActionKind, App["status"]> = {
    deploy: "deploying",
    stop: "stopped",
    restart: "deploying",
  };

  async function runAction(kind: ActionKind) {
    if (!activeOrgId) {
      setNotice(`${capitalize(kind)} unavailable — no active organization.`);
      return;
    }
    setPending(kind);
    setOptimisticStatus(optimisticFor[kind]);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        kind === "deploy"
          ? api.deployApp(activeOrgId, appId, token, on)
          : kind === "stop"
            ? api.stopApp(activeOrgId, appId, token, on)
            : api.restartApp(activeOrgId, appId, token, on),
      );
      // Reconcile with the backend instead of trusting the optimistic state.
      refetch();
    } catch (err) {
      setOptimisticStatus(null);
      setNotice(`${capitalize(kind)} failed — ${errorMessage(err)}`);
    } finally {
      setPending(null);
    }
  }

  // Clear the optimistic status once a fresh fetch lands.
  useEffect(() => {
    if (optimisticStatus) setOptimisticStatus(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetched]);

  async function onDelete() {
    if (!activeOrgId) {
      setNotice("Delete unavailable — no active organization.");
      setConfirmOpen(false);
      return;
    }
    setDeleting(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteApp(activeOrgId, appId, token, on),
      );
      router.push("/dashboard/apps");
    } catch (err) {
      setNotice(`Delete failed — ${errorMessage(err)}`);
      setDeleting(false);
      setConfirmOpen(false);
    }
  }

  if (!app && !loading) {
    return (
      <div className="space-y-6">
        <Link
          href="/dashboard/apps"
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to apps
        </Link>
        <Card className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-sm text-muted-foreground">
            App not found, or the API is unreachable.
          </p>
        </Card>
      </div>
    );
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
          <StatusDot status={app?.status ?? "created"} />
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">
              {app?.name ?? "Loading…"}
            </h1>
            <span className="inline-flex items-center gap-1 font-mono text-sm text-muted-foreground">
              <Globe className="h-3.5 w-3.5" />
              {app?.gitRepository}
            </span>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            onClick={() => runAction("deploy")}
            loading={pending === "deploy"}
            disabled={pending !== null}
          >
            {pending !== "deploy" && <Rocket className="h-4 w-4" />}
            Deploy
          </Button>
          <Button
            variant="secondary"
            onClick={() => runAction("restart")}
            loading={pending === "restart"}
            disabled={pending !== null}
          >
            {pending !== "restart" && <RotateCw className="h-4 w-4" />}
            Restart
          </Button>
          <Button
            variant="destructive"
            onClick={() => runAction("stop")}
            loading={pending === "stop"}
            disabled={pending !== null}
          >
            {pending !== "stop" && <Square className="h-4 w-4" />}
            Stop
          </Button>
        </div>
      </div>

      {notice && <Notice variant="error">{notice}</Notice>}

      {/* Tabs */}
      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      {loading && <p className="text-sm text-muted-foreground">Loading app…</p>}

      {/* Tab content */}
      {app && tab === "Overview" && (
        <TabPanel tab="Overview">
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
            <InfoCard title="Requested CPU">
              <span className="inline-flex items-center gap-1.5 font-mono text-sm">
                <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
                {app.cpu} vCPU
              </span>
            </InfoCard>
            <InfoCard title="Requested memory">
              <span className="inline-flex items-center gap-1.5 font-mono text-sm">
                <MemoryStick className="h-3.5 w-3.5 text-muted-foreground" />
                {formatMemory(app.memoryMb)}
              </span>
            </InfoCard>
            {app.host && (
              <InfoCard title="Host">
                <span className="inline-flex items-center gap-1.5 font-mono text-sm">
                  <Globe className="h-3.5 w-3.5 text-muted-foreground" />
                  {app.host}
                </span>
              </InfoCard>
            )}
            {app.namespace && (
              <InfoCard title="Namespace">
                <span className="inline-flex items-center gap-1.5 font-mono text-sm">
                  <Server className="h-3.5 w-3.5 text-muted-foreground" />
                  {app.namespace}
                </span>
              </InfoCard>
            )}
          </div>
        </TabPanel>
      )}

      {app && tab === "Logs" && (
        <TabPanel tab="Logs">
          <LogsTab appId={appId} appName={app.name} />
        </TabPanel>
      )}

      {app && tab === "Metrics" && (
        <TabPanel tab="Metrics">
          <MetricsTab appId={appId} />
        </TabPanel>
      )}

      {app && tab === "Environment" && (
        <TabPanel tab="Environment">
          <EnvironmentTab appId={appId} />
        </TabPanel>
      )}

      {app && tab === "Domains" && (
        <TabPanel tab="Domains">
          <DomainsTab appId={appId} appName={app.name} />
        </TabPanel>
      )}

      {app && tab === "Settings" && (
        <TabPanel tab="Settings">
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
                {/* TODO(backend): no app-transfer endpoint exists yet; disabled
                  until the API exposes one. */}
                <Button
                  variant="secondary"
                  size="sm"
                  disabled
                  title="App transfer is not available yet"
                >
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
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => setConfirmOpen(true)}
                  loading={deleting}
                >
                  Delete
                </Button>
              </div>
            </CardContent>
          </Card>
        </TabPanel>
      )}

      {confirmOpen && (
        <ConfirmDialog
          title="Delete app?"
          message={`This permanently removes ${app?.name ?? "this app"} and all of its machines. This action cannot be undone.`}
          confirmLabel="Delete app"
          loading={deleting}
          onConfirm={onDelete}
          onCancel={() => setConfirmOpen(false)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Confirm dialog — accessible (role="alertdialog", focus trap, Escape to
// cancel). Implemented inline to avoid window.confirm.
// ---------------------------------------------------------------------------

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  loading,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  loading: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const titleId = useId();
  const descId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const confirmRef = useRef<HTMLButtonElement | null>(null);

  // Focus the destructive action on open so keyboard users land inside.
  useEffect(() => {
    confirmRef.current?.focus();
  }, []);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (e.key === "Escape" && !loading) {
        e.preventDefault();
        onCancel();
        return;
      }
      // Trap Tab focus within the dialog.
      if (e.key === "Tab") {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input, [tabindex]:not([tabindex="-1"])',
        );
        if (!focusable || focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        const activeEl = document.activeElement;
        if (e.shiftKey && activeEl === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && activeEl === last) {
          e.preventDefault();
          first.focus();
        }
      }
    },
    [loading, onCancel],
  );

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !loading) onCancel();
      }}
    >
      <div
        ref={dialogRef}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descId}
        onKeyDown={onKeyDown}
        className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-lg"
      >
        <h2 id={titleId} className="text-lg font-semibold text-destructive">
          {title}
        </h2>
        <p id={descId} className="mt-2 text-sm text-muted-foreground">
          {message}
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={onCancel}
            disabled={loading}
          >
            Cancel
          </Button>
          <Button
            ref={confirmRef}
            variant="destructive"
            size="sm"
            onClick={onConfirm}
            loading={loading}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Logs tab — fetches the current log tail from the API. In demo mode (no
// reachable API) it falls back to sample lines so the layout is reviewable.
// ---------------------------------------------------------------------------

function LogsTab({ appId, appName }: { appId: string; appName: string }) {
  const { activeOrgId, authedCall } = useAuth();
  const demo = isDemoMode();

  const { data, loading, error, refetch } = useResource<string>(
    activeOrgId
      ? () =>
          authedCall((token, on) => api.getLogs(activeOrgId, appId, token, on))
      : null,
    demo ? DEMO_LOGS.join("\n") : "",
    [activeOrgId, appId],
  );

  const lines = data ? data.split("\n") : [];

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
        <span className="font-mono text-xs text-muted-foreground">
          log tail — {appName}
        </span>
        <div className="flex items-center gap-2">
          {demo && data === DEMO_LOGS.join("\n") && (
            <Badge variant="outline">Demo</Badge>
          )}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => refetch()}
            loading={loading}
            aria-label="Refresh logs"
          >
            {!loading && <RefreshCw className="h-3.5 w-3.5" />}
            Refresh
          </Button>
        </div>
      </div>
      {error && !demo && (
        <Notice variant="error" className="m-4">
          Could not load logs — the API is unreachable.
        </Notice>
      )}
      {lines.length === 0 ? (
        <p className="px-4 py-8 text-center text-sm text-muted-foreground">
          {loading ? "Loading logs…" : "No log output yet for this app."}
        </p>
      ) : (
        <div className="scrollbar-thin max-h-[420px] overflow-y-auto bg-background p-4 font-mono text-xs leading-relaxed">
          {lines.map((line, i) => {
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
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Metrics tab
// ---------------------------------------------------------------------------

function MetricsTab({ appId }: { appId: string }) {
  const { activeOrgId, authedCall } = useAuth();

  const demo = isDemoMode();
  const empty: AppMetrics = { cpu: [], memory: [], requests: [] };

  // Demo fallback loads lazily (demo mode only); never shipped to prod.
  const demoMetrics = useDemoData((m) => m.mockMetrics, empty);

  const { data } = useResource<AppMetrics>(
    activeOrgId
      ? () =>
          authedCall((token, on) =>
            api.getMetrics(activeOrgId, appId, token, on),
          )
      : null,
    demoMetrics,
    [activeOrgId, appId, demoMetrics],
  );

  // In demo mode, synthesize any empty series so the charts render; in
  // production, empty series stay empty (no invented data).
  const metrics: AppMetrics = demo
    ? {
        cpu: data.cpu.length ? data.cpu : demoMetrics.cpu,
        memory: data.memory.length ? data.memory : demoMetrics.memory,
        requests: data.requests.length ? data.requests : demoMetrics.requests,
      }
    : data;

  const cpu = metrics.cpu.map((p) => p.v);
  const mem = metrics.memory.map((p) => p.v);
  const req = metrics.requests.map((p) => p.v);

  if (cpu.length === 0 && mem.length === 0 && req.length === 0) {
    return (
      <Card className="flex flex-col items-center justify-center py-16 text-center">
        <p className="text-sm text-muted-foreground">
          No metrics recorded yet for this app.
        </p>
      </Card>
    );
  }

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
        color={BRAND_MAGENTA}
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

  // Demo fallback loads lazily (demo mode only); never shipped to prod.
  const demoEnv = useDemoData((m) => m.mockEnv, [] as EnvVar[]);

  const { data, refetch } = useResource(
    activeOrgId
      ? () =>
          authedCall((token, on) => api.listEnv(activeOrgId, appId, token, on))
      : null,
    { data: demoEnv },
    [activeOrgId, appId, demoEnv],
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
      setNotice("Set unavailable — no active organization.");
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
      setNotice("Could not save the variable — the API is unreachable.");
    } finally {
      setPending(false);
    }
  }

  async function onDelete(k: string) {
    if (!activeOrgId) {
      setNotice("Delete unavailable — no active organization.");
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
      setNotice("Could not delete the variable — the API is unreachable.");
    } finally {
      setBusyKey(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && <Notice>{notice}</Notice>}

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
                  <li key={v.key} className="flex items-center gap-4 px-6 py-3">
                    <span className="w-[200px] shrink-0 truncate font-mono text-sm text-foreground">
                      {v.key}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono text-sm text-muted-foreground">
                      {show
                        ? v.value
                        : "•".repeat(Math.min(24, v.value.length || 8))}
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

function DomainsTab({ appId, appName }: { appId: string; appName: string }) {
  const { activeOrgId, authedCall, orgs } = useAuth();

  // Build the default hostname as <app>.<project>.<org>.vortex.v60ai.com.
  // The project segment defaults to "default" until apps expose their project.
  const orgSlug = slugify(
    orgs.find((o) => o.id === activeOrgId)?.slug ?? "personal",
  );

  // Demo fallback loads lazily (demo mode only); never shipped to prod.
  const demoDomains = useDemoData((m) => m.mockDomains, [] as Domain[]);

  const { data, refetch } = useResource(
    activeOrgId
      ? () =>
          authedCall((token, on) =>
            api.listDomains(activeOrgId, appId, token, on),
          )
      : null,
    { data: demoDomains },
    [activeOrgId, appId, demoDomains],
  );

  const domains = data.data;
  const fqdn = buildAppFqdn(appName, orgSlug);

  const [domain, setDomain] = useState("");
  const [pending, setPending] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function onAdd(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = domain.trim();
    if (!d) return;
    if (!activeOrgId) {
      setNotice("Add unavailable — no active organization.");
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
      setNotice("Could not add the domain — the API is unreachable.");
    } finally {
      setPending(false);
    }
  }

  async function onDelete(id: string) {
    if (!activeOrgId) {
      setNotice("Delete unavailable — no active organization.");
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
      setNotice("Could not delete the domain — the API is unreachable.");
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && <Notice>{notice}</Notice>}

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

// Wraps each tab's content as an ARIA tabpanel. The Tabs primitive owns the
// tab `id`s (generated via useId), so we expose a self-describing panel
// (role + label + focusable) rather than referencing ids we cannot read here.
function TabPanel({ tab, children }: { tab: Tab; children: React.ReactNode }) {
  return (
    <div
      role="tabpanel"
      id={`app-tabpanel-${tab}`}
      aria-label={tab}
      tabIndex={0}
      className="focus-visible:outline-none"
    >
      {children}
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

function last(data: number[]): number {
  return data.length ? data[data.length - 1] : 0;
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function formatMemory(mb: number): string {
  if (mb >= 1024 && mb % 1024 === 0) return `${mb / 1024} GB`;
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${mb} MB`;
}

function errorMessage(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  return "the API is unreachable.";
}

"use client";

import {
  Suspense,
  use,
  useEffect,
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
  Copy,
  Check,
  History,
  Hammer,
  Lock,
  ExternalLink,
  CheckCircle2,
  AlertTriangle,
} from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth";
import {
  api,
  statusVariant,
  streamAppLogs,
  type App,
  type AppDetail,
  type Build,
  type Domain,
  type DomainResult,
  type EnvVar,
  type PodMetrics,
  type Release,
} from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { useResource, invalidate } from "@/lib/use-resource";
import { cn, buildAppFqdn, slugify } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Notice } from "@/components/ui/notice";
import { Tabs } from "@/components/ui/tabs";
import { StatusDot } from "@/components/ui/status-dot";
import { MetricsTimeseries } from "@/components/metrics-timeseries";

const TABS = [
  "Overview",
  "Logs",
  "Metrics",
  "Releases",
  "Builds",
  "Environment",
  "Domains",
  "Settings",
] as const;
type Tab = (typeof TABS)[number];

// Map a `?tab=` query value (case-insensitive) to a real tab, so deep-links like
// `?tab=metrics` open the right panel. Unknown values fall back to Overview.
function tabFromParam(raw: string | null): Tab {
  if (!raw) return "Overview";
  const found = TABS.find((t) => t.toLowerCase() === raw.toLowerCase());
  return found ?? "Overview";
}

type ActionKind = "deploy" | "stop" | "restart";

// Terminal statuses: once a deploy/restart lands on one of these the rollout
// watch stops. Everything else (deploying/restarting/created/empty) is still
// "in flight" and keeps the watcher polling.
const SETTLED_STATUSES = new Set(["running", "stopped", "error"]);

// While a deploy/restart is being watched we poll the app detail this often so
// the status, active release, and host reflect the live rollout rather than a
// single post-action snapshot.
const ROLLOUT_POLL_MS = 2_500;

// Safety cap so the rollout watcher can't poll forever if a deploy never
// settles (e.g. an image that crash-loops). After this it stops polling and
// leaves whatever the last real status was — never a fake "running".
const ROLLOUT_WATCH_TIMEOUT_MS = 5 * 60_000;

// Optional rollout/health fields the API *may* attach to an app once Wave 0's
// working deploy path lands. They are read defensively (see `readRollout`) and
// never fabricated — if the backend doesn't send them, no replica/health count
// is shown (honesty over fake-success).
interface RolloutInfo {
  readyReplicas?: number;
  desiredReplicas?: number;
  healthy?: boolean;
  health?: string;
}

// Safely read possible rollout/health fields off an app detail without assuming
// they exist on the current `AppDetail` type. Returns only the values the
// backend actually provided.
function readRollout(app: AppDetail | null): RolloutInfo {
  if (!app) return {};
  const raw = app as unknown as Record<string, unknown>;
  const num = (v: unknown): number | undefined =>
    typeof v === "number" && Number.isFinite(v) ? v : undefined;
  const str = (v: unknown): string | undefined =>
    typeof v === "string" && v.trim() !== "" ? v : undefined;
  return {
    readyReplicas: num(raw.readyReplicas) ?? num(raw.replicasReady),
    desiredReplicas:
      num(raw.desiredReplicas) ?? num(raw.replicas) ?? num(raw.replicaCount),
    healthy: typeof raw.healthy === "boolean" ? raw.healthy : undefined,
    health: str(raw.health) ?? str(raw.healthStatus),
  };
}

// Resolve the public HTTPS URL for an app. Prefers the backend-reported host
// (the real routed hostname); falls back to the deterministic platform FQDN
// `<app>.<project>.<org>.vortex.v60ai.com` so the URL is shown even before the
// backend echoes a host back.
function appHttpsUrl(app: AppDetail, orgSlug: string): string {
  const host = app.host && app.host.trim() !== "" ? app.host.trim() : null;
  const fqdn = host ?? buildAppFqdn(app.name, orgSlug);
  return fqdn.startsWith("http://") || fqdn.startsWith("https://")
    ? fqdn
    : `https://${fqdn}`;
}

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

// Cap the rendered log tail so a very large response can't bloat the DOM.
const MAX_LOG_LINES = 500;

export default function AppDetailPage({
  params,
}: {
  params: Promise<{ appId: string }>;
}) {
  const { appId } = use(params);
  // useSearchParams() requires a Suspense boundary in Next.js 15 (otherwise the
  // whole route opts out of static rendering). The page param promise is
  // resolved above and threaded in so the inner content can read it directly.
  return (
    <Suspense
      fallback={<p className="text-sm text-muted-foreground">Loading app…</p>}
    >
      <AppDetailContent appId={appId} />
    </Suspense>
  );
}

function AppDetailContent({ appId }: { appId: string }) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { activeOrgId, authedCall, orgs } = useAuth();

  // In demo mode show a mock app as the fallback; in production there is no
  // fabricated app, so a failed/absent fetch renders an explicit empty state.
  // The mock module loads lazily (demo mode only) so it never ships to prod.
  const fallback = useDemoData<AppDetail | null>(
    (m) => m.mockApps.find((a) => a.id === appId) ?? m.mockApps[0] ?? null,
    null,
  );

  // Rollout watch: after a deploy/restart we poll the app detail until its
  // status settles, so the UI reflects the real rollout instead of a single
  // post-action snapshot. Polling is driven by `useResource`'s interval option.
  const [watching, setWatching] = useState(false);

  const {
    data: fetched,
    loading,
    errorStatus,
    refetch,
  } = useResource<AppDetail | null>(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.getApp(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    fallback,
    [activeOrgId, appId, fallback],
    // While watching a rollout, poll fast; otherwise no background polling.
    watching ? { refetchIntervalMs: ROLLOUT_POLL_MS } : {},
  );

  // Brief optimistic status while an action is in flight; the rollout watcher
  // then reconciles it against the live status feed. Once a real status lands
  // for the watched app we drop the optimistic guess.
  const [optimisticStatus, setOptimisticStatus] = useState<
    App["status"] | null
  >(null);
  const app = useMemo<AppDetail | null>(
    () =>
      fetched && optimisticStatus
        ? { ...fetched, status: optimisticStatus }
        : fetched,
    [fetched, optimisticStatus],
  );

  const orgSlug = slugify(
    orgs.find((o) => o.id === activeOrgId)?.slug ?? "personal",
  );

  // Initialize the active tab from the `?tab=` deep-link (e.g. ?tab=metrics).
  const initialTab = useMemo(
    () => tabFromParam(searchParams.get("tab")),
    [searchParams],
  );
  const [tab, setTab] = useState<Tab>(initialTab);

  const [pending, setPending] = useState<ActionKind | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Tracks which action started the current rollout watch so the success
  // banner / URL reveal only appears after a deploy or restart (not a stop),
  // and so a stale watch from a previous action doesn't latch.
  const [watchKind, setWatchKind] = useState<ActionKind | null>(null);
  // Timestamp the watch began, used to honor ROLLOUT_WATCH_TIMEOUT_MS.
  const watchStartedAt = useRef<number>(0);
  // Set true once a watched deploy/restart reaches "running" so we can reveal
  // the live URL with a celebratory banner exactly once per rollout.
  const [justWentLive, setJustWentLive] = useState(false);

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
    setJustWentLive(false);
    try {
      await authedCall((token, on) =>
        kind === "deploy"
          ? api.deployApp(activeOrgId, appId, token, on)
          : kind === "stop"
            ? api.stopApp(activeOrgId, appId, token, on)
            : api.restartApp(activeOrgId, appId, token, on),
      );
      // Start watching the live status feed (poll loop) instead of a single
      // refetch, so the UI follows the real rollout to completion.
      setWatchKind(kind);
      watchStartedAt.current = Date.now();
      setWatching(true);
      refetch();
    } catch (err) {
      setOptimisticStatus(null);
      setWatching(false);
      setWatchKind(null);
      setNotice(
        `${capitalize(kind)} failed — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setPending(null);
    }
  }

  // Reconcile the rollout watch against each fresh app detail. Once a real
  // status lands, drop the optimistic guess; when the status settles (or the
  // watch times out) stop polling. A deploy/restart that reaches "running"
  // triggers the go-live URL reveal.
  useEffect(() => {
    if (!fetched) return;
    // A real status has arrived — the optimistic guess is no longer needed.
    if (optimisticStatus) setOptimisticStatus(null);

    if (!watching) return;

    const status = fetched.status ?? "";
    const settled = SETTLED_STATUSES.has(status);
    const timedOut =
      Date.now() - watchStartedAt.current > ROLLOUT_WATCH_TIMEOUT_MS;

    if (
      status === "running" &&
      (watchKind === "deploy" || watchKind === "restart")
    ) {
      setJustWentLive(true);
    }

    if (settled || timedOut) {
      setWatching(false);
      setWatchKind(null);
    }
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
      // Drop the cached apps list so the deleted app disappears immediately.
      invalidate(`apps:${activeOrgId}`);
      router.push("/dashboard/apps");
    } catch (err) {
      setNotice(
        `Delete failed — ${errorMessage(err, "the API is unreachable.")}`,
      );
      setDeleting(false);
      setConfirmOpen(false);
    }
  }

  if (!app && !loading) {
    // Distinguish a genuine 404 (the app does not exist) from a transport
    // failure (the API never answered) so we don't show a misleading message.
    const notFound = errorStatus === 404;
    return (
      <div className="space-y-6">
        <Link
          href="/dashboard/apps"
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to apps
        </Link>
        <Card className="flex flex-col items-center justify-center gap-3 py-16 text-center">
          <p className="text-sm text-muted-foreground">
            {notFound ? "App not found." : "API unreachable."}
          </p>
          {!notFound && (
            <Button variant="secondary" size="sm" onClick={() => refetch()}>
              <RefreshCw className="h-3.5 w-3.5" />
              Retry
            </Button>
          )}
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

      {/* Live rollout progress — shown while a deploy/restart is in flight so the
          user watches the real status (deploying → running) instead of a single
          optimistic dot. */}
      {app && watching && (
        <RolloutPanel
          status={app.status}
          action={watchKind}
          rollout={readRollout(app)}
          release={fetched?.currentRelease ?? null}
        />
      )}

      {/* Go-live reveal — once a watched deploy reaches "running" we surface the
          clickable HTTPS URL with copy + open. It also stays visible whenever the
          app is running and has a deployed host, so the URL is always reachable. */}
      {app &&
        ((justWentLive && app.status === "running") ||
          (app.status === "running" && !!app.host)) && (
          <LiveUrlCard
            url={appHttpsUrl(app, orgSlug)}
            celebrate={justWentLive}
            onDismiss={() => setJustWentLive(false)}
          />
        )}

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
            {app.image && (
              <InfoCard title="Image">
                <span className="inline-flex min-w-0 items-center gap-1.5 font-mono text-sm">
                  <Package className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{app.image}</span>
                </span>
              </InfoCard>
            )}
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

      {app && tab === "Releases" && (
        <TabPanel tab="Releases">
          <ReleasesTab
            appId={appId}
            currentRevision={fetched?.currentRelease?.revision ?? null}
            onChanged={refetch}
          />
        </TabPanel>
      )}

      {app && tab === "Builds" && (
        <TabPanel tab="Builds">
          <BuildsTab appId={appId} />
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
          <div className="space-y-6">
            <UpdateAppCard app={app} onUpdated={refetch} />
            <ScaleCard appId={appId} onScaled={refetch} />
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
          </div>
        </TabPanel>
      )}

      <ConfirmDialog
        open={confirmOpen}
        title="Delete app?"
        description={`This permanently removes ${app?.name ?? "this app"} and all of its machines. This action cannot be undone.`}
        confirmLabel="Delete app"
        destructive
        loading={deleting}
        onConfirm={onDelete}
        onCancel={() => setConfirmOpen(false)}
      />
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

  // Follow mode streams live lines over the SSE endpoint; off shows the static
  // snapshot tail (GET /logs). The toggle drives which source is active.
  const [follow, setFollow] = useState(false);

  const { data, loading, error, refetch } = useResource<string>(
    activeOrgId && !follow
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.getLogs(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    demo ? DEMO_LOGS.join("\n") : "",
    [activeOrgId, appId, follow],
  );

  // Streamed lines accumulate here while following; capped to MAX_LOG_LINES so a
  // long-lived stream can't grow the DOM unbounded.
  const [streamed, setStreamed] = useState<string[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);

  useEffect(() => {
    if (!follow || !activeOrgId) return;
    setStreamed([]);
    setStreamError(null);
    // The SSE stream sends cookies via a fetch reader; the disposer aborts it on
    // unmount or when follow is turned off (no EventSource/stream leak).
    const close = streamAppLogs(
      activeOrgId,
      appId,
      (line) =>
        setStreamed((prev) => {
          const next = [...prev, line];
          return next.length > MAX_LOG_LINES
            ? next.slice(-MAX_LOG_LINES)
            : next;
        }),
      (msg) => setStreamError(msg),
    );
    return close;
  }, [follow, activeOrgId, appId]);

  // Defensively cap the render to the most recent lines so a large tail can't
  // blow up the DOM; classify each line's level in the same pass.
  const lines = useMemo<{ text: string; level: string }[]>(() => {
    const source = follow ? streamed : data ? data.split("\n") : [];
    const tail =
      source.length > MAX_LOG_LINES ? source.slice(-MAX_LOG_LINES) : source;
    return tail.map((text) => ({ text, level: logLineClass(text) }));
  }, [follow, streamed, data]);

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
        <span className="font-mono text-xs text-muted-foreground">
          log tail — {appName}
        </span>
        <div className="flex items-center gap-2">
          {demo && !follow && data === DEMO_LOGS.join("\n") && (
            <Badge variant="outline">Demo</Badge>
          )}
          <Button
            variant={follow ? "primary" : "secondary"}
            size="sm"
            onClick={() => setFollow((f) => !f)}
            aria-pressed={follow}
          >
            {follow ? (
              <>
                <Square className="h-3.5 w-3.5" />
                Stop following
              </>
            ) : (
              <>
                <Rocket className="h-3.5 w-3.5" />
                Follow
              </>
            )}
          </Button>
          {!follow && (
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
          )}
        </div>
      </div>
      {!follow && error && !demo && (
        <Notice variant="error" className="m-4">
          <div className="flex items-center justify-between gap-3">
            <span>Could not load logs — the API is unreachable.</span>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => refetch()}
              loading={loading}
            >
              Retry
            </Button>
          </div>
        </Notice>
      )}
      {follow && streamError && (
        <Notice variant="error" className="m-4">
          {streamError}
        </Notice>
      )}
      {lines.length === 0 ? (
        <p className="px-4 py-8 text-center text-sm text-muted-foreground">
          {follow
            ? "Waiting for live log output…"
            : loading
              ? "Loading logs…"
              : "No log output yet for this app."}
        </p>
      ) : (
        <div className="scrollbar-thin max-h-[420px] overflow-y-auto bg-background p-4 font-mono text-xs leading-relaxed">
          {lines.map((line, i) => (
            <div key={i} className={cn("whitespace-pre-wrap", line.level)}>
              {line.text}
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Metrics tab
// ---------------------------------------------------------------------------

// Live pod metrics from the cluster metrics-server. There is NO synthetic data:
// when the backend reports `available: false` (no metrics-server, or the app
// isn't deployed) we render an honest "metrics unavailable" empty state rather
// than fabricate load — even in demo mode the empty default is honest.
const EMPTY_POD_METRICS: PodMetrics = {
  available: false,
  pods: [],
  cpuMillicores: 0,
  memoryBytes: 0,
};

function MetricsTab({ appId }: { appId: string }) {
  const { activeOrgId, authedCall } = useAuth();

  const { data, error, refetch } = useResource<PodMetrics>(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.getMetrics(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    EMPTY_POD_METRICS,
    [activeOrgId, appId],
    // Live snapshot: poll every 10s so the numbers stay current while viewing.
    { refetchIntervalMs: 10_000 },
  );

  if (!data.available) {
    return (
      <div className="space-y-4">
        {error && !isDemoMode() && (
          <FetchErrorNotice
            message="Could not load metrics — the API is unreachable."
            onRetry={refetch}
          />
        )}
        <Card className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <p className="text-sm text-muted-foreground">
            Metrics unavailable
            {data.unavailable ? ` — ${data.unavailable}.` : " for this app."}
          </p>
          <p className="text-xs text-muted-foreground">
            Live pod CPU/memory comes from the cluster metrics-server; nothing
            is shown until it reports.
          </p>
          <Button variant="secondary" size="sm" onClick={refetch}>
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </Button>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Short client-side rolling trend (last ~30 polls) rendered as small
          line charts; falls back to the live number until enough history is
          collected. No synthetic data — only values the backend reported. */}
      <MetricsTimeseries
        snapshot={data}
        formatMillicores={formatMillicores}
        formatBytes={formatBytes}
      />

      <Card>
        <CardHeader>
          <CardTitle>Pods ({data.pods.length})</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {data.pods.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              No running pods.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {data.pods.map((p) => (
                <li
                  key={p.pod}
                  className="flex items-center justify-between gap-4 px-6 py-3 text-sm"
                >
                  <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                    {p.pod}
                  </span>
                  <span className="flex shrink-0 items-center gap-4 tabular-nums">
                    <span className="inline-flex items-center gap-1.5">
                      <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
                      {formatMillicores(p.cpuMillicores)}
                    </span>
                    <span className="inline-flex items-center gap-1.5">
                      <MemoryStick className="h-3.5 w-3.5 text-muted-foreground" />
                      {formatBytes(p.memoryBytes)}
                    </span>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
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

  const { data, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listEnv(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    { data: demoEnv },
    [activeOrgId, appId, demoEnv],
  );

  const vars = data.data;

  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [secret, setSecret] = useState(false);
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
        api.setEnv(activeOrgId, appId, { key: k, value, secret }, token, on),
      );
      setKey("");
      setValue("");
      setSecret(false);
      refetch();
    } catch (err) {
      setNotice(
        `Could not save the variable — ${errorMessage(err, "the API is unreachable.")}`,
      );
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
    } catch (err) {
      setNotice(
        `Could not delete the variable — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setBusyKey(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && <Notice>{notice}</Notice>}

      {error && !isDemoMode() && (
        <FetchErrorNotice
          message="Could not load environment variables — the API is unreachable."
          onRetry={refetch}
        />
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
                // The API never returns a secret's value (it comes back empty),
                // so secret rows show a permanent masked placeholder and offer no
                // reveal — there is nothing client-side to reveal.
                const isSecret = v.secret === true;
                return (
                  <li key={v.key} className="flex items-center gap-4 px-6 py-3">
                    <span className="flex w-[200px] shrink-0 items-center gap-1.5 truncate font-mono text-sm text-foreground">
                      {isSecret && (
                        <Lock
                          className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
                          aria-label="Secret"
                        />
                      )}
                      <span className="truncate">{v.key}</span>
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono text-sm text-muted-foreground">
                      {isSecret
                        ? "•••••••• (secret, hidden)"
                        : show
                          ? v.value
                          : "•".repeat(Math.min(24, v.value.length || 8))}
                    </span>
                    {!isSecret ? (
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
                    ) : (
                      <span className="w-9" aria-hidden="true" />
                    )}
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
          <form onSubmit={onAdd} className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-[200px_1fr_auto] sm:items-end">
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
                  type={secret ? "password" : "text"}
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
            </div>
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input
                type="checkbox"
                checked={secret}
                onChange={(e) => setSecret(e.target.checked)}
                className="h-4 w-4 rounded border-border"
              />
              <Lock className="h-3.5 w-3.5" />
              Store as a secret (encrypted at rest; value never shown again)
            </label>
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

  const { data, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listDomains(activeOrgId, appId, token, on, { signal }),
            signal,
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
  const [verifyingId, setVerifyingId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // The DNS instructions for the most recently added/verified domain, surfaced
  // so the user knows exactly which records to publish.
  const [instructions, setInstructions] = useState<DomainResult | null>(null);

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
      const res = await authedCall((token, on) =>
        api.addDomain(activeOrgId, appId, d, token, on),
      );
      // Only surface the DNS instructions card when the API actually returned them.
      if (res.instructions) setInstructions(res);
      setDomain("");
      refetch();
    } catch (err) {
      setNotice(
        `Could not add the domain — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setPending(false);
    }
  }

  async function onVerify(id: string) {
    if (!activeOrgId) {
      setNotice("Verify unavailable — no active organization.");
      return;
    }
    setVerifyingId(id);
    setNotice(null);
    try {
      const res = await authedCall((token, on) =>
        api.verifyDomain(activeOrgId, appId, id, token, on),
      );
      if (res.instructions) setInstructions(res);
      if (res.status === "failed" || res.verified === false) {
        setNotice(
          `Verification failed for ${res.domain} — the TXT record was not found yet. DNS can take a few minutes to propagate.`,
        );
      }
      refetch();
    } catch (err) {
      setNotice(
        `Could not verify the domain — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setVerifyingId(null);
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
    } catch (err) {
      setNotice(
        `Could not delete the domain — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && <Notice>{notice}</Notice>}

      {error && !isDemoMode() && (
        <FetchErrorNotice
          message="Could not load domains — the API is unreachable."
          onRetry={refetch}
        />
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

      {instructions && (
        <DomainInstructionsCard
          result={instructions}
          onDismiss={() => setInstructions(null)}
        />
      )}

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
              {domains.map((d: Domain) => {
                // status is the source of truth; verified is the legacy mirror.
                const status =
                  d.status ?? (d.verified ? "verified" : "pending");
                return (
                  <li
                    key={d.id}
                    className="flex items-center justify-between gap-3 px-6 py-3"
                  >
                    <span className="inline-flex min-w-0 items-center gap-2 font-mono text-sm">
                      <Globe className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="truncate">{d.domain}</span>
                    </span>
                    <div className="flex shrink-0 items-center gap-3">
                      <DomainStatusBadge status={status} />
                      {status !== "verified" && (
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => onVerify(d.id)}
                          loading={verifyingId === d.id}
                          disabled={verifyingId === d.id}
                        >
                          {verifyingId !== d.id && (
                            <ShieldCheck className="h-3.5 w-3.5" />
                          )}
                          Verify
                        </Button>
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
                );
              })}
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
// Releases tab — deploy history + rollback
// ---------------------------------------------------------------------------

const RELEASE_VARIANT: Record<
  string,
  "success" | "warning" | "outline" | "destructive" | "info"
> = {
  active: "success",
  deploying: "warning",
  failed: "destructive",
  superseded: "outline",
  rolled_back: "info",
};

function ReleasesTab({
  appId,
  currentRevision,
  onChanged,
}: {
  appId: string;
  currentRevision: number | null;
  onChanged: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listReleases(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    { data: [] as Release[], page: { limit: 0, offset: 0, hasMore: false } },
    [activeOrgId, appId],
  );

  const releases = data.data;
  const [rollingBack, setRollingBack] = useState<number | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<Release | null>(null);

  async function onRollback(rev: number) {
    if (!activeOrgId) {
      setNotice("Rollback unavailable — no active organization.");
      return;
    }
    setRollingBack(rev);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.rollbackApp(activeOrgId, appId, rev, token, on),
      );
      setConfirm(null);
      refetch();
      onChanged();
    } catch (err) {
      setNotice(
        `Rollback failed — ${errorMessage(err, "the API is unreachable.")}`,
      );
    } finally {
      setRollingBack(null);
    }
  }

  return (
    <div className="space-y-4">
      {notice && <Notice variant="error">{notice}</Notice>}
      {error && !isDemoMode() && (
        <FetchErrorNotice
          message="Could not load releases — the API is unreachable."
          onRetry={refetch}
        />
      )}

      <Card>
        <CardHeader>
          <CardTitle>Release history</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {releases.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              {loading ? "Loading releases…" : "No releases yet."}
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {releases.map((r) => {
                const isCurrent =
                  r.revision === currentRevision || r.status === "active";
                return (
                  <li
                    key={r.id}
                    className="flex items-center justify-between gap-4 px-6 py-3"
                  >
                    <div className="flex items-center gap-3">
                      <History className="h-4 w-4 text-muted-foreground" />
                      <div className="min-w-0">
                        <p className="flex items-center gap-2 text-sm font-medium">
                          Revision {r.revision}
                          <Badge
                            variant={RELEASE_VARIANT[r.status] ?? "muted"}
                            className="capitalize"
                          >
                            {r.status.replace("_", " ")}
                          </Badge>
                        </p>
                        <p className="truncate font-mono text-xs text-muted-foreground">
                          {r.image || r.gitRef || "—"}
                          {r.note ? ` · ${r.note}` : ""}
                        </p>
                      </div>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={isCurrent || rollingBack !== null}
                      loading={rollingBack === r.revision}
                      onClick={() => setConfirm(r)}
                      title={
                        isCurrent
                          ? "This is the active release"
                          : `Roll back to revision ${r.revision}`
                      }
                    >
                      {rollingBack !== r.revision && (
                        <RotateCw className="h-3.5 w-3.5" />
                      )}
                      {isCurrent ? "Current" : "Rollback"}
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={confirm !== null}
        title="Roll back app?"
        description={
          confirm
            ? `Redeploy revision ${confirm.revision} (${confirm.image || confirm.gitRef || "snapshot"}). A new release is recorded for the rollback.`
            : undefined
        }
        confirmLabel="Roll back"
        loading={confirm ? rollingBack === confirm.revision : false}
        onConfirm={() => confirm && onRollback(confirm.revision)}
        onCancel={() => {
          if (rollingBack === null) setConfirm(null);
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Builds tab — git-source image builds + build logs
// ---------------------------------------------------------------------------

const BUILD_VARIANT: Record<
  string,
  "success" | "warning" | "outline" | "destructive"
> = {
  succeeded: "success",
  building: "warning",
  pending: "outline",
  failed: "destructive",
};

function BuildsTab({ appId }: { appId: string }) {
  const { activeOrgId, authedCall } = useAuth();

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listBuilds(activeOrgId, appId, token, on, { signal }),
            signal,
          )
      : null,
    { data: [] as Build[], page: { limit: 0, offset: 0, hasMore: false } },
    [activeOrgId, appId],
  );

  const builds = data.data;
  const [openId, setOpenId] = useState<string | null>(null);
  const [logs, setLogs] = useState<Record<string, string>>({});
  const [loadingLogs, setLoadingLogs] = useState<string | null>(null);

  async function toggleLogs(b: Build) {
    if (openId === b.id) {
      setOpenId(null);
      return;
    }
    setOpenId(b.id);
    // Build logs ride on the build record; fetch the single build to get them
    // when the list didn't include them.
    if (logs[b.id] === undefined && activeOrgId) {
      setLoadingLogs(b.id);
      try {
        const full = await authedCall((token, on) =>
          api.getBuild(activeOrgId, appId, b.id, token, on),
        );
        setLogs((l) => ({ ...l, [b.id]: full.logs ?? "" }));
      } catch (err) {
        setLogs((l) => ({
          ...l,
          [b.id]: `Could not load build logs — ${errorMessage(err, "the API is unreachable.")}`,
        }));
      } finally {
        setLoadingLogs(null);
      }
    }
  }

  return (
    <div className="space-y-4">
      {error && !isDemoMode() && (
        <FetchErrorNotice
          message="Could not load builds — the API is unreachable."
          onRetry={refetch}
        />
      )}
      <Card>
        <CardHeader>
          <CardTitle>Builds</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {builds.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              {loading ? "Loading builds…" : "No builds yet."}
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {builds.map((b) => (
                <li key={b.id} className="px-6 py-3">
                  <div className="flex items-center justify-between gap-4">
                    <div className="flex min-w-0 items-center gap-3">
                      <Hammer className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <div className="min-w-0">
                        <p className="flex items-center gap-2 text-sm font-medium">
                          <Badge
                            variant={BUILD_VARIANT[b.status] ?? "muted"}
                            className="capitalize"
                          >
                            {b.status}
                          </Badge>
                          <span className="truncate font-mono text-xs text-muted-foreground">
                            {b.commitRef || b.image || b.id}
                          </span>
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {new Date(b.createdAt).toLocaleString()}
                        </p>
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => toggleLogs(b)}
                    >
                      {openId === b.id ? "Hide logs" : "View logs"}
                    </Button>
                  </div>
                  {openId === b.id && (
                    <pre className="scrollbar-thin mt-3 max-h-72 overflow-auto whitespace-pre-wrap rounded-lg bg-background p-3 font-mono text-xs leading-relaxed text-muted-foreground">
                      {loadingLogs === b.id
                        ? "Loading logs…"
                        : logs[b.id] || "No logs captured for this build."}
                    </pre>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Settings — update app + scale
// ---------------------------------------------------------------------------

function UpdateAppCard({
  app,
  onUpdated,
}: {
  app: App;
  onUpdated: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();
  const [image, setImage] = useState(app.image ?? "");
  const [cpu, setCpu] = useState(String(app.cpu ?? ""));
  const [memoryMb, setMemoryMb] = useState(String(app.memoryMb ?? ""));
  const [gitRepository, setGitRepository] = useState(app.gitRepository ?? "");
  const [gitBranch, setGitBranch] = useState(app.gitBranch ?? "");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<{
    variant: "success" | "error";
    msg: string;
  } | null>(null);

  async function onSave(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!activeOrgId) {
      setNotice({ variant: "error", msg: "No active organization." });
      return;
    }
    // Only send fields that actually changed (PATCH applies what is present).
    const input: Parameters<typeof api.updateApp>[2] = {};
    if (image !== (app.image ?? "")) input.image = image;
    if (gitRepository !== (app.gitRepository ?? ""))
      input.gitRepository = gitRepository;
    if (gitBranch !== (app.gitBranch ?? "")) input.gitBranch = gitBranch;
    const cpuNum = Number(cpu);
    if (cpu !== "" && Number.isFinite(cpuNum) && cpuNum !== app.cpu)
      input.cpu = cpuNum;
    const memNum = Number(memoryMb);
    if (memoryMb !== "" && Number.isFinite(memNum) && memNum !== app.memoryMb)
      input.memoryMb = memNum;

    if (Object.keys(input).length === 0) {
      setNotice({ variant: "error", msg: "No changes to save." });
      return;
    }
    setSaving(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.updateApp(activeOrgId, app.id, input, token, on),
      );
      setNotice({ variant: "success", msg: "App updated." });
      onUpdated();
    } catch (err) {
      setNotice({
        variant: "error",
        msg: errorMessage(err, "Couldn't update the app."),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>App configuration</CardTitle>
      </CardHeader>
      <CardContent>
        {notice && (
          <Notice variant={notice.variant} className="mb-4">
            {notice.msg}
          </Notice>
        )}
        <form onSubmit={onSave} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="upd-image">Image</Label>
            <Input
              id="upd-image"
              className="font-mono"
              placeholder="registry/app:tag"
              value={image}
              onChange={(e) => setImage(e.target.value)}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="upd-cpu">CPU (vCPU)</Label>
              <Input
                id="upd-cpu"
                type="number"
                step="0.1"
                min="0"
                value={cpu}
                onChange={(e) => setCpu(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="upd-mem">Memory (MB)</Label>
              <Input
                id="upd-mem"
                type="number"
                step="64"
                min="0"
                value={memoryMb}
                onChange={(e) => setMemoryMb(e.target.value)}
              />
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="upd-repo">Git repository</Label>
              <Input
                id="upd-repo"
                className="font-mono"
                value={gitRepository}
                onChange={(e) => setGitRepository(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="upd-branch">Git branch</Label>
              <Input
                id="upd-branch"
                className="font-mono"
                value={gitBranch}
                onChange={(e) => setGitBranch(e.target.value)}
              />
            </div>
          </div>
          <div className="flex justify-end">
            <Button type="submit" loading={saving} disabled={!activeOrgId}>
              Save configuration
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function ScaleCard({
  appId,
  onScaled,
}: {
  appId: string;
  onScaled: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();
  const [minReplicas, setMin] = useState("");
  const [maxReplicas, setMax] = useState("");
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<{
    variant: "success" | "error";
    msg: string;
  } | null>(null);

  async function onScale(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!activeOrgId) {
      setNotice({ variant: "error", msg: "No active organization." });
      return;
    }
    const input: Parameters<typeof api.scaleApp>[2] = {};
    if (minReplicas !== "") input.minReplicas = Number(minReplicas);
    if (maxReplicas !== "") input.maxReplicas = Number(maxReplicas);
    if (input.minReplicas === undefined && input.maxReplicas === undefined) {
      setNotice({
        variant: "error",
        msg: "Set a min and/or max replica count.",
      });
      return;
    }
    setSaving(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.scaleApp(activeOrgId, appId, input, token, on),
      );
      setNotice({ variant: "success", msg: "Scaling applied." });
      onScaled();
    } catch (err) {
      setNotice({
        variant: "error",
        msg: errorMessage(err, "Couldn't scale the app."),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Scaling</CardTitle>
      </CardHeader>
      <CardContent>
        {notice && (
          <Notice variant={notice.variant} className="mb-4">
            {notice.msg}
          </Notice>
        )}
        <form
          onSubmit={onScale}
          className="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
        >
          <div className="space-y-2">
            <Label htmlFor="scale-min">Min replicas</Label>
            <Input
              id="scale-min"
              type="number"
              min="0"
              placeholder="1"
              value={minReplicas}
              onChange={(e) => setMin(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="scale-max">Max replicas</Label>
            <Input
              id="scale-max"
              type="number"
              min="0"
              placeholder="3"
              value={maxReplicas}
              onChange={(e) => setMax(e.target.value)}
            />
          </div>
          <Button type="submit" loading={saving} disabled={!activeOrgId}>
            Apply
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Rollout progress + go-live URL
// ---------------------------------------------------------------------------

// Live rollout status while a deploy/restart is in flight. Mirrors the real
// backend status (deploying → running) and surfaces replica/health detail ONLY
// when the API actually reports it — nothing is fabricated (invariant #6).
function RolloutPanel({
  status,
  action,
  rollout,
  release,
}: {
  status: string;
  action: ActionKind | null;
  rollout: RolloutInfo;
  release: Release | null;
}) {
  // statusVariant() can return "muted", which is not a Badge variant; map it to
  // the visually-equivalent "outline" so the Badge type stays satisfied.
  const sv = statusVariant(status);
  const badgeVariant = sv === "muted" ? "outline" : sv;
  const failed = status === "error";
  const running = status === "running";
  const stopped = status === "stopped";
  const verb =
    action === "restart"
      ? "Restarting"
      : action === "stop"
        ? "Stopping"
        : "Deploying";

  // Show "X/Y" only when the backend gave both numbers; otherwise omit it
  // rather than invent counts.
  const hasReplicas =
    rollout.readyReplicas !== undefined &&
    rollout.desiredReplicas !== undefined;
  const replicaLabel = hasReplicas
    ? `${rollout.readyReplicas}/${rollout.desiredReplicas} replicas ready`
    : null;

  // Health line, again only when the backend reports it.
  const healthLabel =
    rollout.health ??
    (rollout.healthy === true
      ? "healthy"
      : rollout.healthy === false
        ? "unhealthy"
        : null);

  // Determinate bar when replica counts are known; otherwise full on a settled
  // state (running/stopped/failed) and an indeterminate "in progress" shimmer
  // while still rolling out.
  const ready = rollout.readyReplicas ?? 0;
  const desired = rollout.desiredReplicas ?? 0;
  const settledNow = running || stopped || failed;
  const pct =
    hasReplicas && desired > 0
      ? Math.min(100, Math.round((ready / desired) * 100))
      : settledNow
        ? 100
        : null;

  return (
    <Card className="overflow-hidden">
      <CardContent className="space-y-3 py-4">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2.5">
            {failed ? (
              <AlertTriangle className="h-4 w-4 text-destructive" />
            ) : running || stopped ? (
              <CheckCircle2 className="h-4 w-4 text-success" />
            ) : (
              <Loader2 className="h-4 w-4 animate-spin text-warning motion-reduce:animate-none" />
            )}
            <span className="text-sm font-medium">
              {failed
                ? "Rollout failed"
                : running
                  ? "Rollout complete"
                  : stopped
                    ? "Stopped"
                    : `${verb}…`}
            </span>
            <Badge variant={badgeVariant} className="capitalize">
              {status || "pending"}
            </Badge>
          </div>
          {replicaLabel && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground">
              {replicaLabel}
            </span>
          )}
        </div>

        {/* Progress bar: determinate when replica counts are known, else an
            indeterminate sweep while the rollout is in flight. */}
        <div
          className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-label="Rollout progress"
          aria-valuenow={pct ?? undefined}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          {pct !== null ? (
            <div
              className={cn(
                "h-full rounded-full transition-[width] duration-500",
                failed
                  ? "bg-destructive"
                  : running
                    ? "bg-success"
                    : stopped
                      ? "bg-muted-foreground"
                      : "bg-warning",
              )}
              style={{ width: `${pct}%` }}
            />
          ) : (
            <div className="h-full w-1/3 animate-pulse rounded-full bg-warning motion-reduce:animate-none" />
          )}
        </div>

        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {healthLabel && (
            <span className="inline-flex items-center gap-1.5">
              <ShieldCheck className="h-3.5 w-3.5" />
              Health: <span className="capitalize">{healthLabel}</span>
            </span>
          )}
          {release && (
            <span className="inline-flex items-center gap-1.5 font-mono">
              <History className="h-3.5 w-3.5" />
              Revision {release.revision}
              <span className="capitalize">
                ({release.status.replace("_", " ")})
              </span>
            </span>
          )}
          {!settledNow && (
            <span>Watching the live status — this updates automatically.</span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

// Prominent reveal of the app's working HTTPS URL once it's live, with copy and
// open actions. Celebrates the first go-live moment, then persists as a quiet
// header while the app is running.
function LiveUrlCard({
  url,
  celebrate,
  onDismiss,
}: {
  url: string;
  celebrate: boolean;
  onDismiss: () => void;
}) {
  return (
    <Card
      className={cn(
        "overflow-hidden border-success/40",
        celebrate && "glow-violet",
      )}
    >
      <CardContent className="flex flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <CheckCircle2 className="h-5 w-5 shrink-0 text-success" />
          <div className="min-w-0">
            <p className="text-sm font-medium">
              {celebrate ? "Your app is live" : "Live URL"}
            </p>
            <a
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-w-0 max-w-full items-center gap-1.5 font-mono text-sm text-primary hover:underline"
            >
              <Globe className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate">{url}</span>
            </a>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <CopyButton value={url} label="Copy URL" />
          {/* Anchor styled as a primary button — the Button primitive only
              renders a <button>, so an <a> can't nest inside it. */}
          <a
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            className={cn(
              "inline-flex h-8 items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-3 text-xs font-medium transition-colors",
              "bg-primary text-primary-foreground shadow-sm hover:bg-primary/90",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
              "pointer-coarse:min-h-11",
            )}
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Open
          </a>
          {celebrate && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onDismiss}
              aria-label="Dismiss"
            >
              Dismiss
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
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

// Error banner shown above a tab's empty state when the fetch actually failed
// (gated `error && !isDemoMode()` at the call site, mirroring the Logs tab) so
// a real API failure is never disguised as a success-looking empty state.
function FetchErrorNotice({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <Notice variant="error">
      <div className="flex items-center justify-between gap-3">
        <span>{message}</span>
        <Button variant="secondary" size="sm" onClick={onRetry}>
          Retry
        </Button>
      </div>
    </Notice>
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

// Copy-to-clipboard button with a brief "copied" confirmation. Self-contained so
// it can sit next to any value (DNS records, connection strings, tokens).
function CopyButton({
  value,
  label = "Copy",
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  async function onCopy() {
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(value);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }
  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      onClick={onCopy}
      className={className}
      aria-label={label}
    >
      {copied ? (
        <Check className="h-3.5 w-3.5" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
      {copied ? "Copied" : label}
    </Button>
  );
}

function DomainStatusBadge({ status }: { status: string }) {
  if (status === "verified") {
    return (
      <Badge variant="success">
        <ShieldCheck className="mr-1 h-3 w-3" />
        Verified
      </Badge>
    );
  }
  if (status === "failed") {
    return (
      <Badge variant="destructive">
        <ShieldAlert className="mr-1 h-3 w-3" />
        Failed
      </Badge>
    );
  }
  return (
    <Badge variant="warning">
      <ShieldAlert className="mr-1 h-3 w-3" />
      Pending
    </Badge>
  );
}

// Shows the exact DNS records to publish for a custom domain: the TXT ownership
// challenge and the A/CNAME target, each with copy buttons.
function DomainInstructionsCard({
  result,
  onDismiss,
}: {
  result: DomainResult;
  onDismiss: () => void;
}) {
  const ins = result.instructions;
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>DNS records for {result.domain}</CardTitle>
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        <p className="text-muted-foreground">
          Publish these records at your DNS provider, then click Verify. The
          domain is not routed or issued TLS until ownership is proven.
        </p>
        <DnsRecordRow
          label="TXT (ownership challenge)"
          name={ins.txtName}
          value={ins.txtValue}
        />
        <DnsRecordRow
          label={`${ins.targetType || "CNAME"} (traffic target)`}
          name={result.domain}
          value={ins.targetValue}
        />
      </CardContent>
    </Card>
  );
}

function DnsRecordRow({
  label,
  name,
  value,
}: {
  label: string;
  name: string;
  value: string;
}) {
  return (
    <div className="space-y-1.5 rounded-lg border border-border p-3">
      <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
          {name}
        </code>
        <span className="text-muted-foreground">→</span>
        <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
          {value}
        </code>
        <CopyButton value={value} />
      </div>
    </div>
  );
}

// Format a millicores integer as cores (e.g. 1500m -> "1.5 cores", 250m -> "250m").
function formatMillicores(m: number): string {
  if (m >= 1000) return `${(m / 1000).toFixed(2).replace(/\.?0+$/, "")} cores`;
  return `${m}m`;
}

// Format a byte count as a human-readable MiB/GiB string.
function formatBytes(b: number): string {
  if (b <= 0) return "0 B";
  const gib = b / 1024 ** 3;
  if (gib >= 1) return `${gib.toFixed(2).replace(/\.?0+$/, "")} GiB`;
  const mib = b / 1024 ** 2;
  if (mib >= 1) return `${mib.toFixed(1).replace(/\.0$/, "")} MiB`;
  return `${(b / 1024).toFixed(0)} KiB`;
}

// Map a single log line to its level color class in one pass.
function logLineClass(line: string): string {
  if (line.includes("[error]")) return "text-destructive";
  if (line.includes("[warn]")) return "text-warning";
  return "text-muted-foreground";
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function formatMemory(mb: number): string {
  if (mb >= 1024 && mb % 1024 === 0) return `${mb / 1024} GB`;
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  return `${mb} MB`;
}

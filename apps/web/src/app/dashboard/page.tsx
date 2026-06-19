"use client";

import { useRef } from "react";
import Link from "next/link";
import { Boxes, Rocket, Globe2, ArrowUpRight, Database } from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  type App,
  type Database as DatabaseModel,
  type Settings,
} from "@/lib/api";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { errorMessage } from "@/lib/errors";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { OnboardingChecklist } from "@/components/onboarding-checklist";
import { StatCard } from "@/components/stat-card";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";
import { Notice } from "@/components/ui/notice";
import { Skeleton } from "@/components/ui/skeleton";

export default function DashboardOverview() {
  const { user, activeOrgId, authedCall } = useAuth();

  // Demo fallbacks load lazily (and only in demo mode) so mock data is never
  // shipped to / shown in production.
  const demoApps = useDemoData((m) => m.mockApps, [] as App[]);
  const demoDatabases = useDemoData(
    (m) => m.mockDatabases,
    [] as DatabaseModel[],
  );
  const demoSettings = useDemoData<Settings | null>(
    (m) => m.mockSettings,
    null,
  );

  // useResource only reports a boolean `error`; capture each fetcher's actual
  // failure here so we can show the real ApiError message instead of a generic
  // placeholder (invariant #6: honesty over fake-success).
  const appsErrorRef = useRef<string | null>(null);
  const dbErrorRef = useRef<string | null>(null);

  const {
    data,
    loading: appsLoading,
    error: appsError,
    errorStatus: appsStatus,
    refetch: refetchApps,
  } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listApps(activeOrgId, token, on, { signal })
                .then((res) => {
                  appsErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  appsErrorRef.current = errorMessage(
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
    { cacheKey: activeOrgId ? `apps:${activeOrgId}` : undefined },
  );
  const apps = data.data;

  const {
    data: dbData,
    loading: dbLoading,
    error: dbError,
    errorStatus: dbStatus,
    refetch: refetchDatabases,
  } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listDatabases(activeOrgId, token, on, { signal })
                .then((res) => {
                  dbErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  dbErrorRef.current = errorMessage(
                    err,
                    "Failed to load databases.",
                  );
                  throw err;
                }),
            signal,
          )
      : null,
    { data: demoDatabases },
    [activeOrgId, demoDatabases],
    { cacheKey: activeOrgId ? `databases:${activeOrgId}` : undefined },
  );
  const databases = dbData.data;

  // Regions are platform config (admin settings); fall back to mock when the
  // public/admin endpoint is unreachable or the user isn't an admin.
  const { data: settings } = useResource(
    user?.isAdmin
      ? () => authedCall((token, on) => api.getSettings(token, on))
      : null,
    demoSettings,
    [user?.isAdmin, demoSettings],
  );
  const regions = settings?.regions ?? [];

  const running = apps.filter((a) => a.status === "running").length;
  const firstName = (user?.name ?? "there").split(" ")[0];

  // Surface real failures instead of silently rendering empty/zero stats.
  // In demo mode the hook falls back to mock data, so don't alarm there.
  const demo = isDemoMode();
  const fetchFailed = !demo && (appsError || dbError);
  // A 403 means the account simply lacks access — that is not retryable, so we
  // distinguish it from an unreachable/transient failure (which gets Retry).
  const forbidden = fetchFailed && (appsStatus === 403 || dbStatus === 403);
  // The real backend message for the failure (falls back to a generic string
  // only on true transport errors, via errorMessage at the fetcher).
  const fetchErrorText =
    appsErrorRef.current ?? dbErrorRef.current ?? "Failed to load dashboard.";
  // Initial load: no data yet and the fetchers are still in flight.
  const initialLoading = appsLoading || dbLoading;

  const retry = () => {
    if (appsError) refetchApps();
    if (dbError) refetchDatabases();
  };

  return (
    <div className="space-y-8">
      <PageHeader
        title={`Welcome back, ${firstName}`}
        description="Here's what's happening across your organization."
        actions={
          <Link href="/dashboard/apps">
            <Button>
              <Rocket className="h-4 w-4" />
              New App
            </Button>
          </Link>
        }
      />

      {fetchFailed ? (
        forbidden ? (
          <Notice variant="warning">
            <span>
              You don&apos;t have permission to view these stats. Ask an
              organization admin for access.
            </span>
          </Notice>
        ) : (
          <Notice variant="error">
            <div className="flex items-start justify-between gap-3">
              <span>{fetchErrorText}</span>
              <Button
                variant="secondary"
                size="sm"
                className="shrink-0"
                onClick={retry}
              >
                Retry
              </Button>
            </div>
          </Notice>
        )
      ) : null}

      <div className="grid gap-4 sm:grid-cols-3">
        {initialLoading ? (
          <>
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-28 w-full" />
          </>
        ) : (
          <>
            <StatCard
              label="Apps"
              value={apps.length}
              icon={Boxes}
              hint={`${running} running`}
            />
            <StatCard
              label="Databases"
              value={databases.length}
              icon={Database}
              hint={`${databases.filter((d) => d.status === "running").length} running`}
            />
            <StatCard
              label="Regions"
              value={regions.length}
              icon={Globe2}
              hint={
                regions.length > 0
                  ? regions.slice(0, 3).join(", ") +
                    (regions.length > 3 ? "…" : "")
                  : "—"
              }
            />
          </>
        )}
      </div>

      {/* First-run getting-started checklist. Derived from the apps already
          loaded above (no extra fetches) and self-hides once the org has a
          deployed app or the user dismisses it. Only render once apps have
          loaded without error so completion is based on real data, not an
          empty/loading state (invariant #6). */}
      {!fetchFailed && !initialLoading ? (
        <OnboardingChecklist apps={apps} orgId={activeOrgId} />
      ) : null}

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <CardTitle>Recent apps</CardTitle>
          <Link
            href="/dashboard/apps"
            className="text-sm text-primary hover:underline"
          >
            View all
          </Link>
        </CardHeader>
        <CardContent className="p-0">
          <ul className="divide-y divide-border">
            {apps.slice(0, 5).map((app) => (
              <li key={app.id}>
                <Link
                  href={`/dashboard/apps/${app.id}`}
                  className="flex items-center justify-between px-6 py-4 transition-colors hover:bg-muted/50"
                >
                  <div className="flex items-center gap-3">
                    <StatusDot status={app.status} />
                    <div>
                      <p className="text-sm font-medium">{app.name}</p>
                      <p className="font-mono text-xs text-muted-foreground">
                        {app.gitRepository}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    <span className="hidden font-mono text-xs text-muted-foreground sm:inline">
                      {app.gitBranch}
                    </span>
                    <ArrowUpRight className="h-4 w-4 text-muted-foreground" />
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}

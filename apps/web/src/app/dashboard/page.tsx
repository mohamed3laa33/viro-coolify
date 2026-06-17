"use client";

import Link from "next/link";
import { Boxes, Rocket, Globe2, ArrowUpRight, Database } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { mockApps, mockDatabases, mockSettings } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";

export default function DashboardOverview() {
  const { user, activeOrgId, authedCall } = useAuth();

  const { data } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listApps(activeOrgId, token, on))
      : null,
    { data: mockApps },
    [activeOrgId],
  );
  const apps = data.data;

  const { data: dbData } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listDatabases(activeOrgId, token, on))
      : null,
    { data: mockDatabases },
    [activeOrgId],
  );
  const databases = dbData.data;

  // Regions are platform config (admin settings); fall back to mock when the
  // public/admin endpoint is unreachable or the user isn't an admin.
  const { data: settings } = useResource(
    user?.isAdmin
      ? () => authedCall((token, on) => api.getSettings(token, on))
      : null,
    mockSettings,
    [user?.isAdmin],
  );
  const regions = settings.regions ?? [];

  const running = apps.filter((a) => a.status === "running").length;
  const firstName = (user?.name ?? "there").split(" ")[0];

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

      <div className="grid gap-4 sm:grid-cols-3">
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
              ? regions.slice(0, 3).join(", ") + (regions.length > 3 ? "…" : "")
              : "—"
          }
        />
      </div>

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

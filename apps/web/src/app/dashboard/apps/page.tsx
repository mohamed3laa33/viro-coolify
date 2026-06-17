"use client";

import { useState } from "react";
import Link from "next/link";
import { Plus, Search, GitBranch, Package } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { mockApps } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge } from "@/components/ui/badge";

export default function AppsPage() {
  const { accessToken } = useAuth();
  const [query, setQuery] = useState("");

  const { data } = useResource(
    () => api.listApps(accessToken ?? ""),
    { data: mockApps },
    [accessToken],
  );

  const apps = data.data.filter((a) =>
    a.name.toLowerCase().includes(query.toLowerCase()),
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Apps"
        description="Deploy and manage your applications across regions."
        actions={
          <Button>
            <Plus className="h-4 w-4" />
            New App
          </Button>
        }
      />

      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          className="pl-9"
          placeholder="Search apps…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {apps.map((app) => (
          <Link key={app.uuid} href={`/dashboard/apps/${app.uuid}`}>
            <Card className="h-full p-5 transition-colors hover:border-primary/40">
              <div className="flex items-start justify-between">
                <div className="min-w-0">
                  <p className="truncate font-medium">{app.name}</p>
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {app.fqdn}
                  </p>
                </div>
                <StatusDot status={app.status} />
              </div>

              <div className="mt-5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span className="inline-flex items-center gap-1">
                  <GitBranch className="h-3.5 w-3.5" />
                  {app.git_branch}
                </span>
                <span className="inline-flex items-center gap-1">
                  <Package className="h-3.5 w-3.5" />
                  {app.build_pack}
                </span>
              </div>

              <div className="mt-4">
                <Badge variant="outline">{app.git_repository}</Badge>
              </div>
            </Card>
          </Link>
        ))}
      </div>

      {apps.length === 0 && (
        <Card className="flex flex-col items-center justify-center py-16 text-center">
          <p className="text-sm text-muted-foreground">
            No apps match “{query}”.
          </p>
        </Card>
      )}
    </div>
  );
}

"use client";

import { Globe, Plus, ShieldCheck, ShieldAlert } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App, type Domain } from "@/lib/api";
import { mockApps, mockDomains } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

// A custom domain joined to the app it routes to.
interface DomainRow extends Domain {
  app: string;
}

export default function DomainsPage() {
  const { activeOrgId, authedCall } = useAuth();

  // Load the org's apps, then fan out to each app's domains. Falls back to mock
  // data when the API is unreachable or no org is active.
  const { data: appsData } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listApps(activeOrgId, token, on))
      : null,
    { data: mockApps },
    [activeOrgId],
  );
  const apps = appsData.data;

  const { data: rows } = useResource<DomainRow[]>(
    activeOrgId
      ? () =>
          authedCall(async (token, on) => {
            const lists = await Promise.all(
              apps.map(async (app: App) => {
                try {
                  const res = await api.listDomains(
                    activeOrgId,
                    app.id,
                    token,
                    on,
                  );
                  return (res.data ?? []).map((d) => ({ ...d, app: app.name }));
                } catch {
                  return [] as DomainRow[];
                }
              }),
            );
            return lists.flat();
          })
      : null,
    mockDomains.map((d) => ({ ...d, app: mockApps[0]?.name ?? "—" })),
    [activeOrgId, apps.map((a) => a.id).join(",")],
  );

  const domains = rows ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Domains"
        description="Custom domains with automatic, zero-config TLS certificates."
        actions={
          <Button>
            <Plus className="h-4 w-4" />
            Add domain
          </Button>
        }
      />

      <Card>
        <CardContent className="p-0">
          {domains.length === 0 ? (
            <p className="px-6 py-10 text-center text-sm text-muted-foreground">
              No custom domains yet. Add one from an app&apos;s Domains tab.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {domains.map((d) => (
                <li
                  key={d.id}
                  className="flex items-center justify-between px-6 py-4"
                >
                  <div className="flex items-center gap-3">
                    <Globe className="h-4 w-4 text-muted-foreground" />
                    <div>
                      <p className="font-mono text-sm">{d.domain}</p>
                      <p className="text-xs text-muted-foreground">
                        routes to {d.app}
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-4">
                    {d.verified ? (
                      <Badge variant="success">
                        <ShieldCheck className="mr-1 h-3 w-3" />
                        TLS
                      </Badge>
                    ) : (
                      <Badge variant="warning">
                        <ShieldAlert className="mr-1 h-3 w-3" />
                        Pending
                      </Badge>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

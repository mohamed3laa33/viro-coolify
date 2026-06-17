"use client";

import { Plus, Database as DatabaseIcon } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Template } from "@/lib/api";
import { mockDatabases, mockTemplates } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge } from "@/components/ui/badge";

// Accent colour per database key; falls back to muted for unknown engines.
const ENGINE_ACCENT: Record<string, string> = {
  postgresql: "text-info",
  mysql: "text-warning",
  mariadb: "text-warning",
  mongodb: "text-success",
  redis: "text-destructive",
};

function engineAccent(key: string): string {
  return ENGINE_ACCENT[key] ?? "text-muted-foreground";
}

export default function DatabasesPage() {
  const { activeOrgId, authedCall } = useAuth();

  const { data } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listDatabases(activeOrgId, token, on))
      : null,
    { data: mockDatabases },
    [activeOrgId],
  );
  const databases = data.data;

  // Engine catalog is driven by admin templates of kind "database".
  const { data: templatesData } = useResource(
    () => authedCall((token, on) => api.listTemplates(token, on)),
    { data: mockTemplates },
    [],
  );
  const engineTemplates: Template[] = templatesData.data
    .filter((t) => t.kind === "database" && t.active)
    .sort((a, b) => a.sortOrder - b.sortOrder);

  // Map a database engine string to its catalog template (for the label).
  function engineLabel(engine: string): string {
    return (
      templatesData.data.find((t) => t.key === engine)?.name ?? engine
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Databases"
        description="Managed Postgres, Redis, MySQL, and MongoDB with automated backups."
        actions={
          <Button>
            <Plus className="h-4 w-4" />
            Create database
          </Button>
        }
      />

      {/* Engine CTA strip — sourced from the launchable catalog */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {engineTemplates.map((tpl) => (
          <Card
            key={tpl.key}
            className="flex items-center justify-between p-4 transition-colors hover:border-primary/40"
          >
            <div className="flex items-center gap-3">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted">
                <DatabaseIcon
                  className={engineAccent(tpl.key) + " h-4 w-4"}
                />
              </div>
              <span className="text-sm font-medium">{tpl.name}</span>
            </div>
            <Button variant="ghost" size="sm">
              <Plus className="h-4 w-4" />
            </Button>
          </Card>
        ))}
      </div>

      <Card>
        <CardContent className="p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-6 py-3 font-medium">Name</th>
                <th className="px-6 py-3 font-medium">Engine</th>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 font-medium">ID</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {databases.map((db) => {
                return (
                  <tr key={db.id} className="hover:bg-muted/40">
                    <td className="px-6 py-4 font-medium">{db.name}</td>
                    <td className="px-6 py-4">
                      <Badge variant="outline">{engineLabel(db.engine)}</Badge>
                    </td>
                    <td className="px-6 py-4">
                      <StatusDot status={db.status} showLabel />
                    </td>
                    <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                      {db.id}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}

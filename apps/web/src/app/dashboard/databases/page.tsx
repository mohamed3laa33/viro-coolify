"use client";

import { Plus, Database as DatabaseIcon } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { mockDatabases } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge } from "@/components/ui/badge";

const ENGINE_META: Record<string, { label: string; accent: string }> = {
  postgresql: { label: "PostgreSQL", accent: "text-info" },
  mysql: { label: "MySQL", accent: "text-warning" },
  mariadb: { label: "MariaDB", accent: "text-warning" },
  mongodb: { label: "MongoDB", accent: "text-success" },
  redis: { label: "Redis", accent: "text-destructive" },
};

function engineMeta(engine: string): { label: string; accent: string } {
  return ENGINE_META[engine] ?? { label: engine, accent: "text-muted-foreground" };
}

const ENGINES = ["postgresql", "mysql", "mongodb", "redis"];

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

      {/* Engine CTA strip */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {ENGINES.map((engine) => {
          const meta = engineMeta(engine);
          return (
            <Card
              key={engine}
              className="flex items-center justify-between p-4 transition-colors hover:border-primary/40"
            >
              <div className="flex items-center gap-3">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted">
                  <DatabaseIcon className={meta.accent + " h-4 w-4"} />
                </div>
                <span className="text-sm font-medium">{meta.label}</span>
              </div>
              <Button variant="ghost" size="sm">
                <Plus className="h-4 w-4" />
              </Button>
            </Card>
          );
        })}
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
                const meta = engineMeta(db.engine);
                return (
                  <tr key={db.id} className="hover:bg-muted/40">
                    <td className="px-6 py-4 font-medium">{db.name}</td>
                    <td className="px-6 py-4">
                      <Badge variant="outline">{meta.label}</Badge>
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

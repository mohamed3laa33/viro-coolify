"use client";

import { useState, type FormEvent } from "react";
import { Plus, Database as DatabaseIcon } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Template } from "@/lib/api";
import { mockDatabases, mockTemplates } from "@/lib/mock";
import { isDemoMode } from "@/lib/demo";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
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

  const demo = isDemoMode();
  const { data, error, refetch } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listDatabases(activeOrgId, token, on))
      : null,
    { data: demo ? mockDatabases : [] },
    [activeOrgId],
  );
  const databases = data.data;

  // Engine catalog is driven by the public services catalog (kind "database").
  const { data: templatesData } = useResource(
    () => api.getServiceCatalog(),
    { data: demo ? mockTemplates : [] },
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

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [engine, setEngine] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  function startCreate(presetEngine?: string) {
    setEngine(presetEngine ?? engineTemplates[0]?.key ?? "postgresql");
    setNotice(null);
    setCreating(true);
  }

  async function onCreate(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    if (!activeOrgId) {
      setNotice("Create unavailable — no active organization.");
      return;
    }
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.createDatabase(activeOrgId, { name: trimmed, engine }, token, on),
      );
      setName("");
      setCreating(false);
      refetch();
    } catch {
      setNotice("Could not create the database — the API is unreachable.");
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Databases"
        description="Managed Postgres, Redis, MySQL, and MongoDB with automated backups."
        actions={
          <Button onClick={() => startCreate()}>
            <Plus className="h-4 w-4" />
            Create database
          </Button>
        }
      />

      {creating && (
        <Card>
          <CardHeader>
            <CardTitle>Create database</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {notice && <Notice variant="error">{notice}</Notice>}
            <form
              onSubmit={onCreate}
              className="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
            >
              <div className="space-y-2">
                <Label htmlFor="db-name">Name</Label>
                <Input
                  id="db-name"
                  placeholder="primary-postgres"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  autoFocus
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="db-engine">Engine</Label>
                <Select
                  id="db-engine"
                  value={engine}
                  onChange={(e) => setEngine(e.target.value)}
                >
                  {engineTemplates.length === 0 && (
                    <option value="postgresql">PostgreSQL</option>
                  )}
                  {engineTemplates.map((tpl) => (
                    <option key={tpl.key} value={tpl.key}>
                      {tpl.name}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="flex items-center gap-2">
                <Button type="submit" loading={pending}>
                  Create
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setCreating(false);
                    setName("");
                  }}
                >
                  Cancel
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

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
            <Button
              variant="ghost"
              size="sm"
              onClick={() => startCreate(tpl.key)}
              aria-label={`Create ${tpl.name} database`}
            >
              <Plus className="h-4 w-4" />
            </Button>
          </Card>
        ))}
      </div>

      {error && !demo ? (
        <Notice variant="error">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span>Could not load databases — the API is unreachable.</span>
            <Button variant="secondary" size="sm" onClick={refetch}>
              Retry
            </Button>
          </div>
        </Notice>
      ) : databases.length === 0 ? (
        <EmptyState
          icon={DatabaseIcon}
          title="No databases yet"
          description="Spin up a managed Postgres, Redis, MySQL, or MongoDB database with automated backups."
          action={
            <Button onClick={() => startCreate()}>
              <Plus className="h-4 w-4" />
              Create database
            </Button>
          }
        />
      ) : (
        <Card>
          <CardContent className="p-0">
            <div className="overflow-x-auto">
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
                          <Badge variant="outline">
                            {engineLabel(db.engine)}
                          </Badge>
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
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

"use client";

import { useState, type FormEvent } from "react";
import { Plus, Trash2, Database as DatabaseIcon } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Database, type Template } from "@/lib/api";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { errorMessage } from "@/lib/errors";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

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

  // Demo fallbacks load lazily (demo mode only); never shipped to prod.
  const demoDatabases = useDemoData((m) => m.mockDatabases, [] as Database[]);
  const demoTemplates = useDemoData((m) => m.mockTemplates, [] as Template[]);

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listDatabases(activeOrgId, token, on, { signal }),
            signal,
          )
      : null,
    { data: demoDatabases },
    [activeOrgId, demoDatabases],
    { cacheKey: activeOrgId ? `databases:${activeOrgId}` : undefined },
  );
  const databases = data.data;
  const showError = error && !demo;

  // Engine catalog is driven by the public services catalog (kind "database").
  const { data: templatesData } = useResource(
    (signal) => api.getServiceCatalog({ signal }),
    { data: demoTemplates },
    [demoTemplates],
    { cacheKey: "catalog" },
  );
  const engineTemplates: Template[] = templatesData.data
    .filter((t) => t.kind === "database" && t.active)
    .sort((a, b) => a.sortOrder - b.sortOrder);

  // Invariant #1: business values come from the API/admin catalog. When the
  // catalog is empty in production we must NOT fall back to a hardcoded engine —
  // the form is disabled and an info notice explains why.
  const noEngines = !demo && engineTemplates.length === 0;

  // Map a database engine string to its catalog template (for the label).
  function engineLabel(engine: string): string {
    return templatesData.data.find((t) => t.key === engine)?.name ?? engine;
  }

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [engine, setEngine] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Per-row delete state.
  const [busy, setBusy] = useState<Record<string, "delete">>({});
  const [rowNotice, setRowNotice] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<Database | null>(null);

  function startCreate(presetEngine?: string) {
    // Fall back to the first catalog engine, never a hardcoded one — when the
    // catalog is empty this stays "" so the disabled form cannot submit a value.
    setEngine(presetEngine ?? engineTemplates[0]?.key ?? "");
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
    if (!engine) {
      setNotice("Select a database engine from the catalog.");
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
    } catch (err) {
      setNotice(errorMessage(err, "Could not create the database."));
    } finally {
      setPending(false);
    }
  }

  async function onConfirmDelete() {
    const db = confirmDelete;
    if (!db) return;
    if (!activeOrgId) {
      setRowNotice("Delete unavailable — no active organization.");
      setConfirmDelete(null);
      return;
    }
    setBusy((b) => ({ ...b, [db.id]: "delete" }));
    setRowNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteDatabase(activeOrgId, db.id, token, on),
      );
      setConfirmDelete(null);
      refetch();
    } catch (err) {
      setRowNotice(errorMessage(err, `Could not delete ${db.name}.`));
    } finally {
      setBusy((b) => {
        const next = { ...b };
        delete next[db.id];
        return next;
      });
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
            {noEngines && (
              <Notice variant="info">
                No database engines are available in the catalog yet.
              </Notice>
            )}
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
                  disabled={noEngines}
                >
                  {engineTemplates.length === 0 && (
                    <option value="">No engines available</option>
                  )}
                  {engineTemplates.map((tpl) => (
                    <option key={tpl.key} value={tpl.key}>
                      {tpl.name}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="flex items-center gap-2">
                <Button type="submit" loading={pending} disabled={noEngines}>
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
                <DatabaseIcon className={engineAccent(tpl.key) + " h-4 w-4"} />
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

      {rowNotice && <Notice variant="error">{rowNotice}</Notice>}

      {showError ? (
        <Notice variant="error">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span>Could not load databases — the API is unreachable.</span>
            <Button variant="secondary" size="sm" onClick={refetch}>
              Retry
            </Button>
          </div>
        </Notice>
      ) : loading && databases.length === 0 ? (
        <Card>
          <CardContent className="space-y-3 p-6">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
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
                    <th className="px-6 py-3 text-right font-medium">
                      Actions
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {databases.map((db) => {
                    const deleting = busy[db.id] === "delete";
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
                        <td className="px-6 py-4">
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={deleting}
                              disabled={deleting}
                              onClick={() => setConfirmDelete(db)}
                              aria-label={`Delete ${db.name}`}
                              title="Delete"
                              className="text-destructive hover:text-destructive"
                            >
                              {!deleting && <Trash2 className="h-4 w-4" />}
                            </Button>
                          </div>
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

      <ConfirmDialog
        open={confirmDelete !== null}
        title={
          confirmDelete ? `Delete ${confirmDelete.name}?` : "Delete database?"
        }
        description="This permanently removes the database and its Kubernetes release. This action cannot be undone."
        confirmLabel="Delete database"
        destructive
        loading={confirmDelete ? busy[confirmDelete.id] === "delete" : false}
        onConfirm={onConfirmDelete}
        onCancel={() => setConfirmDelete(null)}
      />
    </div>
  );
}

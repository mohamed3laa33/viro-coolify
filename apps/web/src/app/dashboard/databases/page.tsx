"use client";

import { Fragment, useCallback, useState, type FormEvent } from "react";
import {
  Plus,
  Trash2,
  Database as DatabaseIcon,
  Play,
  Square,
  RotateCw,
  Plug,
  Copy,
  Check,
  Eye,
  EyeOff,
  Loader2,
  Archive,
  History,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  type Database,
  type DatabaseDetail,
  type Template,
  type ListResponse,
  type OnUnauthorized,
  type CallOpts,
} from "@/lib/api";
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

// ---------------------------------------------------------------------------
// Backup / restore contract.
//
// A managed database's point-in-time backup. Mirrors the backend `DatabaseBackup`
// shape this UI expects once the backup endpoints land. Defined locally (not in
// lib/api) so this page degrades honestly when the API client predates the
// feature — see `backupApi` below.
// ---------------------------------------------------------------------------
interface DatabaseBackup {
  id: string;
  databaseId: string;
  // pending | running | completed | failed (open string from the backend).
  status: string;
  // ISO timestamps. createdAt is when the backup was requested; completedAt is
  // set once it finishes. sizeBytes is reported by the backend when known.
  createdAt: string;
  completedAt?: string;
  sizeBytes?: number;
  // Optional human-readable failure reason surfaced verbatim (invariant #6).
  error?: string;
}

// The three methods this page calls when the API client exposes them. Kept as a
// structural interface so we can feature-detect them on the runtime `api` object
// without assuming `lib/api` already declares them (Wave 0 / a parallel agent
// may add them; this branch must not break the build if they are absent).
interface BackupApi {
  createDatabaseBackup(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<DatabaseBackup>;
  listDatabaseBackups(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<DatabaseBackup>>;
  restoreDatabaseBackup(
    orgId: string,
    databaseId: string,
    backupId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database>;
}

// Runtime feature-detection: returns the typed backup API only when ALL three
// methods are present on the client. Until the backend + client ship these
// endpoints, the UI renders an honest "not available yet" state rather than a
// fake-success action (invariant #6). No `any`/`ts-ignore`: we narrow through a
// record of `unknown` values.
function resolveBackupApi(): BackupApi | null {
  const candidate = api as unknown as Record<string, unknown>;
  const required = [
    "createDatabaseBackup",
    "listDatabaseBackups",
    "restoreDatabaseBackup",
  ] as const;
  if (required.every((name) => typeof candidate[name] === "function")) {
    return api as unknown as BackupApi;
  }
  return null;
}

// Map a backup status to a Badge variant (mirrors statusVariant's intent for
// the backup lifecycle).
function backupBadgeVariant(
  status: string,
): "success" | "warning" | "destructive" | "outline" {
  switch (status) {
    case "completed":
      return "success";
    case "pending":
    case "running":
      return "warning";
    case "failed":
      return "destructive";
    default:
      return "outline";
  }
}

// Human-readable timestamp; falls back to the raw value if it cannot be parsed.
function formatTimestamp(iso: string | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

// Human-readable backup size; absent when the backend has not reported it.
function formatSize(bytes: number | undefined): string | null {
  if (bytes === undefined || bytes < 0) return null;
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return `${value.toFixed(value >= 10 || Number.isInteger(value) ? 0 : 1)} ${units[i]}`;
}

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

  // Per-row action state (delete + lifecycle).
  type RowAction = "delete" | "deploy" | "stop" | "restart";
  const [busy, setBusy] = useState<Record<string, RowAction>>({});
  const [rowNotice, setRowNotice] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<Database | null>(null);

  // The database whose connection info is open (lazily fetched), plus its detail.
  const [connOpen, setConnOpen] = useState<string | null>(null);
  const [connDetail, setConnDetail] = useState<Record<string, DatabaseDetail>>(
    {},
  );
  const [connLoading, setConnLoading] = useState<string | null>(null);
  const [connError, setConnError] = useState<string | null>(null);

  // Backup / restore. `backupApi` is null until the client exposes the backup
  // endpoints, in which case the panel renders an honest unavailable state.
  const [backupApi] = useState<BackupApi | null>(() => resolveBackupApi());
  // The database whose backups panel is open (mutually exclusive with conn).
  const [backupsOpen, setBackupsOpen] = useState<string | null>(null);
  const [backups, setBackups] = useState<Record<string, DatabaseBackup[]>>({});
  const [backupsLoading, setBackupsLoading] = useState<string | null>(null);
  const [backupsError, setBackupsError] = useState<string | null>(null);
  // The database currently having a backup created (its id), and the backup the
  // user has asked to restore (behind the destructive ConfirmDialog).
  const [backingUp, setBackingUp] = useState<string | null>(null);
  const [confirmRestore, setConfirmRestore] = useState<{
    db: Database;
    backup: DatabaseBackup;
  } | null>(null);
  const [restoring, setRestoring] = useState<string | null>(null);

  const loadBackups = useCallback(
    async (db: Database) => {
      if (!backupApi || !activeOrgId) return;
      setBackupsLoading(db.id);
      setBackupsError(null);
      try {
        const res = await authedCall((token, on) =>
          backupApi.listDatabaseBackups(activeOrgId, db.id, token, on),
        );
        setBackups((b) => ({ ...b, [db.id]: res.data }));
      } catch (err) {
        setBackupsError(errorMessage(err, "Could not load backups."));
      } finally {
        setBackupsLoading(null);
      }
    },
    [backupApi, activeOrgId, authedCall],
  );

  function toggleBackups(db: Database) {
    if (backupsOpen === db.id) {
      setBackupsOpen(null);
      return;
    }
    // Keep panels mutually exclusive so a row never has two expanded drawers.
    setConnOpen(null);
    setBackupsOpen(db.id);
    setBackupsError(null);
    if (backupApi && backups[db.id] === undefined) {
      void loadBackups(db);
    }
  }

  async function onBackupNow(db: Database) {
    if (!backupApi) return;
    if (!activeOrgId) {
      setBackupsError("Backup unavailable — no active organization.");
      return;
    }
    setBackingUp(db.id);
    setBackupsError(null);
    try {
      await authedCall((token, on) =>
        backupApi.createDatabaseBackup(activeOrgId, db.id, token, on),
      );
      // Re-list so the new (pending) backup appears with its real status/time.
      await loadBackups(db);
    } catch (err) {
      setBackupsError(errorMessage(err, `Could not back up ${db.name}.`));
    } finally {
      setBackingUp(null);
    }
  }

  async function onConfirmRestore() {
    const target = confirmRestore;
    if (!target || !backupApi) {
      setConfirmRestore(null);
      return;
    }
    const { db, backup } = target;
    if (!activeOrgId) {
      setBackupsError("Restore unavailable — no active organization.");
      setConfirmRestore(null);
      return;
    }
    setRestoring(backup.id);
    setBackupsError(null);
    try {
      await authedCall((token, on) =>
        backupApi.restoreDatabaseBackup(
          activeOrgId,
          db.id,
          backup.id,
          token,
          on,
        ),
      );
      setConfirmRestore(null);
      // The database re-deploys from the snapshot; refresh the row status and
      // the backups list (a restore may record its own audit entry).
      refetch();
      await loadBackups(db);
    } catch (err) {
      setBackupsError(errorMessage(err, `Could not restore ${db.name}.`));
    } finally {
      setRestoring(null);
    }
  }

  async function toggleConn(db: Database) {
    if (connOpen === db.id) {
      setConnOpen(null);
      return;
    }
    // Keep panels mutually exclusive so a row never has two expanded drawers.
    setBackupsOpen(null);
    setConnOpen(db.id);
    setConnError(null);
    if (connDetail[db.id] || !activeOrgId) return;
    setConnLoading(db.id);
    try {
      const detail = await authedCall((token, on) =>
        api.getDatabase(activeOrgId, db.id, token, on),
      );
      setConnDetail((d) => ({ ...d, [db.id]: detail }));
    } catch (err) {
      setConnError(errorMessage(err, `Could not load connection info.`));
    } finally {
      setConnLoading(null);
    }
  }

  async function runLifecycle(
    db: Database,
    action: "deploy" | "stop" | "restart",
  ) {
    if (!activeOrgId) {
      setRowNotice("Action unavailable — no active organization.");
      return;
    }
    setBusy((b) => ({ ...b, [db.id]: action }));
    setRowNotice(null);
    try {
      await authedCall((token, on) =>
        action === "deploy"
          ? api.deployDatabase(activeOrgId, db.id, token, on)
          : action === "stop"
            ? api.stopDatabase(activeOrgId, db.id, token, on)
            : api.restartDatabase(activeOrgId, db.id, token, on),
      );
      refetch();
    } catch (err) {
      setRowNotice(errorMessage(err, `Could not ${action} ${db.name}.`));
    } finally {
      setBusy((b) => {
        const next = { ...b };
        delete next[db.id];
        return next;
      });
    }
  }

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
                    const action = busy[db.id];
                    const anyBusy = action !== undefined;
                    return (
                      <Fragment key={db.id}>
                        <tr className="hover:bg-muted/40">
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
                                onClick={() => toggleConn(db)}
                                aria-label={`Connection info for ${db.name}`}
                                title="Connection info"
                                aria-expanded={connOpen === db.id}
                              >
                                <Plug className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => toggleBackups(db)}
                                aria-label={`Backups for ${db.name}`}
                                title="Backups"
                                aria-expanded={backupsOpen === db.id}
                              >
                                <History className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                loading={action === "deploy"}
                                disabled={anyBusy}
                                onClick={() => runLifecycle(db, "deploy")}
                                aria-label={`Start ${db.name}`}
                                title="Start"
                              >
                                {action !== "deploy" && (
                                  <Play className="h-4 w-4" />
                                )}
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                loading={action === "restart"}
                                disabled={anyBusy}
                                onClick={() => runLifecycle(db, "restart")}
                                aria-label={`Restart ${db.name}`}
                                title="Restart"
                              >
                                {action !== "restart" && (
                                  <RotateCw className="h-4 w-4" />
                                )}
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                loading={action === "stop"}
                                disabled={anyBusy}
                                onClick={() => runLifecycle(db, "stop")}
                                aria-label={`Stop ${db.name}`}
                                title="Stop"
                              >
                                {action !== "stop" && (
                                  <Square className="h-4 w-4" />
                                )}
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                loading={action === "delete"}
                                disabled={anyBusy}
                                onClick={() => setConfirmDelete(db)}
                                aria-label={`Delete ${db.name}`}
                                title="Delete"
                                className="text-destructive hover:text-destructive"
                              >
                                {action !== "delete" && (
                                  <Trash2 className="h-4 w-4" />
                                )}
                              </Button>
                            </div>
                          </td>
                        </tr>
                        {connOpen === db.id && (
                          <tr>
                            <td
                              colSpan={5}
                              className="bg-surface-2/40 px-6 py-4"
                            >
                              <ConnectionInfo
                                detail={connDetail[db.id]}
                                loading={connLoading === db.id}
                                error={connLoading === db.id ? null : connError}
                              />
                            </td>
                          </tr>
                        )}
                        {backupsOpen === db.id && (
                          <tr>
                            <td
                              colSpan={5}
                              className="bg-surface-2/40 px-6 py-4"
                            >
                              <BackupPanel
                                available={backupApi !== null}
                                backups={backups[db.id]}
                                loading={backupsLoading === db.id}
                                error={backupsError}
                                backingUp={backingUp === db.id}
                                restoringId={restoring}
                                onBackupNow={() => onBackupNow(db)}
                                onRestore={(backup) =>
                                  setConfirmRestore({ db, backup })
                                }
                              />
                            </td>
                          </tr>
                        )}
                      </Fragment>
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

      <ConfirmDialog
        open={confirmRestore !== null}
        title={
          confirmRestore
            ? `Restore ${confirmRestore.db.name} from backup?`
            : "Restore from backup?"
        }
        description="This overwrites the database's current data with the contents of the selected backup. Any data written since that backup will be lost. This action cannot be undone."
        confirmLabel="Restore backup"
        destructive
        loading={
          confirmRestore ? restoring === confirmRestore.backup.id : false
        }
        onConfirm={onConfirmRestore}
        onCancel={() => setConfirmRestore(null)}
      />
    </div>
  );
}

// Renders a database's backups: a "Back up now" action plus the list of existing
// backups, each with its status, timestamp, optional size, and a destructive
// Restore action. Honesty over fake-success (invariant #6): when the API client
// does not yet expose the backup endpoints, the panel says so instead of
// pretending a backup ran. Errors are surfaced verbatim via the parent's
// `errorMessage` helper; the list shows a Skeleton while loading.
function BackupPanel({
  available,
  backups,
  loading,
  error,
  backingUp,
  restoringId,
  onBackupNow,
  onRestore,
}: {
  available: boolean;
  backups: DatabaseBackup[] | undefined;
  loading: boolean;
  error: string | null;
  backingUp: boolean;
  restoringId: string | null;
  onBackupNow: () => void;
  onRestore: (backup: DatabaseBackup) => void;
}) {
  if (!available) {
    return (
      <div className="space-y-2">
        <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Backups
        </p>
        <Notice variant="info">
          Backups are not available yet for this database. The platform exposes
          this once the backup service is enabled.
        </Notice>
      </div>
    );
  }

  // A restore is in flight for one of this list's backups.
  const restoreBusy = restoringId !== null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Backups
        </p>
        <Button
          size="sm"
          variant="secondary"
          onClick={onBackupNow}
          loading={backingUp}
          disabled={restoreBusy}
        >
          {!backingUp && <Archive className="h-4 w-4" />}
          Back up now
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        On-demand snapshots of this database. Restoring overwrites current data.
      </p>

      {error && <Notice variant="error">{error}</Notice>}

      {loading && backups === undefined ? (
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : backups && backups.length > 0 ? (
        <div className="overflow-x-auto rounded-md border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2 font-medium">Completed</th>
                <th className="px-4 py-2 font-medium">Size</th>
                <th className="px-4 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {backups.map((b) => {
                const size = formatSize(b.sizeBytes);
                // Only a completed backup can be restored from.
                const canRestore = b.status === "completed";
                return (
                  <tr key={b.id} className="hover:bg-muted/40">
                    <td className="px-4 py-2">
                      <Badge variant={backupBadgeVariant(b.status)}>
                        {b.status}
                      </Badge>
                      {b.status === "failed" && b.error && (
                        <span className="ml-2 text-xs text-destructive">
                          {b.error}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {formatTimestamp(b.createdAt)}
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {formatTimestamp(b.completedAt)}
                    </td>
                    <td className="px-4 py-2 text-muted-foreground">
                      {size ?? "—"}
                    </td>
                    <td className="px-4 py-2">
                      <div className="flex items-center justify-end">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => onRestore(b)}
                          loading={restoringId === b.id}
                          disabled={!canRestore || restoreBusy || backingUp}
                          aria-label={`Restore from backup ${b.id}`}
                          title={
                            canRestore
                              ? "Restore from this backup"
                              : "Only a completed backup can be restored"
                          }
                          className="text-destructive hover:text-destructive"
                        >
                          {restoringId !== b.id && (
                            <RotateCw className="h-4 w-4" />
                          )}
                          Restore
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          No backups yet. Create one with “Back up now”.
        </p>
      )}
    </div>
  );
}

// Renders a database's in-cluster connection info: host/port/database/username,
// a masked password with reveal, and the full connection string. Every value has
// a copy-to-clipboard button. Databases are internal-only (ClusterIP), so the
// host is the cluster service DNS — reachable from the org's own workloads only.
function ConnectionInfo({
  detail,
  loading,
  error,
}: {
  detail: DatabaseDetail | undefined;
  loading: boolean;
  error: string | null;
}) {
  const [showPassword, setShowPassword] = useState(false);

  if (loading) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        Loading connection info…
      </p>
    );
  }
  if (error) {
    return <Notice variant="error">{error}</Notice>;
  }
  if (!detail) return null;

  const c = detail.connection;
  const rows: { label: string; value: string; mono?: boolean }[] = [
    { label: "Host", value: c.host, mono: true },
    { label: "Port", value: String(c.port) },
    { label: "Database", value: c.database, mono: true },
    { label: "Username", value: c.username, mono: true },
  ];

  return (
    <div className="space-y-3">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Connection info
      </p>
      <p className="text-xs text-muted-foreground">
        Internal only (ClusterIP). Reachable from this org&apos;s workloads, not
        the public internet.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        {rows.map((r) => (
          <ConnField
            key={r.label}
            label={r.label}
            value={r.value}
            mono={r.mono}
          />
        ))}
      </div>

      {/* Masked password with reveal + copy. */}
      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">Password</p>
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
            {showPassword ? c.password : "•".repeat(16)}
          </code>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setShowPassword((s) => !s)}
            aria-label={showPassword ? "Hide password" : "Reveal password"}
          >
            {showPassword ? (
              <EyeOff className="h-4 w-4" />
            ) : (
              <Eye className="h-4 w-4" />
            )}
          </Button>
          <CopyToClipboard value={c.password} label="Copy password" />
        </div>
      </div>

      {/* Connection string (carries the password — masked source, copyable). */}
      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">Connection string</p>
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 font-mono text-xs">
            {showPassword
              ? c.connectionString
              : c.connectionString.replace(c.password, "••••••")}
          </code>
          <CopyToClipboard
            value={c.connectionString}
            label="Copy connection string"
          />
        </div>
      </div>
    </div>
  );
}

function ConnField({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted-foreground">{label}</p>
      <div className="flex items-center gap-2">
        <code
          className={
            "min-w-0 flex-1 truncate rounded bg-surface-2 px-2 py-1 text-xs" +
            (mono ? " font-mono" : "")
          }
        >
          {value}
        </code>
        <CopyToClipboard value={value} label={`Copy ${label.toLowerCase()}`} />
      </div>
    </div>
  );
}

function CopyToClipboard({ value, label }: { value: string; label: string }) {
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
      variant="ghost"
      size="icon"
      onClick={onCopy}
      aria-label={label}
      title={label}
    >
      {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
    </Button>
  );
}

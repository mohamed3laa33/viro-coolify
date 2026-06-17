"use client";

import { useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import {
  ChevronDown,
  ChevronRight,
  FolderGit2,
  GitBranch,
  Package,
  Plus,
  Trash2,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App, type Project } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { invalidate, useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Notice } from "@/components/ui/notice";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusDot } from "@/components/ui/status-dot";

export default function ProjectsPage() {
  const { activeOrgId, authedCall } = useAuth();
  const demo = isDemoMode();

  // Demo fallback loads lazily (demo mode only); never shipped to prod.
  const demoProjects = useDemoData((m) => m.mockProjects, [] as Project[]);

  // useResource reports a boolean `error`; capture the real failure here so we
  // can surface the actual ApiError message instead of a generic placeholder.
  const fetchErrorRef = useRef<string | null>(null);
  const projectsKey = activeOrgId ? `projects:${activeOrgId}` : undefined;

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listProjects(activeOrgId, token, on, { signal })
                .then((res) => {
                  fetchErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  fetchErrorRef.current = errorMessage(
                    err,
                    "Couldn’t load projects — the API is unreachable.",
                  );
                  throw err;
                }),
            signal,
          )
      : null,
    { data: demoProjects },
    [activeOrgId, demoProjects],
    { cacheKey: projectsKey },
  );

  const projects = data.data;
  const showError = error && !demo;
  const showLoading = loading && projects.length === 0;

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  function refreshProjects() {
    if (projectsKey) invalidate(projectsKey);
    refetch();
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
        api.createProject(activeOrgId, trimmed, token, on),
      );
      setName("");
      setCreating(false);
      refreshProjects();
    } catch (err) {
      setNotice(errorMessage(err, "Could not create the project."));
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Projects"
        description="Group apps and scope team access with projects."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            New Project
          </Button>
        }
      />

      {notice && <Notice>{notice}</Notice>}

      {showError && (
        <Notice variant="error" className="items-center justify-between gap-4">
          <span>
            {fetchErrorRef.current ??
              "Couldn’t load projects — the API is unreachable."}
          </span>
          <Button size="sm" variant="secondary" onClick={refetch}>
            Retry
          </Button>
        </Notice>
      )}

      {creating && (
        <Card>
          <CardContent className="pt-6">
            <form
              onSubmit={onCreate}
              className="flex flex-col gap-4 sm:flex-row sm:items-end"
            >
              <div className="flex-1 space-y-2">
                <Label htmlFor="project-name">Project name</Label>
                <Input
                  id="project-name"
                  placeholder="Platform"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  autoFocus
                  required
                />
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

      {showLoading ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Card key={i}>
              <div className="flex items-center gap-3 px-6 py-4">
                <Skeleton className="h-4 w-4 rounded-full" />
                <div className="space-y-2">
                  <Skeleton className="h-4 w-40" />
                  <Skeleton className="h-3 w-24" />
                </div>
              </div>
            </Card>
          ))}
        </div>
      ) : (
        <div className="space-y-3">
          {projects.map((p) => (
            <ProjectRow key={p.id} project={p} onDeleted={refreshProjects} />
          ))}
        </div>
      )}

      {!showLoading && projects.length === 0 && !showError && (
        <EmptyState
          icon={FolderGit2}
          title="No projects yet"
          description="Create your first project to group apps and scope team access."
          action={
            <Button onClick={() => setCreating(true)}>
              <Plus className="h-4 w-4" />
              New Project
            </Button>
          }
        />
      )}
    </div>
  );
}

function ProjectRow({
  project,
  onDeleted,
}: {
  project: Project;
  onDeleted: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();
  const [open, setOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [rowNotice, setRowNotice] = useState<string | null>(null);

  // Fallback: in demo mode, use a stand-in slice of mock apps; otherwise empty.
  // Loaded lazily so mock data is never shipped to / shown in production.
  const fallbackApps = useDemoData((m) => m.mockApps.slice(0, 3), [] as App[]);

  const { data, loading } = useResource(
    open && activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listProjectApps(activeOrgId, project.id, token, on, {
                signal,
              }),
            signal,
          )
      : null,
    { data: fallbackApps },
    [open, activeOrgId, project.id, fallbackApps],
  );

  const apps: App[] = open ? data.data : [];
  const appsListId = `project-apps-${project.id}`;

  async function onConfirmDelete() {
    if (!activeOrgId) {
      setRowNotice("Delete unavailable — no active organization.");
      setConfirmDelete(false);
      return;
    }
    setDeleting(true);
    setRowNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteProject(activeOrgId, project.id, token, on),
      );
      setConfirmDelete(false);
      onDeleted();
    } catch (err) {
      setConfirmDelete(false);
      setRowNotice(errorMessage(err, `Could not delete ${project.name}.`));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <Card>
      <div className="flex items-center justify-between gap-2 pr-4">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-controls={appsListId}
          className="flex flex-1 items-center justify-between px-6 py-4 text-left"
        >
          <div className="flex items-center gap-3">
            {open ? (
              <ChevronDown className="h-4 w-4 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-4 w-4 text-muted-foreground" />
            )}
            <FolderGit2 className="h-4 w-4 text-primary" />
            <div>
              <p className="flex items-center gap-2 text-sm font-medium">
                {project.name}
                {project.isDefault && <Badge variant="info">Default</Badge>}
              </p>
              <p className="font-mono text-xs text-muted-foreground">
                {project.slug}
              </p>
            </div>
          </div>
        </button>
        {!project.isDefault && (
          <Button
            variant="ghost"
            size="sm"
            loading={deleting}
            disabled={deleting}
            onClick={() => setConfirmDelete(true)}
            aria-label={`Delete ${project.name}`}
            title="Delete"
            className="text-destructive hover:text-destructive"
          >
            {!deleting && <Trash2 className="h-4 w-4" />}
          </Button>
        )}
      </div>

      {rowNotice && (
        <div className="border-t border-border px-6 py-3">
          <Notice variant="error">{rowNotice}</Notice>
        </div>
      )}

      {open && (
        <CardContent id={appsListId} className="border-t border-border p-0">
          {loading && (
            <div className="space-y-2 px-6 py-4">
              <Skeleton className="h-5 w-48" />
              <Skeleton className="h-5 w-40" />
            </div>
          )}
          {!loading && apps.length === 0 && (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              No apps in this project.
            </p>
          )}
          {!loading && apps.length > 0 && (
            <ul className="divide-y divide-border">
              {apps.map((app) => (
                <li key={app.id}>
                  <Link
                    href={`/dashboard/apps/${app.id}`}
                    className="flex items-center justify-between px-6 py-3 transition-colors hover:bg-muted"
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
                    <div className="flex items-center gap-3 text-xs text-muted-foreground">
                      <span className="inline-flex items-center gap-1">
                        <GitBranch className="h-3.5 w-3.5" />
                        {app.gitBranch}
                      </span>
                      <span className="inline-flex items-center gap-1">
                        <Package className="h-3.5 w-3.5" />
                        {app.buildPack}
                      </span>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      )}

      <ConfirmDialog
        open={confirmDelete}
        title={`Delete ${project.name}?`}
        description="This permanently deletes the project. The project must be empty (no apps or services) before it can be deleted."
        confirmLabel="Delete"
        destructive
        loading={deleting}
        onConfirm={onConfirmDelete}
        onCancel={() => setConfirmDelete(false)}
      />
    </Card>
  );
}

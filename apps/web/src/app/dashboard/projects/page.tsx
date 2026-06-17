"use client";

import { useState, type FormEvent } from "react";
import Link from "next/link";
import {
  ChevronDown,
  ChevronRight,
  FolderGit2,
  GitBranch,
  Package,
  Plus,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type App, type Project } from "@/lib/api";
import { mockApps, mockProjects } from "@/lib/mock";
import { isDemoMode } from "@/lib/demo";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { Badge } from "@/components/ui/badge";
import { StatusDot } from "@/components/ui/status-dot";

export default function ProjectsPage() {
  const { activeOrgId, authedCall } = useAuth();
  const demo = isDemoMode();

  const { data, error, refetch } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listProjects(activeOrgId, token, on))
      : null,
    { data: demo ? mockProjects : [] },
    [activeOrgId],
  );

  const projects = data.data;

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

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
      refetch();
    } catch {
      setNotice("Could not create the project — the API is unreachable.");
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

      {error && !demo && (
        <Notice
          variant="error"
          className="items-center justify-between gap-4"
        >
          <span>Couldn’t load projects — the API is unreachable.</span>
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

      <div className="space-y-3">
        {projects.map((p) => (
          <ProjectRow key={p.id} project={p} />
        ))}
      </div>

      {projects.length === 0 && !(error && !demo) && (
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

function ProjectRow({ project }: { project: Project }) {
  const { activeOrgId, authedCall } = useAuth();
  const [open, setOpen] = useState(false);

  // Fallback: in demo mode, use a stand-in slice of mock apps; otherwise empty.
  const fallbackApps = isDemoMode() ? mockApps.slice(0, 3) : [];

  const { data, loading } = useResource(
    open && activeOrgId
      ? () =>
          authedCall((token, on) =>
            api.listProjectApps(activeOrgId, project.id, token, on),
          )
      : null,
    { data: fallbackApps },
    [open, activeOrgId, project.id],
  );

  const apps: App[] = open ? data.data : [];
  const appsListId = `project-apps-${project.id}`;

  return (
    <Card>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls={appsListId}
        className="flex w-full items-center justify-between px-6 py-4 text-left"
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

      {open && (
        <CardContent id={appsListId} className="border-t border-border p-0">
          {loading && (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              Loading apps…
            </p>
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
    </Card>
  );
}

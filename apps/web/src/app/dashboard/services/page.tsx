"use client";

import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  Plus,
  Play,
  Square,
  RotateCw,
  Trash2,
  Boxes,
  X,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  ApiError,
  statusVariant,
  type Service,
  type Template,
  type Project,
  type CreateServiceInput,
} from "@/lib/api";
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
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";

// Extract a human-readable message from an unknown thrown value, preferring the
// real ApiError message over a generic fallback.
function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

// The status helper can yield "muted", which the Badge renders as "outline".
function statusBadgeVariant(status: string): BadgeVariant {
  const v = statusVariant(status);
  return v === "muted" ? "outline" : v;
}

// Per-instance lifecycle action we expose as a button.
type Action = "deploy" | "stop" | "restart";

export default function ServicesPage() {
  const { activeOrgId, authedCall } = useAuth();
  const demo = isDemoMode();

  // useResource only reports a boolean `error`; capture the actual failure here
  // so we can show the real ApiError message instead of a misleading empty state.
  const fetchErrorRef = useRef<string | null>(null);

  const { data, loading, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listServices(activeOrgId, token, on, { signal })
                .then((res) => {
                  fetchErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  fetchErrorRef.current = errorMessage(
                    err,
                    "Failed to load services.",
                  );
                  throw err;
                }),
            signal,
          )
      : null,
    { data: [] as Service[] },
    [activeOrgId],
  );

  const showError = error && !demo;
  const services = data.data;

  const [creating, setCreating] = useState(false);

  // Per-service pending action so each row's buttons spin independently.
  const [busy, setBusy] = useState<Record<string, Action | "delete">>({});
  const [rowNotice, setRowNotice] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<Service | null>(null);

  async function runAction(svc: Service, action: Action) {
    if (!activeOrgId) {
      setRowNotice("Action unavailable — no active organization.");
      return;
    }
    setBusy((b) => ({ ...b, [svc.id]: action }));
    setRowNotice(null);
    try {
      await authedCall((token, on) => {
        if (action === "deploy")
          return api.deployService(activeOrgId, svc.id, token, on);
        if (action === "stop")
          return api.stopService(activeOrgId, svc.id, token, on);
        return api.restartService(activeOrgId, svc.id, token, on);
      });
      refetch();
    } catch (err) {
      setRowNotice(errorMessage(err, `Could not ${action} ${svc.name}.`));
    } finally {
      setBusy((b) => {
        const next = { ...b };
        delete next[svc.id];
        return next;
      });
    }
  }

  async function onConfirmDelete() {
    const svc = confirmDelete;
    if (!svc) return;
    if (!activeOrgId) {
      setRowNotice("Delete unavailable — no active organization.");
      setConfirmDelete(null);
      return;
    }
    setBusy((b) => ({ ...b, [svc.id]: "delete" }));
    setRowNotice(null);
    try {
      await authedCall((token, on) =>
        api.deleteService(activeOrgId, svc.id, token, on),
      );
      setConfirmDelete(null);
      refetch();
    } catch (err) {
      setRowNotice(errorMessage(err, `Could not delete ${svc.name}.`));
    } finally {
      setBusy((b) => {
        const next = { ...b };
        delete next[svc.id];
        return next;
      });
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Services"
        description="Launch one-click services from the catalog and manage their lifecycle."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            Launch service
          </Button>
        }
      />

      {creating && (
        <LaunchServiceForm
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            refetch();
          }}
        />
      )}

      {showError && (
        <Notice variant="error">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <span>{fetchErrorRef.current ?? "Failed to load services."}</span>
            <Button size="sm" variant="secondary" onClick={refetch}>
              Retry
            </Button>
          </div>
        </Notice>
      )}

      {rowNotice && <Notice variant="error">{rowNotice}</Notice>}

      {loading && !showError ? (
        <Card>
          <CardContent className="space-y-3 p-6">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      ) : services.length === 0 && !showError ? (
        <EmptyState
          icon={Boxes}
          title="No services yet"
          description="Launch a one-click service from the catalog to get started."
          action={
            <Button onClick={() => setCreating(true)}>
              <Plus className="h-4 w-4" />
              Launch your first service
            </Button>
          }
        />
      ) : !showError ? (
        <Card>
          <CardContent className="p-0">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                    <th className="px-6 py-3 font-medium">Name</th>
                    <th className="px-6 py-3 font-medium">Template</th>
                    <th className="px-6 py-3 font-medium">Status</th>
                    <th className="px-6 py-3 font-medium">Host</th>
                    <th className="px-6 py-3 text-right font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {services.map((svc) => {
                    const pending = busy[svc.id];
                    return (
                      <tr key={svc.id} className="hover:bg-muted/40">
                        <td className="px-6 py-4">
                          <div className="font-medium">{svc.name}</div>
                          <div className="font-mono text-xs text-muted-foreground">
                            {svc.id}
                          </div>
                        </td>
                        <td className="px-6 py-4">
                          <Badge variant="outline">{svc.template}</Badge>
                        </td>
                        <td className="px-6 py-4">
                          <div className="flex items-center gap-2">
                            <StatusDot status={svc.status} showLabel />
                            <Badge
                              variant={statusBadgeVariant(svc.status)}
                              className="capitalize"
                            >
                              {svc.status}
                            </Badge>
                          </div>
                        </td>
                        <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                          {svc.host ?? "—"}
                        </td>
                        <td className="px-6 py-4">
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={pending === "deploy"}
                              disabled={!!pending}
                              onClick={() => runAction(svc, "deploy")}
                              aria-label={`Deploy ${svc.name}`}
                              title="Deploy"
                            >
                              {pending !== "deploy" && (
                                <Play className="h-4 w-4" />
                              )}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={pending === "restart"}
                              disabled={!!pending}
                              onClick={() => runAction(svc, "restart")}
                              aria-label={`Restart ${svc.name}`}
                              title="Restart"
                            >
                              {pending !== "restart" && (
                                <RotateCw className="h-4 w-4" />
                              )}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={pending === "stop"}
                              disabled={!!pending}
                              onClick={() => runAction(svc, "stop")}
                              aria-label={`Stop ${svc.name}`}
                              title="Stop"
                            >
                              {pending !== "stop" && (
                                <Square className="h-4 w-4" />
                              )}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              loading={pending === "delete"}
                              disabled={!!pending}
                              onClick={() => setConfirmDelete(svc)}
                              aria-label={`Delete ${svc.name}`}
                              title="Delete"
                              className="text-destructive hover:text-destructive"
                            >
                              {pending !== "delete" && (
                                <Trash2 className="h-4 w-4" />
                              )}
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
      ) : null}

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete ${confirmDelete.name}?`}
          description="This permanently removes the service and its Kubernetes release. This action cannot be undone."
          confirmLabel="Delete service"
          pending={busy[confirmDelete.id] === "delete"}
          onConfirm={onConfirmDelete}
          onCancel={() => setConfirmDelete(null)}
        />
      )}
    </div>
  );
}

function LaunchServiceForm({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();
  const demo = isDemoMode();

  // Catalog is read from the public services catalog; only launchable
  // service/app kinds belong here (databases have their own page).
  const { data: catalogData } = useResource(
    () => api.getServiceCatalog(),
    { data: [] as Template[] },
    [],
  );
  const templates: Template[] = catalogData.data
    .filter((t) => (t.kind === "service" || t.kind === "app") && t.active)
    .sort((a, b) => a.sortOrder - b.sortOrder);

  // The create endpoint is project-scoped, so we must pick a project.
  const { data: projectsData } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api.listProjects(activeOrgId, token, on, { signal }),
            signal,
          )
      : null,
    { data: [] as Project[] },
    [activeOrgId],
  );
  const projects = projectsData.data;

  const [templateKey, setTemplateKey] = useState("");
  const [projectId, setProjectId] = useState("");
  const [name, setName] = useState("");
  const [cpu, setCpu] = useState("");
  const [memoryMb, setMemoryMb] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  // Default the selects to the first available option once data arrives.
  useEffect(() => {
    if (!templateKey && templates.length > 0) setTemplateKey(templates[0].key);
  }, [templates, templateKey]);
  useEffect(() => {
    if (!projectId && projects.length > 0) {
      const def = projects.find((p) => p.isDefault) ?? projects[0];
      setProjectId(def.id);
    }
  }, [projects, projectId]);

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    if (!activeOrgId) {
      setNotice("Launch unavailable — no active organization.");
      return;
    }
    if (!projectId) {
      setNotice("Select a project to launch this service into.");
      return;
    }
    if (!templateKey) {
      setNotice("Select a service template from the catalog.");
      return;
    }
    const input: CreateServiceInput = {
      templateKey,
      name: trimmed,
    };
    // Only send resources the user actually specified; blank => platform default.
    const cpuValue = Number(cpu);
    if (cpu.trim() && Number.isFinite(cpuValue) && cpuValue > 0) {
      input.cpu = cpuValue;
    }
    const memValue = Number(memoryMb);
    if (memoryMb.trim() && Number.isFinite(memValue) && memValue > 0) {
      input.memoryMb = memValue;
    }
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.createService(activeOrgId, projectId, input, token, on),
      );
      onCreated();
    } catch (err) {
      setNotice(errorMessage(err, "Could not launch the service."));
    } finally {
      setPending(false);
    }
  }

  const noProjects = !demo && projects.length === 0;
  const noTemplates = !demo && templates.length === 0;

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Launch service</CardTitle>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {notice && <Notice variant="error">{notice}</Notice>}
        {noProjects && (
          <Notice variant="warning">
            Create a project first — services are launched into a project.
          </Notice>
        )}
        {noTemplates && (
          <Notice variant="info">
            No launchable services are available in the catalog yet.
          </Notice>
        )}
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="svc-template">Service</Label>
              <Select
                id="svc-template"
                value={templateKey}
                onChange={(e) => setTemplateKey(e.target.value)}
                disabled={noTemplates}
              >
                {templates.length === 0 && (
                  <option value="">No services available</option>
                )}
                {templates.map((tpl) => (
                  <option key={tpl.key} value={tpl.key}>
                    {tpl.name}
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="svc-project">Project</Label>
              <Select
                id="svc-project"
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
                disabled={noProjects}
              >
                {projects.length === 0 && (
                  <option value="">No projects available</option>
                )}
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="svc-name">Name</Label>
              <Input
                id="svc-name"
                placeholder="my-wordpress"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="svc-cpu">CPU (vCPU)</Label>
              <Input
                id="svc-cpu"
                type="number"
                inputMode="decimal"
                min="0"
                step="0.1"
                className="font-mono"
                placeholder="Platform default"
                value={cpu}
                onChange={(e) => setCpu(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="svc-memory">Memory (MB)</Label>
              <Input
                id="svc-memory"
                type="number"
                inputMode="numeric"
                min="0"
                step="64"
                className="font-mono"
                placeholder="Platform default"
                value={memoryMb}
                onChange={(e) => setMemoryMb(e.target.value)}
              />
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              loading={pending}
              disabled={noProjects || noTemplates}
            >
              {!pending && <Plus className="h-4 w-4" />}
              Launch service
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function ConfirmDialog({
  title,
  description,
  confirmLabel,
  pending,
  onConfirm,
  onCancel,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  pending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);

  // Focus the safe (cancel) action on open and close on Escape.
  useEffect(() => {
    cancelRef.current?.focus();
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onCancel();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onCancel}
    >
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-desc"
        className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="confirm-title" className="text-lg font-semibold">
          {title}
        </h2>
        <p id="confirm-desc" className="mt-2 text-sm text-muted-foreground">
          {description}
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button
            ref={cancelRef}
            variant="ghost"
            onClick={onCancel}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            loading={pending}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}

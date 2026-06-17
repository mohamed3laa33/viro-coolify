"use client";

import { useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Search, GitBranch, Package, X, Rocket } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, ApiError, statusVariant, type CreateAppInput } from "@/lib/api";
import { mockApps } from "@/lib/mock";
import { isDemoMode } from "@/lib/demo";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { StatusDot } from "@/components/ui/status-dot";
import { Badge, type BadgeVariant } from "@/components/ui/badge";

// Build packs the backend recognizes. Surfaced as a dropdown rather than a
// free-text field so we only ever submit a supported value.
const BUILD_PACKS = ["nixpacks", "dockerfile", "static"] as const;

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

export default function AppsPage() {
  const { activeOrgId, authedCall } = useAuth();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);

  // useResource only reports a boolean `error`; capture the actual failure here
  // so we can show the real ApiError message instead of a misleading empty state.
  const fetchErrorRef = useRef<string | null>(null);

  const { data, error, refetch } = useResource(
    activeOrgId
      ? (signal) =>
          authedCall(
            (token, on) =>
              api
                .listApps(activeOrgId, token, on, { signal })
                .then((res) => {
                  fetchErrorRef.current = null;
                  return res;
                })
                .catch((err: unknown) => {
                  fetchErrorRef.current = errorMessage(
                    err,
                    "Failed to load apps.",
                  );
                  throw err;
                }),
            signal,
          )
      : null,
    { data: isDemoMode() ? mockApps : [] },
    [activeOrgId],
  );

  const showError = error && !isDemoMode();
  const apps = data.data.filter((a) =>
    a.name.toLowerCase().includes(query.toLowerCase()),
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Apps"
        description="Deploy and manage your applications across regions."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            New App
          </Button>
        }
      />

      {creating && (
        <CreateAppForm
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
            <span>
              {fetchErrorRef.current ?? "Failed to load apps."}
            </span>
            <Button size="sm" variant="secondary" onClick={refetch}>
              Retry
            </Button>
          </div>
        </Notice>
      )}

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
          <Link key={app.id} href={`/dashboard/apps/${app.id}`}>
            <Card className="h-full p-5 transition-colors hover:border-primary/40">
              <div className="flex items-start justify-between">
                <div className="min-w-0">
                  <p className="truncate font-medium">{app.name}</p>
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {app.gitRepository}
                  </p>
                </div>
                <StatusDot status={app.status} />
              </div>

              <div className="mt-5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span className="inline-flex items-center gap-1">
                  <GitBranch className="h-3.5 w-3.5" />
                  {app.gitBranch}
                </span>
                <span className="inline-flex items-center gap-1">
                  <Package className="h-3.5 w-3.5" />
                  {app.buildPack}
                </span>
              </div>

              <div className="mt-4">
                <Badge variant={statusBadgeVariant(app.status)} className="capitalize">
                  {app.status}
                </Badge>
              </div>
            </Card>
          </Link>
        ))}
      </div>

      {apps.length === 0 &&
        !showError &&
        (query ? (
          <Card className="flex flex-col items-center justify-center py-16 text-center">
            <p className="text-sm text-muted-foreground">
              No apps match “{query}”.
            </p>
          </Card>
        ) : (
          <EmptyState
            icon={Rocket}
            title="No apps yet"
            description="Deploy your first application from a Git repository to get started."
            action={
              <Button onClick={() => setCreating(true)}>
                <Plus className="h-4 w-4" />
                Create your first app
              </Button>
            }
          />
        ))}
    </div>
  );
}

function CreateAppForm({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const { activeOrgId, authedCall } = useAuth();
  const router = useRouter();
  const [name, setName] = useState("");
  const [gitRepository, setGitRepository] = useState("");
  const [gitBranch, setGitBranch] = useState("main");
  const [buildPack, setBuildPack] = useState<string>(BUILD_PACKS[0]);
  // Requested resources; left blank to let the backend apply platform defaults.
  const [cpu, setCpu] = useState("");
  const [memoryMb, setMemoryMb] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    if (!activeOrgId) {
      setNotice("Create unavailable — no active organization.");
      return;
    }
    const input: CreateAppInput = {
      name: trimmed,
      gitRepository: gitRepository.trim(),
      gitBranch: gitBranch.trim() || "main",
      buildPack: buildPack.trim() || BUILD_PACKS[0],
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
      const app = await authedCall((token, on) =>
        api.createApp(activeOrgId, input, token, on),
      );
      onCreated();
      router.push(`/dashboard/apps/${app.id}`);
    } catch (err) {
      setNotice(errorMessage(err, "Could not create the app."));
    } finally {
      setPending(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>New app</CardTitle>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
          <X className="h-4 w-4" />
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {notice && <Notice variant="error">{notice}</Notice>}
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="app-name">Name</Label>
              <Input
                id="app-name"
                placeholder="marketing-site"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="app-repo">Git repository</Label>
              <Input
                id="app-repo"
                className="font-mono"
                placeholder="github.com/acme/marketing"
                value={gitRepository}
                onChange={(e) => setGitRepository(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="app-branch">Branch</Label>
              <Input
                id="app-branch"
                className="font-mono"
                placeholder="main"
                value={gitBranch}
                onChange={(e) => setGitBranch(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="app-buildpack">Build pack</Label>
              <Select
                id="app-buildpack"
                value={buildPack}
                onChange={(e) => setBuildPack(e.target.value)}
              >
                {BUILD_PACKS.map((bp) => (
                  <option key={bp} value={bp}>
                    {bp}
                  </option>
                ))}
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="app-cpu">CPU (vCPU)</Label>
              <Input
                id="app-cpu"
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
              <Label htmlFor="app-memory">Memory (MB)</Label>
              <Input
                id="app-memory"
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
            <Button type="submit" loading={pending}>
              {!pending && <Plus className="h-4 w-4" />}
              Create app
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

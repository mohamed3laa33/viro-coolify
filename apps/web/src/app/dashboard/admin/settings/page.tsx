"use client";

import { useEffect, useState, type FormEvent } from "react";
import { X } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Settings } from "@/lib/api";
import { mockPlans, mockSettings } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";
import { Select } from "@/components/ui/select";

function pctLabel(factor: number): string {
  return `${Math.round(factor * 100)}%`;
}

export default function AdminSettingsPage() {
  const { authedCall } = useAuth();

  const { data: settings, usingFallback } = useResource(
    () => authedCall((token, on) => api.getSettings(token, on)),
    mockSettings,
    [],
  );

  const { data: plansData } = useResource(
    () => authedCall((token, on) => api.listAdminPlans(token, on)),
    { data: mockPlans },
    [],
  );
  const plans = plansData.data;

  const [form, setForm] = useState<Settings>(settings);
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<
    { variant: "success" | "error"; message: string } | null
  >(null);
  const [regionDraft, setRegionDraft] = useState("");

  // Sync local form state once the resource resolves (fetch or fallback).
  useEffect(() => {
    setForm(settings);
  }, [settings]);

  function set<K extends keyof Settings>(key: K, value: Settings[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  function addRegion() {
    const r = regionDraft.trim().toLowerCase();
    if (!r || form.regions.includes(r)) {
      setRegionDraft("");
      return;
    }
    set("regions", [...form.regions, r]);
    setRegionDraft("");
  }

  function removeRegion(region: string) {
    const next = form.regions.filter((r) => r !== region);
    setForm((f) => ({
      ...f,
      regions: next,
      defaultRegion: f.defaultRegion === region ? (next[0] ?? "") : f.defaultRegion,
    }));
  }

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) => api.updateSettings(form, token, on));
      setNotice({ variant: "success", message: "Settings saved." });
    } catch {
      setNotice({
        variant: "error",
        message: "Save failed (API unreachable — demo mode).",
      });
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Platform settings"
        description="Defaults, resource overcommit, and regions for the whole platform."
      />

      {usingFallback && (
        <Notice variant="warning">
          Showing demo data — admin API unreachable. Edits won&apos;t persist.
        </Notice>
      )}

      {notice && <Notice variant={notice.variant}>{notice.message}</Notice>}

      <form onSubmit={onSubmit} className="space-y-6">
        <Card>
          <CardHeader>
            <CardTitle>Defaults</CardTitle>
            <CardDescription>
              Applied to new resources when no explicit value is provided.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-3">
              <div className="space-y-2">
                <Label htmlFor="default-cpu">Default CPU</Label>
                <Input
                  id="default-cpu"
                  type="number"
                  min={0}
                  step="0.25"
                  value={form.defaultCpu}
                  onChange={(e) =>
                    set("defaultCpu", Number(e.target.value))
                  }
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="default-mem">Default memory (MB)</Label>
                <Input
                  id="default-mem"
                  type="number"
                  min={0}
                  value={form.defaultMemoryMb}
                  onChange={(e) =>
                    set("defaultMemoryMb", Number(e.target.value))
                  }
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="default-plan">Default plan</Label>
                <Select
                  id="default-plan"
                  value={form.defaultPlanId}
                  onChange={(e) => set("defaultPlanId", e.target.value)}
                >
                  <option value="">— none —</option>
                  {plans.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name} ({p.id})
                    </option>
                  ))}
                </Select>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Resource overcommit</CardTitle>
            <CardDescription>
              Fraction of physical capacity schedulable per node (0–1). Higher
              means denser packing.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label htmlFor="cpu-overcommit">CPU overcommit factor</Label>
                <Badge variant="info">{pctLabel(form.cpuOvercommitFactor)}</Badge>
              </div>
              <input
                id="cpu-overcommit"
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={form.cpuOvercommitFactor}
                onChange={(e) =>
                  set("cpuOvercommitFactor", Number(e.target.value))
                }
                className="w-full accent-[hsl(var(--primary))]"
              />
            </div>
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label htmlFor="mem-overcommit">
                  Memory overcommit factor
                </Label>
                <Badge variant="info">
                  {pctLabel(form.memoryOvercommitFactor)}
                </Badge>
              </div>
              <input
                id="mem-overcommit"
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={form.memoryOvercommitFactor}
                onChange={(e) =>
                  set("memoryOvercommitFactor", Number(e.target.value))
                }
                className="w-full accent-[hsl(var(--primary))]"
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Regions</CardTitle>
            <CardDescription>
              The set of regions available to organizations, and the default.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="default-region">Default region</Label>
              <Select
                id="default-region"
                value={form.defaultRegion}
                onChange={(e) => set("defaultRegion", e.target.value)}
              >
                {form.regions.length === 0 && (
                  <option value="">— no regions —</option>
                )}
                {form.regions.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-2">
              <Label>Available regions</Label>
              <div className="flex flex-wrap gap-2">
                {form.regions.map((r) => (
                  <span
                    key={r}
                    className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-2 px-3 py-1 text-xs font-medium"
                  >
                    {r}
                    {r === form.defaultRegion && (
                      <Badge variant="info">default</Badge>
                    )}
                    <button
                      type="button"
                      onClick={() => removeRegion(r)}
                      className="text-muted-foreground hover:text-destructive"
                      aria-label={`Remove ${r}`}
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </span>
                ))}
                {form.regions.length === 0 && (
                  <p className="text-sm text-muted-foreground">
                    No regions configured.
                  </p>
                )}
              </div>
            </div>

            <div className="flex items-end gap-2">
              <div className="flex-1 space-y-2 sm:max-w-xs">
                <Label htmlFor="region-add">Add region</Label>
                <Input
                  id="region-add"
                  value={regionDraft}
                  placeholder="ord"
                  onChange={(e) => setRegionDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addRegion();
                    }
                  }}
                />
              </div>
              <Button type="button" variant="secondary" onClick={addRegion}>
                Add
              </Button>
            </div>
          </CardContent>
        </Card>

        <div className="flex justify-end">
          <Button type="submit" loading={pending}>
            Save settings
          </Button>
        </div>
      </form>
    </div>
  );
}

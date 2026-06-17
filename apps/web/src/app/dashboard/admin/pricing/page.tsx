"use client";

import { useEffect, useState, type FormEvent } from "react";
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  formatHourlyPrice,
  type PricingComponent,
  type PricingComponentInput,
} from "@/lib/api";
import { isDemoMode } from "@/lib/demo";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Notice } from "@/components/ui/notice";

const EMPTY_COMPONENT: PricingComponentInput = {
  key: "",
  name: "",
  unit: "core-hour",
  pricePerHour: 0,
  currency: "usd",
  active: true,
  sortOrder: 0,
};

export default function AdminPricingPage() {
  const { authedCall } = useAuth();
  const demo = isDemoMode();

  // Demo fallback loads lazily (demo mode only); prod shows a real empty table.
  const demoPricing = useDemoData(
    (m) => m.mockPricing,
    [] as PricingComponent[],
  );

  const { data, refetch, usingFallback } = useResource(
    () => authedCall((token, on) => api.listPricing(token, on)),
    { data: demoPricing },
    [demoPricing],
  );
  const components = [...data.data].sort((a, b) => a.sortOrder - b.sortOrder);

  // null = closed; "new" = create form; otherwise the key being edited.
  const [editing, setEditing] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<PricingComponent | null>(
    null,
  );

  const editingComponent =
    editing && editing !== "new"
      ? (components.find((c) => c.key === editing) ?? null)
      : null;

  async function onConfirmDelete() {
    const c = confirmDelete;
    if (!c) return;
    setNotice(null);
    try {
      await authedCall((token, on) => api.deletePricing(c.key, token, on));
      setConfirmDelete(null);
      refetch();
    } catch {
      setNotice("Delete failed (API unreachable — demo mode).");
      setConfirmDelete(null);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Pricing"
        description="Hourly metered rates billed per unit of usage across the platform."
        actions={
          <Button
            onClick={() => setEditing((cur) => (cur === "new" ? null : "new"))}
          >
            <Plus className="h-4 w-4" />
            New component
          </Button>
        }
      />

      {usingFallback && demo && (
        <Notice variant="warning">
          Showing demo data — admin API unreachable. Edits won&apos;t persist.
        </Notice>
      )}

      {notice && <Notice variant="error">{notice}</Notice>}

      {editing === "new" && (
        <PricingForm
          key="new"
          initial={EMPTY_COMPONENT}
          title="Create pricing component"
          keyEditable
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) =>
                api.createPricing(input, token, on),
              );
              setEditing(null);
              refetch();
            } catch {
              setNotice("Create failed (API unreachable — demo mode).");
            }
          }}
        />
      )}

      <Card>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                  <th className="px-6 py-3 font-medium">Component</th>
                  <th className="px-6 py-3 font-medium">Unit</th>
                  <th className="px-6 py-3 font-medium">Price / hour</th>
                  <th className="px-6 py-3 font-medium">Status</th>
                  <th className="px-6 py-3 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {components.length === 0 && (
                  <tr>
                    <td
                      colSpan={5}
                      className="px-6 py-8 text-center text-sm text-muted-foreground"
                    >
                      No pricing components yet.
                    </td>
                  </tr>
                )}
                {components.map((c) => (
                  <tr key={c.key} className="hover:bg-muted/40">
                    <td className="px-6 py-4">
                      <p className="font-medium">{c.name}</p>
                      <p className="font-mono text-xs text-muted-foreground">
                        {c.key} · sort {c.sortOrder}
                      </p>
                    </td>
                    <td className="px-6 py-4 text-muted-foreground">
                      {c.unit}
                    </td>
                    <td className="px-6 py-4 tabular-nums">
                      {formatHourlyPrice(c)}
                    </td>
                    <td className="px-6 py-4">
                      <Badge variant={c.active ? "success" : "outline"}>
                        {c.active ? "Active" : "Inactive"}
                      </Badge>
                    </td>
                    <td className="px-6 py-4">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() =>
                            setEditing((cur) => (cur === c.key ? null : c.key))
                          }
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          Edit
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setConfirmDelete(c)}
                          aria-label={`Delete ${c.name}`}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-destructive" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {editingComponent && (
        <PricingForm
          key={editingComponent.key}
          initial={editingComponent}
          title={`Edit ${editingComponent.name}`}
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) =>
                api.updatePricing(editingComponent.key, input, token, on),
              );
              setEditing(null);
              refetch();
            } catch {
              setNotice("Update failed (API unreachable — demo mode).");
            }
          }}
        />
      )}

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete ${confirmDelete.name}?`}
          description="This removes the metered rate. Existing usage already billed is unaffected."
          confirmLabel="Delete component"
          onConfirm={onConfirmDelete}
          onCancel={() => setConfirmDelete(null)}
        />
      )}
    </div>
  );
}

function PricingForm({
  initial,
  title,
  keyEditable = false,
  onCancel,
  onSubmit,
}: {
  initial: PricingComponentInput;
  title: string;
  keyEditable?: boolean;
  onCancel: () => void;
  onSubmit: (input: PricingComponentInput) => Promise<void>;
}) {
  const [form, setForm] = useState<PricingComponentInput>(initial);
  const [pending, setPending] = useState(false);

  function set<K extends keyof PricingComponentInput>(
    key: K,
    value: PricingComponentInput[K],
  ) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPending(true);
    try {
      await onSubmit(form);
    } finally {
      setPending(false);
    }
  }

  return (
    <Card>
      <CardContent className="pt-6">
        <h3 className="mb-4 text-base font-semibold">{title}</h3>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="pricing-key">Key (slug)</Label>
              <Input
                id="pricing-key"
                value={form.key}
                onChange={(e) => set("key", e.target.value)}
                disabled={!keyEditable}
                required
                placeholder="cpu"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pricing-name">Name</Label>
              <Input
                id="pricing-name"
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                required
                placeholder="CPU"
              />
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div className="space-y-2">
              <Label htmlFor="pricing-unit">Unit</Label>
              <Input
                id="pricing-unit"
                value={form.unit}
                onChange={(e) => set("unit", e.target.value)}
                placeholder="core-hour"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pricing-price">Price / hour (cents)</Label>
              <Input
                id="pricing-price"
                type="number"
                min={0}
                step="0.0001"
                value={form.pricePerHour}
                onChange={(e) => set("pricePerHour", Number(e.target.value))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pricing-currency">Currency</Label>
              <Input
                id="pricing-currency"
                value={form.currency}
                onChange={(e) => set("currency", e.target.value)}
                placeholder="usd"
              />
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2 sm:items-end">
            <div className="space-y-2">
              <Label htmlFor="pricing-sort">Sort order</Label>
              <Input
                id="pricing-sort"
                type="number"
                value={form.sortOrder}
                onChange={(e) => set("sortOrder", Number(e.target.value))}
              />
            </div>
            <Toggle
              label="Active"
              checked={form.active}
              onChange={(v) => set("active", v)}
            />
          </div>

          <div className="flex items-center gap-2">
            <Button type="submit" loading={pending}>
              Save component
            </Button>
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function Toggle({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onChange(!checked)}
      className="inline-flex h-10 items-center gap-2 text-sm font-medium pointer-coarse:min-h-11"
    >
      <span
        className={cn(
          "relative inline-flex h-5 w-9 items-center rounded-full transition-colors",
          checked ? "bg-primary" : "bg-muted",
        )}
      >
        <span
          className={cn(
            "inline-block h-4 w-4 transform rounded-full bg-white transition-transform",
            checked ? "translate-x-4" : "translate-x-0.5",
          )}
        />
      </span>
      {label}
    </button>
  );
}

// Accessible delete confirmation (role="alertdialog", focus the safe action,
// Escape to cancel). Inline to avoid window.confirm.
function ConfirmDialog({
  title,
  description,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
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
        aria-labelledby="pricing-confirm-title"
        aria-describedby="pricing-confirm-desc"
        className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between">
          <h2
            id="pricing-confirm-title"
            className="text-lg font-semibold text-destructive"
          >
            {title}
          </h2>
          <button
            type="button"
            onClick={onCancel}
            aria-label="Close"
            className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <p
          id="pricing-confirm-desc"
          className="mt-2 text-sm text-muted-foreground"
        >
          {description}
        </p>
        <div className="mt-6 flex justify-end gap-2">
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}

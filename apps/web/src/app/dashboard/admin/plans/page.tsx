"use client";

import { useState, type FormEvent } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type AdminPlan, type AdminPlanInput } from "@/lib/api";
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

const EMPTY_PLAN: AdminPlanInput = {
  id: "",
  name: "",
  description: "",
  priceCents: 0,
  currency: "usd",
  includedHours: 0,
  overagePerHourCents: 0,
  maxCpu: 1,
  maxMemoryMb: 512,
  maxApps: 1,
  isDefault: false,
  sortOrder: 0,
  active: true,
  stripePriceId: "",
};

function toInput(plan: AdminPlan): AdminPlanInput {
  return { ...plan };
}

function formatPrice(plan: AdminPlan): string {
  return (plan.priceCents / 100).toLocaleString(undefined, {
    style: "currency",
    currency: (plan.currency || "usd").toUpperCase(),
    minimumFractionDigits: plan.priceCents % 100 === 0 ? 0 : 2,
  });
}

export default function AdminPlansPage() {
  const { authedCall } = useAuth();

  // Demo fallback loads lazily (demo mode only); prod shows a real empty table.
  const demoPlans = useDemoData((m) => m.mockPlans, [] as AdminPlan[]);

  const { data, refetch, usingFallback } = useResource(
    () => authedCall((token, on) => api.listAdminPlans(token, on)),
    { data: demoPlans },
    [demoPlans],
  );
  const plans = data.data;

  // null = closed; "new" = create form; otherwise the plan id being edited.
  const [editing, setEditing] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const editingPlan =
    editing && editing !== "new"
      ? (plans.find((p) => p.id === editing) ?? null)
      : null;

  async function onDelete(plan: AdminPlan) {
    if (
      typeof window !== "undefined" &&
      !window.confirm(`Delete plan "${plan.name}"? This cannot be undone.`)
    ) {
      return;
    }
    setNotice(null);
    try {
      await authedCall((token, on) => api.deletePlan(plan.id, token, on));
      refetch();
    } catch {
      setNotice("Delete failed (API unreachable — demo mode).");
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Plans"
        description="Pricing, included usage, and per-plan resource quotas."
        actions={
          <Button
            onClick={() => setEditing((cur) => (cur === "new" ? null : "new"))}
          >
            <Plus className="h-4 w-4" />
            New plan
          </Button>
        }
      />

      {usingFallback && (
        <Notice variant="warning">
          Showing demo data — admin API unreachable. Edits won&apos;t persist.
        </Notice>
      )}

      {notice && <Notice variant="error">{notice}</Notice>}

      {editing === "new" && (
        <PlanForm
          key="new"
          initial={EMPTY_PLAN}
          title="Create plan"
          idEditable
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) => api.createPlan(input, token, on));
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
                  <th className="px-6 py-3 font-medium">Plan</th>
                  <th className="px-6 py-3 font-medium">Price</th>
                  <th className="px-6 py-3 font-medium">Included</th>
                  <th className="px-6 py-3 font-medium">Quotas</th>
                  <th className="px-6 py-3 font-medium">Status</th>
                  <th className="px-6 py-3 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {plans.map((plan) => (
                  <tr key={plan.id} className="hover:bg-muted/40">
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-2 font-medium">
                        {plan.name}
                        {plan.isDefault && (
                          <Badge variant="info">Default</Badge>
                        )}
                      </div>
                      <p className="font-mono text-xs text-muted-foreground">
                        {plan.id} · sort {plan.sortOrder}
                      </p>
                    </td>
                    <td className="px-6 py-4">
                      {formatPrice(plan)}
                      <span className="text-xs text-muted-foreground">
                        {" "}
                        /mo
                      </span>
                    </td>
                    <td className="px-6 py-4 text-muted-foreground">
                      {plan.includedHours}h
                      <span className="block text-xs">
                        +
                        {(plan.overagePerHourCents / 100).toLocaleString(
                          undefined,
                          {
                            style: "currency",
                            currency: (plan.currency || "usd").toUpperCase(),
                          },
                        )}
                        /h over
                      </span>
                    </td>
                    <td className="px-6 py-4 text-xs text-muted-foreground">
                      {plan.maxCpu} CPU · {plan.maxMemoryMb}MB · {plan.maxApps}{" "}
                      apps
                    </td>
                    <td className="px-6 py-4">
                      <Badge variant={plan.active ? "success" : "outline"}>
                        {plan.active ? "Active" : "Inactive"}
                      </Badge>
                    </td>
                    <td className="px-6 py-4">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() =>
                            setEditing((cur) =>
                              cur === plan.id ? null : plan.id,
                            )
                          }
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          Edit
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => onDelete(plan)}
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

      {editingPlan && (
        <PlanForm
          key={editingPlan.id}
          initial={toInput(editingPlan)}
          title={`Edit ${editingPlan.name}`}
          onCancel={() => setEditing(null)}
          onSubmit={async (input) => {
            try {
              await authedCall((token, on) =>
                api.updatePlan(editingPlan.id, input, token, on),
              );
              setEditing(null);
              refetch();
            } catch {
              setNotice("Update failed (API unreachable — demo mode).");
            }
          }}
        />
      )}
    </div>
  );
}

function PlanForm({
  initial,
  title,
  idEditable = false,
  onCancel,
  onSubmit,
}: {
  initial: AdminPlanInput;
  title: string;
  idEditable?: boolean;
  onCancel: () => void;
  onSubmit: (input: AdminPlanInput) => Promise<void>;
}) {
  const [form, setForm] = useState<AdminPlanInput>(initial);
  const [pending, setPending] = useState(false);

  function set<K extends keyof AdminPlanInput>(
    key: K,
    value: AdminPlanInput[K],
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
            <Field label="Plan ID (slug)" htmlFor="plan-id">
              <Input
                id="plan-id"
                value={form.id}
                onChange={(e) => set("id", e.target.value)}
                disabled={!idEditable}
                required
                placeholder="hobby"
              />
            </Field>
            <Field label="Name" htmlFor="plan-name">
              <Input
                id="plan-name"
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                required
              />
            </Field>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Stripe price ID" htmlFor="plan-stripe">
              <Input
                id="plan-stripe"
                value={form.stripePriceId}
                onChange={(e) => set("stripePriceId", e.target.value)}
                placeholder="price_..."
              />
            </Field>
          </div>

          <Field label="Description" htmlFor="plan-desc">
            <Input
              id="plan-desc"
              value={form.description}
              onChange={(e) => set("description", e.target.value)}
            />
          </Field>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Price (cents)" htmlFor="plan-price">
              <Input
                id="plan-price"
                type="number"
                min={0}
                value={form.priceCents}
                onChange={(e) => set("priceCents", Number(e.target.value))}
              />
            </Field>
            <Field label="Currency" htmlFor="plan-currency">
              <Input
                id="plan-currency"
                value={form.currency}
                onChange={(e) => set("currency", e.target.value)}
              />
            </Field>
            <Field label="Sort order" htmlFor="plan-sort">
              <Input
                id="plan-sort"
                type="number"
                value={form.sortOrder}
                onChange={(e) => set("sortOrder", Number(e.target.value))}
              />
            </Field>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Included hours" htmlFor="plan-hours">
              <Input
                id="plan-hours"
                type="number"
                min={0}
                value={form.includedHours}
                onChange={(e) => set("includedHours", Number(e.target.value))}
              />
            </Field>
            <Field label="Overage / hour (cents)" htmlFor="plan-overage">
              <Input
                id="plan-overage"
                type="number"
                min={0}
                value={form.overagePerHourCents}
                onChange={(e) =>
                  set("overagePerHourCents", Number(e.target.value))
                }
              />
            </Field>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Max CPU" htmlFor="plan-cpu">
              <Input
                id="plan-cpu"
                type="number"
                min={0}
                step="0.25"
                value={form.maxCpu}
                onChange={(e) => set("maxCpu", Number(e.target.value))}
              />
            </Field>
            <Field label="Max memory (MB)" htmlFor="plan-mem">
              <Input
                id="plan-mem"
                type="number"
                min={0}
                value={form.maxMemoryMb}
                onChange={(e) => set("maxMemoryMb", Number(e.target.value))}
              />
            </Field>
            <Field label="Max apps" htmlFor="plan-apps">
              <Input
                id="plan-apps"
                type="number"
                min={0}
                value={form.maxApps}
                onChange={(e) => set("maxApps", Number(e.target.value))}
              />
            </Field>
          </div>

          <div className="flex flex-wrap items-center gap-6">
            <Toggle
              label="Default plan"
              checked={form.isDefault}
              onChange={(v) => set("isDefault", v)}
            />
            <Toggle
              label="Active"
              checked={form.active}
              onChange={(v) => set("active", v)}
            />
          </div>

          <div className="flex items-center gap-2">
            <Button type="submit" loading={pending}>
              Save plan
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

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
    </div>
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
      className="inline-flex items-center gap-2 text-sm font-medium pointer-coarse:min-h-11"
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

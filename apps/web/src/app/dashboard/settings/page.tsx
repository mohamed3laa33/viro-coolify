"use client";

import { useState } from "react";
import { Check, Loader2 } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { api, type Plan } from "@/lib/api";
import { mockBilling, mockPlans } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";
import { PageHeader } from "@/components/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge, type BadgeVariant } from "@/components/ui/badge";

const TABS = ["General", "Team", "Billing"] as const;
type Tab = (typeof TABS)[number];

type Role = "owner" | "admin" | "member";

const ROLE_VARIANT: Record<Role, BadgeVariant> = {
  owner: "default",
  admin: "info",
  member: "outline",
};

const MEMBERS: Array<{ name: string; email: string; role: Role }> = [
  { name: "Demo User", email: "you@viro.dev", role: "owner" },
  { name: "Grace Hopper", email: "grace@acme.dev", role: "admin" },
  { name: "Alan Turing", email: "alan@acme.dev", role: "member" },
  { name: "Katherine Johnson", email: "kj@acme.dev", role: "member" },
];

export default function SettingsPage() {
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>("General");

  return (
    <div className="space-y-6">
      <PageHeader
        title="Settings"
        description="Manage your organization, team, and billing."
      />

      <div className="border-b border-border">
        <nav className="-mb-px flex gap-6">
          {TABS.map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => setTab(t)}
              className={cn(
                "border-b-2 px-1 pb-3 text-sm font-medium transition-colors",
                tab === t
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t}
            </button>
          ))}
        </nav>
      </div>

      {tab === "General" && (
        <Card>
          <CardHeader>
            <CardTitle>Organization</CardTitle>
            <CardDescription>
              Update your organization profile and contact details.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="org-name">Organization name</Label>
                <Input id="org-name" defaultValue="Acme Corp" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="org-slug">Slug</Label>
                <Input id="org-slug" defaultValue="acme-corp" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="billing-email">Billing email</Label>
                <Input
                  id="billing-email"
                  type="email"
                  defaultValue={user?.email ?? "billing@acme.dev"}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="region">Default region</Label>
                <Input id="region" defaultValue="iad" />
              </div>
            </div>
            <div className="flex justify-end">
              <Button>Save changes</Button>
            </div>
          </CardContent>
        </Card>
      )}

      {tab === "Team" && (
        <Card>
          <CardHeader className="flex-row items-center justify-between space-y-0">
            <div>
              <CardTitle>Team members</CardTitle>
              <CardDescription className="mt-1">
                People with access to this organization.
              </CardDescription>
            </div>
            <Button size="sm">Invite member</Button>
          </CardHeader>
          <CardContent className="p-0">
            <ul className="divide-y divide-border">
              {MEMBERS.map((m) => (
                <li
                  key={m.email}
                  className="flex items-center justify-between px-6 py-4"
                >
                  <div className="flex items-center gap-3">
                    <span className="flex h-9 w-9 items-center justify-center rounded-full bg-brand-balloon text-xs font-semibold text-white">
                      {m.name
                        .split(" ")
                        .map((p) => p[0])
                        .join("")
                        .slice(0, 2)
                        .toUpperCase()}
                    </span>
                    <div>
                      <p className="text-sm font-medium">{m.name}</p>
                      <p className="text-xs text-muted-foreground">{m.email}</p>
                    </div>
                  </div>
                  <Badge variant={ROLE_VARIANT[m.role]} className="capitalize">
                    {m.role}
                  </Badge>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {tab === "Billing" && <BillingTab />}
    </div>
  );
}

function formatPrice(plan: Plan): string {
  const amount = (plan.priceCents / 100).toLocaleString(undefined, {
    style: "currency",
    currency: (plan.currency || "usd").toUpperCase(),
    minimumFractionDigits: plan.priceCents % 100 === 0 ? 0 : 2,
  });
  return `${amount} / month`;
}

function BillingTab() {
  const { activeOrgId, authedCall } = useAuth();
  const [pendingPlan, setPendingPlan] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const { data: plansData } = useResource(
    () => api.getPlans(),
    { data: mockPlans, provider: "stripe" },
    [],
  );
  const plans = plansData.data;

  const { data: billing, refetch } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.getBilling(activeOrgId, token, on))
      : null,
    mockBilling,
    [activeOrgId],
  );

  const currentPlanId = billing.plan?.id ?? billing.subscription?.planId ?? null;
  const usage = billing.usage;
  const usedHours = usage?.hoursUsed ?? 0;
  const totalHours = usage?.includedHours ?? billing.plan?.includedHours ?? 0;
  const usagePct =
    totalHours > 0 ? Math.min(100, Math.round((usedHours / totalHours) * 100)) : 0;

  async function subscribe(planId: string) {
    if (!activeOrgId) {
      setNotice("Subscribe unavailable — no active organization (demo mode).");
      return;
    }
    setPendingPlan(planId);
    setNotice(null);
    try {
      const res = await authedCall((token, on) =>
        api.subscribe(activeOrgId, planId, token, on),
      );
      if (res.checkoutUrl && typeof window !== "undefined") {
        window.location.href = res.checkoutUrl;
        return;
      }
      setNotice(`Subscription updated — status: ${res.subscription.status}`);
      refetch();
    } catch {
      setNotice("Subscription queued locally (API unreachable — demo mode).");
    } finally {
      setPendingPlan(null);
    }
  }

  return (
    <div className="space-y-6">
      {notice && (
        <div className="rounded-md border border-primary/30 bg-primary/10 px-4 py-2 text-sm text-primary">
          {notice}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Usage this month</CardTitle>
          <CardDescription>
            Resets on the 1st. Overages are billed per-unit.
            {billing.plan && (
              <Badge variant="info" className="ml-2 align-middle">
                {billing.plan.name}
              </Badge>
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center justify-between text-sm">
            <span className="text-foreground">Compute (machine-hours)</span>
            <span className="text-muted-foreground">
              {usedHours}h / {totalHours}h
            </span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-brand-balloon"
              style={{ width: `${usagePct}%` }}
            />
          </div>
          {usage && usage.overageHours > 0 && (
            <p className="text-sm text-warning">
              {usage.overageHours}h over your included hours this period.
            </p>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {plans.map((plan) => {
          const isCurrent = plan.id === currentPlanId;
          return (
            <Card
              key={plan.id}
              className={cn("flex flex-col", isCurrent && "glow-violet")}
            >
              <CardHeader>
                <div className="flex items-center justify-between">
                  <CardTitle>{plan.name}</CardTitle>
                  {isCurrent && <Badge variant="success">Current</Badge>}
                </div>
                <CardDescription>{formatPrice(plan)}</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-1 flex-col justify-between space-y-4">
                <ul className="space-y-2 text-sm">
                  <li className="flex items-center gap-2">
                    <Check className="h-4 w-4 text-success" />
                    <span className="text-muted-foreground">
                      {plan.includedHours} included hours
                    </span>
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="h-4 w-4 text-success" />
                    <span className="text-muted-foreground">
                      {(plan.overagePerHourCents / 100).toLocaleString(undefined, {
                        style: "currency",
                        currency: (plan.currency || "usd").toUpperCase(),
                      })}{" "}
                      / overage hour
                    </span>
                  </li>
                  {plan.description && (
                    <li className="text-muted-foreground">{plan.description}</li>
                  )}
                </ul>
                <Button
                  className="w-full"
                  variant={isCurrent ? "secondary" : "primary"}
                  disabled={isCurrent || pendingPlan !== null}
                  onClick={() => subscribe(plan.id)}
                >
                  {pendingPlan === plan.id && (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  )}
                  {isCurrent ? "Current plan" : `Switch to ${plan.name}`}
                </Button>
              </CardContent>
            </Card>
          );
        })}
      </div>
    </div>
  );
}

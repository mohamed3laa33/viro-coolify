"use client";

import { useState } from "react";
import { Check } from "lucide-react";
import { useAuth } from "@/lib/auth";
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

function BillingTab() {
  const usage = [
    { label: "Compute (machine-hours)", used: 412, total: 750, unit: "h" },
    { label: "Bandwidth", used: 84, total: 200, unit: "GB" },
    { label: "Postgres storage", used: 12, total: 40, unit: "GB" },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-3">
      <div className="space-y-6 lg:col-span-2">
        <Card>
          <CardHeader>
            <CardTitle>Usage this month</CardTitle>
            <CardDescription>
              Resets on the 1st. Overages are billed per-unit.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            {usage.map((u) => {
              const pct = Math.round((u.used / u.total) * 100);
              return (
                <div key={u.label}>
                  <div className="flex items-center justify-between text-sm">
                    <span className="text-foreground">{u.label}</span>
                    <span className="text-muted-foreground">
                      {u.used}
                      {u.unit} / {u.total}
                      {u.unit}
                    </span>
                  </div>
                  <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-brand-balloon"
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                </div>
              );
            })}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Payment method</CardTitle>
            <CardDescription>
              Billing is handled by Stripe.{" "}
              <Badge variant="warning" className="ml-1 align-middle">
                Test mode
              </Badge>
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <span className="flex h-9 w-12 items-center justify-center rounded-md border border-border bg-surface-2 font-mono text-xs">
                VISA
              </span>
              <span className="text-sm text-muted-foreground">
                •••• 4242 — exp 12/29
              </span>
            </div>
            <Button variant="secondary" size="sm">
              Update
            </Button>
          </CardContent>
        </Card>
      </div>

      <Card className="h-fit glow-violet">
        <CardHeader>
          <CardTitle>Launch plan</CardTitle>
          <CardDescription>$29 / month</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <ul className="space-y-2 text-sm">
            {[
              "Unlimited apps",
              "Autoscaling machines",
              "Managed Postgres",
              "Email support",
            ].map((f) => (
              <li key={f} className="flex items-center gap-2">
                <Check className="h-4 w-4 text-success" />
                <span className="text-muted-foreground">{f}</span>
              </li>
            ))}
          </ul>
          <Button className="w-full">Upgrade to Scale</Button>
          <p className="text-center text-xs text-muted-foreground">
            Next invoice June 30, 2026
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

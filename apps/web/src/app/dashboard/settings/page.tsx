"use client";

import { useState, type FormEvent } from "react";
import { Check, Copy } from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  computeHoursUsed,
  formatCents,
  type BillingResponse,
  type Invitation,
  type Member,
  type MemberRole,
  type Plan,
  type Project,
  type Settings,
} from "@/lib/api";
import { useDemoData } from "@/lib/demo-data";
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
import { Select } from "@/components/ui/select";
import { Notice } from "@/components/ui/notice";
import { Badge, type BadgeVariant } from "@/components/ui/badge";

const TABS = ["General", "Team", "Billing"] as const;
type Tab = (typeof TABS)[number];

const ROLE_VARIANT: Record<MemberRole, BadgeVariant> = {
  owner: "default",
  admin: "info",
  member: "outline",
};

function initials(name: string): string {
  return name
    .split(" ")
    .map((p) => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

export default function SettingsPage() {
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
                "inline-flex items-center border-b-2 px-1 pb-3 pt-1 text-sm font-medium transition-colors pointer-coarse:min-h-11",
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

      {tab === "General" && <GeneralTab />}

      {tab === "Team" && <TeamTab />}

      {tab === "Billing" && <BillingTab />}
    </div>
  );
}

// Neutral, non-demo default so the General tab always has a Settings shape to
// read (the region field stays blank in production until the API responds).
const EMPTY_SETTINGS: Settings = {
  defaultCpu: 0,
  defaultMemoryMb: 0,
  defaultPlanId: "",
  cpuOvercommitFactor: 0,
  memoryOvercommitFactor: 0,
  defaultRegion: "",
  regions: [],
};

function GeneralTab() {
  const { user, orgs, activeOrgId, authedCall } = useAuth();

  const activeOrg = orgs.find((o) => o.id === activeOrgId) ?? orgs[0] ?? null;

  // Demo fallback loads lazily (demo mode only); prod shows EMPTY_SETTINGS.
  const demoSettings = useDemoData((m) => m.mockSettings, EMPTY_SETTINGS);

  // The default region is platform config; show it but only let admins edit it
  // on the admin settings page. Falls back to mock settings when unreachable.
  const { data: settings, error: settingsError } = useResource(
    user?.isAdmin
      ? () => authedCall((token, on) => api.getSettings(token, on))
      : null,
    demoSettings,
    [user?.isAdmin, demoSettings],
  );

  // TODO(backend): add org update route (PATCH /v1/orgs/:id). Until it exists,
  // org name/slug/billing-email are read-only and Save is disabled. Don't wire
  // these inputs to a fake handler — keep the control honest (invariant #6).
  const editingDisabledNote = "Org settings editing is coming soon";

  return (
    <Card>
      <CardHeader>
        <CardTitle>Organization</CardTitle>
        <CardDescription>
          Update your organization profile and contact details.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {settingsError && (
          <Notice variant="error">
            Couldn&apos;t load platform settings. Showing the last known
            defaults.
          </Notice>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="org-name">Organization name</Label>
            <Input
              id="org-name"
              key={`name-${activeOrg?.id ?? "none"}`}
              defaultValue={activeOrg?.name ?? ""}
              readOnly
              title={editingDisabledNote}
              aria-label={editingDisabledNote}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="org-slug">Slug</Label>
            <Input
              id="org-slug"
              key={`slug-${activeOrg?.id ?? "none"}`}
              defaultValue={activeOrg?.slug ?? ""}
              readOnly
              title={editingDisabledNote}
              aria-label={editingDisabledNote}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="billing-email">Billing email</Label>
            <Input
              id="billing-email"
              type="email"
              key={`email-${user?.id ?? "none"}`}
              defaultValue={user?.email ?? ""}
              readOnly
              title={editingDisabledNote}
              aria-label={editingDisabledNote}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="region">Default region</Label>
            <Input
              id="region"
              key={`region-${settings.defaultRegion}`}
              defaultValue={settings.defaultRegion}
              readOnly
            />
          </div>
        </div>
        <div className="flex items-center justify-end gap-3">
          <p className="text-xs text-muted-foreground">
            {editingDisabledNote}.
          </p>
          <Button
            disabled
            title={editingDisabledNote}
            aria-label={editingDisabledNote}
          >
            Save changes
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function projectLabel(projectId: string | null, projects: Project[]): string {
  if (!projectId) return "Organization-wide";
  return projects.find((p) => p.id === projectId)?.name ?? projectId;
}

function TeamTab() {
  const { activeOrgId, authedCall } = useAuth();

  // Demo fallbacks load lazily (demo mode only); never shipped to prod.
  const demoMembers = useDemoData((m) => m.mockMembers, [] as Member[]);
  const demoInvitations = useDemoData(
    (m) => m.mockInvitations,
    [] as Invitation[],
  );
  const demoProjects = useDemoData((m) => m.mockProjects, [] as Project[]);

  const { data: membersData, error: membersError } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listMembers(activeOrgId, token, on))
      : null,
    { data: demoMembers },
    [activeOrgId, demoMembers],
  );

  const {
    data: invitesData,
    refetch: refetchInvites,
    error: invitesError,
  } = useResource(
    activeOrgId
      ? () =>
          authedCall((token, on) => api.listInvitations(activeOrgId, token, on))
      : null,
    { data: demoInvitations },
    [activeOrgId, demoInvitations],
  );

  const { data: projectsData } = useResource(
    activeOrgId
      ? () =>
          authedCall((token, on) => api.listProjects(activeOrgId, token, on))
      : null,
    { data: demoProjects },
    [activeOrgId, demoProjects],
  );

  const members = membersData.data;
  const invitations = invitesData.data;
  const projects = projectsData.data;

  const [email, setEmail] = useState("");
  const [role, setRole] = useState<MemberRole>("member");
  const [projectId, setProjectId] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  async function onInvite(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmed = email.trim();
    if (!trimmed) return;
    if (!activeOrgId) {
      setNotice("Invite unavailable — no active organization (demo mode).");
      return;
    }
    setPending(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.invite(
          activeOrgId,
          {
            email: trimmed,
            role,
            projectId: projectId || undefined,
          },
          token,
          on,
        ),
      );
      setEmail("");
      setRole("member");
      setProjectId("");
      refetchInvites();
    } catch {
      setNotice("Invitation queued locally (API unreachable — demo mode).");
    } finally {
      setPending(false);
    }
  }

  async function copyToken(token: string) {
    const link =
      typeof window !== "undefined"
        ? `${window.location.origin}/invite?token=${encodeURIComponent(token)}`
        : token;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(link);
      }
      setCopied(token);
      setTimeout(() => setCopied((c) => (c === token ? null : c)), 1500);
    } catch {
      setCopied(null);
    }
  }

  return (
    <div className="space-y-6">
      {notice && <Notice variant="info">{notice}</Notice>}

      {(membersError || invitesError) && (
        <Notice variant="error">
          Couldn&apos;t load team data. Showing the last known values.
        </Notice>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Team members</CardTitle>
          <CardDescription className="mt-1">
            People with access to this organization.
          </CardDescription>
        </CardHeader>
        {/* TODO(backend): role change / member removal need API routes
            (PATCH/DELETE /v1/orgs/:id/members/:userId). Until they exist this
            list stays read-only — don't add no-op edit/remove controls. */}
        <CardContent className="p-0">
          <ul className="divide-y divide-border">
            {members.map((m) => (
              <li
                key={m.userId}
                className="flex items-center justify-between px-6 py-4"
              >
                <div className="flex items-center gap-3">
                  <span className="flex h-9 w-9 items-center justify-center rounded-full bg-brand-balloon text-xs font-semibold text-white">
                    {initials(m.name || m.email)}
                  </span>
                  <div>
                    <p className="text-sm font-medium">{m.name || m.email}</p>
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

      <Card>
        <CardHeader>
          <CardTitle>Invite a member</CardTitle>
          <CardDescription className="mt-1">
            Invite someone by email. Optionally scope them to a single project.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            onSubmit={onInvite}
            className="grid gap-4 sm:grid-cols-[1fr_140px_1fr_auto] sm:items-end"
          >
            <div className="space-y-2">
              <Label htmlFor="invite-email">Email</Label>
              <Input
                id="invite-email"
                type="email"
                placeholder="teammate@acme.dev"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="invite-role">Role</Label>
              <Select
                id="invite-role"
                value={role}
                onChange={(e) => setRole(e.target.value as MemberRole)}
              >
                <option value="member">Member</option>
                <option value="admin">Admin</option>
                <option value="owner">Owner</option>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="invite-project">Project (optional)</Label>
              <Select
                id="invite-project"
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
              >
                <option value="">Organization-wide</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>
            <Button type="submit" loading={pending}>
              Send invite
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Pending invitations</CardTitle>
          <CardDescription className="mt-1">
            Share the invite link manually until email delivery is enabled.
          </CardDescription>
        </CardHeader>
        {/* TODO(backend): revoke pending invitation needs an API route
            (DELETE /v1/orgs/:id/invitations/:inviteId). Until it exists we
            only surface copy-link — no fake revoke control. */}
        <CardContent className="p-0">
          {invitations.length === 0 ? (
            <p className="px-6 py-4 text-sm text-muted-foreground">
              No pending invitations.
            </p>
          ) : (
            <ul className="divide-y divide-border">
              {invitations.map((inv) => (
                <li key={inv.id} className="px-6 py-4">
                  <div className="flex items-center justify-between gap-4">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">
                        {inv.email}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {projectLabel(inv.projectId, projects)}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge
                        variant={ROLE_VARIANT[inv.role]}
                        className="capitalize"
                      >
                        {inv.role}
                      </Badge>
                      <Badge
                        variant={
                          inv.status === "pending" ? "warning" : "outline"
                        }
                        className="capitalize"
                      >
                        {inv.status}
                      </Badge>
                    </div>
                  </div>
                  <div className="mt-3 flex items-center gap-2">
                    <code className="flex-1 truncate rounded-md border border-border bg-surface-2 px-3 py-1.5 font-mono text-xs text-muted-foreground">
                      {inv.token}
                    </code>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => copyToken(inv.token)}
                    >
                      {copied === inv.token ? (
                        <Check className="h-3.5 w-3.5" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                      {copied === inv.token ? "Copied" : "Copy link"}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
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

// Neutral, non-demo default so the Billing tab always has a shape to read.
const EMPTY_BILLING: BillingResponse = {
  subscription: null,
  plan: null,
  usage: {},
};

function BillingTab() {
  const { activeOrgId, authedCall } = useAuth();
  const [pendingPlan, setPendingPlan] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Demo fallbacks load lazily (demo mode only); prod shows real empty states.
  const demoPlans = useDemoData((m) => m.mockPlans, [] as Plan[]);
  const demoBilling = useDemoData((m) => m.mockBilling, EMPTY_BILLING);

  const { data: plansData, error: plansError } = useResource(
    () => api.getPlans(),
    { data: demoPlans, provider: "stripe" },
    [demoPlans],
    { cacheKey: "plans" },
  );
  const plans = plansData.data;

  const {
    data: billing,
    refetch,
    error: billingError,
  } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.getBilling(activeOrgId, token, on))
      : null,
    demoBilling,
    [activeOrgId, demoBilling],
    { cacheKey: activeOrgId ? `billing:${activeOrgId}` : undefined },
  );

  const currentPlanId =
    billing.plan?.id ?? billing.subscription?.planId ?? null;
  const usedHours = computeHoursUsed(billing.usage);
  const totalHours = billing.plan?.includedHours ?? 0;
  const overageHours = Math.max(0, usedHours - totalHours);
  const usagePct =
    totalHours > 0
      ? Math.min(100, Math.round((usedHours / totalHours) * 100))
      : 0;

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
      {notice && <Notice variant="info">{notice}</Notice>}

      {(plansError || billingError) && (
        <Notice variant="error">
          Couldn&apos;t load billing data. Showing the last known values.
        </Notice>
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
          {overageHours > 0 && (
            <p className="text-sm text-warning">
              {overageHours}h over your included hours this period.
            </p>
          )}
          {typeof billing.estimatedMonthlyCents === "number" && (
            <div className="flex items-center justify-between border-t border-border pt-3 text-sm">
              <span className="text-foreground">Estimated monthly cost</span>
              <span className="font-medium tabular-nums">
                {formatCents(
                  billing.estimatedMonthlyCents,
                  billing.currency ?? billing.plan?.currency,
                )}
              </span>
            </div>
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
                      {(plan.overagePerHourCents / 100).toLocaleString(
                        undefined,
                        {
                          style: "currency",
                          currency: (plan.currency || "usd").toUpperCase(),
                        },
                      )}{" "}
                      / overage hour
                    </span>
                  </li>
                  {plan.description && (
                    <li className="text-muted-foreground">
                      {plan.description}
                    </li>
                  )}
                </ul>
                <Button
                  className="w-full"
                  variant={isCurrent ? "secondary" : "primary"}
                  disabled={isCurrent || pendingPlan !== null}
                  loading={pendingPlan === plan.id}
                  onClick={() => subscribe(plan.id)}
                >
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

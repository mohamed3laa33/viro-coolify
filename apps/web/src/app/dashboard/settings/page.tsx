"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  api,
  ApiError,
  computeHoursUsed,
  formatCents,
  formatHourlyPrice,
  type BillingResponse,
  type Invitation,
  type Member,
  type MemberRole,
  type Plan,
  type PricingComponent,
  type Project,
  type Settings,
} from "@/lib/api";
import { isDemoMode } from "@/lib/demo";
import { errorMessage } from "@/lib/errors";
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
import { Notice, type NoticeVariant } from "@/components/ui/notice";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Tabs } from "@/components/ui/tabs";

const TABS = ["General", "Team", "Billing"] as const;
type Tab = (typeof TABS)[number];

// A transient action notice. The variant lets failed mutations render an honest
// `error` banner (invariant #6) rather than reusing the neutral `info` style.
interface ActionNotice {
  variant: NoticeVariant;
  message: string;
}

// Build the notice for a failed mutation. In production this is always an honest
// `error` banner carrying the backend's message (or a real network fallback) and
// never "queued locally"/"demo mode" wording (invariant #6). Only in demo mode,
// and only for a true network failure, do we hint that the unreachable API means
// the action was not actually applied.
function failureNotice(err: unknown, fallback: string): ActionNotice {
  // A real backend 4xx/5xx is always surfaced honestly via its message.
  const backendError = err instanceof ApiError;
  if (isDemoMode() && !backendError) {
    // Demo mode + a true network/TypeError: the API was unreachable, so the
    // mutation was NOT applied. Flag that explicitly so it is not read as
    // success, without claiming it was queued/persisted (invariant #6).
    return {
      variant: "warning",
      message: `${fallback} (demo mode: API unreachable, not applied)`,
    };
  }
  return { variant: "error", message: errorMessage(err, fallback) };
}

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

      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      <TabPanel tab={tab}>
        {tab === "General" && <GeneralTab />}
        {tab === "Team" && <TeamTab />}
        {tab === "Billing" && <BillingTab />}
      </TabPanel>
    </div>
  );
}

// Wraps the active tab's content as an ARIA tabpanel. The Tabs primitive owns
// the tab `id`s (generated via useId), so we expose a self-describing panel
// (role + label + focusable) rather than referencing ids we cannot read here.
function TabPanel({ tab, children }: { tab: Tab; children: ReactNode }) {
  return (
    <div
      role="tabpanel"
      id={`settings-tabpanel-${tab}`}
      aria-label={tab}
      tabIndex={0}
      className="focus-visible:outline-none"
    >
      {children}
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

  const [name, setName] = useState<string | null>(null);
  const [billingEmail, setBillingEmail] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<ActionNotice | null>(null);
  const [fieldError, setFieldError] = useState(false);

  // Controlled inputs seeded from the active org/user, but only once the user
  // starts editing (null = "use the live value"). This keeps the inputs in sync
  // when the active org changes without clobbering in-progress edits.
  const nameValue = name ?? activeOrg?.name ?? "";
  const billingEmailValue = billingEmail ?? user?.email ?? "";

  async function onSave(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!activeOrgId) {
      setNotice({
        variant: "error",
        message: "No active organization to update.",
      });
      return;
    }
    const trimmedName = nameValue.trim();
    const trimmedEmail = billingEmailValue.trim();
    if (!trimmedName) {
      setFieldError(true);
      setNotice({
        variant: "error",
        message: "Organization name is required.",
      });
      return;
    }
    setFieldError(false);
    setSaving(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.updateOrg(
          activeOrgId,
          { name: trimmedName, billingEmail: trimmedEmail || undefined },
          token,
          on,
        ),
      );
      setNotice({ variant: "success", message: "Organization updated." });
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't update the organization."));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Organization</CardTitle>
        <CardDescription>
          Update your organization profile and contact details.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {notice && <Notice variant={notice.variant}>{notice.message}</Notice>}
        {settingsError && (
          <Notice variant="error">
            Couldn&apos;t load platform settings. Showing the last known
            defaults.
          </Notice>
        )}
        <form onSubmit={onSave} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="org-name">Organization name</Label>
              <Input
                id="org-name"
                value={nameValue}
                onChange={(e) => setName(e.target.value)}
                aria-invalid={fieldError || undefined}
                aria-describedby={fieldError ? "org-name-error" : undefined}
                required
              />
              {fieldError && (
                <p id="org-name-error" className="text-xs text-destructive">
                  Organization name is required.
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="org-slug">Slug</Label>
              <Input
                id="org-slug"
                key={`slug-${activeOrg?.id ?? "none"}`}
                defaultValue={activeOrg?.slug ?? ""}
                readOnly
                title="The slug is fixed at creation time."
                aria-label="Organization slug (read-only)"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="billing-email">Billing email</Label>
              <Input
                id="billing-email"
                type="email"
                value={billingEmailValue}
                onChange={(e) => setBillingEmail(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="region">Default region</Label>
              <Input
                id="region"
                key={`region-${settings.defaultRegion}`}
                defaultValue={settings.defaultRegion}
                readOnly
                title="The default region is set on the admin settings page."
                aria-label="Default region (read-only)"
              />
            </div>
          </div>
          <div className="flex items-center justify-end">
            <Button type="submit" loading={saving} disabled={!activeOrgId}>
              Save changes
            </Button>
          </div>
        </form>
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

  const {
    data: membersData,
    refetch: refetchMembers,
    error: membersError,
  } = useResource(
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
  const [notice, setNotice] = useState<ActionNotice | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  // userId currently being role-changed (disables that row's Select).
  const [savingRole, setSavingRole] = useState<string | null>(null);
  // Member pending removal confirmation, then the in-flight delete.
  const [memberToRemove, setMemberToRemove] = useState<Member | null>(null);
  const [removingMember, setRemovingMember] = useState(false);
  // Invitation pending revoke confirmation, then the in-flight delete.
  const [inviteToRevoke, setInviteToRevoke] = useState<Invitation | null>(null);
  const [revokingInvite, setRevokingInvite] = useState(false);

  async function onInvite(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmed = email.trim();
    if (!trimmed) return;
    if (!activeOrgId) {
      setNotice({
        variant: "error",
        message: "No active organization to invite to.",
      });
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
      setNotice({
        variant: "success",
        message: `Invitation sent to ${trimmed}.`,
      });
      refetchInvites();
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't send the invitation."));
    } finally {
      setPending(false);
    }
  }

  async function onChangeRole(member: Member, nextRole: MemberRole) {
    if (!activeOrgId || nextRole === member.role) return;
    setSavingRole(member.userId);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.updateMember(
          activeOrgId,
          member.userId,
          { role: nextRole },
          token,
          on,
        ),
      );
      setNotice({
        variant: "success",
        message: `${member.name || member.email} is now ${nextRole}.`,
      });
      refetchMembers();
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't change the member's role."));
    } finally {
      setSavingRole(null);
    }
  }

  async function onConfirmRemoveMember() {
    if (!activeOrgId || !memberToRemove) return;
    setRemovingMember(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.removeMember(activeOrgId, memberToRemove.userId, token, on),
      );
      setNotice({
        variant: "success",
        message: `Removed ${memberToRemove.name || memberToRemove.email}.`,
      });
      setMemberToRemove(null);
      refetchMembers();
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't remove the member."));
    } finally {
      setRemovingMember(false);
    }
  }

  async function onConfirmRevokeInvite() {
    if (!activeOrgId || !inviteToRevoke) return;
    setRevokingInvite(true);
    setNotice(null);
    try {
      await authedCall((token, on) =>
        api.revokeInvitation(activeOrgId, inviteToRevoke.id, token, on),
      );
      setNotice({
        variant: "success",
        message: `Revoked the invitation for ${inviteToRevoke.email}.`,
      });
      setInviteToRevoke(null);
      refetchInvites();
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't revoke the invitation."));
    } finally {
      setRevokingInvite(false);
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
      {notice && <Notice variant={notice.variant}>{notice.message}</Notice>}

      {(membersError || invitesError) && (
        <Notice variant="error">
          Couldn&apos;t load team data. Showing the last known values.
        </Notice>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Team members</CardTitle>
          <CardDescription className="mt-1">
            People with access to this organization. Owners can change roles or
            remove members.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          <ul className="divide-y divide-border">
            {members.map((m) => (
              <li
                key={m.userId}
                className="flex items-center justify-between gap-4 px-6 py-4"
              >
                <div className="flex min-w-0 items-center gap-3">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-brand-balloon text-xs font-semibold text-white">
                    {initials(m.name || m.email)}
                  </span>
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">
                      {m.name || m.email}
                    </p>
                    <p className="truncate text-xs text-muted-foreground">
                      {m.email}
                    </p>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Label
                    htmlFor={`member-role-${m.userId}`}
                    className="sr-only"
                  >
                    Role for {m.name || m.email}
                  </Label>
                  <div className="w-32">
                    <Select
                      id={`member-role-${m.userId}`}
                      value={m.role}
                      disabled={!activeOrgId || savingRole === m.userId}
                      onChange={(e) =>
                        onChangeRole(m, e.target.value as MemberRole)
                      }
                    >
                      <option value="member">Member</option>
                      <option value="admin">Admin</option>
                      <option value="owner">Owner</option>
                    </Select>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={!activeOrgId}
                    onClick={() => setMemberToRemove(m)}
                  >
                    Remove
                  </Button>
                </div>
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
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={!activeOrgId}
                      onClick={() => setInviteToRevoke(inv)}
                    >
                      Revoke
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={memberToRemove !== null}
        title="Remove member?"
        description={
          memberToRemove
            ? `${memberToRemove.name || memberToRemove.email} will lose access to this organization. They can be re-invited later.`
            : undefined
        }
        confirmLabel="Remove"
        destructive
        loading={removingMember}
        onConfirm={onConfirmRemoveMember}
        onCancel={() => {
          if (!removingMember) setMemberToRemove(null);
        }}
      />

      <ConfirmDialog
        open={inviteToRevoke !== null}
        title="Revoke invitation?"
        description={
          inviteToRevoke
            ? `The invitation for ${inviteToRevoke.email} will stop working immediately.`
            : undefined
        }
        confirmLabel="Revoke"
        destructive
        loading={revokingInvite}
        onConfirm={onConfirmRevokeInvite}
        onCancel={() => {
          if (!revokingInvite) setInviteToRevoke(null);
        }}
      />
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
  const [notice, setNotice] = useState<ActionNotice | null>(null);

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

  // Public hourly rates (admin-managed). Read-only here; no demo fabrication —
  // an empty list is the honest prod fallback (invariant #1: business values
  // come from the API/admin, never hardcoded).
  const { data: pricingData } = useResource(
    () => api.getPricing(),
    { data: [] as PricingComponent[] },
    [],
    { cacheKey: "pricing" },
  );
  const pricing = pricingData.data
    .filter((c) => c.active)
    .sort((a, b) => a.sortOrder - b.sortOrder);

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
      setNotice({
        variant: "error",
        message: "No active organization to subscribe.",
      });
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
      setNotice({
        variant: "success",
        message: `Subscription updated — status: ${res.subscription.status}`,
      });
      refetch();
    } catch (err) {
      setNotice(failureNotice(err, "Couldn't update the subscription."));
    } finally {
      setPendingPlan(null);
    }
  }

  return (
    <div className="space-y-6">
      {notice && <Notice variant={notice.variant}>{notice.message}</Notice>}

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

      {pricing.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Hourly rates</CardTitle>
            <CardDescription>
              Metered, per-unit rates used to compute overages and usage-based
              charges.
            </CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <ul className="divide-y divide-border">
              {pricing.map((c) => (
                <li
                  key={c.key}
                  className="flex items-center justify-between gap-4 px-6 py-3 text-sm"
                >
                  <span className="text-foreground">{c.name}</span>
                  <span className="font-medium tabular-nums text-muted-foreground">
                    {formatHourlyPrice(c)}
                  </span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

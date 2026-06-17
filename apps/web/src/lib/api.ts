// Typed API client for the Vortex Go control-plane.
//
// Reads the base URL from NEXT_PUBLIC_VORTEX_API_URL (falling back to the legacy
// NEXT_PUBLIC_VIRO_API_URL, then http://localhost:8080) and attaches
// `Authorization: Bearer <token>` when a token is provided.
//
// Resource endpoints are org-scoped: /v1/orgs/{orgId}/...

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_VORTEX_API_URL ??
  process.env.NEXT_PUBLIC_VIRO_API_URL ??
  "http://localhost:8080";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface User {
  id: string;
  email: string;
  name: string;
  isAdmin?: boolean;
}

export interface AuthResponse {
  user: User;
  accessToken: string;
  refreshToken: string;
}

export interface Org {
  id: string;
  name: string;
  slug: string;
  createdAt: string;
}

// Status is an open string from the backend:
// created | deploying | restarting | stopped | running | error
export type AppStatus = string;

export interface App {
  id: string;
  orgId: string;
  coolifyUuid: string;
  name: string;
  gitRepository: string;
  gitBranch: string;
  buildPack: string;
  status: AppStatus;
  createdAt: string;
}

// Engine is an open string: postgresql | mysql | mariadb | mongodb | redis | ...
export type DatabaseEngine = string;

export interface Database {
  id: string;
  orgId: string;
  name: string;
  engine: DatabaseEngine;
  status: string;
  createdAt: string;
}

export interface Plan {
  id: string;
  name: string;
  description: string;
  priceCents: number;
  currency: string;
  includedHours: number;
  overagePerHourCents: number;
  // Admin-managed quota/catalog fields. Optional on the public billing
  // endpoint; always present on the admin endpoints.
  maxCpu?: number;
  maxMemoryMb?: number;
  maxApps?: number;
  isDefault?: boolean;
  sortOrder?: number;
  active?: boolean;
  stripePriceId?: string;
}

// The full plan shape returned/edited by the admin API.
export interface AdminPlan extends Plan {
  maxCpu: number;
  maxMemoryMb: number;
  maxApps: number;
  isDefault: boolean;
  sortOrder: number;
  active: boolean;
  stripePriceId: string;
}

// Payload for creating/updating an admin plan. The backend requires an `id`
// (the plan slug) in the create body; it is immutable on update (taken from the
// path), so callers may send the full plan shape including the id.
export type AdminPlanInput = AdminPlan;

export type TemplateKind = "service" | "database" | "app";

export interface Template {
  key: string;
  name: string;
  description: string;
  category: string;
  kind: TemplateKind;
  image: string;
  defaultPort: number;
  active: boolean;
  sortOrder: number;
}

// On create the key is supplied in the body; on update it is in the path.
export type TemplateInput = Template;

export interface Settings {
  defaultCpu: number;
  defaultMemoryMb: number;
  defaultPlanId: string;
  cpuOvercommitFactor: number;
  memoryOvercommitFactor: number;
  defaultRegion: string;
  regions: string[];
}

export interface AdminOverview {
  orgCount: number;
  userCount: number;
  subscriptionsByPlan: Record<string, number>;
  usageTotals: Record<string, number>;
}

export interface Subscription {
  orgId: string;
  planId: string;
  status: string;
  createdAt?: string;
  currentPeriodEnd?: string;
}

// The backend reports usage as a metric -> quantity map (e.g.
// { "compute_hours": 412, "builds": 30 }); it is never a fixed shape.
export type Usage = Record<string, number>;

export interface BillingResponse {
  subscription: Subscription | null;
  plan: Plan | null;
  usage: Usage;
}

// Metric key the backend records for metered compute, used to derive the
// "hours used" figure shown in the billing UI.
export const COMPUTE_HOURS_METRICS = ["compute_hours", "machine_hours", "hours"];

/** Sum the compute-hours-style usage metrics from a usage map. */
export function computeHoursUsed(usage: Usage | null | undefined): number {
  if (!usage) return 0;
  for (const key of COMPUTE_HOURS_METRICS) {
    if (typeof usage[key] === "number") return usage[key];
  }
  return 0;
}

export interface SubscribeResponse {
  subscription: Subscription;
  checkoutUrl?: string;
}

export interface PlansResponse {
  data: Plan[];
  provider: string;
}

export interface ListResponse<T> {
  data: T[];
}

export interface Project {
  id: string;
  name: string;
  slug: string;
  isDefault: boolean;
  createdAt: string;
}

export type MemberRole = "owner" | "admin" | "member";

export interface Member {
  userId: string;
  email: string;
  name: string;
  role: MemberRole;
}

export type InvitationStatus = "pending" | "accepted" | "revoked" | "expired";

export interface Invitation {
  id: string;
  email: string;
  role: MemberRole;
  projectId: string | null;
  token: string;
  status: InvitationStatus;
  createdAt: string;
}

export interface InviteInput {
  email: string;
  role: MemberRole;
  projectId?: string;
}

export interface EnvVar {
  key: string;
  value: string;
}

export interface Domain {
  id: string;
  domain: string;
  verified: boolean;
}

export interface MetricPoint {
  t: string;
  v: number;
}

export interface AppMetrics {
  cpu: MetricPoint[];
  memory: MetricPoint[];
  requests: MetricPoint[];
}

export interface CreateAppInput {
  name: string;
  gitRepository: string;
  gitBranch: string;
  buildPack: string;
}

export interface CreateDatabaseInput {
  name: string;
  engine: string;
}

export interface CreateProjectInput {
  name: string;
}

export interface SignupInput {
  email: string;
  name: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

// ---------------------------------------------------------------------------
// Status helper
// ---------------------------------------------------------------------------

export type StatusVariant =
  | "success"
  | "warning"
  | "muted"
  | "destructive"
  | "info";

/**
 * Map an open-string resource status to a UI variant.
 * running -> success, deploying/restarting -> warning, stopped -> muted,
 * error -> destructive, created -> info. Unknown values fall back to muted.
 */
export function statusVariant(status: string): StatusVariant {
  switch (status) {
    case "running":
      return "success";
    case "deploying":
    case "restarting":
      return "warning";
    case "stopped":
      return "muted";
    case "error":
      return "destructive";
    case "created":
      return "info";
    default:
      return "muted";
  }
}

// ---------------------------------------------------------------------------
// Error
// ---------------------------------------------------------------------------

export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

// ---------------------------------------------------------------------------
// Request helper
// ---------------------------------------------------------------------------

/**
 * Optional hook invoked when a tokened request returns 401. It should attempt
 * a token refresh and resolve with a fresh access token to retry with, or
 * null/undefined to give up. It is only called once per request.
 */
export type OnUnauthorized = () => Promise<string | null>;

interface RequestOptions {
  method?: string;
  body?: unknown;
  token?: string | null;
  signal?: AbortSignal;
  onUnauthorized?: OnUnauthorized;
}

export function buildUrl(path: string): string {
  const base = API_BASE_URL.replace(/\/+$/, "");
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${base}${suffix}`;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, token, signal, onUnauthorized } = options;

  async function exec(authToken: string | null | undefined): Promise<Response> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (authToken) {
      headers["Authorization"] = `Bearer ${authToken}`;
    }
    return fetch(buildUrl(path), {
      method,
      headers,
      signal,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  }

  let res = await exec(token);

  // Refresh-on-401: if we sent a token, got a 401, and have a refresh hook,
  // try to refresh once and retry the request a single time.
  if (res.status === 401 && token && onUnauthorized) {
    const newToken = await onUnauthorized();
    if (newToken) {
      res = await exec(newToken);
    }
  }

  if (!res.ok) {
    let message = `Request failed with status ${res.status}`;
    try {
      const data = (await res.json()) as { message?: string; error?: string };
      message = data.message ?? data.error ?? message;
    } catch {
      // Non-JSON error body; keep default message.
    }
    throw new ApiError(message, res.status);
  }

  if (res.status === 204) {
    return undefined as T;
  }

  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// API surface
// ---------------------------------------------------------------------------

export const api = {
  // Auth
  signup(input: SignupInput): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/signup", {
      method: "POST",
      body: input,
    });
  },

  login(input: LoginInput): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/login", {
      method: "POST",
      body: input,
    });
  },

  refresh(refreshToken: string): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/refresh", {
      method: "POST",
      body: { refreshToken },
    });
  },

  me(token: string, onUnauthorized?: OnUnauthorized): Promise<User> {
    return request<User>("/v1/me", { token, onUnauthorized });
  },

  // Orgs
  listOrgs(token: string, onUnauthorized?: OnUnauthorized): Promise<ListResponse<Org>> {
    return request<ListResponse<Org>>("/v1/orgs", { token, onUnauthorized });
  },

  createOrg(name: string, token: string, onUnauthorized?: OnUnauthorized): Promise<Org> {
    return request<Org>("/v1/orgs", {
      method: "POST",
      body: { name },
      token,
      onUnauthorized,
    });
  },

  // Apps (org-scoped)
  listApps(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<App>> {
    return request<ListResponse<App>>(`/v1/orgs/${orgId}/apps`, {
      token,
      onUnauthorized,
    });
  },

  getApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}`, {
      token,
      onUnauthorized,
    });
  },

  createApp(
    orgId: string,
    input: CreateAppInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
    });
  },

  deployApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/deploy`, {
      method: "POST",
      token,
      onUnauthorized,
    });
  },

  stopApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/stop`, {
      method: "POST",
      token,
      onUnauthorized,
    });
  },

  restartApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/restart`, {
      method: "POST",
      token,
      onUnauthorized,
    });
  },

  deleteApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/apps/${appId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
    });
  },

  // Databases (org-scoped)
  listDatabases(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Database>> {
    return request<ListResponse<Database>>(`/v1/orgs/${orgId}/databases`, {
      token,
      onUnauthorized,
    });
  },

  createDatabase(
    orgId: string,
    input: CreateDatabaseInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Database> {
    return request<Database>(`/v1/orgs/${orgId}/databases`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
    });
  },

  // Projects (org-scoped)
  listProjects(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Project>> {
    return request<ListResponse<Project>>(`/v1/orgs/${orgId}/projects`, {
      token,
      onUnauthorized,
    });
  },

  createProject(
    orgId: string,
    name: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Project> {
    return request<Project>(`/v1/orgs/${orgId}/projects`, {
      method: "POST",
      body: { name },
      token,
      onUnauthorized,
    });
  },

  listProjectApps(
    orgId: string,
    projectId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<App>> {
    return request<ListResponse<App>>(
      `/v1/orgs/${orgId}/projects/${projectId}/apps`,
      { token, onUnauthorized },
    );
  },

  // Members (org-scoped)
  listMembers(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Member>> {
    return request<ListResponse<Member>>(`/v1/orgs/${orgId}/members`, {
      token,
      onUnauthorized,
    });
  },

  // Invitations
  listInvitations(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Invitation>> {
    return request<ListResponse<Invitation>>(
      `/v1/orgs/${orgId}/invitations`,
      { token, onUnauthorized },
    );
  },

  invite(
    orgId: string,
    input: InviteInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Invitation> {
    return request<Invitation>(`/v1/orgs/${orgId}/invitations`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
    });
  },

  acceptInvitation(
    inviteToken: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Invitation> {
    return request<Invitation>(`/v1/invitations/accept`, {
      method: "POST",
      body: { token: inviteToken },
      token,
      onUnauthorized,
    });
  },

  // App environment variables (org-scoped)
  listEnv(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<EnvVar>> {
    return request<ListResponse<EnvVar>>(
      `/v1/orgs/${orgId}/apps/${appId}/env`,
      { token, onUnauthorized },
    );
  },

  setEnv(
    orgId: string,
    appId: string,
    input: EnvVar,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<EnvVar> {
    return request<EnvVar>(`/v1/orgs/${orgId}/apps/${appId}/env`, {
      method: "PUT",
      body: input,
      token,
      onUnauthorized,
    });
  },

  deleteEnv(
    orgId: string,
    appId: string,
    key: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<void> {
    return request<void>(
      `/v1/orgs/${orgId}/apps/${appId}/env/${encodeURIComponent(key)}`,
      { method: "DELETE", token, onUnauthorized },
    );
  },

  // App domains (org-scoped)
  listDomains(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Domain>> {
    return request<ListResponse<Domain>>(
      `/v1/orgs/${orgId}/apps/${appId}/domains`,
      { token, onUnauthorized },
    );
  },

  addDomain(
    orgId: string,
    appId: string,
    domain: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Domain> {
    return request<Domain>(`/v1/orgs/${orgId}/apps/${appId}/domains`, {
      method: "POST",
      body: { domain },
      token,
      onUnauthorized,
    });
  },

  deleteDomain(
    orgId: string,
    appId: string,
    domainId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<void> {
    return request<void>(
      `/v1/orgs/${orgId}/apps/${appId}/domains/${domainId}`,
      { method: "DELETE", token, onUnauthorized },
    );
  },

  // App metrics (org-scoped)
  getMetrics(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<AppMetrics> {
    return request<AppMetrics>(`/v1/orgs/${orgId}/apps/${appId}/metrics`, {
      token,
      onUnauthorized,
    });
  },

  // Public one-click services catalog (no auth required). Mirrors the
  // admin-managed templates but is readable by any signed-in user.
  getServiceCatalog(): Promise<ListResponse<Template>> {
    return request<ListResponse<Template>>("/v1/services/catalog");
  },

  // Billing
  getPlans(): Promise<PlansResponse> {
    return request<PlansResponse>("/v1/billing/plans");
  },

  getBilling(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<BillingResponse> {
    return request<BillingResponse>(`/v1/orgs/${orgId}/billing`, {
      token,
      onUnauthorized,
    });
  },

  subscribe(
    orgId: string,
    planId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<SubscribeResponse> {
    return request<SubscribeResponse>(`/v1/orgs/${orgId}/billing/subscribe`, {
      method: "POST",
      body: { planId },
      token,
      onUnauthorized,
    });
  },

  // -------------------------------------------------------------------------
  // Admin (super-admin token required) — mirrors /v1/admin/*
  // -------------------------------------------------------------------------

  // Admin: plans
  listAdminPlans(
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<AdminPlan>> {
    return request<ListResponse<AdminPlan>>("/v1/admin/plans", {
      token,
      onUnauthorized,
    });
  },

  createPlan(
    input: AdminPlanInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<AdminPlan> {
    return request<AdminPlan>("/v1/admin/plans", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
    });
  },

  updatePlan(
    id: string,
    input: Partial<AdminPlanInput>,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<AdminPlan> {
    return request<AdminPlan>(`/v1/admin/plans/${id}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
    });
  },

  deletePlan(
    id: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<void> {
    return request<void>(`/v1/admin/plans/${id}`, {
      method: "DELETE",
      token,
      onUnauthorized,
    });
  },

  // Admin: templates (launchable catalog)
  listTemplates(
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<ListResponse<Template>> {
    return request<ListResponse<Template>>("/v1/admin/templates", {
      token,
      onUnauthorized,
    });
  },

  createTemplate(
    input: TemplateInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Template> {
    return request<Template>("/v1/admin/templates", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
    });
  },

  updateTemplate(
    key: string,
    input: Partial<TemplateInput>,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Template> {
    return request<Template>(`/v1/admin/templates/${encodeURIComponent(key)}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
    });
  },

  deleteTemplate(
    key: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<void> {
    return request<void>(`/v1/admin/templates/${encodeURIComponent(key)}`, {
      method: "DELETE",
      token,
      onUnauthorized,
    });
  },

  // Admin: platform settings (singleton)
  getSettings(
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Settings> {
    return request<Settings>("/v1/admin/settings", {
      token,
      onUnauthorized,
    });
  },

  updateSettings(
    input: Partial<Settings>,
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<Settings> {
    return request<Settings>("/v1/admin/settings", {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
    });
  },

  // Admin: platform overview
  getAdminOverview(
    token: string,
    onUnauthorized?: OnUnauthorized,
  ): Promise<AdminOverview> {
    return request<AdminOverview>("/v1/admin/overview", {
      token,
      onUnauthorized,
    });
  },
};

export type VortexApi = typeof api;

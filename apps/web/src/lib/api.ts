// Typed API client for the Vortex Go control-plane.
//
// Reads the base URL from NEXT_PUBLIC_VORTEX_API_URL (falling back to the legacy
// NEXT_PUBLIC_VIRO_API_URL) and attaches `Authorization: Bearer <token>` when a
// token is provided. In development the URL falls back to http://localhost:8080;
// in production an unset URL is a hard error (no silent localhost default).
//
// Resource endpoints are org-scoped: /v1/orgs/{orgId}/...

function resolveApiBaseUrl(): string {
  const configured =
    process.env.NEXT_PUBLIC_VORTEX_API_URL ??
    process.env.NEXT_PUBLIC_VIRO_API_URL;
  if (configured) return configured;
  if (process.env.NODE_ENV === "production") {
    throw new Error(
      "NEXT_PUBLIC_VORTEX_API_URL is not set. The API base URL must be " +
        "configured explicitly in production; there is no localhost fallback.",
    );
  }
  return "http://localhost:8080";
}

export const API_BASE_URL = resolveApiBaseUrl();

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

// Mutable org fields editable via PATCH /v1/orgs/{orgId}.
export interface UpdateOrgInput {
  name?: string;
  billingEmail?: string;
}

// Status is an open string from the backend:
// created | deploying | restarting | stopped | running | error
export type AppStatus = string;

export interface App {
  id: string;
  orgId: string;
  projectId: string;
  name: string;
  gitRepository: string;
  gitBranch: string;
  buildPack: string;
  // Prebuilt container image to deploy instead of building from source.
  image?: string;
  // Requested resources (overcommit is applied server-side).
  cpu: number;
  memoryMb: number;
  status: AppStatus;
  // Kubernetes placement returned by the deploy backend.
  namespace?: string;
  release?: string;
  host?: string;
  createdAt: string;
}

// Service is a one-click catalog instance (WordPress, a database, etc.) owned by
// an organization and grouped under a project. Mirrors domain.Service.
export interface Service {
  id: string;
  orgId: string;
  projectId: string;
  template: string;
  name: string;
  cpu: number;
  memoryMb: number;
  status: string;
  // Kubernetes placement returned by the deploy backend.
  namespace?: string;
  release?: string;
  host?: string;
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
  // Estimated cost for the current period, derived server-side from metered
  // usage and the hourly pricing components. Optional: older API builds omit it.
  estimatedMonthlyCents?: number;
  currency?: string;
}

// ---------------------------------------------------------------------------
// Hourly pricing components (admin-managed metered rates)
// ---------------------------------------------------------------------------

// A single metered pricing line item, e.g. "CPU core-hour" at $0.02/hour.
// Mirrors the backend `PricingComponent`. The `key` is the stable identifier
// (immutable on update; supplied in the body on create, in the path otherwise).
export interface PricingComponent {
  key: string;
  name: string;
  // Human-readable billing unit, e.g. "core-hour", "GB-hour".
  unit: string;
  // Price per unit per hour, expressed in whole cents (integer or fractional).
  pricePerHour: number;
  currency: string;
  active: boolean;
  sortOrder: number;
}

// On create the key is supplied in the body; on update it lives in the path.
export type PricingComponentInput = PricingComponent;

/** Format a pricing component's per-hour rate, e.g. "$0.02 / core-hour". */
export function formatHourlyPrice(c: PricingComponent): string {
  return `${formatCents(c.pricePerHour, c.currency)} / ${c.unit || "hour"}`;
}

/**
 * Format a cent amount as a currency string. Sub-cent values (fractional cents,
 * common for per-hour metered rates) keep up to 4 fraction digits; whole-dollar
 * amounts drop the cents.
 */
export function formatCents(cents: number, currency = "usd"): string {
  const amount = cents / 100;
  const cur = (currency || "usd").toUpperCase();
  const isWhole = Number.isInteger(amount);
  const hasSubCent = Math.abs(amount * 100 - Math.round(amount * 100)) > 1e-9;
  return amount.toLocaleString(undefined, {
    style: "currency",
    currency: cur,
    minimumFractionDigits: isWhole ? 0 : 2,
    maximumFractionDigits: hasSubCent ? 4 : 2,
  });
}

// Metric key the backend records for metered compute, used to derive the
// "hours used" figure shown in the billing UI.
export const COMPUTE_HOURS_METRICS = [
  "compute_hours",
  "machine_hours",
  "hours",
];

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
  // Optional prebuilt container image to deploy instead of building from source.
  image?: string;
  // Optional requested resources; the backend falls back to platform defaults.
  cpu?: number;
  memoryMb?: number;
}

export interface CreateServiceInput {
  // Catalog template key (e.g. "wordpress"). Required by the backend.
  templateKey: string;
  name: string;
  // Optional requested resources; the backend falls back to platform defaults.
  cpu?: number;
  memoryMb?: number;
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

/**
 * Per-call options public API methods accept so callers can cancel in-flight
 * requests (e.g. on unmount or when inputs change). Optional everywhere.
 */
export interface CallOpts {
  signal?: AbortSignal;
}

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

async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
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
      // Send/receive the HttpOnly auth cookies (vortex_access/vortex_refresh).
      // The session is cookie-based; the optional Bearer header is only a
      // fallback for non-browser clients.
      credentials: "include",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  }

  let res = await exec(token);

  // Refresh-on-401: on a 401, if a refresh hook is provided, refresh once
  // (rotating the HttpOnly cookies server-side) and retry the request once.
  // No JS-held token is required — the refreshed cookie carries the new session.
  if (res.status === 401 && onUnauthorized) {
    const refreshed = await onUnauthorized();
    if (refreshed) {
      res = await exec(token);
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
  signup(input: SignupInput, opts?: CallOpts): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/signup", {
      method: "POST",
      body: input,
      signal: opts?.signal,
    });
  },

  login(input: LoginInput, opts?: CallOpts): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/login", {
      method: "POST",
      body: input,
      signal: opts?.signal,
    });
  },

  // Refresh rotates the session. The refresh token is read from the HttpOnly
  // cookie; refreshToken is optional and only used by non-browser callers.
  refresh(refreshToken?: string, opts?: CallOpts): Promise<AuthResponse> {
    return request<AuthResponse>("/v1/auth/refresh", {
      method: "POST",
      body: refreshToken ? { refreshToken } : {},
      signal: opts?.signal,
    });
  },

  // Logout revokes the refresh token server-side and clears the auth cookies.
  logout(opts?: CallOpts): Promise<void> {
    return request<void>("/v1/auth/logout", {
      method: "POST",
      signal: opts?.signal,
    });
  },

  me(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<User> {
    return request<User>("/v1/me", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Orgs
  listOrgs(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Org>> {
    return request<ListResponse<Org>>("/v1/orgs", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createOrg(
    name: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Org> {
    return request<Org>("/v1/orgs", {
      method: "POST",
      body: { name },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updateOrg(
    orgId: string,
    input: UpdateOrgInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Org> {
    return request<Org>(`/v1/orgs/${orgId}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Apps (org-scoped)
  listApps(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<App>> {
    return request<ListResponse<App>>(`/v1/orgs/${orgId}/apps`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  getApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // App logs (org-scoped). The backend returns {"logs": string}; we unwrap it.
  getLogs(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<string> {
    return request<{ logs: string }>(`/v1/orgs/${orgId}/apps/${appId}/logs`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    }).then((res) => res.logs);
  },

  createApp(
    orgId: string,
    input: CreateAppInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deployApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/deploy`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  stopApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/stop`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  restartApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/restart`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteApp(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/apps/${appId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // -------------------------------------------------------------------------
  // One-click Services (org-scoped) — mirrors httpx/services.go.
  // Create is project-scoped; lifecycle ops are org-scoped by serviceId.
  // -------------------------------------------------------------------------
  listServices(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Service>> {
    return request<ListResponse<Service>>(`/v1/orgs/${orgId}/services`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createService(
    orgId: string,
    projectId: string,
    input: CreateServiceInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Service> {
    return request<Service>(
      `/v1/orgs/${orgId}/projects/${projectId}/services`,
      {
        method: "POST",
        body: input,
        token,
        onUnauthorized,
        signal: opts?.signal,
      },
    );
  },

  deployService(
    orgId: string,
    serviceId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Service> {
    return request<Service>(`/v1/orgs/${orgId}/services/${serviceId}/deploy`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  stopService(
    orgId: string,
    serviceId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Service> {
    return request<Service>(`/v1/orgs/${orgId}/services/${serviceId}/stop`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  restartService(
    orgId: string,
    serviceId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Service> {
    return request<Service>(`/v1/orgs/${orgId}/services/${serviceId}/restart`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteService(
    orgId: string,
    serviceId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/services/${serviceId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Databases (org-scoped)
  listDatabases(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Database>> {
    return request<ListResponse<Database>>(`/v1/orgs/${orgId}/databases`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createDatabase(
    orgId: string,
    input: CreateDatabaseInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database> {
    return request<Database>(`/v1/orgs/${orgId}/databases`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteDatabase(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/databases/${databaseId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Projects (org-scoped)
  listProjects(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Project>> {
    return request<ListResponse<Project>>(`/v1/orgs/${orgId}/projects`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createProject(
    orgId: string,
    name: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Project> {
    return request<Project>(`/v1/orgs/${orgId}/projects`, {
      method: "POST",
      body: { name },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  listProjectApps(
    orgId: string,
    projectId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<App>> {
    return request<ListResponse<App>>(
      `/v1/orgs/${orgId}/projects/${projectId}/apps`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  deleteProject(
    orgId: string,
    projectId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/projects/${projectId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Members (org-scoped)
  listMembers(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Member>> {
    return request<ListResponse<Member>>(`/v1/orgs/${orgId}/members`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updateMember(
    orgId: string,
    userId: string,
    input: { role: MemberRole },
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Member> {
    return request<Member>(`/v1/orgs/${orgId}/members/${userId}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  removeMember(
    orgId: string,
    userId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/members/${userId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Invitations
  listInvitations(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Invitation>> {
    return request<ListResponse<Invitation>>(`/v1/orgs/${orgId}/invitations`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  invite(
    orgId: string,
    input: InviteInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Invitation> {
    return request<Invitation>(`/v1/orgs/${orgId}/invitations`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  acceptInvitation(
    inviteToken: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Invitation> {
    return request<Invitation>(`/v1/invitations/accept`, {
      method: "POST",
      body: { token: inviteToken },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  revokeInvitation(
    orgId: string,
    inviteId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/orgs/${orgId}/invitations/${inviteId}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // App environment variables (org-scoped)
  listEnv(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<EnvVar>> {
    return request<ListResponse<EnvVar>>(
      `/v1/orgs/${orgId}/apps/${appId}/env`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  setEnv(
    orgId: string,
    appId: string,
    input: EnvVar,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<EnvVar> {
    return request<EnvVar>(`/v1/orgs/${orgId}/apps/${appId}/env`, {
      method: "PUT",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteEnv(
    orgId: string,
    appId: string,
    key: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(
      `/v1/orgs/${orgId}/apps/${appId}/env/${encodeURIComponent(key)}`,
      { method: "DELETE", token, onUnauthorized, signal: opts?.signal },
    );
  },

  // App domains (org-scoped)
  listDomains(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Domain>> {
    return request<ListResponse<Domain>>(
      `/v1/orgs/${orgId}/apps/${appId}/domains`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  addDomain(
    orgId: string,
    appId: string,
    domain: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Domain> {
    return request<Domain>(`/v1/orgs/${orgId}/apps/${appId}/domains`, {
      method: "POST",
      body: { domain },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteDomain(
    orgId: string,
    appId: string,
    domainId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(
      `/v1/orgs/${orgId}/apps/${appId}/domains/${domainId}`,
      { method: "DELETE", token, onUnauthorized, signal: opts?.signal },
    );
  },

  // App metrics (org-scoped)
  getMetrics(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<AppMetrics> {
    return request<AppMetrics>(`/v1/orgs/${orgId}/apps/${appId}/metrics`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Public one-click services catalog (no auth required). Mirrors the
  // admin-managed templates but is readable by any signed-in user.
  getServiceCatalog(opts?: CallOpts): Promise<ListResponse<Template>> {
    return request<ListResponse<Template>>("/v1/services/catalog", {
      signal: opts?.signal,
    });
  },

  // Billing
  getPlans(opts?: CallOpts): Promise<PlansResponse> {
    return request<PlansResponse>("/v1/billing/plans", {
      signal: opts?.signal,
    });
  },

  // Public hourly pricing components (no auth required) — the rates shown to
  // users. Mirrors the admin-managed list but is read-only here.
  getPricing(opts?: CallOpts): Promise<ListResponse<PricingComponent>> {
    return request<ListResponse<PricingComponent>>("/v1/billing/pricing", {
      signal: opts?.signal,
    });
  },

  getBilling(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<BillingResponse> {
    return request<BillingResponse>(`/v1/orgs/${orgId}/billing`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  subscribe(
    orgId: string,
    planId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<SubscribeResponse> {
    return request<SubscribeResponse>(`/v1/orgs/${orgId}/billing/subscribe`, {
      method: "POST",
      body: { planId },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // -------------------------------------------------------------------------
  // Admin (super-admin token required) — mirrors /v1/admin/*
  // -------------------------------------------------------------------------

  // Admin: plans
  listAdminPlans(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<AdminPlan>> {
    return request<ListResponse<AdminPlan>>("/v1/admin/plans", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createPlan(
    input: AdminPlanInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<AdminPlan> {
    return request<AdminPlan>("/v1/admin/plans", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updatePlan(
    id: string,
    input: Partial<AdminPlanInput>,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<AdminPlan> {
    return request<AdminPlan>(`/v1/admin/plans/${id}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deletePlan(
    id: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/admin/plans/${id}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Admin: templates (launchable catalog)
  listTemplates(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<Template>> {
    return request<ListResponse<Template>>("/v1/admin/templates", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createTemplate(
    input: TemplateInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Template> {
    return request<Template>("/v1/admin/templates", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updateTemplate(
    key: string,
    input: Partial<TemplateInput>,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Template> {
    return request<Template>(`/v1/admin/templates/${encodeURIComponent(key)}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  deleteTemplate(
    key: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/admin/templates/${encodeURIComponent(key)}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Admin: platform settings (singleton)
  getSettings(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Settings> {
    return request<Settings>("/v1/admin/settings", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updateSettings(
    input: Partial<Settings>,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Settings> {
    return request<Settings>("/v1/admin/settings", {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Admin: platform overview
  getAdminOverview(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<AdminOverview> {
    return request<AdminOverview>("/v1/admin/overview", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // Admin: hourly pricing components — mirrors /v1/admin/pricing.
  listPricing(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<PricingComponent>> {
    return request<ListResponse<PricingComponent>>("/v1/admin/pricing", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createPricing(
    input: PricingComponentInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<PricingComponent> {
    return request<PricingComponent>("/v1/admin/pricing", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  updatePricing(
    key: string,
    patch: Partial<PricingComponentInput>,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<PricingComponent> {
    return request<PricingComponent>(
      `/v1/admin/pricing/${encodeURIComponent(key)}`,
      {
        method: "PATCH",
        body: patch,
        token,
        onUnauthorized,
        signal: opts?.signal,
      },
    );
  },

  deletePricing(
    key: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/admin/pricing/${encodeURIComponent(key)}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },
};

export type VortexApi = typeof api;

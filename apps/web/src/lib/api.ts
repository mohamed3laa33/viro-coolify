// Typed API client for the Viro Go control-plane.
//
// Reads the base URL from NEXT_PUBLIC_VIRO_API_URL (default http://localhost:8080)
// and attaches `Authorization: Bearer <token>` when a token is provided.
//
// Resource endpoints are org-scoped: /v1/orgs/{orgId}/...

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_VIRO_API_URL ?? "http://localhost:8080";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface User {
  id: string;
  email: string;
  name: string;
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
}

export interface Subscription {
  id: string;
  orgId: string;
  planId: string;
  status: string;
  currentPeriodEnd?: string;
}

export interface Usage {
  hoursUsed: number;
  includedHours: number;
  overageHours: number;
}

export interface BillingResponse {
  subscription: Subscription | null;
  plan: Plan | null;
  usage: Usage | null;
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
};

export type ViroApi = typeof api;

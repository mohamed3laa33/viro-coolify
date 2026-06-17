// Typed API client for the Viro Go control-plane.
//
// Reads the base URL from NEXT_PUBLIC_VIRO_API_URL (default http://localhost:8080)
// and attaches `Authorization: Bearer <token>` when a token is provided.

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

export type AppStatus = "running" | "stopped" | "error" | "deploying";

export interface App {
  uuid: string;
  name: string;
  fqdn: string;
  status: AppStatus;
  git_repository: string;
  git_branch: string;
  build_pack: string;
}

export type DatabaseType = "postgres" | "redis" | "mysql" | "mongo";

export interface Database {
  uuid: string;
  name: string;
  type: DatabaseType;
  status: string;
}

export interface ListResponse<T> {
  data: T[];
}

export interface ActionResponse {
  status: string;
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

interface RequestOptions {
  method?: string;
  body?: unknown;
  token?: string | null;
  signal?: AbortSignal;
}

export function buildUrl(path: string): string {
  const base = API_BASE_URL.replace(/\/+$/, "");
  const suffix = path.startsWith("/") ? path : `/${path}`;
  return `${base}${suffix}`;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, token, signal } = options;

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(buildUrl(path), {
    method,
    headers,
    signal,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

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

  me(token: string): Promise<User> {
    return request<User>("/v1/me", { token });
  },

  // Orgs
  listOrgs(token: string): Promise<ListResponse<Org>> {
    return request<ListResponse<Org>>("/v1/orgs", { token });
  },

  createOrg(name: string, token: string): Promise<Org> {
    return request<Org>("/v1/orgs", {
      method: "POST",
      body: { name },
      token,
    });
  },

  // Apps
  listApps(token: string): Promise<ListResponse<App>> {
    return request<ListResponse<App>>("/v1/apps", { token });
  },

  getApp(uuid: string, token: string): Promise<App> {
    return request<App>(`/v1/apps/${uuid}`, { token });
  },

  deployApp(uuid: string, token: string): Promise<ActionResponse> {
    return request<ActionResponse>(`/v1/apps/${uuid}/deploy`, {
      method: "POST",
      token,
    });
  },

  stopApp(uuid: string, token: string): Promise<ActionResponse> {
    return request<ActionResponse>(`/v1/apps/${uuid}/stop`, {
      method: "POST",
      token,
    });
  },

  restartApp(uuid: string, token: string): Promise<ActionResponse> {
    return request<ActionResponse>(`/v1/apps/${uuid}/restart`, {
      method: "POST",
      token,
    });
  },

  // Databases
  listDatabases(token: string): Promise<ListResponse<Database>> {
    return request<ListResponse<Database>>("/v1/databases", { token });
  },
};

export type ViroApi = typeof api;

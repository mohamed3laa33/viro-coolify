// Typed API client for the Vortex Go control-plane.
//
// Reads the base URL from NEXT_PUBLIC_VORTEX_API_URL (falling back to the legacy
// NEXT_PUBLIC_VIRO_API_URL) and attaches `Authorization: Bearer <token>` when a
// token is provided. In development the URL falls back to http://localhost:8080.
//
// IMPORTANT: resolving the base URL must NEVER throw at module load. A missing
// NEXT_PUBLIC_VORTEX_API_URL in production used to throw here, which crashed the
// entire client bundle on import (every page that imported this module
// white-screened before any error boundary could mount). Instead we degrade
// gracefully: fall back to a same-origin relative base ("") so requests resolve
// against the current host, and surface the misconfiguration as a handled,
// per-request error (see `request`) that route error boundaries can render.
//
// Resource endpoints are org-scoped: /v1/orgs/{orgId}/...

// Sentinel meaning "no API base URL was configured in production". `buildUrl`
// treats it as a same-origin relative base so module load never throws, while
// `request` converts an actual call into a clear, catchable ApiError.
const UNCONFIGURED = "";

function resolveApiBaseUrl(): string {
  const configured =
    process.env.NEXT_PUBLIC_VORTEX_API_URL ??
    process.env.NEXT_PUBLIC_VIRO_API_URL;
  if (configured) return configured;
  if (process.env.NODE_ENV === "production") {
    // Do not throw at import time — that white-screens the whole app. Degrade
    // to a same-origin relative base; a real request surfaces a handled error.
    return UNCONFIGURED;
  }
  return "http://localhost:8080";
}

export const API_BASE_URL = resolveApiBaseUrl();

/** True when no API base URL was configured (production misconfiguration). */
export const API_BASE_URL_CONFIGURED = API_BASE_URL !== UNCONFIGURED;

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
  // Per-org hard spend cap in whole cents (current-period charge ceiling). 0 (or
  // omitted on older API builds) means no per-org cap — the platform default
  // applies. Admin/DB-driven; set via setOrgSpendCap.
  spendCapCents?: number;
  createdAt: string;
}

// Mutable org fields editable via PATCH /v1/orgs/{orgId}.
export interface UpdateOrgInput {
  name?: string;
  billingEmail?: string;
}

// Payload for setting an org's hard spend cap. `spendCapCents` is the per-org
// current-period charge ceiling in whole cents; 0 clears the per-org cap so the
// platform default (PlatformSettings.DefaultSpendCapCents) applies instead. The
// value is admin/DB-driven — never hardcoded in the UI (invariant #1).
export interface SetOrgSpendCapInput {
  spendCapCents: number;
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

// GET /apps/{id} returns the app fields top-level plus the currently-active
// release alongside them (Release is defined below). currentRelease is omitted
// when the app has never deployed.
export interface AppDetail extends App {
  currentRelease?: Release;
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
  projectId?: string;
  name: string;
  engine: DatabaseEngine;
  cpu?: number;
  memoryMb?: number;
  storageGb?: number;
  status: string;
  namespace?: string;
  release?: string;
  host?: string;
  createdAt: string;
}

// In-cluster connection info for a managed database. Databases are internal-only
// (ClusterIP), so `host` is the cluster service DNS — reachable from the org's
// own workloads, not the public internet. The password is exposed ONLY here.
export interface DatabaseConnInfo {
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  connectionString: string;
}

// GET /orgs/{org}/databases/{id} returns the database fields flattened top-level
// plus its `connection` block.
export interface DatabaseDetail extends Database {
  connection: DatabaseConnInfo;
}

// BackupStatus mirrors the backend lifecycle of a managed database snapshot:
// pending (queued) → running → succeeded | failed. It is an open string so a
// newer backend phase never breaks the client.
export type BackupStatus = string;

// A point-in-time snapshot of a managed database. Mirrors the backend backup
// record. `sizeBytes`/`finishedAt` are populated once the snapshot completes;
// `error` carries the failure reason when status is "failed" (honest failure,
// never a fake success — invariant #6).
export interface DatabaseBackup {
  id: string;
  orgId: string;
  databaseId: string;
  status: BackupStatus;
  sizeBytes?: number;
  error?: string;
  createdAt: string;
  finishedAt?: string;
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
  // Current-period charge breakdown (all whole cents, in the plan currency):
  // baseCents = plan base price; overageCents = overage beyond included hours;
  // usageSoFarCents = metered compute cost so far this period; chargeCents =
  // base + overage (the projected invoice). periodStart is the period start.
  baseCents?: number;
  overageCents?: number;
  usageSoFarCents?: number;
  chargeCents?: number;
  periodStart?: string;
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

// ---------------------------------------------------------------------------
// Personal access tokens (PAT)
// ---------------------------------------------------------------------------

// The safe listing shape — NEVER carries the secret, only the display prefix.
export interface ApiToken {
  id: string;
  name: string;
  // First few chars of the plaintext (e.g. "vrt_ab12") for human recognition.
  prefix: string;
  scopes: string[];
  expiresAt?: string | null;
  lastUsedAt?: string | null;
  createdAt: string;
}

// The create-only response: identical to ApiToken plus the one-time plaintext
// `token` (shown ONCE; never returned again).
export interface CreatedApiToken extends ApiToken {
  token: string;
}

export interface CreateTokenInput {
  name: string;
  scopes?: string[];
  // 0/undefined = never expires (capped server-side).
  expiresInDays?: number;
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

export interface AuditEvent {
  id: string;
  orgId?: string;
  actorUserId?: string;
  actorEmail?: string;
  action: string;
  targetType?: string;
  targetId?: string;
  metadata?: string;
  at: string;
}

export interface EnvVar {
  key: string;
  value: string;
  // True when the value is stored as a secret. The API NEVER returns the value
  // of a secret (it comes back empty/masked); only the key + this flag.
  secret?: boolean;
}

// Payload for PUT .../env. `secret: true` stores the value encrypted-at-rest and
// the API will never echo the value back on subsequent lists.
export interface SetEnvInput {
  key: string;
  value: string;
  secret?: boolean;
}

// DomainStatus mirrors the backend lifecycle: pending (awaiting DNS TXT),
// verified (ownership proven, routed + TLS), failed (last check didn't match).
export type DomainStatus = "pending" | "verified" | "failed";

export interface Domain {
  id: string;
  orgId?: string;
  appId?: string;
  domain: string;
  // `verified` is a convenience mirror of `status === "verified"`.
  verified: boolean;
  status?: DomainStatus;
  verificationToken?: string;
  verifiedAt?: string;
  createdAt?: string;
}

// The exact DNS records the user must publish to (a) prove ownership via the TXT
// challenge and (b) point traffic at the platform (A or CNAME). Returned on
// add-domain and verify so the flow is self-documenting.
export interface DomainInstructions {
  verificationToken: string;
  txtName: string;
  txtValue: string;
  // "A" when an explicit Gateway LB host/IP is configured, else "CNAME".
  targetType: string;
  targetValue: string;
}

// AddDomain / VerifyDomain return the domain record flattened top-level plus the
// DNS `instructions` block alongside it.
export interface DomainResult extends Domain {
  instructions: DomainInstructions;
}

// ---------------------------------------------------------------------------
// Releases (deploy history) + rollback
// ---------------------------------------------------------------------------

export type ReleaseStatus =
  | "deploying"
  | "active"
  | "failed"
  | "superseded"
  | "rolled_back";

export interface Release {
  id: string;
  appId: string;
  orgId: string;
  revision: number;
  image: string;
  gitRef?: string;
  configHash?: string;
  cpu: number;
  memoryMb: number;
  status: ReleaseStatus;
  note?: string;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Builds (git-source image builds)
// ---------------------------------------------------------------------------

export type BuildStatus = "pending" | "building" | "succeeded" | "failed";

export interface Build {
  id: string;
  appId: string;
  orgId: string;
  status: BuildStatus;
  commitRef?: string;
  image?: string;
  logs?: string;
  createdAt: string;
  finishedAt?: string;
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

// The pagination block returned alongside growth-prone list endpoints.
export interface PageMeta {
  limit: number;
  offset: number;
  hasMore: boolean;
  nextOffset?: number;
  total?: number;
}

export interface PagedResponse<T> {
  data: T[];
  page: PageMeta;
}

export interface PageParams {
  limit?: number;
  offset?: number;
}

// ---------------------------------------------------------------------------
// Update / scale app
// ---------------------------------------------------------------------------

// All fields optional: PATCH /apps/{id} only applies the ones supplied.
export interface UpdateAppInput {
  image?: string;
  cpu?: number;
  memoryMb?: number;
  gitRepository?: string;
  gitBranch?: string;
}

export interface ScaleAppInput {
  minReplicas?: number;
  maxReplicas?: number;
}

// ---------------------------------------------------------------------------
// Live pod metrics (metrics-server snapshot)
// ---------------------------------------------------------------------------

export interface PodMetric {
  pod: string;
  cpuMillicores: number;
  memoryBytes: number;
}

// A LIVE point-in-time snapshot from the cluster metrics-server. When
// `available` is false the data is honestly absent (never synthesized);
// `unavailable` explains why.
export interface PodMetrics {
  available: boolean;
  unavailable?: string;
  pods: PodMetric[];
  cpuMillicores: number;
  memoryBytes: number;
}

// ---------------------------------------------------------------------------
// App rollout / health status (live deploy progress)
// ---------------------------------------------------------------------------

// A LIVE point-in-time view of an app's rollout and health from the deploy
// backend. `status` is the reconciled workload status (running | deploying |
// stopped | error | …, same open vocabulary as App.status). `phase` is the raw
// controller phase reported by the backend (e.g. "Running", "Pending", "Scaled
// to zero"). Replica counts come straight from the Deployment/StatefulSet:
// `readyReplicas` of `replicas` are ready. When the backend cannot be reached
// `available` is false (with an `unavailable` reason) and the data is honestly
// absent — never synthesized (invariant #6).
export interface AppStatusInfo {
  status: AppStatus;
  phase?: string;
  replicas?: number;
  readyReplicas?: number;
  available?: boolean;
  unavailable?: string;
}

// ---------------------------------------------------------------------------
// Legacy time-series metrics (kept for the dashboard sparklines / demo)
// ---------------------------------------------------------------------------

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
  // When unconfigured, base is "" — yielding a same-origin relative URL.
  return `${base}${suffix}`;
}

/** Build a `?limit=&offset=` suffix from pagination params (empty when absent). */
export function pageQuery(p?: PageParams): string {
  const q = new URLSearchParams();
  if (p?.limit !== undefined) q.set("limit", String(p.limit));
  if (p?.offset !== undefined) q.set("offset", String(p.offset));
  const s = q.toString();
  return s ? `?${s}` : "";
}

/**
 * Absolute URL of the live SSE log stream for an app (`?follow=true`). The stream
 * is consumed via a fetch reader rather than EventSource so the request carries
 * the auth cookies (`credentials: "include"`) — EventSource cannot. See
 * {@link streamAppLogs}.
 */
export function appLogStreamUrl(orgId: string, appId: string): string {
  return buildUrl(`/v1/orgs/${orgId}/apps/${appId}/logs?follow=true`);
}

/**
 * Absolute URL of the live SSE rollout/health stream for an app
 * (`?follow=true`). Like the log stream it is consumed via a fetch reader rather
 * than EventSource so the request carries the HttpOnly auth cookies
 * (`credentials: "include"`). See {@link streamAppStatus}.
 */
export function appStatusStreamUrl(orgId: string, appId: string): string {
  return buildUrl(`/v1/orgs/${orgId}/apps/${appId}/status?follow=true`);
}

/**
 * Open the live app log SSE stream and invoke `onLine` for each streamed log
 * line. Uses fetch + a streaming reader (not EventSource) so the HttpOnly auth
 * cookies are sent. Returns a disposer that aborts the underlying request; call
 * it on unmount to close the stream and avoid a leak.
 *
 * Lines arrive as Server-Sent Events (`data: <line>\n\n`); this parser extracts
 * the payload of each `data:` field. An `event: error` frame is surfaced via the
 * optional `onError` callback. The stream is best-effort: transport failures
 * (including a deliberate abort) resolve quietly rather than throwing.
 */
export function streamAppLogs(
  orgId: string,
  appId: string,
  onLine: (line: string) => void,
  onError?: (message: string) => void,
): () => void {
  const controller = new AbortController();

  void (async () => {
    if (!API_BASE_URL_CONFIGURED) {
      onError?.("The API base URL is not configured.");
      return;
    }
    let res: Response;
    try {
      res = await fetch(appLogStreamUrl(orgId, appId), {
        headers: { Accept: "text/event-stream" },
        credentials: "include",
        signal: controller.signal,
      });
    } catch {
      if (!controller.signal.aborted) onError?.("Log stream unavailable.");
      return;
    }
    if (!res.ok || !res.body) {
      onError?.(`Log stream failed (${res.status}).`);
      return;
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        // SSE frames are separated by a blank line.
        let sep: number;
        while ((sep = buf.indexOf("\n\n")) >= 0) {
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          parseSseFrame(frame, onLine, onError);
        }
      }
    } catch {
      // Aborted (unmount) or transport drop — both are normal stop conditions.
    }
  })();

  return () => controller.abort();
}

// Parse one SSE frame: collect `data:` field payloads as a line; an `event:
// error` frame routes its data to onError instead.
function parseSseFrame(
  frame: string,
  onLine: (line: string) => void,
  onError?: (message: string) => void,
): void {
  let isError = false;
  const dataParts: string[] = [];
  for (const raw of frame.split("\n")) {
    const line = raw.replace(/\r$/, "");
    if (line.startsWith("event:")) {
      if (line.slice(6).trim() === "error") isError = true;
    } else if (line.startsWith("data:")) {
      dataParts.push(line.slice(5).replace(/^ /, ""));
    }
  }
  if (dataParts.length === 0) return;
  const payload = dataParts.join("\n");
  if (isError) onError?.(payload);
  else onLine(payload);
}

/**
 * Open the live app rollout/health SSE stream and invoke `onStatus` for each
 * streamed {@link AppStatusInfo} snapshot. Uses fetch + a streaming reader (not
 * EventSource) so the HttpOnly auth cookies are sent. Returns a disposer that
 * aborts the underlying request; call it on unmount to close the stream and
 * avoid a leak.
 *
 * Each SSE `data:` frame carries one JSON-encoded status snapshot; an
 * `event: error` frame is surfaced via the optional `onError` callback. Frames
 * that fail to parse as JSON are skipped (best-effort), and transport failures
 * (including a deliberate abort) resolve quietly rather than throwing.
 */
export function streamAppStatus(
  orgId: string,
  appId: string,
  onStatus: (status: AppStatusInfo) => void,
  onError?: (message: string) => void,
): () => void {
  const controller = new AbortController();

  void (async () => {
    if (!API_BASE_URL_CONFIGURED) {
      onError?.("The API base URL is not configured.");
      return;
    }
    let res: Response;
    try {
      res = await fetch(appStatusStreamUrl(orgId, appId), {
        headers: { Accept: "text/event-stream" },
        credentials: "include",
        signal: controller.signal,
      });
    } catch {
      if (!controller.signal.aborted) onError?.("Status stream unavailable.");
      return;
    }
    if (!res.ok || !res.body) {
      onError?.(`Status stream failed (${res.status}).`);
      return;
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    // Each non-error data frame is a JSON snapshot; decode it before dispatch.
    const onLine = (line: string): void => {
      let snapshot: AppStatusInfo;
      try {
        snapshot = JSON.parse(line) as AppStatusInfo;
      } catch {
        // Skip malformed frames rather than tearing down the whole stream.
        return;
      }
      onStatus(snapshot);
    };
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        // SSE frames are separated by a blank line.
        let sep: number;
        while ((sep = buf.indexOf("\n\n")) >= 0) {
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          parseSseFrame(frame, onLine, onError);
        }
      }
    } catch {
      // Aborted (unmount) or transport drop — both are normal stop conditions.
    }
  })();

  return () => controller.abort();
}

async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const { method = "GET", body, token, signal, onUnauthorized } = options;

  // Fail loud-but-handled if the API base URL was never configured. Throwing an
  // ApiError here (rather than at module load) keeps the app shell alive: the
  // call site's catch / route error boundary renders a real fallback instead of
  // the whole bundle white-screening on import.
  if (!API_BASE_URL_CONFIGURED) {
    throw new ApiError(
      "The API base URL is not configured. Set NEXT_PUBLIC_VORTEX_API_URL.",
      0,
    );
  }

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

  // A 2xx response may have an empty or non-JSON body (e.g. a 200/201 with no
  // content). Parse defensively so a missing/non-JSON body resolves to undefined
  // instead of throwing an uncaught SyntaxError out of every API call.
  const text = await res.text();
  if (text === "") {
    return undefined as T;
  }
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError("server returned a malformed response", res.status);
  }
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

  // PUT /orgs/{org}/spend-cap: set the org's hard spend cap (whole cents; 0
  // clears the per-org cap so the platform default applies). Returns the updated
  // org. The cap value is admin/DB-driven — the UI must not invent a default.
  setOrgSpendCap(
    orgId: string,
    input: SetOrgSpendCapInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Org> {
    return request<Org>(`/v1/orgs/${orgId}/spend-cap`, {
      method: "PUT",
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
  ): Promise<AppDetail> {
    return request<AppDetail>(`/v1/orgs/${orgId}/apps/${appId}`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // PATCH /apps/{id}: update the image, requested resources, or git source. Only
  // the supplied fields are applied. Returns the updated app.
  updateApp(
    orgId: string,
    appId: string,
    input: UpdateAppInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}`, {
      method: "PATCH",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // POST /apps/{id}/scale: set the autoscaling replica bounds. Returns the app
  // (HTTP 202 Accepted).
  scaleApp(
    orgId: string,
    appId: string,
    input: ScaleAppInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/scale`, {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // GET /apps/{id}/releases: the app's deploy history (newest first), paginated.
  listReleases(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts & PageParams,
  ): Promise<PagedResponse<Release>> {
    return request<PagedResponse<Release>>(
      `/v1/orgs/${orgId}/apps/${appId}/releases${pageQuery(opts)}`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  // POST /apps/{id}/rollback: redeploy a prior release. `revision` 0/undefined
  // rolls back to the previous release. Returns the app (HTTP 202 Accepted).
  rollbackApp(
    orgId: string,
    appId: string,
    revision: number | undefined,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<App> {
    return request<App>(`/v1/orgs/${orgId}/apps/${appId}/rollback`, {
      method: "POST",
      body: revision ? { revision } : {},
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // GET /apps/{id}/builds: git-source image build history, paginated.
  listBuilds(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts & PageParams,
  ): Promise<PagedResponse<Build>> {
    return request<PagedResponse<Build>>(
      `/v1/orgs/${orgId}/apps/${appId}/builds${pageQuery(opts)}`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  // GET /apps/{id}/builds/{buildId}: one build, including its captured logs.
  getBuild(
    orgId: string,
    appId: string,
    buildId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Build> {
    return request<Build>(`/v1/orgs/${orgId}/apps/${appId}/builds/${buildId}`, {
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

  // GET /orgs/{org}/databases/{id}: one database plus its in-cluster connection
  // info (host/port/database/username/password/connectionString). This is the
  // ONLY endpoint that returns the password.
  getDatabase(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<DatabaseDetail> {
    return request<DatabaseDetail>(
      `/v1/orgs/${orgId}/databases/${databaseId}`,
      {
        token,
        onUnauthorized,
        signal: opts?.signal,
      },
    );
  },

  // GET /databases/{id}/backups: the database's snapshot history (newest first).
  listDatabaseBackups(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<DatabaseBackup>> {
    return request<ListResponse<DatabaseBackup>>(
      `/v1/orgs/${orgId}/databases/${databaseId}/backups`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  // POST /databases/{id}/backups: trigger a new point-in-time snapshot. Returns
  // the backup record (initially pending/running); poll listDatabaseBackups for
  // completion.
  createDatabaseBackup(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<DatabaseBackup> {
    return request<DatabaseBackup>(
      `/v1/orgs/${orgId}/databases/${databaseId}/backups`,
      { method: "POST", token, onUnauthorized, signal: opts?.signal },
    );
  },

  // POST /databases/{id}/backups/{backupId}/restore: restore the database from a
  // prior snapshot (destructive — overwrites current data). Returns the updated
  // database.
  restoreDatabase(
    orgId: string,
    databaseId: string,
    backupId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database> {
    return request<Database>(
      `/v1/orgs/${orgId}/databases/${databaseId}/backups/${backupId}/restore`,
      { method: "POST", token, onUnauthorized, signal: opts?.signal },
    );
  },

  deployDatabase(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database> {
    return request<Database>(
      `/v1/orgs/${orgId}/databases/${databaseId}/deploy`,
      { method: "POST", token, onUnauthorized, signal: opts?.signal },
    );
  },

  stopDatabase(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database> {
    return request<Database>(`/v1/orgs/${orgId}/databases/${databaseId}/stop`, {
      method: "POST",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  restartDatabase(
    orgId: string,
    databaseId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<Database> {
    return request<Database>(
      `/v1/orgs/${orgId}/databases/${databaseId}/restart`,
      { method: "POST", token, onUnauthorized, signal: opts?.signal },
    );
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

  // PUT .../env: set a variable. Pass `secret: true` to store it encrypted; the
  // API will never echo a secret value back on subsequent lists.
  setEnv(
    orgId: string,
    appId: string,
    input: SetEnvInput,
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

  // POST .../domains: attach a custom domain. The response carries the DNS
  // `instructions` (TXT challenge + A/CNAME target) the user must publish before
  // verifying. The domain starts `pending` and is not routed until verified.
  addDomain(
    orgId: string,
    appId: string,
    domain: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<DomainResult> {
    return request<DomainResult>(`/v1/orgs/${orgId}/apps/${appId}/domains`, {
      method: "POST",
      body: { domain },
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // POST .../domains/{id}/verify: look up the DNS TXT challenge and mark the
  // domain verified (and route it) on match, else failed. Returns the updated
  // domain plus the (re-)instructions.
  verifyDomain(
    orgId: string,
    appId: string,
    domainId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<DomainResult> {
    return request<DomainResult>(
      `/v1/orgs/${orgId}/apps/${appId}/domains/${domainId}/verify`,
      { method: "POST", token, onUnauthorized, signal: opts?.signal },
    );
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

  // App metrics (org-scoped). The backend returns a LIVE pod metrics snapshot
  // (PodMetrics) — `available: false` means metrics-server is unavailable or the
  // app is not deployed; the UI must render an honest empty state, never fake it.
  getMetrics(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<PodMetrics> {
    return request<PodMetrics>(`/v1/orgs/${orgId}/apps/${appId}/metrics`, {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // GET /apps/{id}/status: a LIVE rollout/health snapshot (reconciled status,
  // controller phase, ready/total replicas). `available: false` means the deploy
  // backend was unreachable — the UI must render an honest unknown state, never
  // fake it. For continuous updates use streamAppStatus.
  getAppStatus(
    orgId: string,
    appId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<AppStatusInfo> {
    return request<AppStatusInfo>(`/v1/orgs/${orgId}/apps/${appId}/status`, {
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

  // -------------------------------------------------------------------------
  // Personal access tokens (PAT) — /v1/tokens. The plaintext `vrt_` token is
  // returned ONCE on create; listing never leaks the secret.
  // -------------------------------------------------------------------------
  listTokens(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<ListResponse<ApiToken>> {
    return request<ListResponse<ApiToken>>("/v1/tokens", {
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  createToken(
    input: CreateTokenInput,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<CreatedApiToken> {
    return request<CreatedApiToken>("/v1/tokens", {
      method: "POST",
      body: input,
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  revokeToken(
    id: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts,
  ): Promise<void> {
    return request<void>(`/v1/tokens/${id}`, {
      method: "DELETE",
      token,
      onUnauthorized,
      signal: opts?.signal,
    });
  },

  // -------------------------------------------------------------------------
  // Audit log. Platform-level (super-admin) and org-scoped (org admin+) listings
  // are paginated (limit/offset, has-more).
  // -------------------------------------------------------------------------
  listAdminAudit(
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts & PageParams,
  ): Promise<PagedResponse<AuditEvent>> {
    return request<PagedResponse<AuditEvent>>(
      `/v1/admin/audit${pageQuery(opts)}`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },

  listOrgAudit(
    orgId: string,
    token: string,
    onUnauthorized?: OnUnauthorized,
    opts?: CallOpts & PageParams,
  ): Promise<PagedResponse<AuditEvent>> {
    return request<PagedResponse<AuditEvent>>(
      `/v1/orgs/${orgId}/audit${pageQuery(opts)}`,
      { token, onUnauthorized, signal: opts?.signal },
    );
  },
};

export type VortexApi = typeof api;

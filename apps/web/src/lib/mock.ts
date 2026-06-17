// Mock data used as a graceful fallback when the API is unreachable, so the
// dashboard renders standalone without a running control-plane.

import type {
  App,
  AppMetrics,
  BillingResponse,
  Database,
  Domain,
  EnvVar,
  Invitation,
  Member,
  Org,
  Plan,
  Project,
  User,
} from "@/lib/api";

export const mockUser: User = {
  id: "usr_demo",
  email: "you@viro.dev",
  name: "Demo User",
};

export const mockOrgs: Org[] = [
  {
    id: "org_personal",
    name: "Personal",
    slug: "personal",
    createdAt: "2026-01-12T10:00:00Z",
  },
  {
    id: "org_acme",
    name: "Acme Corp",
    slug: "acme-corp",
    createdAt: "2026-02-03T10:00:00Z",
  },
];

export const mockApps: App[] = [
  {
    id: "app_7f3a",
    orgId: "org_acme",
    coolifyUuid: "cool_7f3a",
    name: "marketing-site",
    gitRepository: "github.com/acme/marketing",
    gitBranch: "main",
    buildPack: "nixpacks",
    status: "running",
    createdAt: "2026-03-01T10:00:00Z",
  },
  {
    id: "app_2b91",
    orgId: "org_acme",
    coolifyUuid: "cool_2b91",
    name: "api-gateway",
    gitRepository: "github.com/acme/gateway",
    gitBranch: "main",
    buildPack: "dockerfile",
    status: "running",
    createdAt: "2026-03-04T10:00:00Z",
  },
  {
    id: "app_44de",
    orgId: "org_acme",
    coolifyUuid: "cool_44de",
    name: "worker-queue",
    gitRepository: "github.com/acme/worker",
    gitBranch: "production",
    buildPack: "nixpacks",
    status: "stopped",
    createdAt: "2026-03-09T10:00:00Z",
  },
  {
    id: "app_9c10",
    orgId: "org_acme",
    coolifyUuid: "cool_9c10",
    name: "image-resizer",
    gitRepository: "github.com/acme/resizer",
    gitBranch: "main",
    buildPack: "dockerfile",
    status: "error",
    createdAt: "2026-03-12T10:00:00Z",
  },
  {
    id: "app_1aa2",
    orgId: "org_acme",
    coolifyUuid: "cool_1aa2",
    name: "analytics-edge",
    gitRepository: "github.com/acme/analytics",
    gitBranch: "main",
    buildPack: "static",
    status: "running",
    createdAt: "2026-03-15T10:00:00Z",
  },
];

export const mockDatabases: Database[] = [
  {
    id: "db_pg_01",
    orgId: "org_acme",
    name: "primary-postgres",
    engine: "postgresql",
    status: "running",
    createdAt: "2026-03-01T10:00:00Z",
  },
  {
    id: "db_redis_01",
    orgId: "org_acme",
    name: "session-cache",
    engine: "redis",
    status: "running",
    createdAt: "2026-03-02T10:00:00Z",
  },
  {
    id: "db_mysql_01",
    orgId: "org_acme",
    name: "legacy-mysql",
    engine: "mysql",
    status: "stopped",
    createdAt: "2026-03-03T10:00:00Z",
  },
  {
    id: "db_mongo_01",
    orgId: "org_acme",
    name: "events-mongo",
    engine: "mongodb",
    status: "running",
    createdAt: "2026-03-04T10:00:00Z",
  },
];

export const mockPlans: Plan[] = [
  {
    id: "plan_hobby",
    name: "Hobby",
    description: "For side projects and experiments.",
    priceCents: 0,
    currency: "usd",
    includedHours: 100,
    overagePerHourCents: 2,
  },
  {
    id: "plan_launch",
    name: "Launch",
    description: "For production apps and small teams.",
    priceCents: 2900,
    currency: "usd",
    includedHours: 750,
    overagePerHourCents: 2,
  },
  {
    id: "plan_scale",
    name: "Scale",
    description: "For high-traffic apps with autoscaling.",
    priceCents: 9900,
    currency: "usd",
    includedHours: 3000,
    overagePerHourCents: 1,
  },
];

export const mockBilling: BillingResponse = {
  subscription: {
    id: "sub_demo",
    orgId: "org_acme",
    planId: "plan_launch",
    status: "active",
    currentPeriodEnd: "2026-06-30T00:00:00Z",
  },
  plan: mockPlans[1],
  usage: {
    hoursUsed: 412,
    includedHours: 750,
    overageHours: 0,
  },
};

export const mockRegions = [
  "iad",
  "lhr",
  "fra",
  "sin",
  "syd",
  "gru",
] as const;

export const mockProjects: Project[] = [
  {
    id: "proj_default",
    name: "Default",
    slug: "default",
    isDefault: true,
    createdAt: "2026-01-12T10:00:00Z",
  },
  {
    id: "proj_platform",
    name: "Platform",
    slug: "platform",
    isDefault: false,
    createdAt: "2026-02-20T10:00:00Z",
  },
  {
    id: "proj_growth",
    name: "Growth",
    slug: "growth",
    isDefault: false,
    createdAt: "2026-03-08T10:00:00Z",
  },
];

export const mockMembers: Member[] = [
  {
    userId: "usr_demo",
    email: "you@viro.dev",
    name: "Demo User",
    role: "owner",
  },
  {
    userId: "usr_grace",
    email: "grace@acme.dev",
    name: "Grace Hopper",
    role: "admin",
  },
  {
    userId: "usr_alan",
    email: "alan@acme.dev",
    name: "Alan Turing",
    role: "member",
  },
  {
    userId: "usr_kj",
    email: "kj@acme.dev",
    name: "Katherine Johnson",
    role: "member",
  },
];

export const mockInvitations: Invitation[] = [
  {
    id: "inv_01",
    email: "ada@acme.dev",
    role: "member",
    projectId: "proj_platform",
    token: "inv_tok_ada_8f21c4",
    status: "pending",
    createdAt: "2026-06-10T10:00:00Z",
  },
  {
    id: "inv_02",
    email: "linus@acme.dev",
    role: "admin",
    projectId: null,
    token: "inv_tok_linus_3a90fe",
    status: "pending",
    createdAt: "2026-06-14T10:00:00Z",
  },
];

export const mockEnv: EnvVar[] = [
  { key: "NODE_ENV", value: "production" },
  { key: "PORT", value: "8080" },
  {
    key: "DATABASE_URL",
    value: "postgres://app:s3cr3t@primary-postgres.internal:5432/app",
  },
  { key: "REDIS_URL", value: "redis://app:s3cr3t@session-cache.internal:6379" },
  { key: "LOG_LEVEL", value: "info" },
];

export const mockDomains: Domain[] = [
  { id: "dom_01", domain: "acme.com", verified: true },
  { id: "dom_02", domain: "www.acme.com", verified: true },
  { id: "dom_03", domain: "staging.acme.com", verified: false },
];

function metricSeries(seed: number, base: number, n = 24): { t: string; v: number }[] {
  const out: { t: string; v: number }[] = [];
  const now = Date.now();
  let v = base + (seed % 17);
  for (let i = n - 1; i >= 0; i--) {
    v += Math.sin(i / 2 + seed) * 6 + ((seed * (i + 1)) % 9) - 4;
    out.push({
      t: new Date(now - i * 60_000).toISOString(),
      v: Math.max(1, Math.round(v)),
    });
  }
  return out;
}

export const mockMetrics: AppMetrics = {
  cpu: metricSeries(3, 38),
  memory: metricSeries(11, 52),
  requests: metricSeries(7, 120),
};

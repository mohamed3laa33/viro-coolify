// Mock data used as a graceful fallback when the API is unreachable, so the
// dashboard renders standalone without a running control-plane.

import type {
  App,
  BillingResponse,
  Database,
  Org,
  Plan,
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

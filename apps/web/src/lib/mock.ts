// Mock data used as a graceful fallback when the API is unreachable, so the
// dashboard renders standalone without a running control-plane.

import type {
  AdminOverview,
  AdminPlan,
  App,
  AppMetrics,
  BillingResponse,
  Database,
  Domain,
  EnvVar,
  Invitation,
  Member,
  Org,
  PricingComponent,
  Project,
  Settings,
  Template,
  User,
} from "@/lib/api";

export const mockUser: User = {
  id: "usr_demo",
  email: "you@vortex.v60ai.com",
  name: "Demo User",
  isAdmin: true,
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
    projectId: "proj_default",
    name: "marketing-site",
    gitRepository: "github.com/acme/marketing",
    gitBranch: "main",
    buildPack: "nixpacks",
    cpu: 1,
    memoryMb: 512,
    status: "running",
    createdAt: "2026-03-01T10:00:00Z",
  },
  {
    id: "app_2b91",
    orgId: "org_acme",
    projectId: "proj_platform",
    name: "api-gateway",
    gitRepository: "github.com/acme/gateway",
    gitBranch: "main",
    buildPack: "dockerfile",
    cpu: 2,
    memoryMb: 1024,
    status: "running",
    createdAt: "2026-03-04T10:00:00Z",
  },
  {
    id: "app_44de",
    orgId: "org_acme",
    projectId: "proj_platform",
    name: "worker-queue",
    gitRepository: "github.com/acme/worker",
    gitBranch: "production",
    buildPack: "nixpacks",
    cpu: 1,
    memoryMb: 512,
    status: "stopped",
    createdAt: "2026-03-09T10:00:00Z",
  },
  {
    id: "app_9c10",
    orgId: "org_acme",
    projectId: "proj_growth",
    name: "image-resizer",
    gitRepository: "github.com/acme/resizer",
    gitBranch: "main",
    buildPack: "dockerfile",
    cpu: 1,
    memoryMb: 256,
    status: "error",
    createdAt: "2026-03-12T10:00:00Z",
  },
  {
    id: "app_1aa2",
    orgId: "org_acme",
    projectId: "proj_growth",
    name: "analytics-edge",
    gitRepository: "github.com/acme/analytics",
    gitBranch: "main",
    buildPack: "static",
    cpu: 1,
    memoryMb: 256,
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

// Full (admin-shaped) plans. Public billing reads the same array; the extra
// admin fields are harmless there. Ids align to hobby/launch/scale so the
// "current plan" highlighting matches the live API.
export const mockPlans: AdminPlan[] = [
  {
    id: "hobby",
    name: "Hobby",
    description: "For side projects and experiments.",
    priceCents: 0,
    currency: "usd",
    includedHours: 100,
    overagePerHourCents: 2,
    maxCpu: 1,
    maxMemoryMb: 256,
    maxApps: 1,
    isDefault: true,
    sortOrder: 0,
    active: true,
    stripePriceId: "",
  },
  {
    id: "launch",
    name: "Launch",
    description: "For production apps and small teams.",
    priceCents: 2900,
    currency: "usd",
    includedHours: 750,
    overagePerHourCents: 2,
    maxCpu: 2,
    maxMemoryMb: 2048,
    maxApps: 10,
    isDefault: false,
    sortOrder: 1,
    active: true,
    stripePriceId: "price_launch_demo",
  },
  {
    id: "scale",
    name: "Scale",
    description: "For high-traffic apps with autoscaling.",
    priceCents: 9900,
    currency: "usd",
    includedHours: 3000,
    overagePerHourCents: 1,
    maxCpu: 8,
    maxMemoryMb: 8192,
    maxApps: 100,
    isDefault: false,
    sortOrder: 2,
    active: true,
    stripePriceId: "price_scale_demo",
  },
];

export const mockBilling: BillingResponse = {
  subscription: {
    orgId: "org_acme",
    planId: "launch",
    status: "active",
    createdAt: "2026-05-30T00:00:00Z",
    currentPeriodEnd: "2026-06-30T00:00:00Z",
  },
  plan: mockPlans[1],
  usage: {
    compute_hours: 412,
  },
  estimatedMonthlyCents: 4120,
  currency: "usd",
};

export const mockPricing: PricingComponent[] = [
  {
    key: "cpu",
    name: "CPU",
    unit: "core-hour",
    pricePerHour: 2,
    currency: "usd",
    active: true,
    sortOrder: 0,
  },
  {
    key: "memory",
    name: "Memory",
    unit: "GB-hour",
    pricePerHour: 0.5,
    currency: "usd",
    active: true,
    sortOrder: 1,
  },
  {
    key: "egress",
    name: "Egress",
    unit: "GB",
    pricePerHour: 9,
    currency: "usd",
    active: true,
    sortOrder: 2,
  },
  {
    key: "storage",
    name: "Block storage",
    unit: "GB-hour",
    pricePerHour: 0.02,
    currency: "usd",
    active: false,
    sortOrder: 3,
  },
];

export const mockTemplates: Template[] = [
  {
    key: "postgresql",
    name: "PostgreSQL",
    description: "Production-ready Postgres with automated backups.",
    category: "Databases",
    kind: "database",
    image: "postgres:16",
    defaultPort: 5432,
    active: true,
    sortOrder: 0,
  },
  {
    key: "mysql",
    name: "MySQL",
    description: "Popular relational database.",
    category: "Databases",
    kind: "database",
    image: "mysql:8",
    defaultPort: 3306,
    active: true,
    sortOrder: 1,
  },
  {
    key: "mariadb",
    name: "MariaDB",
    description: "Drop-in MySQL replacement.",
    category: "Databases",
    kind: "database",
    image: "mariadb:11",
    defaultPort: 3306,
    active: true,
    sortOrder: 2,
  },
  {
    key: "mongodb",
    name: "MongoDB",
    description: "Document database for flexible schemas.",
    category: "Databases",
    kind: "database",
    image: "mongo:7",
    defaultPort: 27017,
    active: true,
    sortOrder: 3,
  },
  {
    key: "redis",
    name: "Redis",
    description: "In-memory cache and message broker.",
    category: "Databases",
    kind: "database",
    image: "redis:7",
    defaultPort: 6379,
    active: true,
    sortOrder: 4,
  },
  {
    key: "ghost",
    name: "Ghost",
    description: "Modern publishing platform.",
    category: "Apps",
    kind: "app",
    image: "ghost:5",
    defaultPort: 2368,
    active: true,
    sortOrder: 5,
  },
  {
    key: "minio",
    name: "MinIO",
    description: "S3-compatible object storage.",
    category: "Services",
    kind: "service",
    image: "minio/minio:latest",
    defaultPort: 9000,
    active: false,
    sortOrder: 6,
  },
];

export const mockSettings: Settings = {
  defaultCpu: 1,
  defaultMemoryMb: 512,
  defaultPlanId: "hobby",
  cpuOvercommitFactor: 0.8,
  memoryOvercommitFactor: 0.9,
  defaultRegion: "iad",
  regions: ["iad", "lhr", "fra", "sin", "syd", "gru"],
};

export const mockAdminOverview: AdminOverview = {
  orgCount: 128,
  userCount: 342,
  subscriptionsByPlan: {
    hobby: 74,
    launch: 41,
    scale: 13,
  },
  usageTotals: {
    machineHours: 18420,
    cpuSeconds: 9_640_000,
    memoryMbHours: 4_210_000,
    egressGb: 2310,
  },
};

export const mockRegions = ["iad", "lhr", "fra", "sin", "syd", "gru"] as const;

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
    email: "you@vortex.v60ai.com",
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
    token: "demo-invite-code-ada",
    status: "pending",
    createdAt: "2026-06-10T10:00:00Z",
  },
  {
    id: "inv_02",
    email: "linus@acme.dev",
    role: "admin",
    projectId: null,
    token: "demo-invite-code-linus",
    status: "pending",
    createdAt: "2026-06-14T10:00:00Z",
  },
];

export const mockEnv: EnvVar[] = [
  { key: "NODE_ENV", value: "production" },
  { key: "PORT", value: "8080" },
  {
    key: "DATABASE_URL",
    value: "demo-database-connection",
  },
  { key: "REDIS_URL", value: "demo-cache-connection" },
  { key: "LOG_LEVEL", value: "info" },
];

export const mockDomains: Domain[] = [
  { id: "dom_01", domain: "acme.com", verified: true },
  { id: "dom_02", domain: "www.acme.com", verified: true },
  { id: "dom_03", domain: "staging.acme.com", verified: false },
];

function metricSeries(
  seed: number,
  base: number,
  n = 24,
): { t: string; v: number }[] {
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

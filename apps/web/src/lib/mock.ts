// Mock data used as a graceful fallback when the API is unreachable, so the
// dashboard renders standalone without a running control-plane.

import type { App, Database, Org, User } from "@/lib/api";

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
    uuid: "app_7f3a",
    name: "marketing-site",
    fqdn: "marketing-site.viro.app",
    status: "running",
    git_repository: "github.com/acme/marketing",
    git_branch: "main",
    build_pack: "nixpacks",
  },
  {
    uuid: "app_2b91",
    name: "api-gateway",
    fqdn: "api-gateway.viro.app",
    status: "running",
    git_repository: "github.com/acme/gateway",
    git_branch: "main",
    build_pack: "dockerfile",
  },
  {
    uuid: "app_44de",
    name: "worker-queue",
    fqdn: "worker-queue.viro.app",
    status: "stopped",
    git_repository: "github.com/acme/worker",
    git_branch: "production",
    build_pack: "nixpacks",
  },
  {
    uuid: "app_9c10",
    name: "image-resizer",
    fqdn: "image-resizer.viro.app",
    status: "error",
    git_repository: "github.com/acme/resizer",
    git_branch: "main",
    build_pack: "dockerfile",
  },
  {
    uuid: "app_1aa2",
    name: "analytics-edge",
    fqdn: "analytics-edge.viro.app",
    status: "running",
    git_repository: "github.com/acme/analytics",
    git_branch: "main",
    build_pack: "static",
  },
];

export const mockDatabases: Database[] = [
  {
    uuid: "db_pg_01",
    name: "primary-postgres",
    type: "postgres",
    status: "running",
  },
  {
    uuid: "db_redis_01",
    name: "session-cache",
    type: "redis",
    status: "running",
  },
  {
    uuid: "db_mysql_01",
    name: "legacy-mysql",
    type: "mysql",
    status: "stopped",
  },
  {
    uuid: "db_mongo_01",
    name: "events-mongo",
    type: "mongo",
    status: "running",
  },
];

export const mockRegions = [
  "iad",
  "lhr",
  "fra",
  "sin",
  "syd",
  "gru",
] as const;

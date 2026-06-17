"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Boxes,
  Database,
  FolderGit2,
  Globe,
  LayoutDashboard,
  LineChart,
  PackageSearch,
  Settings,
  Shield,
  SlidersHorizontal,
  Tags,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { mockApps, mockBilling } from "@/lib/mock";
import { useResource } from "@/lib/use-resource";
import { Logo } from "@/components/logo";

interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
  match: (pathname: string) => boolean;
}

const NAV: NavItem[] = [
  {
    label: "Apps",
    href: "/dashboard/apps",
    icon: Boxes,
    match: (p) => p === "/dashboard" || p.startsWith("/dashboard/apps"),
  },
  {
    label: "Projects",
    href: "/dashboard/projects",
    icon: FolderGit2,
    match: (p) => p.startsWith("/dashboard/projects"),
  },
  {
    label: "Databases",
    href: "/dashboard/databases",
    icon: Database,
    match: (p) => p.startsWith("/dashboard/databases"),
  },
  {
    label: "Domains",
    href: "/dashboard/domains",
    icon: Globe,
    match: (p) => p.startsWith("/dashboard/domains"),
  },
  {
    label: "Metrics",
    href: "/dashboard/metrics",
    icon: LineChart,
    match: (p) => p.startsWith("/dashboard/metrics"),
  },
  {
    label: "Settings",
    href: "/dashboard/settings",
    icon: Settings,
    match: (p) => p.startsWith("/dashboard/settings"),
  },
];

const ADMIN_NAV: NavItem[] = [
  {
    label: "Overview",
    href: "/dashboard/admin",
    icon: LayoutDashboard,
    match: (p) => p === "/dashboard/admin",
  },
  {
    label: "Plans",
    href: "/dashboard/admin/plans",
    icon: Tags,
    match: (p) => p.startsWith("/dashboard/admin/plans"),
  },
  {
    label: "Catalog",
    href: "/dashboard/admin/catalog",
    icon: PackageSearch,
    match: (p) => p.startsWith("/dashboard/admin/catalog"),
  },
  {
    label: "Settings",
    href: "/dashboard/admin/settings",
    icon: SlidersHorizontal,
    match: (p) => p.startsWith("/dashboard/admin/settings"),
  },
];

function NavLink({ item, pathname }: { item: NavItem; pathname: string }) {
  const active = item.match(pathname);
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      className={cn(
        "group flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors",
        active
          ? "bg-primary/15 text-foreground"
          : "text-muted-foreground hover:bg-muted hover:text-foreground",
      )}
    >
      <Icon
        className={cn(
          "h-4 w-4 shrink-0",
          active ? "text-primary" : "text-muted-foreground",
        )}
      />
      {item.label}
    </Link>
  );
}

export function Sidebar() {
  const pathname = usePathname() ?? "";
  const { user, activeOrgId, authedCall } = useAuth();

  // Plan + quota usage are sourced from the billing endpoint and the live app
  // count — never hardcoded. Falls back to mock data when the API is offline.
  const { data: billing } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.getBilling(activeOrgId, token, on))
      : null,
    mockBilling,
    [activeOrgId],
  );
  const { data: appsData } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listApps(activeOrgId, token, on))
      : null,
    { data: mockApps },
    [activeOrgId],
  );

  const planName = billing.plan?.name ?? null;
  const maxApps = billing.plan?.maxApps;
  const appCount = appsData.data.length;
  const usagePct =
    typeof maxApps === "number" && maxApps > 0
      ? Math.min(100, Math.round((appCount / maxApps) * 100))
      : 0;

  return (
    <aside className="hidden w-60 shrink-0 flex-col border-r border-border bg-card md:flex">
      <div className="flex h-16 items-center gap-2 border-b border-border px-5">
        <Link href="/dashboard" className="flex items-center gap-2">
          <Logo size={26} withWordmark />
        </Link>
      </div>

      <nav className="flex-1 space-y-1 overflow-y-auto p-3 scrollbar-thin">
        {NAV.map((item) => (
          <NavLink key={item.href} item={item} pathname={pathname} />
        ))}

        {user?.isAdmin && (
          <div className="pt-4">
            <div className="flex items-center gap-2 px-3 pb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <Shield className="h-3.5 w-3.5" />
              Admin
            </div>
            <div className="space-y-1">
              {ADMIN_NAV.map((item) => (
                <NavLink key={item.href} item={item} pathname={pathname} />
              ))}
            </div>
          </div>
        )}
      </nav>

      {planName && (
        <div className="border-t border-border p-4">
          <div className="rounded-lg border border-border bg-surface-2 p-3">
            <p className="text-xs font-medium text-foreground">
              {planName} plan
            </p>
            {typeof maxApps === "number" && maxApps > 0 && (
              <>
                <p className="mt-1 text-xs text-muted-foreground">
                  {appCount} of {maxApps} apps used
                </p>
                <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-brand-balloon"
                    style={{ width: `${usagePct}%` }}
                  />
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}

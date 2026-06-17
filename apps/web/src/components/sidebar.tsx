"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Boxes,
  Database,
  Globe,
  LineChart,
  Settings,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
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

export function Sidebar() {
  const pathname = usePathname() ?? "";

  return (
    <aside className="hidden w-60 shrink-0 flex-col border-r border-border bg-card md:flex">
      <div className="flex h-16 items-center gap-2 border-b border-border px-5">
        <Link href="/dashboard" className="flex items-center gap-2">
          <Logo size={26} withWordmark />
        </Link>
      </div>

      <nav className="flex-1 space-y-1 p-3">
        {NAV.map((item) => {
          const active = item.match(pathname);
          const Icon = item.icon;
          return (
            <Link
              key={item.href}
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
        })}
      </nav>

      <div className="border-t border-border p-4">
        <div className="rounded-lg border border-border bg-surface-2 p-3">
          <p className="text-xs font-medium text-foreground">Free plan</p>
          <p className="mt-1 text-xs text-muted-foreground">
            3 of 5 apps used
          </p>
          <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div className="h-full w-3/5 rounded-full bg-brand-balloon" />
          </div>
        </div>
      </div>
    </aside>
  );
}

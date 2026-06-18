"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Boxes,
  CircleDollarSign,
  Database,
  FolderGit2,
  Globe,
  LayoutDashboard,
  LineChart,
  Package,
  PackageSearch,
  Settings,
  Shield,
  SlidersHorizontal,
  Tags,
  X,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { api, type App, type BillingResponse } from "@/lib/api";
import { useDemoData } from "@/lib/demo-data";
import { useResource } from "@/lib/use-resource";
import { Logo } from "@/components/logo";

export interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
  match: (pathname: string) => boolean;
}

export const NAV: NavItem[] = [
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
    label: "Services",
    href: "/dashboard/services",
    icon: Package,
    match: (p) => p.startsWith("/dashboard/services"),
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

export const ADMIN_NAV: NavItem[] = [
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
    label: "Pricing",
    href: "/dashboard/admin/pricing",
    icon: CircleDollarSign,
    match: (p) => p.startsWith("/dashboard/admin/pricing"),
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

function NavLink({
  item,
  pathname,
  onNavigate,
}: {
  item: NavItem;
  pathname: string;
  onNavigate?: () => void;
}) {
  const active = item.match(pathname);
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      aria-current={active ? "page" : undefined}
      onClick={onNavigate}
      className={cn(
        "group flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors pointer-coarse:min-h-11",
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

/**
 * Canonical DOM event name the {@link MobileSidebar} drawer listens for. This is
 * the single source of truth for the open mechanism: {@link openMobileSidebar}
 * dispatches it and the drawer listens for it — nothing else.
 *
 * The preferred way for other client components (e.g. the Topbar hamburger) to
 * open the drawer is to import and call {@link openMobileSidebar}. The event
 * constant is exported as well so a caller can dispatch the SAME event directly
 * (`window.dispatchEvent(new CustomEvent(SIDEBAR_OPEN_EVENT))`) without importing
 * any shared provider — keeping this component fully self-contained.
 */
export const SIDEBAR_OPEN_EVENT = "vortex:sidebar-open";

/**
 * Canonical opener for the mobile navigation drawer. Safe to call from any
 * client component; no-ops during SSR. Prefer this over dispatching the event
 * by hand.
 */
export function openMobileSidebar() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent(SIDEBAR_OPEN_EVENT));
}

/**
 * Shared nav + plan-usage card. Used by both the persistent desktop rail and
 * the mobile drawer so the two stay in sync (single source of truth).
 */
function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname() ?? "";
  const { user, activeOrgId, authedCall } = useAuth();

  // Demo fallbacks load lazily (demo mode only) so mock data never ships to prod.
  const demoBilling = useDemoData<BillingResponse | null>(
    (m) => m.mockBilling,
    null,
  );
  const demoApps = useDemoData((m) => m.mockApps, [] as App[]);

  // Plan + quota usage are sourced from the billing endpoint and the live app
  // count — never hardcoded. Falls back to mock data when the API is offline.
  const { data: billing } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.getBilling(activeOrgId, token, on))
      : null,
    demoBilling,
    [activeOrgId, demoBilling],
    { cacheKey: activeOrgId ? `billing:${activeOrgId}` : undefined },
  );
  const { data: appsData } = useResource(
    activeOrgId
      ? () => authedCall((token, on) => api.listApps(activeOrgId, token, on))
      : null,
    { data: demoApps },
    [activeOrgId, demoApps],
    { cacheKey: activeOrgId ? `apps:${activeOrgId}` : undefined },
  );

  const planName = billing?.plan?.name ?? null;
  const maxApps = billing?.plan?.maxApps;
  const appCount = appsData.data.length;
  const usagePct =
    typeof maxApps === "number" && maxApps > 0
      ? Math.min(100, Math.round((appCount / maxApps) * 100))
      : 0;

  return (
    <>
      <nav className="flex-1 space-y-1 overflow-y-auto p-3 scrollbar-thin">
        {NAV.map((item) => (
          <NavLink
            key={item.href}
            item={item}
            pathname={pathname}
            onNavigate={onNavigate}
          />
        ))}

        {user?.isAdmin && (
          <div className="pt-4">
            <div className="flex items-center gap-2 px-3 pb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <Shield className="h-3.5 w-3.5" />
              Admin
            </div>
            <div className="space-y-1">
              {ADMIN_NAV.map((item) => (
                <NavLink
                  key={item.href}
                  item={item}
                  pathname={pathname}
                  onNavigate={onNavigate}
                />
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
    </>
  );
}

/** Persistent left rail, visible at md and up. */
export function Sidebar() {
  return (
    <aside className="hidden w-60 shrink-0 flex-col border-r border-border bg-card md:flex">
      <div className="flex h-16 items-center gap-2 border-b border-border px-5">
        <Link href="/dashboard" className="flex items-center gap-2">
          <Logo size={26} withWordmark />
        </Link>
      </div>
      <SidebarContent />
    </aside>
  );
}

/**
 * Mobile-only slide-in drawer mirroring the desktop sidebar. Opens on the
 * {@link SIDEBAR_OPEN_EVENT} custom event (dispatched by {@link openMobileSidebar}).
 * Accessible: role="dialog" aria-modal, Escape to close, focus is trapped while
 * open and restored on close, body scroll is locked, and a backdrop click closes.
 */
export function MobileSidebar() {
  const [open, setOpen] = useState(false);
  const pathname = usePathname() ?? "";
  const panelRef = useRef<HTMLDivElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);

  const close = useCallback(() => setOpen(false), []);

  // Open on the canonical custom event — the only open mechanism. Callers reach
  // it via openMobileSidebar() or by dispatching SIDEBAR_OPEN_EVENT directly.
  useEffect(() => {
    function onOpen() {
      setOpen(true);
    }
    window.addEventListener(SIDEBAR_OPEN_EVENT, onOpen);
    return () => window.removeEventListener(SIDEBAR_OPEN_EVENT, onOpen);
  }, []);

  // Close automatically when the route changes (e.g. after tapping a link).
  useEffect(() => {
    setOpen(false);
  }, [pathname]);

  // Body-scroll lock, focus management, Escape + focus trap — only while open.
  useEffect(() => {
    if (!open) return;

    previouslyFocused.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Move focus into the panel.
    const panel = panelRef.current;
    panel?.focus();

    function focusable(): HTMLElement[] {
      if (!panel) return [];
      return Array.from(
        panel.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      );
    }

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        setOpen(false);
        return;
      }
      if (e.key !== "Tab") return;
      const items = focusable();
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const activeEl = document.activeElement;
      if (e.shiftKey) {
        if (activeEl === first || activeEl === panel) {
          e.preventDefault();
          last.focus();
        }
      } else if (activeEl === last) {
        e.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = prevOverflow;
      previouslyFocused.current?.focus();
    };
  }, [open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden">
      <div
        className="absolute inset-0 bg-black/60"
        onClick={close}
        aria-hidden="true"
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label="Navigation"
        tabIndex={-1}
        className="absolute inset-y-0 left-0 flex w-64 max-w-[85%] flex-col border-r border-border bg-card shadow-xl outline-none"
      >
        <div className="flex h-16 items-center justify-between border-b border-border px-5">
          <Link
            href="/dashboard"
            className="flex items-center gap-2"
            onClick={close}
          >
            <Logo size={26} withWordmark />
          </Link>
          <button
            type="button"
            onClick={close}
            aria-label="Close navigation"
            className="flex items-center justify-center rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground pointer-coarse:min-h-11 pointer-coarse:min-w-11"
          >
            <X className="h-5 w-5" />
          </button>
        </div>
        <SidebarContent onNavigate={close} />
      </div>
    </div>
  );
}

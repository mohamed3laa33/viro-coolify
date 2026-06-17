"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronDown, LogOut, Check, Menu } from "lucide-react";
import { cn, initials } from "@/lib/utils";
import { useAuth } from "@/lib/auth";

/**
 * Event name the mobile sidebar drawer listens for to open itself. The drawer
 * state lives in components/sidebar.tsx (navigation is owned there, never
 * redefined in the topbar); we only fire the toggle from the hamburger.
 *
 * TODO(sidebar): once components/sidebar.tsx exports a typed drawer context /
 * hook (e.g. useSidebarDrawer), swap this DOM event for a direct call so the
 * aria-expanded state below can reflect the real open/closed state.
 */
const SIDEBAR_TOGGLE_EVENT = "vortex:toggle-sidebar";

export function Topbar() {
  const router = useRouter();
  const { user, logout, orgs, activeOrgId, setActiveOrgId } = useAuth();

  const activeOrg = orgs.find((o) => o.id === activeOrgId) ?? orgs[0] ?? null;
  const [orgOpen, setOrgOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);

  const orgRef = useRef<HTMLDivElement>(null);
  const userRef = useRef<HTMLDivElement>(null);
  const orgTriggerRef = useRef<HTMLButtonElement>(null);
  const userTriggerRef = useRef<HTMLButtonElement>(null);
  const orgMenuRef = useRef<HTMLDivElement>(null);
  const userMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (orgRef.current && !orgRef.current.contains(e.target as Node)) {
        setOrgOpen(false);
      }
      if (userRef.current && !userRef.current.contains(e.target as Node)) {
        setUserOpen(false);
      }
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  // Move focus to the first item when a menu opens so keyboard users land
  // inside the menu rather than on the trigger.
  useEffect(() => {
    if (orgOpen) {
      orgMenuRef.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    }
  }, [orgOpen]);

  useEffect(() => {
    if (userOpen) {
      userMenuRef.current
        ?.querySelector<HTMLElement>('[role="menuitem"]')
        ?.focus();
    }
  }, [userOpen]);

  // Shared keyboard handling for a menu: Escape closes and returns focus to the
  // trigger; ArrowUp/ArrowDown move between menuitems with wraparound.
  const handleMenuKeyDown = useCallback(
    (
      e: React.KeyboardEvent<HTMLDivElement>,
      close: () => void,
      trigger: React.RefObject<HTMLButtonElement | null>,
    ) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
        trigger.current?.focus();
        return;
      }
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault();
      const items = Array.from(
        e.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]'),
      );
      if (items.length === 0) return;
      const current = items.indexOf(document.activeElement as HTMLElement);
      const delta = e.key === "ArrowDown" ? 1 : -1;
      const next = (current + delta + items.length) % items.length;
      items[next]?.focus();
    },
    [],
  );

  function toggleMobileNav() {
    window.dispatchEvent(new CustomEvent(SIDEBAR_TOGGLE_EVENT));
  }

  const displayName = user?.name ?? "Account";
  const displayEmail = user?.email ?? "";
  const userInitials = initials(displayName) || "?";
  const orgLabel = activeOrg?.name ?? "No organization";
  const orgInitial = activeOrg?.name?.[0] ?? "—";

  function handleLogout() {
    logout();
    router.push("/login");
  }

  return (
    <header className="flex h-16 shrink-0 items-center justify-between border-b border-border bg-card/60 px-4 backdrop-blur sm:px-6">
      <div className="flex items-center gap-2">
        {/* Mobile hamburger — opens the sidebar drawer (navigation owned by
            components/sidebar.tsx). Hidden from md up where the sidebar is
            always visible. */}
        <button
          type="button"
          onClick={toggleMobileNav}
          aria-label="Open navigation"
          aria-expanded={false}
          className="flex h-9 w-9 items-center justify-center rounded-md border border-border bg-surface-2 text-foreground hover:bg-muted md:hidden"
        >
          <Menu className="h-5 w-5" />
        </button>

        {/* Org switcher */}
        <div className="relative" ref={orgRef}>
          <button
            ref={orgTriggerRef}
            type="button"
            onClick={() => setOrgOpen((o) => !o)}
            aria-haspopup="menu"
            aria-expanded={orgOpen}
            className="flex items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm font-medium text-foreground hover:bg-muted"
          >
            <span className="flex h-5 w-5 items-center justify-center rounded bg-brand-balloon text-[10px] font-bold text-white">
              {orgInitial}
            </span>
            {orgLabel}
            <ChevronDown className="h-4 w-4 text-muted-foreground" />
          </button>

          {orgOpen && (
            <div
              ref={orgMenuRef}
              role="menu"
              aria-label="Switch organization"
              onKeyDown={(e) =>
                handleMenuKeyDown(e, () => setOrgOpen(false), orgTriggerRef)
              }
              className="absolute left-0 z-20 mt-2 w-56 rounded-lg border border-border bg-card p-1 shadow-xl"
            >
              <p className="px-2 py-1.5 text-xs font-medium text-muted-foreground">
                Organizations
              </p>
              {orgs.length === 0 && (
                <p className="px-2 py-2 text-sm text-muted-foreground">
                  No organizations yet.
                </p>
              )}
              {orgs.map((org) => (
                <button
                  key={org.id}
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setActiveOrgId(org.id);
                    setOrgOpen(false);
                    orgTriggerRef.current?.focus();
                  }}
                  className="flex w-full items-center justify-between rounded-md px-2 py-2 text-sm text-foreground hover:bg-muted"
                >
                  <span className="flex items-center gap-2">
                    <span className="flex h-5 w-5 items-center justify-center rounded bg-brand-balloon text-[10px] font-bold text-white">
                      {org.name[0]}
                    </span>
                    {org.name}
                  </span>
                  {org.id === activeOrg?.id && (
                    <Check className="h-4 w-4 text-primary" />
                  )}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* User menu */}
      <div className="relative" ref={userRef}>
        <button
          ref={userTriggerRef}
          type="button"
          onClick={() => setUserOpen((o) => !o)}
          aria-label="Account menu"
          aria-haspopup="menu"
          aria-expanded={userOpen}
          className="flex items-center gap-2 rounded-full p-1 pr-2 hover:bg-muted"
        >
          <span className="flex h-8 w-8 items-center justify-center rounded-full bg-brand-balloon text-xs font-semibold text-white">
            {userInitials}
          </span>
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        </button>

        {userOpen && (
          <div
            ref={userMenuRef}
            role="menu"
            aria-label="Account menu"
            onKeyDown={(e) =>
              handleMenuKeyDown(e, () => setUserOpen(false), userTriggerRef)
            }
            className="absolute right-0 z-20 mt-2 w-60 rounded-lg border border-border bg-card p-1 shadow-xl"
          >
            <div className="px-3 py-2">
              <p className="truncate text-sm font-medium text-foreground">
                {displayName}
              </p>
              {displayEmail && (
                <p className="truncate text-xs text-muted-foreground">
                  {displayEmail}
                </p>
              )}
            </div>
            <div className="my-1 h-px bg-border" />
            <button
              type="button"
              role="menuitem"
              onClick={handleLogout}
              className={cn(
                "flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm text-foreground hover:bg-muted",
              )}
            >
              <LogOut className="h-4 w-4 text-muted-foreground" />
              Log out
            </button>
          </div>
        )}
      </div>
    </header>
  );
}

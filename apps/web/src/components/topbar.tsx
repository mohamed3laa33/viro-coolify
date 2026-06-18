"use client";

import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { usePathname, useRouter } from "next/navigation";
import { ChevronDown, LogOut, Check, Menu, Plus } from "lucide-react";
import { cn, initials } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { openMobileSidebar } from "@/components/sidebar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Notice } from "@/components/ui/notice";

export function Topbar() {
  const router = useRouter();
  const pathname = usePathname();
  const { user, logout, orgs, activeOrgId, setActiveOrgId, authedCall } =
    useAuth();

  const activeOrg = orgs.find((o) => o.id === activeOrgId) ?? orgs[0] ?? null;
  const [orgOpen, setOrgOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  // Reflects the mobile drawer's open state for aria-expanded. We can detect
  // open (we dispatch it) and the common close paths (route change / Escape),
  // which the drawer also uses to close itself.
  const [navOpen, setNavOpen] = useState(false);

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

  // The drawer closes on navigation and on Escape; mirror that here so the
  // hamburger's aria-expanded does not get stuck reporting "open".
  useEffect(() => {
    setNavOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!navOpen) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setNavOpen(false);
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [navOpen]);

  // Move focus to the first item when a menu opens so keyboard users land
  // inside the menu rather than on the trigger.
  useEffect(() => {
    if (orgOpen) {
      orgMenuRef.current
        ?.querySelector<HTMLElement>('[role="menuitem"]')
        ?.focus();
    }
  }, [orgOpen]);

  useEffect(() => {
    if (userOpen) {
      userMenuRef.current
        ?.querySelector<HTMLElement>('[role="menuitem"]')
        ?.focus();
    }
  }, [userOpen]);

  // Shared keyboard handling for a menu (roving tabindex pattern): Escape closes
  // and returns focus to the trigger; ArrowUp/ArrowDown move between menuitems
  // with wraparound; Home/End jump to the first/last item; Tab closes the menu
  // (focus continues to the next page control). Click-outside is handled above.
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
      // Tab moves focus out of the menu, so close it (it is no longer current).
      if (e.key === "Tab") {
        close();
        return;
      }
      if (
        e.key !== "ArrowDown" &&
        e.key !== "ArrowUp" &&
        e.key !== "Home" &&
        e.key !== "End"
      ) {
        return;
      }
      e.preventDefault();
      const items = Array.from(
        e.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]'),
      );
      if (items.length === 0) return;
      const current = items.indexOf(document.activeElement as HTMLElement);
      let next: number;
      if (e.key === "Home") {
        next = 0;
      } else if (e.key === "End") {
        next = items.length - 1;
      } else {
        const delta = e.key === "ArrowDown" ? 1 : -1;
        next = (current + delta + items.length) % items.length;
      }
      items[next]?.focus();
    },
    [],
  );

  // Close a menu when focus leaves it entirely (e.g. Tab/Shift+Tab or a
  // programmatic focus move), matching the click-outside behaviour for pointers.
  const handleMenuBlur = useCallback(
    (e: React.FocusEvent<HTMLDivElement>, close: () => void) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
        close();
      }
    },
    [],
  );

  function toggleMobileNav() {
    openMobileSidebar();
    setNavOpen(true);
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

  function openCreateOrg() {
    setOrgOpen(false);
    setCreateOpen(true);
  }

  return (
    <header className="flex h-16 shrink-0 items-center justify-between gap-2 border-b border-border bg-card/60 px-4 backdrop-blur sm:px-6">
      <div className="flex min-w-0 items-center gap-2">
        {/* Mobile hamburger — opens the sidebar drawer via the canonical
            openMobileSidebar() exported from components/sidebar.tsx (navigation
            is owned there). aria-haspopup="dialog" because the drawer is a modal
            dialog; aria-expanded tracks the open paths we can observe. */}
        <button
          type="button"
          onClick={toggleMobileNav}
          aria-label="Open navigation"
          aria-haspopup="dialog"
          aria-expanded={navOpen}
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-foreground hover:bg-muted md:hidden pointer-coarse:min-h-11 pointer-coarse:min-w-11"
        >
          <Menu className="h-5 w-5" />
        </button>

        {/* Org switcher */}
        <div className="relative min-w-0" ref={orgRef}>
          <button
            ref={orgTriggerRef}
            type="button"
            onClick={() => setOrgOpen((o) => !o)}
            aria-haspopup="menu"
            aria-expanded={orgOpen}
            className="flex min-w-0 items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm font-medium text-foreground hover:bg-muted pointer-coarse:min-h-11"
          >
            <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded bg-brand-balloon text-[10px] font-bold text-white">
              {orgInitial}
            </span>
            <span className="truncate">{orgLabel}</span>
            <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
          </button>

          {orgOpen && (
            <div
              ref={orgMenuRef}
              role="menu"
              aria-label="Switch organization"
              onKeyDown={(e) =>
                handleMenuKeyDown(e, () => setOrgOpen(false), orgTriggerRef)
              }
              onBlur={(e) => handleMenuBlur(e, () => setOrgOpen(false))}
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
                  tabIndex={-1}
                  onClick={() => {
                    setActiveOrgId(org.id);
                    setOrgOpen(false);
                    orgTriggerRef.current?.focus();
                  }}
                  className="flex w-full items-center justify-between rounded-md px-2 py-2 text-sm text-foreground hover:bg-muted pointer-coarse:min-h-11"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded bg-brand-balloon text-[10px] font-bold text-white">
                      {org.name[0]}
                    </span>
                    <span className="truncate">{org.name}</span>
                  </span>
                  {org.id === activeOrg?.id && (
                    <Check className="h-4 w-4 shrink-0 text-primary" />
                  )}
                </button>
              ))}
              <div className="my-1 h-px bg-border" />
              <button
                type="button"
                role="menuitem"
                tabIndex={-1}
                onClick={openCreateOrg}
                className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-sm text-foreground hover:bg-muted pointer-coarse:min-h-11"
              >
                <Plus className="h-4 w-4 text-muted-foreground" />
                Create organization
              </button>
            </div>
          )}
        </div>
      </div>

      {/* User menu */}
      <div className="relative shrink-0" ref={userRef}>
        <button
          ref={userTriggerRef}
          type="button"
          onClick={() => setUserOpen((o) => !o)}
          aria-label="Account menu"
          aria-haspopup="menu"
          aria-expanded={userOpen}
          className="flex items-center gap-2 rounded-full p-1 pr-2 hover:bg-muted pointer-coarse:min-h-11 pointer-coarse:min-w-11"
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
            onBlur={(e) => handleMenuBlur(e, () => setUserOpen(false))}
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
              tabIndex={-1}
              onClick={handleLogout}
              className={cn(
                "flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm text-foreground hover:bg-muted pointer-coarse:min-h-11",
              )}
            >
              <LogOut className="h-4 w-4 text-muted-foreground" />
              Log out
            </button>
          </div>
        )}
      </div>

      <CreateOrgDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        authedCall={authedCall}
        setActiveOrgId={setActiveOrgId}
      />
    </header>
  );
}

/**
 * Accessible modal for creating an organization. On success it switches the
 * active org to the new one and reloads so the AuthProvider re-fetches the org
 * list (listOrgs) — the org switcher then shows the new org. Mirrors the a11y
 * contract of ConfirmDialog: role="dialog" + aria-modal, useId aria ids, Tab
 * focus-trap, initial focus on the name field, focus restore on close, body
 * scroll-lock, and Escape/backdrop dismissal (disabled while submitting).
 */
function CreateOrgDialog({
  open,
  onClose,
  authedCall,
  setActiveOrgId,
}: {
  open: boolean;
  onClose: () => void;
  authedCall: ReturnType<typeof useAuth>["authedCall"];
  setActiveOrgId: (id: string) => void;
}) {
  const titleId = useId();
  const nameId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);

  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Reset transient state whenever the dialog opens.
  useEffect(() => {
    if (open) {
      setName("");
      setError(null);
      setSubmitting(false);
    }
  }, [open]);

  // Body scroll-lock, initial focus on the name field, focus restore on close.
  useEffect(() => {
    if (!open) return;

    previouslyFocused.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    nameRef.current?.focus();

    return () => {
      document.body.style.overflow = prevOverflow;
      previouslyFocused.current?.focus();
    };
  }, [open]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      if (e.key === "Escape") {
        if (submitting) return;
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key === "Tab") {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), [tabindex]:not([tabindex="-1"])',
        );
        if (!focusable || focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        const activeEl = document.activeElement;
        if (e.shiftKey && activeEl === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && activeEl === last) {
          e.preventDefault();
          first.focus();
        }
      }
    },
    [submitting, onClose],
  );

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      const org = await authedCall((token, onUnauthorized) =>
        api.createOrg(trimmed, token, onUnauthorized),
      );
      // Persist the new org as active, then reload so the AuthProvider's mount
      // flow re-fetches the org list (listOrgs) and the switcher shows it.
      setActiveOrgId(org.id);
      window.location.assign("/dashboard");
    } catch (err) {
      setError(errorMessage(err, "Could not reach the server."));
      setSubmitting(false);
    }
  }

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !submitting) onClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-lg"
      >
        <h2 id={titleId} className="text-lg font-semibold text-foreground">
          Create organization
        </h2>
        <form onSubmit={onSubmit} className="mt-4 space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor={nameId}>Name</Label>
            <Input
              id={nameId}
              ref={nameRef}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Acme Inc."
              autoComplete="off"
              disabled={submitting}
              required
            />
          </div>
          {error && <Notice variant="error">{error}</Notice>}
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={onClose}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              size="sm"
              loading={submitting}
              disabled={!name.trim()}
            >
              Create
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

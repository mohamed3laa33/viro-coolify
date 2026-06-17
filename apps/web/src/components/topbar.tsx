"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronDown, LogOut, Check } from "lucide-react";
import { cn, initials } from "@/lib/utils";
import { useAuth } from "@/lib/auth";

export function Topbar() {
  const router = useRouter();
  const { user, logout, orgs, activeOrgId, setActiveOrgId } = useAuth();

  const activeOrg = orgs.find((o) => o.id === activeOrgId) ?? orgs[0] ?? null;
  const [orgOpen, setOrgOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);

  const orgRef = useRef<HTMLDivElement>(null);
  const userRef = useRef<HTMLDivElement>(null);

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
      {/* Org switcher */}
      <div className="relative" ref={orgRef}>
        <button
          type="button"
          onClick={() => setOrgOpen((o) => !o)}
          className="flex items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm font-medium text-foreground hover:bg-muted"
        >
          <span className="flex h-5 w-5 items-center justify-center rounded bg-brand-balloon text-[10px] font-bold text-white">
            {orgInitial}
          </span>
          {orgLabel}
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        </button>

        {orgOpen && (
          <div className="absolute left-0 z-20 mt-2 w-56 rounded-lg border border-border bg-card p-1 shadow-xl">
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
                onClick={() => {
                  setActiveOrgId(org.id);
                  setOrgOpen(false);
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

      {/* User menu */}
      <div className="relative" ref={userRef}>
        <button
          type="button"
          onClick={() => setUserOpen((o) => !o)}
          className="flex items-center gap-2 rounded-full p-1 pr-2 hover:bg-muted"
        >
          <span className="flex h-8 w-8 items-center justify-center rounded-full bg-brand-balloon text-xs font-semibold text-white">
            {userInitials}
          </span>
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        </button>

        {userOpen && (
          <div className="absolute right-0 z-20 mt-2 w-60 rounded-lg border border-border bg-card p-1 shadow-xl">
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

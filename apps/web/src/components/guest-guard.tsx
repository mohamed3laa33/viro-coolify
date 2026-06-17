"use client";

import { useEffect, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { useAuth } from "@/lib/auth";
import { safeNextPath } from "@/lib/utils";

/**
 * Reverse of {@link AuthGuard}: keeps already-authenticated users off the
 * guest-only pages (/login, /signup). Once auth state has hydrated and a
 * session is present it redirects to the sanitized `next` target (e.g. an
 * invite-accept deep link) or the default dashboard. While loading (or while
 * the redirect is in flight) it renders a lightweight placeholder instead of
 * the auth forms.
 */
export function GuestGuard({ children }: { children: ReactNode }) {
  const { accessToken, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (loading || !accessToken) return;
    const param =
      typeof window !== "undefined"
        ? new URLSearchParams(window.location.search).get("next")
        : null;
    router.replace(safeNextPath(param));
  }, [loading, accessToken, router]);

  if (loading || accessToken) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return <>{children}</>;
}

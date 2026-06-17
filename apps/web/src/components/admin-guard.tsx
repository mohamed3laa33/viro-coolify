"use client";

import { useEffect, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { useAuth } from "@/lib/auth";

/**
 * Client-side guard for the super-admin area. Assumes the surrounding dashboard
 * layout already enforces authentication; this layer additionally redirects
 * users without the `isAdmin` flag back to the dashboard once auth state has
 * hydrated. While loading (or while a redirect is in flight) it renders a
 * lightweight placeholder instead of the protected children.
 */
export function AdminGuard({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !user?.isAdmin) {
      router.replace("/dashboard");
    }
  }, [loading, user, router]);

  if (loading || !user?.isAdmin) {
    return (
      <div className="flex h-full items-center justify-center py-24">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return <>{children}</>;
}

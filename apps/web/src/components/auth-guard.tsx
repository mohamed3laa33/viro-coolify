"use client";

import { useEffect, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { useAuth } from "@/lib/auth";

/**
 * Client-side guard for authenticated areas. Redirects to /login once auth
 * state has hydrated and there is no access token. While loading (or while the
 * redirect is in flight) it renders a lightweight placeholder instead of the
 * protected children.
 */
export function AuthGuard({ children }: { children: ReactNode }) {
  const { accessToken, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !accessToken) {
      router.replace("/login");
    }
  }, [loading, accessToken, router]);

  if (loading || !accessToken) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return <>{children}</>;
}

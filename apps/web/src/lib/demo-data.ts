"use client";

// Demo/mock data accessor that keeps `@/lib/mock` OUT of the production bundle.
//
// `src/lib/mock.ts` is a sizeable module of fabricated orgs/apps/billing/etc.
// used only as a graceful fallback in demo mode. Importing it statically would
// ship it (and its secret-SHAPED placeholder strings) to every production user.
//
// Instead, call sites use the hooks below, which load the mock module via a
// *dynamic* `import("@/lib/mock")` gated on `isDemoMode()`. The gate is written
// as the raw, statically-inlinable env expression so that a production build
// (where `process.env.NODE_ENV === "production"` and `NEXT_PUBLIC_DEMO_MODE`
// is unset) dead-code-eliminates the `import()` and tree-shakes mock.ts out of
// the client bundle entirely.

import { useEffect, useState } from "react";
import type * as Mock from "@/lib/mock";

type MockModule = typeof Mock;

/**
 * True when demo/mock fallbacks are allowed. Written inline (not via
 * `isDemoMode()`) so bundlers can fold it to a constant in production and strip
 * the guarded dynamic import. Mirrors `@/lib/demo`'s `isDemoMode()`.
 */
export function demoEnabled(): boolean {
  return (
    process.env.NODE_ENV !== "production" ||
    process.env.NEXT_PUBLIC_DEMO_MODE === "1"
  );
}

/**
 * Load the mock module on demand — only in demo mode. Returns null in
 * production so the guarded `import()` is statically unreachable and removed.
 */
async function loadMock(): Promise<MockModule | null> {
  if (!demoEnabled()) return null;
  return import("@/lib/mock");
}

/**
 * React hook: returns `empty` immediately, then (only in demo mode) swaps in a
 * slice of the dynamically-loaded mock module via `select`. In production the
 * import never runs, so this always returns `empty`.
 *
 * Pass a memoized/stable `select` or rely on the default deps; `select` is
 * intentionally excluded from the dependency array (it is typically an inline
 * arrow) so the import runs once per mount.
 */
export function useDemoData<T>(select: (m: MockModule) => T, empty: T): T {
  const [value, setValue] = useState<T>(empty);

  useEffect(() => {
    let active = true;
    if (!demoEnabled()) return;
    void loadMock().then((mock) => {
      if (active && mock) setValue(select(mock));
    });
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return value;
}

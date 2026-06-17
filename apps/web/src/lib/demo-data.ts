"use client";

// Demo/mock data accessor that keeps `@/lib/mock` OUT of the production bundle.
//
// `src/lib/mock.ts` is a sizeable module of fabricated orgs/apps/billing/etc.
// used only as a graceful fallback in demo mode. Importing it statically would
// ship it (and its fake placeholder strings) to every production user.
//
// Instead, call sites use the hooks below, which load the mock module via a
// *dynamic* `import("@/lib/mock")`. The `import()` is wrapped in a
// `process.env.NODE_ENV !== "production"` guard. Next/Webpack inlines
// `process.env.NODE_ENV` to a string literal at build time, so in a production
// build the guard folds to `false`, the branch becomes dead code, and the
// `import("@/lib/mock")` is dropped — taking mock.ts (and its chunk) out of the
// client bundle. The runtime `demoEnabled()` gate (which also honors
// `NEXT_PUBLIC_DEMO_MODE`) is kept for the behavioral decision in dev/preview.

import { useEffect, useState } from "react";
import type * as Mock from "@/lib/mock";

type MockModule = typeof Mock;

/**
 * True when demo/mock fallbacks are allowed: any non-production build, or an
 * explicit `NEXT_PUBLIC_DEMO_MODE=1`. This is the runtime behavioral gate;
 * static elimination of the mock chunk is handled separately by the
 * `process.env.NODE_ENV !== "production"` guard around the `import()` in
 * `loadMock()`. Mirrors `@/lib/demo`'s `isDemoMode()`.
 */
export function demoEnabled(): boolean {
  return (
    process.env.NODE_ENV !== "production" ||
    process.env.NEXT_PUBLIC_DEMO_MODE === "1"
  );
}

/**
 * Load the mock module on demand — only in demo mode. The `import()` lives
 * behind a `process.env.NODE_ENV !== "production"` guard that bundlers fold to a
 * constant: in a production build the branch is dead-code-eliminated and the
 * `import("@/lib/mock")` (and mock.ts's chunk) never ships. Returns null
 * whenever demo mode is off.
 */
async function loadMock(): Promise<MockModule | null> {
  // The literal NODE_ENV check is what lets bundlers statically drop the
  // import() in production; demoEnabled() additionally honors NEXT_PUBLIC_DEMO_MODE
  // in non-production builds without re-introducing the prod import.
  if (process.env.NODE_ENV !== "production" && demoEnabled()) {
    return import("@/lib/mock");
  }
  return null;
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

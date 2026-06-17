"use client";

import { useCallback, useEffect, useRef, useState } from "react";

interface ResourceState<T> {
  data: T;
  loading: boolean;
  /** True when the network call failed (or was skipped) and the fallback shows. */
  usingFallback: boolean;
  /** True when the network call itself failed (distinct from a skipped fetch). */
  error: boolean;
  /** Re-run the fetcher imperatively (e.g. after a mutation). */
  refetch: () => void;
}

/**
 * Per-call tuning for {@link useResource}. All optional.
 *
 * - `cacheKey` opts a call into the module-level request cache. Callers should
 *   pass a stable string that uniquely identifies the request (conventionally
 *   the request URL plus any scoping ids, e.g. `apps:${orgId}`). Concurrent
 *   callers with the same key share one in-flight fetch (dedupe), and a result
 *   is reused for `ttlMs` before a fresh fetch runs. When omitted, the hook
 *   behaves exactly as before: every mount fetches, nothing is shared.
 * - `ttlMs` is how long (ms) a cached value is considered fresh. Default 30s.
 * - `revalidateOnFocus` re-runs the fetcher when the tab/window regains focus
 *   (only for keyed calls, and only if the cached value is older than `ttlMs`).
 *   Defaults to true.
 */
export interface UseResourceOptions {
  cacheKey?: string;
  ttlMs?: number;
  revalidateOnFocus?: boolean;
}

const DEFAULT_TTL_MS = 30_000;

interface CacheEntry<T = unknown> {
  /** Resolved value (present once a fetch has succeeded at least once). */
  value?: T;
  /** Epoch ms when `value` was last written; 0 until first success. */
  updatedAt: number;
  /** Shared in-flight fetch so concurrent callers dedupe onto one request. */
  inFlight?: Promise<T>;
}

// Module-level cache keyed by the caller-supplied `cacheKey`. Lives for the
// lifetime of the JS context (cleared on full reload), which is exactly the
// scope we want for client-side request dedupe + short-TTL reuse.
const cache = new Map<string, CacheEntry>();

/** Test/utility seam: drop all cached entries. */
export function __clearResourceCache(): void {
  cache.clear();
}

function isFresh(entry: CacheEntry | undefined, ttlMs: number): boolean {
  return (
    !!entry &&
    entry.updatedAt > 0 &&
    "value" in entry &&
    Date.now() - entry.updatedAt < ttlMs
  );
}

/**
 * Run `fetcher` through the cache for `key`, deduping concurrent callers and
 * reusing a fresh value within `ttlMs`. `force` skips the freshness check (used
 * by explicit refetch / focus revalidation) but still dedupes onto any
 * in-flight request.
 */
function fetchThroughCache<T>(
  key: string,
  fetcher: (signal: AbortSignal) => Promise<T>,
  signal: AbortSignal,
  ttlMs: number,
  force: boolean,
): Promise<T> {
  const entry = (cache.get(key) as CacheEntry<T> | undefined) ?? {
    updatedAt: 0,
  };

  if (!force && isFresh(entry, ttlMs)) {
    return Promise.resolve(entry.value as T);
  }
  if (entry.inFlight) {
    return entry.inFlight;
  }

  // A shared in-flight fetch must NOT be tied to one caller's AbortSignal —
  // otherwise the first unmount would cancel everyone. We run the fetch with
  // its own signal and let each caller drop out via its own signal below.
  const shared = fetcher(new AbortController().signal)
    .then((value) => {
      cache.set(key, { value, updatedAt: Date.now() });
      return value;
    })
    .catch((err: unknown) => {
      // Clear the in-flight marker so the next caller can retry.
      const cur = cache.get(key) as CacheEntry<T> | undefined;
      if (cur) cache.set(key, { ...cur, inFlight: undefined });
      throw err;
    });

  cache.set(key, { ...entry, inFlight: shared });

  // Respect the caller's own cancellation without aborting the shared fetch.
  return new Promise<T>((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const onAbort = () => {
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal.addEventListener("abort", onAbort, { once: true });
    shared.then(
      (value) => {
        signal.removeEventListener("abort", onAbort);
        resolve(value);
      },
      (err) => {
        signal.removeEventListener("abort", onAbort);
        reject(err);
      },
    );
  });
}

/**
 * Fetch a resource on the client, falling back to the supplied value when the
 * API is unreachable — or when the caller passes a `null` fetcher (e.g. no
 * active org or token yet).
 *
 * The `fallback` is whatever the caller decides to show on failure: in demo
 * mode that is mock data, and in production it is a real empty/error state.
 * Gating belongs at the call site (see `isDemoMode()`); the hook stays neutral.
 *
 * Each effect run creates a fresh AbortController and passes its `signal` to
 * the fetcher, then aborts it on cleanup — giving true request cancellation
 * (e.g. forwarded into `authedCall(fn, signal)`) on top of the stale-result
 * guard. The fetcher may ignore the signal for backwards compatibility.
 *
 * When `options.cacheKey` is supplied the hook additionally: (1) dedupes
 * concurrent callers of the same key onto one fetch, (2) reuses a fresh cached
 * value within the TTL instead of refetching on every mount, and (3)
 * revalidates when the window regains focus. See {@link UseResourceOptions}.
 */
export function useResource<T>(
  fetcher: ((signal: AbortSignal) => Promise<T>) | null,
  fallback: T,
  deps: ReadonlyArray<unknown> = [],
  options: UseResourceOptions = {},
): ResourceState<T> {
  const {
    cacheKey,
    ttlMs = DEFAULT_TTL_MS,
    revalidateOnFocus = true,
  } = options;

  const [data, setData] = useState<T>(fallback);
  const [loading, setLoading] = useState(true);
  const [usingFallback, setUsingFallback] = useState(false);
  const [error, setError] = useState(false);
  const [nonce, setNonce] = useState(0);

  const refetch = useCallback(() => setNonce((n) => n + 1), []);

  // Latest fetcher in a ref so the focus listener always calls the current one
  // without re-subscribing on every render.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    let active = true;

    // No fetcher (missing org/token): show fallback without a network call.
    if (!fetcher) {
      setData(fallback);
      setUsingFallback(true);
      setError(false);
      setLoading(false);
      return;
    }

    const controller = new AbortController();

    setLoading(true);

    const run = cacheKey
      ? fetchThroughCache(
          cacheKey,
          fetcher,
          controller.signal,
          ttlMs,
          // A bumped nonce (explicit refetch) forces a fresh fetch.
          nonce > 0,
        )
      : fetcher(controller.signal);

    run
      .then((result) => {
        if (!active) return;
        setData(result);
        setUsingFallback(false);
        setError(false);
      })
      .catch((err: unknown) => {
        if (!active) return;
        // A deliberate cancellation (cleanup/unmount) is not an error state.
        if (
          controller.signal.aborted ||
          (err instanceof DOMException && err.name === "AbortError")
        ) {
          return;
        }
        setData(fallback);
        setUsingFallback(true);
        setError(true);
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce, cacheKey, ttlMs]);

  // Revalidate on window focus for keyed calls whose cached value has gone
  // stale. Reuses the same state-update path as a refetch.
  useEffect(() => {
    if (!cacheKey || !revalidateOnFocus) return;
    if (typeof window === "undefined") return;

    function onFocus() {
      const fn = fetcherRef.current;
      if (!fn) return;
      const entry = cache.get(cacheKey!);
      // Only revalidate when the cached value is stale (or absent).
      if (isFresh(entry, ttlMs)) return;
      setNonce((n) => n + 1);
    }

    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [cacheKey, revalidateOnFocus, ttlMs]);

  return { data, loading, usingFallback, error, refetch };
}

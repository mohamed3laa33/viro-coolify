"use client";

import { useCallback, useEffect, useState } from "react";

interface ResourceState<T> {
  data: T;
  loading: boolean;
  /** True when the network call failed (or was skipped) and the fallback shows. */
  usingFallback: boolean;
  /** Re-run the fetcher imperatively (e.g. after a mutation). */
  refetch: () => void;
}

/**
 * Fetch a resource on the client, falling back to mock data when the API is
 * unreachable — or when the caller passes a `null` fetcher (e.g. no active org
 * or token yet) — so the UI always renders standalone.
 */
export function useResource<T>(
  fetcher: (() => Promise<T>) | null,
  fallback: T,
  deps: ReadonlyArray<unknown> = [],
): ResourceState<T> {
  const [data, setData] = useState<T>(fallback);
  const [loading, setLoading] = useState(true);
  const [usingFallback, setUsingFallback] = useState(false);
  const [nonce, setNonce] = useState(0);

  const refetch = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    let active = true;

    // No fetcher (missing org/token): show fallback without a network call.
    if (!fetcher) {
      setData(fallback);
      setUsingFallback(true);
      setLoading(false);
      return;
    }

    setLoading(true);

    fetcher()
      .then((result) => {
        if (!active) return;
        setData(result);
        setUsingFallback(false);
      })
      .catch(() => {
        if (!active) return;
        setData(fallback);
        setUsingFallback(true);
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  return { data, loading, usingFallback, refetch };
}

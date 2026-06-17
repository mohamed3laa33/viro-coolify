"use client";

import { useEffect, useState } from "react";

interface ResourceState<T> {
  data: T;
  loading: boolean;
  /** True when the network call failed and the fallback is being shown. */
  usingFallback: boolean;
}

/**
 * Fetch a resource on the client, falling back to mock data when the API is
 * unreachable so the UI renders standalone.
 */
export function useResource<T>(
  fetcher: () => Promise<T>,
  fallback: T,
  deps: ReadonlyArray<unknown> = [],
): ResourceState<T> {
  const [data, setData] = useState<T>(fallback);
  const [loading, setLoading] = useState(true);
  const [usingFallback, setUsingFallback] = useState(false);

  useEffect(() => {
    let active = true;
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
  }, deps);

  return { data, loading, usingFallback };
}

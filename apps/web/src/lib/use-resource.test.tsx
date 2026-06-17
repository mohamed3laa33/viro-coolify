import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import {
  useResource,
  invalidate,
  __clearResourceCache,
} from "@/lib/use-resource";
import { ApiError } from "@/lib/api";

beforeEach(() => {
  __clearResourceCache();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useResource — fallback / skip", () => {
  it("shows the fallback immediately when the fetcher is null", async () => {
    const { result } = renderHook(() =>
      useResource<number[]>(null, [1, 2, 3], []),
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.data).toEqual([1, 2, 3]);
    expect(result.current.usingFallback).toBe(true);
    expect(result.current.error).toBe(false);
  });

  it("resolves to the fetched value on success (no fallback, no error)", async () => {
    const { result } = renderHook(() =>
      useResource<string>(async () => "live", "fallback", []),
    );

    await waitFor(() => expect(result.current.data).toBe("live"));
    expect(result.current.usingFallback).toBe(false);
    expect(result.current.error).toBe(false);
  });
});

describe("useResource — error", () => {
  it("falls back and flags error when the fetch rejects", async () => {
    const fetcher = vi.fn(async () => {
      throw new Error("boom");
    });
    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", []),
    );

    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.data).toBe("fallback");
    expect(result.current.usingFallback).toBe(true);
  });

  it("does not flag error on a deliberate AbortError", async () => {
    const fetcher = vi.fn(async (signal: AbortSignal) => {
      return new Promise<string>((_resolve, reject) => {
        signal.addEventListener("abort", () =>
          reject(new DOMException("Aborted", "AbortError")),
        );
      });
    });
    const { result, unmount } = renderHook(() =>
      useResource<string>(fetcher, "fallback", []),
    );
    // Unmount aborts the in-flight request; that must not become an error.
    unmount();
    await new Promise((r) => setTimeout(r, 0));
    expect(result.current.error).toBe(false);
  });
});

describe("useResource — errorStatus", () => {
  it("is null while a fetch is still in flight (no error yet)", async () => {
    const { result } = renderHook(() =>
      useResource<string>(() => new Promise<string>(() => {}), "fallback", []),
    );
    expect(result.current.errorStatus).toBeNull();
  });

  it("is null on success", async () => {
    const { result } = renderHook(() =>
      useResource<string>(async () => "live", "fallback", []),
    );

    await waitFor(() => expect(result.current.data).toBe("live"));
    expect(result.current.errorStatus).toBeNull();
  });

  it("is null when the fetcher is skipped (null)", async () => {
    const { result } = renderHook(() =>
      useResource<string>(null, "fallback", []),
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.usingFallback).toBe(true);
    expect(result.current.errorStatus).toBeNull();
  });

  it("exposes the ApiError status when the backend returns 4xx/5xx", async () => {
    const fetcher = vi.fn(async () => {
      throw new ApiError("forbidden", 403);
    });
    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", []),
    );

    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.errorStatus).toBe(403);
  });

  it("stays null for a non-ApiError (network/TypeError) failure", async () => {
    const fetcher = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", []),
    );

    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.errorStatus).toBeNull();
  });

  it("clears the status back to null once a later fetch succeeds", async () => {
    let attempt = 0;
    const fetcher = vi.fn(async () => {
      attempt += 1;
      if (attempt === 1) throw new ApiError("server error", 500);
      return "recovered";
    });
    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", []),
    );

    await waitFor(() => expect(result.current.errorStatus).toBe(500));

    act(() => result.current.refetch());
    await waitFor(() => expect(result.current.data).toBe("recovered"));
    expect(result.current.errorStatus).toBeNull();
    expect(result.current.error).toBe(false);
  });
});

describe("useResource — invalidate", () => {
  it("drops the cached entry so the next mount refetches", async () => {
    let n = 0;
    const fetcher = vi.fn(async () => `v${++n}`);

    const first = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "inval",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(first.result.current.data).toBe("v1"));
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Drop the cached entry; the warm value must no longer be reused.
    act(() => invalidate("inval"));

    const second = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "inval",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(second.result.current.data).toBe("v2"));
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("is a no-op for a key that was never cached", async () => {
    const fetcher = vi.fn(async () => "cached");

    expect(() => invalidate("never-seen")).not.toThrow();

    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "fresh",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(result.current.data).toBe("cached"));
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

describe("useResource — refetchIntervalMs", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("re-runs the fetcher on each interval tick", async () => {
    let n = 0;
    const fetcher = vi.fn(async () => `v${++n}`);

    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        refetchIntervalMs: 1_000,
      }),
    );

    // Initial fetch resolves first.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.data).toBe("v1");
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Each tick of the interval drives a fresh fetch.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(result.current.data).toBe("v2");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(result.current.data).toBe("v3");
  });

  it("stops polling after unmount", async () => {
    const fetcher = vi.fn(async () => "x");

    const { unmount } = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        refetchIntervalMs: 1_000,
      }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    unmount();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    // No further fetches once the timer is cleared on unmount.
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

describe("useResource — cache + dedupe", () => {
  it("dedupes concurrent callers sharing a cacheKey onto one fetch", async () => {
    let resolveFetch: (v: string) => void = () => {};
    const fetcher = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          resolveFetch = resolve;
        }),
    );

    const a = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], { cacheKey: "shared" }),
    );
    const b = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], { cacheKey: "shared" }),
    );

    // Both mounted with the same key -> exactly one underlying fetch.
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveFetch("value");
      await Promise.resolve();
    });

    await waitFor(() => expect(a.result.current.data).toBe("value"));
    await waitFor(() => expect(b.result.current.data).toBe("value"));
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("reuses a fresh cached value instead of refetching within the TTL", async () => {
    const fetcher = vi.fn(async () => "cached");

    const first = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "reuse",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(first.result.current.data).toBe("cached"));
    expect(fetcher).toHaveBeenCalledTimes(1);

    // A second consumer mounting later should read the cache, not refetch.
    const second = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "reuse",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(second.result.current.data).toBe("cached"));
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("forces a fresh fetch on refetch() even with a warm cache", async () => {
    let n = 0;
    const fetcher = vi.fn(async () => `v${++n}`);

    const { result } = renderHook(() =>
      useResource<string>(fetcher, "fallback", [], {
        cacheKey: "refetch",
        ttlMs: 10_000,
      }),
    );
    await waitFor(() => expect(result.current.data).toBe("v1"));

    act(() => result.current.refetch());
    await waitFor(() => expect(result.current.data).toBe("v2"));
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("does not dedupe or cache when no cacheKey is supplied", async () => {
    const fetcher = vi.fn(async () => "x");

    renderHook(() => useResource<string>(fetcher, "fallback", []));
    renderHook(() => useResource<string>(fetcher, "fallback", []));

    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  });
});

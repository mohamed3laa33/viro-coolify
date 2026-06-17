import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useResource, __clearResourceCache } from "@/lib/use-resource";

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

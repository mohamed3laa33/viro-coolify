import { describe, it, expect, vi, afterEach } from "vitest";

// The API base URL is resolved at module import time from the environment.
// These tests re-import the module under different env shapes (via
// vi.resetModules) to assert it NEVER throws at load — a top-level throw here
// would white-screen the entire app — and that a missing production URL is
// surfaced as a handled, catchable error on the first request instead.

const ORIGINAL_ENV = { ...process.env };

afterEach(() => {
  process.env = { ...ORIGINAL_ENV };
  vi.resetModules();
  vi.unstubAllGlobals();
});

async function importApiWith(env: Record<string, string | undefined>) {
  vi.resetModules();
  process.env = { ...ORIGINAL_ENV, ...env } as NodeJS.ProcessEnv;
  return import("@/lib/api");
}

describe("API base URL resolution", () => {
  it("uses NEXT_PUBLIC_VORTEX_API_URL when set", async () => {
    const mod = await importApiWith({
      NEXT_PUBLIC_VORTEX_API_URL: "https://api.example.com",
      NODE_ENV: "production",
    });
    expect(mod.API_BASE_URL).toBe("https://api.example.com");
    expect(mod.API_BASE_URL_CONFIGURED).toBe(true);
  });

  it("falls back to the legacy NEXT_PUBLIC_VIRO_API_URL", async () => {
    const mod = await importApiWith({
      NEXT_PUBLIC_VORTEX_API_URL: undefined,
      NEXT_PUBLIC_VIRO_API_URL: "https://legacy.example.com",
      NODE_ENV: "production",
    });
    expect(mod.API_BASE_URL).toBe("https://legacy.example.com");
    expect(mod.API_BASE_URL_CONFIGURED).toBe(true);
  });

  it("falls back to localhost in development", async () => {
    const mod = await importApiWith({
      NEXT_PUBLIC_VORTEX_API_URL: undefined,
      NEXT_PUBLIC_VIRO_API_URL: undefined,
      NODE_ENV: "development",
    });
    expect(mod.API_BASE_URL).toBe("http://localhost:8080");
    expect(mod.API_BASE_URL_CONFIGURED).toBe(true);
  });

  it("does NOT throw at module load when the URL is unset in production", async () => {
    // The whole point: importing must succeed and degrade, never crash the app.
    await expect(
      importApiWith({
        NEXT_PUBLIC_VORTEX_API_URL: undefined,
        NEXT_PUBLIC_VIRO_API_URL: undefined,
        NODE_ENV: "production",
      }),
    ).resolves.toBeDefined();

    const mod = await importApiWith({
      NEXT_PUBLIC_VORTEX_API_URL: undefined,
      NEXT_PUBLIC_VIRO_API_URL: undefined,
      NODE_ENV: "production",
    });
    expect(mod.API_BASE_URL_CONFIGURED).toBe(false);
    // Same-origin relative base so buildUrl stays usable.
    expect(mod.buildUrl("/v1/me")).toBe("/v1/me");
  });

  it("surfaces an unconfigured base as a handled ApiError on request, not an uncaught throw", async () => {
    const mod = await importApiWith({
      NEXT_PUBLIC_VORTEX_API_URL: undefined,
      NEXT_PUBLIC_VIRO_API_URL: undefined,
      NODE_ENV: "production",
    });
    // fetch must never be reached when unconfigured.
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);

    await expect(mod.api.getPlans()).rejects.toBeInstanceOf(mod.ApiError);
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});

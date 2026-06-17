import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { api, buildUrl, API_BASE_URL } from "@/lib/api";

interface CapturedCall {
  url: string;
  init: RequestInit;
}

function stubFetch(responseBody: unknown, status = 200) {
  const calls: CapturedCall[] = [];
  const fn = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} });
      return new Response(JSON.stringify(responseBody), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    },
  );
  vi.stubGlobal("fetch", fn);
  return calls;
}

describe("buildUrl", () => {
  it("joins the base URL and path with a single slash", () => {
    expect(buildUrl("/v1/me")).toBe(`${API_BASE_URL}/v1/me`);
  });

  it("normalizes a missing leading slash", () => {
    expect(buildUrl("v1/apps")).toBe(`${API_BASE_URL}/v1/apps`);
  });
});

describe("api client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("builds the org-scoped apps URL and attaches the bearer header", async () => {
    const calls = stubFetch({ data: [] });

    await api.listApps("org_1", "tok_123");

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/apps`);

    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer tok_123");
    expect(calls[0].init.method).toBe("GET");
  });

  it("lists databases under the org scope", async () => {
    const calls = stubFetch({ data: [] });

    await api.listDatabases("org_2", "tok_abc");

    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_2/databases`);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer tok_abc");
  });

  it("sends a JSON body without auth header for login", async () => {
    const calls = stubFetch({
      user: { id: "u1", email: "a@b.c", name: "A" },
      accessToken: "a",
      refreshToken: "r",
    });

    await api.login({ email: "a@b.c", password: "pw" });

    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/auth/login`);
    expect(calls[0].init.method).toBe("POST");
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers.Authorization).toBeUndefined();
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      email: "a@b.c",
      password: "pw",
    });
  });

  it("posts to the org-scoped deploy action endpoint with the bearer token", async () => {
    const calls = stubFetch({ id: "app_1", status: "deploying" }, 202);

    const app = await api.deployApp("org_1", "app_1", "tok_xyz");

    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/deploy`,
    );
    expect(calls[0].init.method).toBe("POST");
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer tok_xyz");
    expect(app.status).toBe("deploying");
  });

  it("subscribes via the org-scoped billing endpoint", async () => {
    const calls = stubFetch(
      { subscription: { id: "sub_1", status: "active" } },
      200,
    );

    const res = await api.subscribe("org_9", "plan_scale", "tok_b");

    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_9/billing/subscribe`,
    );
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      planId: "plan_scale",
    });
    expect(res.subscription.status).toBe("active");
  });

  it("fetches public billing plans without a bearer header", async () => {
    const calls = stubFetch({ data: [], provider: "stripe" });

    await api.getPlans();

    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/billing/plans`);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it("refreshes once on a 401 and retries the request", async () => {
    let attempt = 0;
    const urls: string[] = [];
    const fn = vi.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input));
      attempt += 1;
      if (attempt === 1) {
        return new Response(JSON.stringify({ message: "expired" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ data: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fn);

    const onUnauthorized = vi.fn(async () => "fresh_token");
    await api.listApps("org_1", "stale_token", onUnauthorized);

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(urls).toHaveLength(2);
  });

  it("throws ApiError on a non-ok response", async () => {
    stubFetch({ message: "nope" }, 401);
    await expect(api.me("bad")).rejects.toThrow("nope");
  });
});

describe("environment", () => {
  beforeEach(() => {
    // no-op; documents that API_BASE_URL is resolved from env at import time.
  });

  it("exposes a non-empty base URL", () => {
    expect(API_BASE_URL.length).toBeGreaterThan(0);
  });
});

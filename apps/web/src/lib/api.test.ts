import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  api,
  buildUrl,
  API_BASE_URL,
  computeHoursUsed,
  formatCents,
  formatHourlyPrice,
  type PricingComponent,
} from "@/lib/api";

interface CapturedCall {
  url: string;
  init: RequestInit;
}

function stubFetch(responseBody: unknown, status = 200) {
  const calls: CapturedCall[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    return new Response(JSON.stringify(responseBody), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  });
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

  it("sends the auth cookie on an authed GET and builds the org-scoped URL", async () => {
    const calls = stubFetch({ data: [] });

    await api.listApps("org_1", "tok_123");

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/apps`);
    expect(calls[0].init.method).toBe("GET");
    // The browser session is cookie-based: every request must opt into sending
    // the HttpOnly auth cookies.
    expect(calls[0].init.credentials).toBe("include");
  });

  it("sends the auth cookie when listing databases under the org scope", async () => {
    const calls = stubFetch({ data: [] });

    await api.listDatabases("org_2", "tok_abc");

    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_2/databases`);
    expect(calls[0].init.credentials).toBe("include");
  });

  it("still attaches a Bearer header as the non-browser fallback when a token is passed", async () => {
    // The Bearer header is a fallback for non-browser clients (e.g. the CLI);
    // the browser path relies on cookies, asserted in the cookie tests above.
    const calls = stubFetch({ data: [] });

    await api.listApps("org_1", "tok_123");

    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer tok_123");
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
    // A POST with a JSON body must also opt into cookies so the server can set
    // the HttpOnly session cookies on the response.
    expect(calls[0].init.credentials).toBe("include");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      email: "a@b.c",
      password: "pw",
    });
  });

  it("posts to the org-scoped deploy action endpoint over the cookie session", async () => {
    const calls = stubFetch({ id: "app_1", status: "deploying" }, 202);

    const app = await api.deployApp("org_1", "app_1", "tok_xyz");

    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/deploy`,
    );
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.credentials).toBe("include");
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

  it("fetches the public services catalog without a bearer header", async () => {
    const calls = stubFetch({ data: [] });

    await api.getServiceCatalog();

    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/services/catalog`);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it("refreshes once on a 401 and retries the request, sending cookies on both legs", async () => {
    let attempt = 0;
    const calls: CapturedCall[] = [];
    const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init ?? {} });
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
    expect(calls).toHaveLength(2);
    // Both the original request and the post-refresh retry must carry the
    // HttpOnly cookies — the refreshed session lives in the rotated cookie, not
    // a JS-held token, so the cookie mechanism must be exercised on each leg.
    expect(calls[0].init.credentials).toBe("include");
    expect(calls[1].init.credentials).toBe("include");
  });

  it("throws ApiError on a non-ok response", async () => {
    stubFetch({ message: "nope" }, 401);
    await expect(api.me("bad")).rejects.toThrow("nope");
  });

  it("lists projects under the org scope", async () => {
    const calls = stubFetch({ data: [] });
    await api.listProjects("org_1", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/projects`);
    expect(calls[0].init.method).toBe("GET");
  });

  it("creates a project with a name body", async () => {
    const calls = stubFetch({ id: "proj_1", name: "Platform" });
    await api.createProject("org_1", "Platform", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/projects`);
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      name: "Platform",
    });
  });

  it("lists apps for a project", async () => {
    const calls = stubFetch({ data: [] });
    await api.listProjectApps("org_1", "proj_2", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/projects/proj_2/apps`,
    );
  });

  it("lists members under the org scope", async () => {
    const calls = stubFetch({ data: [] });
    await api.listMembers("org_1", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/members`);
  });

  it("lists invitations under the org scope", async () => {
    const calls = stubFetch({ data: [] });
    await api.listInvitations("org_1", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/invitations`);
  });

  it("posts an invitation with email, role and optional project", async () => {
    const calls = stubFetch({ id: "inv_1" });
    await api.invite(
      "org_1",
      { email: "a@b.c", role: "admin", projectId: "proj_2" },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/invitations`);
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      email: "a@b.c",
      role: "admin",
      projectId: "proj_2",
    });
  });

  it("accepts an invitation by token", async () => {
    const calls = stubFetch({ id: "inv_1", status: "accepted" });
    await api.acceptInvitation("inv_tok_xyz", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/invitations/accept`);
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      token: "inv_tok_xyz",
    });
  });

  it("lists env vars for an app", async () => {
    const calls = stubFetch({ data: [] });
    await api.listEnv("org_1", "app_1", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/apps/app_1/env`);
  });

  it("sets an env var via PUT", async () => {
    const calls = stubFetch({ key: "K", value: "V" });
    await api.setEnv("org_1", "app_1", { key: "K", value: "V" }, "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/apps/app_1/env`);
    expect(calls[0].init.method).toBe("PUT");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      key: "K",
      value: "V",
    });
  });

  it("deletes an env var by url-encoded key", async () => {
    const calls = stubFetch({});
    await api.deleteEnv("org_1", "app_1", "MY KEY", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/env/MY%20KEY`,
    );
    expect(calls[0].init.method).toBe("DELETE");
  });

  it("lists domains for an app", async () => {
    const calls = stubFetch({ data: [] });
    await api.listDomains("org_1", "app_1", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/domains`,
    );
  });

  it("adds a domain with a domain body", async () => {
    const calls = stubFetch({
      id: "dom_1",
      domain: "acme.com",
      verified: false,
    });
    await api.addDomain("org_1", "app_1", "acme.com", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/domains`,
    );
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      domain: "acme.com",
    });
  });

  it("deletes a domain by id", async () => {
    const calls = stubFetch({});
    await api.deleteDomain("org_1", "app_1", "dom_9", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/domains/dom_9`,
    );
    expect(calls[0].init.method).toBe("DELETE");
  });

  it("fetches app metrics over the cookie session", async () => {
    const calls = stubFetch({ cpu: [], memory: [], requests: [] });
    await api.getMetrics("org_1", "app_1", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/apps/app_1/metrics`,
    );
    expect(calls[0].init.credentials).toBe("include");
  });

  it("deletes a database by id under the org scope", async () => {
    const calls = stubFetch({});
    await api.deleteDatabase("org_1", "db_7", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/databases/db_7`);
    expect(calls[0].init.method).toBe("DELETE");
  });

  it("updates an org via PATCH with the editable fields", async () => {
    const calls = stubFetch({ id: "org_1", name: "Acme" });
    await api.updateOrg(
      "org_1",
      { name: "Acme", billingEmail: "billing@acme.com" },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      name: "Acme",
      billingEmail: "billing@acme.com",
    });
  });

  it("deletes an empty project under the org scope", async () => {
    const calls = stubFetch({});
    await api.deleteProject("org_1", "proj_3", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/projects/proj_3`);
    expect(calls[0].init.method).toBe("DELETE");
  });

  it("changes a member's role via PATCH at the member URL", async () => {
    const calls = stubFetch({ userId: "u_2", role: "admin" });
    await api.updateMember("org_1", "u_2", { role: "admin" }, "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/members/u_2`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      role: "admin",
    });
  });

  it("removes a member via DELETE at the member URL", async () => {
    const calls = stubFetch({});
    await api.removeMember("org_1", "u_5", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/orgs/org_1/members/u_5`);
    expect(calls[0].init.method).toBe("DELETE");
  });

  it("revokes an invitation via DELETE at the invitation URL", async () => {
    const calls = stubFetch({});
    await api.revokeInvitation("org_1", "inv_9", "tok");
    expect(calls[0].url).toBe(
      `${API_BASE_URL}/v1/orgs/org_1/invitations/inv_9`,
    );
    expect(calls[0].init.method).toBe("DELETE");
  });
});

describe("admin api client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // Plans
  it("lists admin plans with the bearer token", async () => {
    const calls = stubFetch({ data: [] });
    await api.listAdminPlans("admtok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/plans`);
    expect(calls[0].init.method).toBe("GET");
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer admtok");
  });

  it("creates an admin plan via POST with the full body", async () => {
    const calls = stubFetch({ id: "scale" }, 201);
    await api.createPlan(
      {
        id: "scale",
        name: "Scale",
        description: "big",
        priceCents: 9900,
        currency: "usd",
        includedHours: 3000,
        overagePerHourCents: 1,
        maxCpu: 8,
        maxMemoryMb: 8192,
        maxApps: 100,
        isDefault: false,
        sortOrder: 2,
        active: true,
        stripePriceId: "price_x",
      },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/plans`);
    expect(calls[0].init.method).toBe("POST");
    const body = JSON.parse(calls[0].init.body as string);
    expect(body.id).toBe("scale");
    expect(body.maxCpu).toBe(8);
    expect(body.maxMemoryMb).toBe(8192);
    expect(body.maxApps).toBe(100);
    expect(body.stripePriceId).toBe("price_x");
  });

  it("updates an admin plan via PATCH at the id URL", async () => {
    const calls = stubFetch({ id: "launch" });
    await api.updatePlan("launch", { priceCents: 3900 }, "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/plans/launch`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      priceCents: 3900,
    });
  });

  it("deletes an admin plan via DELETE at the id URL", async () => {
    const calls = stubFetch({});
    await api.deletePlan("hobby", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/plans/hobby`);
    expect(calls[0].init.method).toBe("DELETE");
  });

  // Templates
  it("lists templates", async () => {
    const calls = stubFetch({ data: [] });
    await api.listTemplates("tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/templates`);
    expect(calls[0].init.method).toBe("GET");
  });

  it("creates a template via POST", async () => {
    const calls = stubFetch({ key: "redis" }, 201);
    await api.createTemplate(
      {
        key: "redis",
        name: "Redis",
        description: "cache",
        category: "Databases",
        kind: "database",
        image: "redis:7",
        defaultPort: 6379,
        active: true,
        sortOrder: 4,
      },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/templates`);
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(calls[0].init.body as string).key).toBe("redis");
  });

  it("updates a template via PATCH at the url-encoded key URL", async () => {
    const calls = stubFetch({ key: "my key" });
    await api.updateTemplate("my key", { active: false }, "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/templates/my%20key`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      active: false,
    });
  });

  it("deletes a template via DELETE at the key URL", async () => {
    const calls = stubFetch({});
    await api.deleteTemplate("postgresql", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/templates/postgresql`);
    expect(calls[0].init.method).toBe("DELETE");
  });

  // Settings
  it("gets platform settings", async () => {
    const calls = stubFetch({ regions: [] });
    await api.getSettings("tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/settings`);
    expect(calls[0].init.method).toBe("GET");
  });

  it("updates platform settings via PATCH including overcommit factors", async () => {
    const calls = stubFetch({ regions: [] });
    await api.updateSettings(
      { cpuOvercommitFactor: 0.8, memoryOvercommitFactor: 0.9 },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/settings`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      cpuOvercommitFactor: 0.8,
      memoryOvercommitFactor: 0.9,
    });
  });

  // Overview
  it("fetches the admin overview", async () => {
    const calls = stubFetch({
      orgCount: 0,
      userCount: 0,
      subscriptionsByPlan: {},
      usageTotals: {},
    });
    await api.getAdminOverview("tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/overview`);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer tok");
  });

  // Pricing (hourly components)
  it("reads public pricing without a bearer header", async () => {
    const calls = stubFetch({ data: [] });
    await api.getPricing();
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/billing/pricing`);
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
  });

  it("lists admin pricing with the bearer token", async () => {
    const calls = stubFetch({ data: [] });
    await api.listPricing("admtok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/pricing`);
    expect(calls[0].init.method).toBe("GET");
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer admtok");
  });

  it("creates a pricing component via POST with the full body", async () => {
    const calls = stubFetch({ key: "cpu" }, 201);
    await api.createPricing(
      {
        key: "cpu",
        name: "CPU",
        unit: "core-hour",
        pricePerHour: 2,
        currency: "usd",
        active: true,
        sortOrder: 0,
      },
      "tok",
    );
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/pricing`);
    expect(calls[0].init.method).toBe("POST");
    const body = JSON.parse(calls[0].init.body as string);
    expect(body.key).toBe("cpu");
    expect(body.pricePerHour).toBe(2);
  });

  it("updates a pricing component via PATCH at the url-encoded key", async () => {
    const calls = stubFetch({ key: "core hour" });
    await api.updatePricing("core hour", { pricePerHour: 3 }, "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/pricing/core%20hour`);
    expect(calls[0].init.method).toBe("PATCH");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      pricePerHour: 3,
    });
  });

  it("deletes a pricing component via DELETE at the key URL", async () => {
    const calls = stubFetch({});
    await api.deletePricing("egress", "tok");
    expect(calls[0].url).toBe(`${API_BASE_URL}/v1/admin/pricing/egress`);
    expect(calls[0].init.method).toBe("DELETE");
  });
});

describe("formatCents", () => {
  it("formats whole-dollar amounts without cents", () => {
    expect(formatCents(2900, "usd")).toBe("$29");
  });

  it("formats sub-dollar cent amounts with two fraction digits", () => {
    expect(formatCents(250, "usd")).toBe("$2.50");
  });

  it("keeps sub-cent precision for fractional per-hour rates", () => {
    // 2 cents -> $0.02; a fractional 0.5 cents -> $0.005.
    expect(formatCents(2, "usd")).toBe("$0.02");
    expect(formatCents(0.5, "usd")).toBe("$0.005");
  });

  it("defaults the currency to USD", () => {
    expect(formatCents(100)).toBe("$1");
  });
});

describe("formatHourlyPrice", () => {
  const base: PricingComponent = {
    key: "cpu",
    name: "CPU",
    unit: "core-hour",
    pricePerHour: 2,
    currency: "usd",
    active: true,
    sortOrder: 0,
  };

  it("renders the rate and unit", () => {
    expect(formatHourlyPrice(base)).toBe("$0.02 / core-hour");
  });

  it("falls back to 'hour' when the unit is blank", () => {
    expect(formatHourlyPrice({ ...base, unit: "" })).toBe("$0.02 / hour");
  });

  it("formats sub-cent rates", () => {
    expect(formatHourlyPrice({ ...base, pricePerHour: 0.5 })).toBe(
      "$0.005 / core-hour",
    );
  });
});

describe("computeHoursUsed", () => {
  it("reads the compute_hours metric from a usage map", () => {
    expect(computeHoursUsed({ compute_hours: 412, builds: 9 })).toBe(412);
  });

  it("falls back across known compute metric keys", () => {
    expect(computeHoursUsed({ machine_hours: 100 })).toBe(100);
    expect(computeHoursUsed({ hours: 50 })).toBe(50);
  });

  it("returns 0 for null, undefined, or unrelated metrics", () => {
    expect(computeHoursUsed(null)).toBe(0);
    expect(computeHoursUsed(undefined)).toBe(0);
    expect(computeHoursUsed({ builds: 9 })).toBe(0);
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

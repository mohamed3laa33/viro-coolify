import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  within,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import type { App, Domain, EnvVar } from "@/lib/api";
import { ApiError } from "@/lib/api";
import AppDetailPage from "@/app/dashboard/apps/[appId]/page";

// authedCall invokes the supplied fn with a fake token + no-op onUnauthorized,
// mirroring the real org-scoped client used in production.
const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);
const push = vi.fn();

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    activeOrgId: "org_1",
    authedCall,
    // DomainsTab reads orgs to build the default FQDN.
    orgs: [{ id: "org_1", slug: "acme" }],
  }),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push }),
}));

// Force production-shaped behavior (no demo fallbacks) so the empty/error
// surfaces are exercised deterministically rather than hidden behind mock data.
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

// Neutralize the lazy demo-data hook so it returns the empty fallback verbatim
// (as in production). This keeps fetch call-counts deterministic — the hook
// otherwise swaps in mock data on mount, which would trigger an extra refetch.
vi.mock("@/lib/demo-data", () => ({
  demoEnabled: () => false,
  useDemoData: <T,>(_select: unknown, empty: T) => empty,
}));

// Spy on cache invalidation while keeping useResource real.
const invalidate = vi.fn();
vi.mock("@/lib/use-resource", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/use-resource")>();
  return { ...actual, invalidate: (...a: unknown[]) => invalidate(...a) };
});

const getApp = vi.fn();
const deleteApp = vi.fn();
const updateApp = vi.fn();
const scaleApp = vi.fn();
const listEnv = vi.fn();
const setEnv = vi.fn();
const deleteEnv = vi.fn();
const listDomains = vi.fn();
const addDomain = vi.fn();
const verifyDomain = vi.fn();
const deleteDomain = vi.fn();
const getLogs = vi.fn();
const getMetrics = vi.fn();
const listReleases = vi.fn();
const rollbackApp = vi.fn();
const listBuilds = vi.fn();
const getBuild = vi.fn();

// Keep ApiError + the type exports real so errorMessage()'s `instanceof
// ApiError` check works and the page renders against the real shapes.
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      getApp: (...a: unknown[]) => getApp(...a),
      deleteApp: (...a: unknown[]) => deleteApp(...a),
      updateApp: (...a: unknown[]) => updateApp(...a),
      scaleApp: (...a: unknown[]) => scaleApp(...a),
      listEnv: (...a: unknown[]) => listEnv(...a),
      setEnv: (...a: unknown[]) => setEnv(...a),
      deleteEnv: (...a: unknown[]) => deleteEnv(...a),
      listDomains: (...a: unknown[]) => listDomains(...a),
      addDomain: (...a: unknown[]) => addDomain(...a),
      verifyDomain: (...a: unknown[]) => verifyDomain(...a),
      deleteDomain: (...a: unknown[]) => deleteDomain(...a),
      getLogs: (...a: unknown[]) => getLogs(...a),
      getMetrics: (...a: unknown[]) => getMetrics(...a),
      listReleases: (...a: unknown[]) => listReleases(...a),
      rollbackApp: (...a: unknown[]) => rollbackApp(...a),
      listBuilds: (...a: unknown[]) => listBuilds(...a),
      getBuild: (...a: unknown[]) => getBuild(...a),
    },
  };
});

const APP: App = {
  id: "app_1",
  orgId: "org_1",
  projectId: "proj_1",
  name: "marketing-site",
  gitRepository: "github.com/acme/marketing",
  gitBranch: "main",
  buildPack: "nixpacks",
  cpu: 1,
  memoryMb: 512,
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

const ENV_VAR: EnvVar = { key: "API_KEY", value: "secret-value" };
const DOMAIN: Domain = { id: "dom_1", domain: "app.acme.com", verified: true };

// React's `use()` reads a thenable synchronously only when it carries the
// `status: "fulfilled"` + `value` shape React itself tags resolved promises
// with. A bare `Promise.resolve()` lacks that shape, so under jsdom `use()`
// suspends and never resumes (no re-render ping fires in the test renderer).
// Hand `use()` a pre-fulfilled promise so the page reads the params on the
// first render exactly as it does once Next has resolved them in production.
function resolvedParams<T>(value: T): Promise<T> {
  const p = Promise.resolve(value) as Promise<T> & {
    status: string;
    value: T;
  };
  p.status = "fulfilled";
  p.value = value;
  return p;
}

function renderPage() {
  return render(<AppDetailPage params={resolvedParams({ appId: "app_1" })} />);
}

beforeEach(() => {
  authedCall.mockClear();
  push.mockClear();
  invalidate.mockClear();
  getApp.mockReset().mockResolvedValue(APP);
  deleteApp.mockReset().mockResolvedValue(undefined);
  listEnv.mockReset().mockResolvedValue({ data: [ENV_VAR] });
  setEnv.mockReset().mockResolvedValue(ENV_VAR);
  deleteEnv.mockReset().mockResolvedValue(undefined);
  listDomains.mockReset().mockResolvedValue({ data: [DOMAIN] });
  addDomain.mockReset().mockResolvedValue(DOMAIN);
  deleteDomain.mockReset().mockResolvedValue(undefined);
  getLogs.mockReset().mockResolvedValue("");
  getMetrics.mockReset().mockResolvedValue({
    available: false,
    pods: [],
    cpuMillicores: 0,
    memoryBytes: 0,
  });
  updateApp.mockReset().mockResolvedValue(APP);
  scaleApp.mockReset().mockResolvedValue(APP);
  verifyDomain
    .mockReset()
    .mockResolvedValue({ ...DOMAIN, verified: true, status: "verified" });
  listReleases.mockReset().mockResolvedValue({
    data: [
      {
        id: "rel_2",
        appId: "app_1",
        orgId: "org_1",
        revision: 2,
        image: "registry/app:v2",
        cpu: 1,
        memoryMb: 512,
        status: "active",
        createdAt: "2026-06-02T00:00:00Z",
      },
      {
        id: "rel_1",
        appId: "app_1",
        orgId: "org_1",
        revision: 1,
        image: "registry/app:v1",
        cpu: 1,
        memoryMb: 512,
        status: "superseded",
        createdAt: "2026-06-01T00:00:00Z",
      },
    ],
    page: { limit: 25, offset: 0, hasMore: false },
  });
  rollbackApp.mockReset().mockResolvedValue(APP);
  listBuilds.mockReset().mockResolvedValue({
    data: [
      {
        id: "bld_1",
        appId: "app_1",
        orgId: "org_1",
        status: "succeeded",
        commitRef: "abc123",
        createdAt: "2026-06-01T00:00:00Z",
      },
    ],
    page: { limit: 25, offset: 0, hasMore: false },
  });
  getBuild.mockReset().mockResolvedValue({
    id: "bld_1",
    appId: "app_1",
    orgId: "org_1",
    status: "succeeded",
    logs: "build log output",
    createdAt: "2026-06-01T00:00:00Z",
  });
});

afterEach(() => cleanup());

// Navigate to a named tab once the app has loaded.
async function gotoTab(name: string) {
  await waitFor(() =>
    expect(
      screen.getByRole("heading", { name: "marketing-site" }),
    ).toBeInTheDocument(),
  );
  fireEvent.click(screen.getByRole("tab", { name }));
}

describe("AppDetailPage — delete app", () => {
  it("requires confirming the dialog before calling deleteApp", async () => {
    renderPage();
    await gotoTab("Settings");

    // Opening the Settings danger-zone Delete only opens the dialog; the
    // destructive call must not fire until the dialog is confirmed.
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(deleteApp).not.toHaveBeenCalled();

    // Cancelling dismisses the dialog without deleting (confirm gating).
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(deleteApp).not.toHaveBeenCalled();
  });

  it("deletes, invalidates the apps list, and routes back on confirm", async () => {
    renderPage();
    await gotoTab("Settings");

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete app" }));

    await waitFor(() => expect(deleteApp).toHaveBeenCalled());
    const [orgId, appId] = deleteApp.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");

    // The cached apps list is dropped so the removed app disappears at once.
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith("apps:org_1"));
    await waitFor(() => expect(push).toHaveBeenCalledWith("/dashboard/apps"));
  });

  it("surfaces the backend message and keeps the page on delete failure", async () => {
    deleteApp.mockRejectedValue(new ApiError("app has running machines", 409));
    renderPage();
    await gotoTab("Settings");

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete app" }));

    await waitFor(() => expect(deleteApp).toHaveBeenCalled());
    // Honest failure: the real backend reason is shown, no navigation/invalidate.
    await waitFor(() =>
      expect(screen.getByText(/app has running machines/)).toBeInTheDocument(),
    );
    expect(push).not.toHaveBeenCalled();
    expect(invalidate).not.toHaveBeenCalled();
  });
});

describe("AppDetailPage — environment variables", () => {
  it("sets a variable and refetches on success", async () => {
    listEnv.mockResolvedValueOnce({ data: [] as EnvVar[] });
    renderPage();
    await gotoTab("Environment");

    fireEvent.change(await screen.findByLabelText("Key"), {
      target: { value: "API_KEY" },
    });
    fireEvent.change(screen.getByLabelText("Value"), {
      target: { value: "secret-value" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save/i }));

    await waitFor(() => expect(setEnv).toHaveBeenCalled());
    const [orgId, appId, input] = setEnv.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(input).toEqual({
      key: "API_KEY",
      value: "secret-value",
      secret: false,
    });
    // A successful write refetches the list (listEnv called again).
    await waitFor(() => expect(listEnv.mock.calls.length).toBeGreaterThan(1));
  });

  it("shows the backend message when setting a variable fails", async () => {
    setEnv.mockRejectedValue(new ApiError("invalid variable name", 400));
    renderPage();
    await gotoTab("Environment");

    fireEvent.change(await screen.findByLabelText("Key"), {
      target: { value: "BAD KEY" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save/i }));

    await waitFor(() => expect(setEnv).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByText(/invalid variable name/)).toBeInTheDocument(),
    );
  });

  it("deletes a variable and refetches on success", async () => {
    renderPage();
    await gotoTab("Environment");

    await screen.findByText("API_KEY");
    fireEvent.click(screen.getByRole("button", { name: "Delete variable" }));

    await waitFor(() => expect(deleteEnv).toHaveBeenCalled());
    const [orgId, appId, key] = deleteEnv.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(key).toBe("API_KEY");
    await waitFor(() => expect(listEnv.mock.calls.length).toBeGreaterThan(1));
  });

  it("renders a Notice (not a silent empty) when the env fetch fails", async () => {
    listEnv.mockRejectedValue(new ApiError("forbidden", 403));
    renderPage();
    await gotoTab("Environment");

    // The fetch error must surface as an explicit notice with a retry, never a
    // success-looking empty state.
    await waitFor(() =>
      expect(
        screen.getByText(/Could not load environment variables/),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

describe("AppDetailPage — domains", () => {
  it("adds a domain and refetches on success", async () => {
    listDomains.mockResolvedValueOnce({ data: [] as Domain[] });
    renderPage();
    await gotoTab("Domains");

    fireEvent.change(await screen.findByLabelText("Domain"), {
      target: { value: "app.acme.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Add domain/i }));

    await waitFor(() => expect(addDomain).toHaveBeenCalled());
    const [orgId, appId, domain] = addDomain.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(domain).toBe("app.acme.com");
    await waitFor(() =>
      expect(listDomains.mock.calls.length).toBeGreaterThan(1),
    );
  });

  it("deletes a domain and refetches on success", async () => {
    renderPage();
    await gotoTab("Domains");

    await screen.findByText("app.acme.com");
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));

    await waitFor(() => expect(deleteDomain).toHaveBeenCalled());
    const [orgId, appId, domainId] = deleteDomain.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(domainId).toBe("dom_1");
    await waitFor(() =>
      expect(listDomains.mock.calls.length).toBeGreaterThan(1),
    );
  });

  it("shows the backend message when deleting a domain fails", async () => {
    deleteDomain.mockRejectedValue(new ApiError("domain is in use", 409));
    renderPage();
    await gotoTab("Domains");

    await screen.findByText("app.acme.com");
    fireEvent.click(screen.getByRole("button", { name: "Delete domain" }));

    await waitFor(() => expect(deleteDomain).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByText(/domain is in use/)).toBeInTheDocument(),
    );
  });

  it("renders a Notice (not a silent empty) when the domains fetch fails", async () => {
    listDomains.mockRejectedValue(new ApiError("forbidden", 403));
    renderPage();
    await gotoTab("Domains");

    await waitFor(() =>
      expect(screen.getByText(/Could not load domains/)).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

describe("AppDetailPage — releases & rollback", () => {
  it("rolls back to a prior revision after confirming", async () => {
    renderPage();
    await gotoTab("Releases");

    await screen.findByText("Revision 1");
    // The active release (rev 2) shows "Current"; the superseded one offers a
    // Rollback button.
    fireEvent.click(screen.getByRole("button", { name: "Rollback" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(rollbackApp).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Roll back" }));

    await waitFor(() => expect(rollbackApp).toHaveBeenCalled());
    const [orgId, appId, revision] = rollbackApp.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(revision).toBe(1);
  });
});

describe("AppDetailPage — builds", () => {
  it("lists builds and loads build logs on demand", async () => {
    renderPage();
    await gotoTab("Builds");

    await screen.findByText("abc123");
    fireEvent.click(screen.getByRole("button", { name: "View logs" }));

    await waitFor(() => expect(getBuild).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.getByText("build log output")).toBeInTheDocument(),
    );
    const [orgId, appId, buildId] = getBuild.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(buildId).toBe("bld_1");
  });
});

describe("AppDetailPage — settings update & scale", () => {
  it("PATCHes only the changed fields", async () => {
    renderPage();
    await gotoTab("Settings");

    fireEvent.change(await screen.findByLabelText("Image"), {
      target: { value: "registry/app:v9" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));

    await waitFor(() => expect(updateApp).toHaveBeenCalled());
    const [orgId, appId, input] = updateApp.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(input).toEqual({ image: "registry/app:v9" });
  });

  it("scales the app with the replica bounds", async () => {
    renderPage();
    await gotoTab("Settings");

    fireEvent.change(await screen.findByLabelText("Min replicas"), {
      target: { value: "2" },
    });
    fireEvent.change(screen.getByLabelText("Max replicas"), {
      target: { value: "5" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => expect(scaleApp).toHaveBeenCalled());
    const [, , input] = scaleApp.mock.calls[0];
    expect(input).toEqual({ minReplicas: 2, maxReplicas: 5 });
  });
});

describe("AppDetailPage — domain verify", () => {
  it("verifies a pending domain", async () => {
    listDomains.mockResolvedValue({
      data: [{ id: "dom_2", domain: "pending.acme.com", verified: false }],
    });
    renderPage();
    await gotoTab("Domains");

    await screen.findByText("pending.acme.com");
    fireEvent.click(screen.getByRole("button", { name: "Verify" }));

    await waitFor(() => expect(verifyDomain).toHaveBeenCalled());
    const [orgId, appId, domainId] = verifyDomain.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(appId).toBe("app_1");
    expect(domainId).toBe("dom_2");
  });
});

describe("AppDetailPage — metrics honest empty state", () => {
  it("shows 'Metrics unavailable' when the API reports available:false", async () => {
    getMetrics.mockResolvedValue({
      available: false,
      unavailable: "metrics-server not installed",
      pods: [],
      cpuMillicores: 0,
      memoryBytes: 0,
    });
    renderPage();
    await gotoTab("Metrics");

    await waitFor(() =>
      expect(screen.getByText(/Metrics unavailable/)).toBeInTheDocument(),
    );
    expect(
      screen.getByText(/metrics-server not installed/),
    ).toBeInTheDocument();
  });
});

describe("AppDetailPage — load failure (404 vs network)", () => {
  it("shows 'App not found.' for a 404 with no Retry", async () => {
    getApp.mockRejectedValue(new ApiError("not found", 404));
    renderPage();

    await waitFor(() =>
      expect(screen.getByText("App not found.")).toBeInTheDocument(),
    );
    // A genuine 404 is terminal — retrying would not help, so no Retry button.
    expect(
      screen.queryByRole("button", { name: "Retry" }),
    ).not.toBeInTheDocument();
  });

  it("shows 'API unreachable.' with Retry for a network failure", async () => {
    // A TypeError (no ApiError.status) models a dropped connection.
    getApp.mockRejectedValue(new TypeError("Failed to fetch"));
    renderPage();

    await waitFor(() =>
      expect(screen.getByText("API unreachable.")).toBeInTheDocument(),
    );
    // Transport failures are retryable, so the Retry affordance is present.
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

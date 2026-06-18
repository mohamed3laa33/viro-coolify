import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import {
  ApiError,
  type App,
  type Database,
  type Domain,
  type Project,
  type Template,
} from "@/lib/api";
import { __clearResourceCache } from "@/lib/use-resource";
import DatabasesPage from "@/app/dashboard/databases/page";
import DomainsPage from "@/app/dashboard/domains/page";
import ProjectsPage from "@/app/dashboard/projects/page";

// ---------------------------------------------------------------------------
// Shared mocks
//
// authedCall invokes the supplied fn with a fake token, a no-op onUnauthorized,
// and a forwarded AbortSignal (some pages call the 3-arg form). Demo mode is
// forced OFF so the pages exercise their real production empty/error paths
// instead of the mock fallback (invariant #6: no fabricated success).
// ---------------------------------------------------------------------------

const authedCall = vi.fn(
  <T,>(
    fn: (
      token: string,
      onUnauthorized: () => Promise<null>,
      signal?: AbortSignal,
    ) => Promise<T>,
    signal?: AbortSignal,
  ) => fn("tok", async () => null, signal),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));

vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

// API surface used across the three pages.
const listDatabases = vi.fn();
const deleteDatabase = vi.fn();
const getServiceCatalog = vi.fn();
const createDatabase = vi.fn();
const listApps = vi.fn();
const listDomains = vi.fn();
const addDomain = vi.fn();
const listProjects = vi.fn();
const deleteProject = vi.fn();
const listProjectApps = vi.fn();
const createProject = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listDatabases: (...a: unknown[]) => listDatabases(...a),
      deleteDatabase: (...a: unknown[]) => deleteDatabase(...a),
      getServiceCatalog: (...a: unknown[]) => getServiceCatalog(...a),
      createDatabase: (...a: unknown[]) => createDatabase(...a),
      listApps: (...a: unknown[]) => listApps(...a),
      listDomains: (...a: unknown[]) => listDomains(...a),
      addDomain: (...a: unknown[]) => addDomain(...a),
      listProjects: (...a: unknown[]) => listProjects(...a),
      deleteProject: (...a: unknown[]) => deleteProject(...a),
      listProjectApps: (...a: unknown[]) => listProjectApps(...a),
      createProject: (...a: unknown[]) => createProject(...a),
    },
  };
});

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const PG_TEMPLATE: Template = {
  key: "postgresql",
  name: "PostgreSQL",
  description: "Postgres",
  category: "Databases",
  kind: "database",
  image: "postgres:16",
  defaultPort: 5432,
  active: true,
  sortOrder: 0,
};

const DB: Database = {
  id: "db_1",
  orgId: "org_1",
  name: "primary-postgres",
  engine: "postgresql",
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

const APP: App = {
  id: "app_1",
  orgId: "org_1",
  projectId: "proj_1",
  name: "web",
  gitRepository: "github.com/acme/web",
  gitBranch: "main",
  buildPack: "nixpacks",
  cpu: 1,
  memoryMb: 512,
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

const DOMAIN: Domain = {
  id: "dom_1",
  domain: "app.example.com",
  verified: true,
};

const PROJECT: Project = {
  id: "proj_1",
  name: "Platform",
  slug: "platform",
  isDefault: false,
  createdAt: "2026-06-01T00:00:00Z",
};

beforeEach(() => {
  // Reset the module-level useResource cache so keyed fetches (e.g.
  // `databases:org_1`, `catalog`) don't bleed across tests.
  __clearResourceCache();

  authedCall.mockClear();

  listDatabases.mockReset().mockResolvedValue({ data: [DB] });
  deleteDatabase.mockReset().mockResolvedValue(undefined);
  getServiceCatalog.mockReset().mockResolvedValue({ data: [PG_TEMPLATE] });
  createDatabase.mockReset().mockResolvedValue(DB);

  listApps.mockReset().mockResolvedValue({ data: [APP] });
  listDomains.mockReset().mockResolvedValue({ data: [DOMAIN] });
  addDomain.mockReset().mockResolvedValue(DOMAIN);

  listProjects.mockReset().mockResolvedValue({ data: [PROJECT] });
  deleteProject.mockReset().mockResolvedValue(undefined);
  listProjectApps.mockReset().mockResolvedValue({ data: [] as App[] });
  createProject.mockReset().mockResolvedValue(PROJECT);
});

afterEach(() => cleanup());

// ---------------------------------------------------------------------------
// Databases
// ---------------------------------------------------------------------------

describe("DatabasesPage", () => {
  it("deletes a database via the shared ConfirmDialog", async () => {
    render(<DatabasesPage />);

    // Wait for the row to render from the API.
    await waitFor(() =>
      expect(screen.getByText("primary-postgres")).toBeInTheDocument(),
    );

    // Open the confirmation dialog from the row's delete button.
    fireEvent.click(
      screen.getByRole("button", { name: "Delete primary-postgres" }),
    );

    // The shared ConfirmDialog renders as an alertdialog (not a window.confirm).
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText("Delete primary-postgres?")).toBeInTheDocument();

    // Confirm the destructive action.
    fireEvent.click(screen.getByRole("button", { name: "Delete database" }));

    await waitFor(() => expect(deleteDatabase).toHaveBeenCalled());
    const [orgId, dbId] = deleteDatabase.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(dbId).toBe("db_1");
  });

  it("surfaces the backend message when delete fails (no fake success)", async () => {
    deleteDatabase.mockRejectedValue(
      new ApiError("Database has dependent resources", 409),
    );
    render(<DatabasesPage />);

    await waitFor(() =>
      expect(screen.getByText("primary-postgres")).toBeInTheDocument(),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Delete primary-postgres" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Delete database" }),
    );

    // The real ApiError message is shown verbatim — not a generic placeholder.
    await waitFor(() =>
      expect(
        screen.getByText("Database has dependent resources"),
      ).toBeInTheDocument(),
    );
  });

  it("disables create + hides any hardcoded engine when the catalog is empty", async () => {
    // Empty catalog: invariant #1 means we must NOT fall back to a hardcoded
    // engine; the form is disabled and an info notice explains why.
    getServiceCatalog.mockResolvedValue({ data: [] as Template[] });
    listDatabases.mockResolvedValue({ data: [] as Database[] });
    render(<DatabasesPage />);

    // Open the create form (header CTA — first match).
    fireEvent.click(
      screen.getAllByRole("button", { name: /Create database/i })[0],
    );

    // Info notice replaces the engine list; no fabricated engine offered.
    expect(
      await screen.findByText(
        /No database engines are available in the catalog yet/i,
      ),
    ).toBeInTheDocument();

    // The engine select shows the empty placeholder and is disabled — there is
    // no hardcoded "postgresql"/"mysql"/etc. option to submit.
    const engineSelect = screen.getByLabelText("Engine") as HTMLSelectElement;
    expect(engineSelect).toBeDisabled();
    expect(
      screen.getByRole("option", { name: "No engines available" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "PostgreSQL" }),
    ).not.toBeInTheDocument();

    // The submit button is disabled, so an empty catalog cannot create a db.
    const submit = screen.getByRole("button", { name: /^Create$/i });
    expect(submit).toBeDisabled();
    fireEvent.click(submit);
    expect(createDatabase).not.toHaveBeenCalled();
  });

  it("shows an error notice with Retry on an outage", async () => {
    listDatabases.mockRejectedValue(new Error("network down"));
    render(<DatabasesPage />);

    await waitFor(() =>
      expect(screen.getByText(/Could not load databases/i)).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Domains
// ---------------------------------------------------------------------------

describe("DomainsPage", () => {
  it("renders domains loaded from the API", async () => {
    render(<DomainsPage />);
    await waitFor(() =>
      expect(screen.getByText("app.example.com")).toBeInTheDocument(),
    );
  });

  it("surfaces an error with Retry on an apps-list outage (not a silent empty state)", async () => {
    listApps.mockRejectedValue(new ApiError("upstream unavailable", 503));
    render(<DomainsPage />);

    // The outage shows an error notice — including the HTTP status — and a
    // Retry control, rather than the "No custom domains yet" empty copy.
    await waitFor(() =>
      expect(screen.getByText(/Couldn.t load domains/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/HTTP 503/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(
      screen.queryByText(/No custom domains yet/i),
    ).not.toBeInTheDocument();
  });

  it("re-runs the resource fetch when Retry is clicked (not a dead-end notice)", async () => {
    listApps.mockRejectedValue(new ApiError("upstream unavailable", 503));
    render(<DomainsPage />);

    const retry = await screen.findByRole("button", { name: "Retry" });

    // The Retry control is wired to a real refetch — clicking it drives a fresh
    // fetch attempt (via authedCall) rather than leaving the user stranded.
    const before = authedCall.mock.calls.length;
    fireEvent.click(retry);

    await waitFor(() =>
      expect(authedCall.mock.calls.length).toBeGreaterThan(before),
    );
  });
});

// ---------------------------------------------------------------------------
// Projects
// ---------------------------------------------------------------------------

describe("ProjectsPage", () => {
  it("deletes a project via the shared ConfirmDialog", async () => {
    render(<ProjectsPage />);

    await waitFor(() =>
      expect(screen.getByText("Platform")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete Platform" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText("Delete Platform?")).toBeInTheDocument();

    // Confirm (the dialog's destructive "Delete" button).
    const confirmBtn = screen
      .getAllByRole("button", { name: /^Delete$/i })
      .find((b) => b.closest('[role="alertdialog"]') !== null)!;
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(deleteProject).toHaveBeenCalled());
    const [orgId, projectId] = deleteProject.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(projectId).toBe("proj_1");
  });

  it("surfaces the backend message when delete fails", async () => {
    deleteProject.mockRejectedValue(
      new ApiError("Project must be empty before deletion", 409),
    );
    render(<ProjectsPage />);

    await waitFor(() =>
      expect(screen.getByText("Platform")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete Platform" }));
    const confirmBtn = screen
      .getAllByRole("button", { name: /^Delete$/i })
      .find((b) => b.closest('[role="alertdialog"]') !== null)!;
    fireEvent.click(confirmBtn);

    await waitFor(() =>
      expect(
        screen.getByText("Project must be empty before deletion"),
      ).toBeInTheDocument(),
    );
  });

  it("surfaces a load error with Retry on an outage", async () => {
    listProjects.mockRejectedValue(new ApiError("projects service down", 502));
    render(<ProjectsPage />);

    // The real ApiError message is surfaced (not a generic placeholder), with
    // a Retry control rather than a silent empty state.
    await waitFor(() =>
      expect(screen.getByText("projects service down")).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText(/No projects yet/i)).not.toBeInTheDocument();
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import type { Database, DatabaseDetail } from "@/lib/api";
import DatabasesPage from "@/app/dashboard/databases/page";

// authedCall invokes the supplied fn with a fake token + no-op onUnauthorized.
const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));

// Production-shaped: no demo fallbacks so empty states are real.
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));
vi.mock("@/lib/demo-data", () => ({
  demoEnabled: () => false,
  useDemoData: <T,>(_select: unknown, empty: T) => empty,
}));

const listDatabases = vi.fn();
const getServiceCatalog = vi.fn();
const getDatabase = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listDatabases: (...a: unknown[]) => listDatabases(...a),
      getServiceCatalog: (...a: unknown[]) => getServiceCatalog(...a),
      getDatabase: (...a: unknown[]) => getDatabase(...a),
      deployDatabase: vi.fn(),
      stopDatabase: vi.fn(),
      restartDatabase: vi.fn(),
      deleteDatabase: vi.fn(),
    },
  };
});

const DB: Database = {
  id: "db_1",
  orgId: "org_1",
  name: "primary-postgres",
  engine: "postgresql",
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

const DETAIL: DatabaseDetail = {
  ...DB,
  connection: {
    host: "db_1.org-ns.svc.cluster.local",
    port: 5432,
    database: "appdb",
    username: "appuser",
    password: "super-secret-pw",
    connectionString:
      "postgres://appuser:super-secret-pw@db_1.org-ns.svc.cluster.local:5432/appdb",
  },
};

beforeEach(() => {
  authedCall.mockClear();
  listDatabases.mockReset().mockResolvedValue({ data: [DB] });
  getServiceCatalog.mockReset().mockResolvedValue({ data: [] });
  getDatabase.mockReset().mockResolvedValue(DETAIL);
});

afterEach(() => cleanup());

describe("Databases — connection info", () => {
  it("loads and renders connection info with a masked password and reveal", async () => {
    render(<DatabasesPage />);

    await screen.findByText("primary-postgres");
    fireEvent.click(
      screen.getByRole("button", {
        name: "Connection info for primary-postgres",
      }),
    );

    // Host/username come through verbatim.
    await waitFor(() =>
      expect(
        screen.getByText("db_1.org-ns.svc.cluster.local"),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("appuser")).toBeInTheDocument();

    // The password is masked until revealed.
    expect(screen.queryByText("super-secret-pw")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Reveal password" }));
    await waitFor(() =>
      expect(screen.getByText("super-secret-pw")).toBeInTheDocument(),
    );

    // getDatabase was called for the correct org/db.
    const [orgId, dbId] = getDatabase.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(dbId).toBe("db_1");
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import type { Database, Template } from "@/lib/api";
import DatabasesPage from "@/app/dashboard/databases/page";

const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

const listDatabases = vi.fn();
const getServiceCatalog = vi.fn();
const createDatabase = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listDatabases: (...a: unknown[]) => listDatabases(...a),
      getServiceCatalog: (...a: unknown[]) => getServiceCatalog(...a),
      createDatabase: (...a: unknown[]) => createDatabase(...a),
    },
  };
});

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

const NEW_DB: Database = {
  id: "db_new",
  orgId: "org_1",
  name: "primary-postgres",
  engine: "postgresql",
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

beforeEach(() => {
  authedCall.mockClear();
  listDatabases.mockReset().mockResolvedValue({ data: [] as Database[] });
  getServiceCatalog.mockReset().mockResolvedValue({ data: [PG_TEMPLATE] });
  createDatabase.mockReset().mockResolvedValue(NEW_DB);
});

afterEach(() => cleanup());

describe("Create database form", () => {
  it("creates a database with the entered name + engine", async () => {
    render(<DatabasesPage />);
    await waitFor(() => expect(getServiceCatalog).toHaveBeenCalled());

    // Open the create form via the header CTA (the empty state shows a second
    // "Create database" button, so target the first match).
    fireEvent.click(
      screen.getAllByRole("button", { name: /Create database/i })[0],
    );

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "primary-postgres" },
    });

    // Submit (the form's Create button).
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));

    await waitFor(() => expect(createDatabase).toHaveBeenCalled());
    const [orgId, input] = createDatabase.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(input).toMatchObject({
      name: "primary-postgres",
      engine: "postgresql",
    });
  });
});

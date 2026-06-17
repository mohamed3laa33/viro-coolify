import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import type { Service, Template, Project } from "@/lib/api";
import ServicesPage from "@/app/dashboard/services/page";

// authedCall invokes the supplied fn with a fake token + no-op onUnauthorized.
const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));

// Force production-shaped behavior (no demo fallbacks) so empty/error states
// are exercised deterministically.
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

const listServices = vi.fn();
const getServiceCatalog = vi.fn();
const listProjects = vi.fn();
const createService = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listServices: (...a: unknown[]) => listServices(...a),
      getServiceCatalog: (...a: unknown[]) => getServiceCatalog(...a),
      listProjects: (...a: unknown[]) => listProjects(...a),
      createService: (...a: unknown[]) => createService(...a),
    },
  };
});

const SVC: Service = {
  id: "svc_1",
  orgId: "org_1",
  projectId: "proj_1",
  template: "wordpress",
  name: "my-blog",
  cpu: 1,
  memoryMb: 512,
  status: "running",
  createdAt: "2026-06-01T00:00:00Z",
};

const TEMPLATE: Template = {
  key: "wordpress",
  name: "WordPress",
  description: "Blog",
  category: "Apps",
  kind: "app",
  image: "wordpress:6",
  defaultPort: 80,
  active: true,
  sortOrder: 0,
};

const PROJECT: Project = {
  id: "proj_1",
  name: "Default",
  slug: "default",
  isDefault: true,
  createdAt: "2026-01-01T00:00:00Z",
};

beforeEach(() => {
  authedCall.mockClear();
  listServices.mockReset().mockResolvedValue({ data: [SVC] });
  getServiceCatalog.mockReset().mockResolvedValue({ data: [TEMPLATE] });
  listProjects.mockReset().mockResolvedValue({ data: [PROJECT] });
  createService.mockReset().mockResolvedValue(SVC);
});

afterEach(() => cleanup());

describe("ServicesPage rendering", () => {
  it("renders the services table from the API", async () => {
    render(<ServicesPage />);
    expect(
      screen.getByRole("heading", { name: "Services" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText("my-blog")).toBeInTheDocument(),
    );
    // Template badge + status are shown for the row.
    expect(screen.getByText("wordpress")).toBeInTheDocument();
  });

  it("shows the empty state when there are no services", async () => {
    listServices.mockResolvedValue({ data: [] as Service[] });
    render(<ServicesPage />);
    await waitFor(() =>
      expect(screen.getByText("No services yet")).toBeInTheDocument(),
    );
  });

  it("shows an error notice with retry when the fetch fails", async () => {
    listServices.mockRejectedValue(new Error("nope"));
    render(<ServicesPage />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument(),
    );
  });
});

describe("Launch service form", () => {
  it("creates a service with the selected template + project", async () => {
    listServices.mockResolvedValue({ data: [] as Service[] });
    render(<ServicesPage />);

    // Open the create form.
    fireEvent.click(screen.getByRole("button", { name: /Launch service/i }));

    // Wait for the catalog + projects to populate the selects.
    await waitFor(() => expect(getServiceCatalog).toHaveBeenCalled());
    await waitFor(() => expect(listProjects).toHaveBeenCalled());

    const nameInput = await screen.findByLabelText("Name");
    fireEvent.change(nameInput, { target: { value: "my-new-svc" } });

    // Submit (there are two "Launch service" buttons — the form's is type=submit).
    const submitBtn = screen
      .getAllByRole("button", { name: /Launch service/i })
      .find((b) => (b as HTMLButtonElement).type === "submit")!;
    fireEvent.click(submitBtn);

    await waitFor(() => expect(createService).toHaveBeenCalled());
    const [orgId, projectId, input] = createService.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(projectId).toBe("proj_1");
    expect(input).toMatchObject({
      templateKey: "wordpress",
      name: "my-new-svc",
    });
  });
});

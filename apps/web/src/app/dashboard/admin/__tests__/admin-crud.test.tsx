import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import { ApiError } from "@/lib/api";
import type { AdminPlan, Template, PricingComponent } from "@/lib/api";
import AdminPlansPage from "@/app/dashboard/admin/plans/page";
import AdminCatalogPage from "@/app/dashboard/admin/catalog/page";
import AdminPricingPage from "@/app/dashboard/admin/pricing/page";

// authedCall invokes the supplied fn with a fake token + no-op onUnauthorized,
// mirroring the production caller so the page's api calls flow through.
const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));

// Force production-shaped behavior: isDemoMode() === false means the "Showing
// demo data" warning must stay hidden even when a fetch fails — the page shows
// an honest outage Notice instead (invariant #6).
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

// Keep the demo-data hook out of the picture (no dynamic mock.ts import in
// jsdom): callers get the same empty fallback they'd get in production. We hold
// the fallback in `useState` so its identity stays stable across renders —
// matching the real hook — otherwise useResource's `[demoData]` dep would churn
// and re-run the fetch every render. The `select` arg is intentionally unused.
vi.mock("@/lib/demo-data", async () => {
  const { useState } = await import("react");
  return {
    useDemoData: <T,>(select: (m: unknown) => T, empty: T): T => {
      void select;
      const [value] = useState(empty);
      return value;
    },
  };
});

const listAdminPlans = vi.fn();
const deletePlan = vi.fn();
const listTemplates = vi.fn();
const deleteTemplate = vi.fn();
const listPricing = vi.fn();
const deletePricing = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listAdminPlans: (...a: unknown[]) => listAdminPlans(...a),
      deletePlan: (...a: unknown[]) => deletePlan(...a),
      listTemplates: (...a: unknown[]) => listTemplates(...a),
      deleteTemplate: (...a: unknown[]) => deleteTemplate(...a),
      listPricing: (...a: unknown[]) => listPricing(...a),
      deletePricing: (...a: unknown[]) => deletePricing(...a),
    },
  };
});

const PLAN: AdminPlan = {
  id: "hobby",
  name: "Hobby",
  description: "Starter plan",
  priceCents: 0,
  currency: "usd",
  includedHours: 100,
  overagePerHourCents: 2,
  maxCpu: 1,
  maxMemoryMb: 512,
  maxApps: 3,
  isDefault: true,
  sortOrder: 0,
  active: true,
  stripePriceId: "price_hobby",
};

const TEMPLATE: Template = {
  key: "postgresql",
  name: "PostgreSQL",
  description: "Postgres database",
  category: "Databases",
  kind: "database",
  image: "postgres:16",
  defaultPort: 5432,
  active: true,
  sortOrder: 0,
};

const COMPONENT: PricingComponent = {
  key: "cpu",
  name: "CPU",
  unit: "core-hour",
  pricePerHour: 2,
  currency: "usd",
  active: true,
  sortOrder: 0,
};

beforeEach(() => {
  authedCall.mockClear();
  listAdminPlans.mockReset().mockResolvedValue({ data: [PLAN] });
  deletePlan.mockReset().mockResolvedValue(undefined);
  listTemplates.mockReset().mockResolvedValue({ data: [TEMPLATE] });
  deleteTemplate.mockReset().mockResolvedValue(undefined);
  listPricing.mockReset().mockResolvedValue({ data: [COMPONENT] });
  deletePricing.mockReset().mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("AdminPlansPage delete", () => {
  it("deletes a plan via the shared ConfirmDialog (no window.confirm)", async () => {
    const confirmSpy = vi.spyOn(window, "confirm");
    render(<AdminPlansPage />);
    await waitFor(() => expect(screen.getByText("Hobby")).toBeInTheDocument());

    // The row's trash button opens the dialog; deletePlan must not fire yet.
    fireEvent.click(screen.getByRole("button", { name: "Delete Hobby" }));
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(deletePlan).not.toHaveBeenCalled();

    // The shared alertdialog is rendered.
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/Delete plan/);

    // Confirming routes through the api with the plan id + token.
    fireEvent.click(screen.getByRole("button", { name: "Delete plan" }));
    await waitFor(() => expect(deletePlan).toHaveBeenCalled());
    expect(deletePlan.mock.calls[0][0]).toBe("hobby");
    confirmSpy.mockRestore();
  });

  it("hides the demo warning and shows an honest outage notice when the fetch fails", async () => {
    listAdminPlans.mockRejectedValue(new ApiError("plans exploded", 500));
    render(<AdminPlansPage />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument(),
    );
    // Honest backend message, not a generic placeholder.
    expect(screen.getByText("plans exploded")).toBeInTheDocument();
    // The "Showing demo data" warning must NOT render with isDemoMode() false.
    expect(screen.queryByText(/Showing demo data/i)).not.toBeInTheDocument();
  });
});

describe("AdminCatalogPage delete", () => {
  it("deletes a template via the shared ConfirmDialog (no window.confirm)", async () => {
    const confirmSpy = vi.spyOn(window, "confirm");
    render(<AdminCatalogPage />);
    await waitFor(() =>
      expect(screen.getByText("PostgreSQL")).toBeInTheDocument(),
    );

    // The template row exposes a single trash button.
    const trashButtons = screen.getAllByRole("button");
    const deleteBtn = trashButtons.find((b) =>
      b.querySelector("svg.text-destructive"),
    );
    expect(deleteBtn).toBeDefined();
    fireEvent.click(deleteBtn!);
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(deleteTemplate).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/Delete template/);

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(deleteTemplate).toHaveBeenCalled());
    expect(deleteTemplate.mock.calls[0][0]).toBe("postgresql");
    confirmSpy.mockRestore();
  });

  it("hides the demo warning and shows an honest outage notice when the fetch fails", async () => {
    listTemplates.mockRejectedValue(new ApiError("templates exploded", 503));
    render(<AdminCatalogPage />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument(),
    );
    expect(screen.getByText("templates exploded")).toBeInTheDocument();
    expect(screen.queryByText(/Showing demo data/i)).not.toBeInTheDocument();
  });
});

describe("AdminPricingPage delete", () => {
  it("deletes a pricing component via the shared ConfirmDialog (no window.confirm)", async () => {
    const confirmSpy = vi.spyOn(window, "confirm");
    render(<AdminPricingPage />);
    await waitFor(() => expect(screen.getByText("CPU")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Delete CPU" }));
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(deletePricing).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/Delete CPU\?/);

    fireEvent.click(screen.getByRole("button", { name: "Delete component" }));
    await waitFor(() => expect(deletePricing).toHaveBeenCalled());
    expect(deletePricing.mock.calls[0][0]).toBe("cpu");
    confirmSpy.mockRestore();
  });

  it("hides the demo warning when the fetch fails with isDemoMode() false", async () => {
    // The pricing page has no inline outage Notice; the key contract is that the
    // demo-data warning stays hidden in production even on a failed fetch.
    listPricing.mockRejectedValue(new ApiError("pricing exploded", 500));
    render(<AdminPricingPage />);

    await waitFor(() =>
      expect(
        screen.getByText("No pricing components yet."),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText(/Showing demo data/i)).not.toBeInTheDocument();
  });
});

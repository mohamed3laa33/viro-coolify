import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
} from "@testing-library/react";
import type { App } from "@/lib/api";
import AppsPage from "@/app/dashboard/apps/page";

const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);
const push = vi.fn();

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ activeOrgId: "org_1", authedCall }),
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push }),
}));
vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));

const listApps = vi.fn();
const createApp = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listApps: (...a: unknown[]) => listApps(...a),
      createApp: (...a: unknown[]) => createApp(...a),
    },
  };
});

const NEW_APP: App = {
  id: "app_new",
  orgId: "org_1",
  projectId: "proj_1",
  name: "marketing-site",
  gitRepository: "github.com/acme/marketing",
  gitBranch: "main",
  buildPack: "nixpacks",
  cpu: 1,
  memoryMb: 512,
  status: "created",
  createdAt: "2026-06-01T00:00:00Z",
};

beforeEach(() => {
  authedCall.mockClear();
  push.mockClear();
  listApps.mockReset().mockResolvedValue({ data: [] as App[] });
  createApp.mockReset().mockResolvedValue(NEW_APP);
});

afterEach(() => cleanup());

describe("CreateAppForm", () => {
  it("submits the form and routes to the new app on success", async () => {
    render(<AppsPage />);
    await waitFor(() => expect(listApps).toHaveBeenCalled());

    fireEvent.click(screen.getByRole("button", { name: /New App/i }));

    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "marketing-site" },
    });
    fireEvent.change(screen.getByLabelText("Git repository"), {
      target: { value: "github.com/acme/marketing" },
    });

    fireEvent.click(screen.getByRole("button", { name: /Create app/i }));

    await waitFor(() => expect(createApp).toHaveBeenCalled());
    const [orgId, input] = createApp.mock.calls[0];
    expect(orgId).toBe("org_1");
    expect(input).toMatchObject({
      name: "marketing-site",
      gitRepository: "github.com/acme/marketing",
      gitBranch: "main",
      buildPack: "nixpacks",
    });
    await waitFor(() =>
      expect(push).toHaveBeenCalledWith("/dashboard/apps/app_new"),
    );
  });

  it("does not submit an empty name", async () => {
    render(<AppsPage />);
    await waitFor(() => expect(listApps).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: /New App/i }));

    // Submitting with a blank name is a no-op (required + trimmed guard).
    fireEvent.click(screen.getByRole("button", { name: /Create app/i }));
    expect(createApp).not.toHaveBeenCalled();
  });
});

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
  within,
} from "@testing-library/react";
import type { ApiToken, AuditEvent, CreatedApiToken } from "@/lib/api";
import { __clearResourceCache } from "@/lib/use-resource";
import SettingsPage from "@/app/dashboard/settings/page";

const authedCall = vi.fn(
  <T,>(fn: (t: string, on: () => Promise<null>) => Promise<T>) =>
    fn("tok", async () => null),
);

vi.mock("@/lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u_1", email: "owner@acme.dev", isAdmin: false },
    orgs: [{ id: "org_1", name: "Acme", slug: "acme" }],
    activeOrgId: "org_1",
    authedCall,
  }),
}));

vi.mock("@/lib/demo", () => ({ isDemoMode: () => false }));
vi.mock("@/lib/demo-data", async () => {
  const { useRef } = await import("react");
  return {
    useDemoData: <T,>(_select: unknown, empty: T): T => {
      const ref = useRef(empty);
      return ref.current;
    },
    demoEnabled: () => false,
  };
});

const listTokens = vi.fn();
const createToken = vi.fn();
const revokeToken = vi.fn();
const listOrgAudit = vi.fn();
const listAdminAudit = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      listTokens: (...a: unknown[]) => listTokens(...a),
      createToken: (...a: unknown[]) => createToken(...a),
      revokeToken: (...a: unknown[]) => revokeToken(...a),
      listOrgAudit: (...a: unknown[]) => listOrgAudit(...a),
      listAdminAudit: (...a: unknown[]) => listAdminAudit(...a),
    },
  };
});

const TOKEN: ApiToken = {
  id: "t_1",
  name: "ci-deploy",
  prefix: "vrt_ab12",
  scopes: [],
  createdAt: "2026-06-01T00:00:00Z",
};

const CREATED: CreatedApiToken = {
  ...TOKEN,
  token: "vrt_abcdef0123456789ONCE",
};

const AUDIT: AuditEvent = {
  id: "a_1",
  actorEmail: "owner@acme.dev",
  action: "secret.set",
  targetType: "app_env",
  targetId: "app_1/API_KEY",
  at: "2026-06-10T12:00:00Z",
};

beforeEach(() => {
  __clearResourceCache();
  authedCall.mockClear();
  listTokens.mockReset().mockResolvedValue({ data: [TOKEN] });
  createToken.mockReset().mockResolvedValue(CREATED);
  revokeToken.mockReset().mockResolvedValue(undefined);
  listOrgAudit.mockReset().mockResolvedValue({
    data: [AUDIT],
    page: { limit: 25, offset: 0, hasMore: false },
  });
  listAdminAudit.mockReset().mockResolvedValue({
    data: [],
    page: { limit: 25, offset: 0, hasMore: false },
  });
});

afterEach(() => cleanup());

describe("Settings — API Tokens", () => {
  it("shows the plaintext token ONCE after creation with a copy button", async () => {
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("tab", { name: "API Tokens" }));

    // The existing token lists by prefix, never a secret.
    await screen.findByText(/vrt_ab12/);

    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "ci-deploy" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create token/i }));

    await waitFor(() => expect(createToken).toHaveBeenCalled());
    const [input] = createToken.mock.calls[0];
    expect(input.name).toBe("ci-deploy");

    // The one-time plaintext token is shown with a copy + warning.
    await waitFor(() =>
      expect(screen.getByText("vrt_abcdef0123456789ONCE")).toBeInTheDocument(),
    );
    expect(screen.getByText(/only time the token/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
  });

  it("revokes a token after confirming the dialog", async () => {
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("tab", { name: "API Tokens" }));

    await screen.findByText("ci-deploy");
    fireEvent.click(screen.getByRole("button", { name: "Revoke ci-deploy" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(revokeToken).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokeToken).toHaveBeenCalled());
    expect(revokeToken.mock.calls[0][0]).toBe("t_1");
  });
});

describe("Settings — Audit", () => {
  it("renders org audit rows (actor/action/target)", async () => {
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("tab", { name: "Audit" }));

    await waitFor(() => expect(listOrgAudit).toHaveBeenCalled());
    expect(await screen.findByText("secret.set")).toBeInTheDocument();
    expect(screen.getByText("owner@acme.dev")).toBeInTheDocument();
    expect(screen.getByText("app_env/app_1/API_KEY")).toBeInTheDocument();

    // Pagination params are respected (limit/offset).
    const opts = listOrgAudit.mock.calls[0].at(-1);
    expect(opts).toMatchObject({ limit: 25, offset: 0 });
  });
});

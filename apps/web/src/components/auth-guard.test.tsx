import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";
import { AuthGuard } from "@/components/auth-guard";
import { AdminGuard } from "@/components/admin-guard";
import { AuthProvider } from "@/lib/auth";
import { ApiError } from "@/lib/api";

// Shared router + auth mocks. Each test sets the auth state it needs.
const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace }),
}));

// The default (mocked) auth state used by the unit tests below. The integration
// test flips `useRealAuth` so the guard runs against the REAL AuthProvider/
// useAuth context instead of this canned state.
let authState: {
  accessToken: string | null;
  user: { isAdmin?: boolean } | null;
  loading: boolean;
};
let useRealAuth = false;

// Mock @/lib/auth such that the unit tests get the canned `authState`, while the
// integration test (useRealAuth = true) delegates to the real useAuth — which
// reads the same AuthContext that the real AuthProvider (also exported here from
// the actual module) populates.
vi.mock("@/lib/auth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/auth")>();
  return {
    ...actual,
    useAuth: () => (useRealAuth ? actual.useAuth() : authState),
  };
});

// Stubs for the API calls the real AuthProvider drives on mount. Each test sets
// the implementations it needs; the unit tests never trigger these.
const me = vi.fn();
const refresh = vi.fn();
const listOrgs = vi.fn();
const logout = vi.fn();

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: {
      me: (...a: unknown[]) => me(...a),
      refresh: (...a: unknown[]) => refresh(...a),
      listOrgs: (...a: unknown[]) => listOrgs(...a),
      logout: (...a: unknown[]) => logout(...a),
    },
  };
});

beforeEach(() => {
  replace.mockClear();
  useRealAuth = false;
  authState = { accessToken: null, user: null, loading: true };
  me.mockReset();
  refresh.mockReset();
  listOrgs.mockReset();
  logout.mockReset();
  // Default org fetch resolves empty so the mount path never hangs.
  listOrgs.mockResolvedValue({ data: [] });
  logout.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("AuthGuard", () => {
  it("renders a placeholder (not children) while auth is loading", () => {
    authState = { accessToken: null, user: null, loading: true };
    render(
      <AuthGuard>
        <div>protected</div>
      </AuthGuard>,
    );
    expect(screen.queryByText("protected")).not.toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });

  it("redirects to /login once hydrated with no token", async () => {
    authState = { accessToken: null, user: null, loading: false };
    render(
      <AuthGuard>
        <div>protected</div>
      </AuthGuard>,
    );
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/login"));
    expect(screen.queryByText("protected")).not.toBeInTheDocument();
  });

  it("renders children when authenticated", () => {
    authState = {
      accessToken: "tok",
      user: { isAdmin: false },
      loading: false,
    };
    render(
      <AuthGuard>
        <div>protected</div>
      </AuthGuard>,
    );
    expect(screen.getByText("protected")).toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });
});

// Integration tests drive the guard through the REAL AuthProvider so the
// spinner -> render and the 401 -> refresh-fail -> /login redirect paths are
// exercised end to end (api.me / api.refresh stubbed, not useAuth).
describe("AuthGuard (real AuthProvider)", () => {
  it("shows the spinner, then renders children once /v1/me resolves a session", async () => {
    useRealAuth = true;
    // Keep /v1/me pending so we can observe the loading placeholder first.
    let resolveMe: (u: unknown) => void = () => {};
    me.mockReturnValue(
      new Promise((res) => {
        resolveMe = res;
      }),
    );

    const { container } = render(
      <AuthProvider>
        <AuthGuard>
          <div>protected</div>
        </AuthGuard>
      </AuthProvider>,
    );

    // While /v1/me is in flight the guard renders the spinner placeholder
    // (Loader2 renders an svg with the spin animation).
    expect(container.querySelector(".animate-spin")).not.toBeNull();
    expect(screen.queryByText("protected")).not.toBeInTheDocument();

    // Resolve the session: the guard should swap to the protected children.
    resolveMe({ id: "u_1", email: "a@b.c", name: "A" });
    await waitFor(() =>
      expect(screen.getByText("protected")).toBeInTheDocument(),
    );
    expect(replace).not.toHaveBeenCalled();
  });

  it("redirects to /login when /v1/me 401s and the refresh fails", async () => {
    useRealAuth = true;
    // First /v1/me rejects (401), the cookie refresh also rejects -> no session.
    me.mockRejectedValue(new ApiError("unauthorized", 401));
    refresh.mockRejectedValue(new ApiError("unauthorized", 401));

    render(
      <AuthProvider>
        <AuthGuard>
          <div>protected</div>
        </AuthGuard>
      </AuthProvider>,
    );

    // Once the me -> refresh -> me recovery flow gives up, the guard redirects.
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/login"));
    expect(screen.queryByText("protected")).not.toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });
});

describe("AdminGuard", () => {
  it("redirects non-admins back to the dashboard", async () => {
    authState = {
      accessToken: "tok",
      user: { isAdmin: false },
      loading: false,
    };
    render(
      <AdminGuard>
        <div>admin-only</div>
      </AdminGuard>,
    );
    await waitFor(() => expect(replace).toHaveBeenCalledWith("/dashboard"));
    expect(screen.queryByText("admin-only")).not.toBeInTheDocument();
  });

  it("does not redirect while loading", () => {
    authState = { accessToken: "tok", user: null, loading: true };
    render(
      <AdminGuard>
        <div>admin-only</div>
      </AdminGuard>,
    );
    expect(replace).not.toHaveBeenCalled();
    expect(screen.queryByText("admin-only")).not.toBeInTheDocument();
  });

  it("renders children for an admin user", () => {
    authState = { accessToken: "tok", user: { isAdmin: true }, loading: false };
    render(
      <AdminGuard>
        <div>admin-only</div>
      </AdminGuard>,
    );
    expect(screen.getByText("admin-only")).toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });
});

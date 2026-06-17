import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { AuthGuard } from "@/components/auth-guard";
import { AdminGuard } from "@/components/admin-guard";

// Shared router + auth mocks. Each test sets the auth state it needs.
const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace }),
}));

let authState: {
  accessToken: string | null;
  user: { isAdmin?: boolean } | null;
  loading: boolean;
};

vi.mock("@/lib/auth", () => ({
  useAuth: () => authState,
}));

beforeEach(() => {
  replace.mockClear();
  authState = { accessToken: null, user: null, loading: true };
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

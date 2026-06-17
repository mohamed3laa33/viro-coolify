import {
  describe,
  it,
  expect,
  vi,
  beforeEach,
  afterEach,
  type Mock,
} from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import type { ReactNode } from "react";

// Mock the API client — we exercise the REAL AuthProvider against it. Never mock
// '@/lib/auth' itself; the provider's state machine is the unit under test.
vi.mock("@/lib/api", () => ({
  api: {
    me: vi.fn(),
    refresh: vi.fn(),
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    listOrgs: vi.fn(),
  },
}));

import { AuthProvider, useAuth } from "@/lib/auth";
import { api, type Org, type User, type AuthResponse } from "@/lib/api";

const ACTIVE_ORG_KEY = "viro.activeOrgId";

const mockedApi = api as unknown as {
  me: Mock;
  refresh: Mock;
  login: Mock;
  signup: Mock;
  logout: Mock;
  listOrgs: Mock;
};

const USER: User = { id: "u1", email: "a@b.c", name: "Ada" };

function org(id: string, name = id): Org {
  return { id, name, slug: name, createdAt: "2026-01-01T00:00:00Z" };
}

function authResponse(user: User = USER): AuthResponse {
  // accessToken/refreshToken are legacy fields; the provider ignores them (the
  // real tokens live in HttpOnly cookies) but the type requires them.
  return { user, accessToken: "x", refreshToken: "y" };
}

const wrapper = ({ children }: { children: ReactNode }) => (
  <AuthProvider>{children}</AuthProvider>
);

// A never-resolving promise: lets a test assert the in-flight/loading state
// before any mount-effect promise settles.
function pending<T>(): Promise<T> {
  return new Promise<T>(() => {});
}

beforeEach(() => {
  window.localStorage.clear();
  // Sensible defaults; individual tests override as needed.
  mockedApi.me.mockReset().mockResolvedValue(USER);
  mockedApi.refresh.mockReset().mockResolvedValue(authResponse());
  mockedApi.login.mockReset().mockResolvedValue(authResponse());
  mockedApi.signup.mockReset().mockResolvedValue(authResponse());
  mockedApi.logout.mockReset().mockResolvedValue(undefined);
  mockedApi.listOrgs.mockReset().mockResolvedValue({ data: [] });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("AuthProvider — mount session derivation", () => {
  it("me() success applies the user and loads orgs", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1"), org("o2")] });

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toEqual(USER);
    // accessToken is a presence sentinel, not a real token.
    expect(result.current.accessToken).toBe("session");
    expect(result.current.orgs).toEqual([org("o1"), org("o2")]);
    // First org becomes active when nothing is stored.
    expect(result.current.activeOrgId).toBe("o1");
    expect(window.localStorage.getItem(ACTIVE_ORG_KEY)).toBe("o1");

    expect(mockedApi.refresh).not.toHaveBeenCalled();
  });

  it("starts in the loading state until /v1/me settles", () => {
    mockedApi.me.mockReturnValue(pending<User>());
    mockedApi.listOrgs.mockReturnValue(pending<{ data: Org[] }>());

    const { result } = renderHook(() => useAuth(), { wrapper });

    expect(result.current.loading).toBe(true);
    expect(result.current.user).toBeNull();
  });

  it("fires me() and listOrgs() concurrently on mount", async () => {
    let resolveMe: (u: User) => void = () => {};
    let listOrgsStarted = false;
    mockedApi.me.mockImplementation(
      () =>
        new Promise<User>((resolve) => {
          resolveMe = resolve;
        }),
    );
    mockedApi.listOrgs.mockImplementation(async () => {
      listOrgsStarted = true;
      return { data: [org("o1")] };
    });

    const { result } = renderHook(() => useAuth(), { wrapper });

    // listOrgs must be in flight WITHOUT waiting for me() to resolve — they are
    // launched together via Promise.all, not serialized.
    await waitFor(() => expect(listOrgsStarted).toBe(true));
    expect(mockedApi.me).toHaveBeenCalledTimes(1);
    expect(result.current.loading).toBe(true);

    await act(async () => {
      resolveMe(USER);
      await Promise.resolve();
    });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toEqual(USER);
  });

  it("on me() 401 refreshes, retries me(), and recovers the session", async () => {
    mockedApi.me
      .mockRejectedValueOnce(new Error("401"))
      .mockResolvedValueOnce(USER);
    mockedApi.refresh.mockResolvedValue(authResponse());
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.user).toEqual(USER));
    expect(result.current.loading).toBe(false);
    expect(mockedApi.refresh).toHaveBeenCalledTimes(1);
    expect(mockedApi.me).toHaveBeenCalledTimes(2);
    // Orgs are re-fetched after the successful refresh.
    expect(result.current.orgs).toEqual([org("o1")]);
  });

  it("clears the session when refresh fails after a me() 401", async () => {
    mockedApi.me.mockRejectedValue(new Error("401"));
    mockedApi.refresh.mockRejectedValue(new Error("refresh failed"));

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toBeNull();
    expect(result.current.accessToken).toBeNull();
    expect(result.current.refreshToken).toBeNull();
    expect(mockedApi.refresh).toHaveBeenCalledTimes(1);
  });
});

describe("AuthProvider — loadOrgs active-org selection", () => {
  it("keeps a stored active-org that is still present in the list", async () => {
    window.localStorage.setItem(ACTIVE_ORG_KEY, "o2");
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1"), org("o2")] });

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.orgs).toHaveLength(2));
    expect(result.current.activeOrgId).toBe("o2");
  });

  it("discards a stored active-org missing from the returned list", async () => {
    window.localStorage.setItem(ACTIVE_ORG_KEY, "gone");
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1"), org("o2")] });

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.orgs).toHaveLength(2));
    // Falls back to the first org and rewrites storage.
    expect(result.current.activeOrgId).toBe("o1");
    expect(window.localStorage.getItem(ACTIVE_ORG_KEY)).toBe("o1");
  });

  it("leaves activeOrgId null when the org list is empty", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [] });

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.activeOrgId).toBeNull();
  });
});

describe("AuthProvider — login / signup", () => {
  it("login applies the user and loads orgs", async () => {
    // Mount as a signed-out session so login drives the transition.
    mockedApi.me.mockRejectedValue(new Error("401"));
    mockedApi.refresh.mockRejectedValueOnce(new Error("no session"));

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.user).toBeNull();

    mockedApi.login.mockResolvedValue(authResponse());
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    await act(async () => {
      await result.current.login({ email: "a@b.c", password: "pw" });
    });

    expect(mockedApi.login).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "pw",
    });
    expect(result.current.user).toEqual(USER);
    expect(result.current.accessToken).toBe("session");
    expect(result.current.orgs).toEqual([org("o1")]);
    expect(result.current.activeOrgId).toBe("o1");
  });

  it("signup applies the user and loads orgs", async () => {
    mockedApi.me.mockRejectedValue(new Error("401"));
    mockedApi.refresh.mockRejectedValueOnce(new Error("no session"));

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    mockedApi.signup.mockResolvedValue(authResponse());
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o9")] });

    await act(async () => {
      await result.current.signup({
        email: "a@b.c",
        name: "Ada",
        password: "pw",
      });
    });

    expect(mockedApi.signup).toHaveBeenCalledWith({
      email: "a@b.c",
      name: "Ada",
      password: "pw",
    });
    expect(result.current.user).toEqual(USER);
    expect(result.current.orgs).toEqual([org("o9")]);
    expect(result.current.activeOrgId).toBe("o9");
  });
});

describe("AuthProvider — logout", () => {
  it("calls api.logout and clears all session state", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.user).toEqual(USER));

    act(() => {
      result.current.logout();
    });

    expect(mockedApi.logout).toHaveBeenCalledTimes(1);
    expect(result.current.user).toBeNull();
    expect(result.current.accessToken).toBeNull();
    expect(result.current.orgs).toEqual([]);
    expect(result.current.activeOrgId).toBeNull();
  });

  it("does not throw when the best-effort api.logout rejects", async () => {
    mockedApi.logout.mockRejectedValue(new Error("network"));
    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.logout();
    });

    // The rejection is swallowed; the session is still cleared synchronously.
    expect(result.current.user).toBeNull();
    await Promise.resolve();
  });
});

describe("AuthProvider — single-flight refresh", () => {
  it("shares ONE api.refresh() across two concurrent onUnauthorized calls", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    let resolveRefresh: (r: AuthResponse) => void = () => {};
    mockedApi.refresh.mockReset().mockImplementation(
      () =>
        new Promise<AuthResponse>((resolve) => {
          resolveRefresh = resolve;
        }),
    );

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.user).toEqual(USER));

    // authedCall hands the live onUnauthorized hook to the callback; capture it
    // so we can drive the single-flight path directly.
    let onUnauthorized: () => Promise<string | null> = async () => null;
    await act(async () => {
      await result.current.authedCall(async (_token, unauthorized) => {
        onUnauthorized = unauthorized;
        return null;
      });
    });

    // Two concurrent 401s -> both await the SAME refresh promise.
    let firstResult: string | null = "unset";
    let secondResult: string | null = "unset";
    await act(async () => {
      const p1 = onUnauthorized().then((r) => {
        firstResult = r;
      });
      const p2 = onUnauthorized().then((r) => {
        secondResult = r;
      });
      // Only one refresh is dispatched while both are pending.
      expect(mockedApi.refresh).toHaveBeenCalledTimes(1);
      resolveRefresh(authResponse());
      await Promise.all([p1, p2]);
    });

    expect(mockedApi.refresh).toHaveBeenCalledTimes(1);
    expect(firstResult).toBe("session");
    expect(secondResult).toBe("session");
  });

  it("logs out and resolves null when the shared refresh fails", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.user).toEqual(USER));

    let onUnauthorized: () => Promise<string | null> = async () => null;
    await act(async () => {
      await result.current.authedCall(async (_token, unauthorized) => {
        onUnauthorized = unauthorized;
        return null;
      });
    });

    mockedApi.refresh.mockReset().mockRejectedValue(new Error("expired"));

    let outcome: string | null = "unset";
    await act(async () => {
      outcome = await onUnauthorized();
    });

    expect(outcome).toBeNull();
    expect(mockedApi.refresh).toHaveBeenCalledTimes(1);
    expect(mockedApi.logout).toHaveBeenCalledTimes(1);
    // A failed refresh clears the session.
    expect(result.current.user).toBeNull();
    expect(result.current.accessToken).toBeNull();
  });

  it("starts a fresh refresh after the previous single-flight settles", async () => {
    mockedApi.listOrgs.mockResolvedValue({ data: [org("o1")] });

    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.user).toEqual(USER));

    let onUnauthorized: () => Promise<string | null> = async () => null;
    await act(async () => {
      await result.current.authedCall(async (_token, unauthorized) => {
        onUnauthorized = unauthorized;
        return null;
      });
    });

    mockedApi.refresh.mockReset().mockResolvedValue(authResponse());

    await act(async () => {
      await onUnauthorized();
    });
    await act(async () => {
      await onUnauthorized();
    });

    // The in-flight ref is cleared after each settle, so a later 401 refreshes
    // again rather than reusing a stale resolved promise.
    expect(mockedApi.refresh).toHaveBeenCalledTimes(2);
  });
});

describe("useAuth — guard", () => {
  it("throws when used outside an AuthProvider", () => {
    // Suppress React's error-boundary console noise for the expected throw.
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => renderHook(() => useAuth())).toThrow(
      "useAuth must be used within an AuthProvider",
    );
    spy.mockRestore();
  });
});

"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  type AuthResponse,
  type LoginInput,
  type OnUnauthorized,
  type Org,
  type SignupInput,
  type User,
} from "@/lib/api";

// ---------------------------------------------------------------------------
// SECURITY TODO — token storage hardening (needs backend work)
//
// Tokens currently live in localStorage. This is convenient but NOT the
// long-term-correct place to keep them:
//
//   - localStorage is readable by ANY JavaScript on the origin, so a single
//     XSS payload (a bad dependency, a reflected string, a compromised CDN)
//     can exfiltrate both the access AND refresh token.
//   - The refresh token is valid for up to 30 days and there is no
//     server-side revocation today (see CLAUDE.md "Known gaps"), so a stolen
//     refresh token grants a 30-day foothold that we cannot cut off.
//
// The real fix is to have the API set HttpOnly + Secure + SameSite cookies on
// login/refresh so the tokens are never reachable from JS, and to add
// refresh-token rotation + revocation server-side. That is a backend change;
// until it lands we keep localStorage but route every read/write through the
// safeLocalStorage helper below and single-flight the refresh.
// TODO(backend): issue HttpOnly cookies + add refresh rotation/revocation,
// then delete the localStorage token paths here.
// ---------------------------------------------------------------------------

const ACCESS_KEY = "viro.accessToken";
const REFRESH_KEY = "viro.refreshToken";
const USER_KEY = "viro.user";
const ACTIVE_ORG_KEY = "viro.activeOrgId";

/**
 * SSR-safe localStorage wrapper. Replaces the repeated
 * `typeof window !== "undefined"` guards; every method is a no-op (or returns
 * null) when running on the server or when storage is unavailable.
 */
const safeLocalStorage = {
  get(key: string): string | null {
    if (typeof window === "undefined") return null;
    try {
      return window.localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string): void {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(key, value);
    } catch {
      // Storage full or disabled (private mode); ignore.
    }
  },
  remove(key: string): void {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.removeItem(key);
    } catch {
      // Ignore.
    }
  },
};

interface AuthState {
  user: User | null;
  accessToken: string | null;
  refreshToken: string | null;
  loading: boolean;
}

/**
 * A function that resolves the current (possibly refreshed) access token and
 * runs the supplied request. It wires up refresh-on-401 transparently: pass it
 * a callback that receives the token plus an onUnauthorized hook.
 *
 * An optional AbortSignal can be supplied; it is forwarded to the callback as a
 * third argument so callers (e.g. `useResource`) can cancel the underlying
 * request. The callback may ignore it for backwards compatibility.
 */
export type AuthedCaller = <T>(
  fn: (
    token: string,
    onUnauthorized: OnUnauthorized,
    signal?: AbortSignal,
  ) => Promise<T>,
  signal?: AbortSignal,
) => Promise<T>;

interface AuthContextValue extends AuthState {
  orgs: Org[];
  activeOrgId: string | null;
  setActiveOrgId: (id: string) => void;
  /** Run an API call with the current token and automatic refresh-on-401. */
  authedCall: AuthedCaller;
  login: (input: LoginInput) => Promise<void>;
  signup: (input: SignupInput) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined);

function readStored<T>(key: string): T | null {
  const raw = safeLocalStorage.get(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    user: null,
    accessToken: null,
    refreshToken: null,
    loading: true,
  });
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [activeOrgId, setActiveOrgIdState] = useState<string | null>(null);

  // Refs mirror the latest tokens so callbacks don't capture stale closures.
  const accessRef = useRef<string | null>(null);
  const refreshRef = useRef<string | null>(null);

  // Single-flight refresh: holds the in-flight api.refresh() promise so that
  // concurrent 401s await ONE network call instead of stampeding the endpoint
  // (and racing to overwrite each other's tokens).
  const refreshInFlight = useRef<Promise<string | null> | null>(null);

  const writeTokens = useCallback(
    (access: string | null, refresh: string | null) => {
      accessRef.current = access;
      refreshRef.current = refresh;
      if (access) safeLocalStorage.set(ACCESS_KEY, access);
      else safeLocalStorage.remove(ACCESS_KEY);
      if (refresh) safeLocalStorage.set(REFRESH_KEY, refresh);
      else safeLocalStorage.remove(REFRESH_KEY);
    },
    [],
  );

  const setActiveOrgId = useCallback((id: string) => {
    setActiveOrgIdState(id);
    safeLocalStorage.set(ACTIVE_ORG_KEY, id);
  }, []);

  const logout = useCallback(() => {
    // TODO(backend): no server-side revocation today — the refresh token stays
    // valid for its full 30-day lifetime even after this client-side logout.
    writeTokens(null, null);
    refreshInFlight.current = null;
    safeLocalStorage.remove(USER_KEY);
    safeLocalStorage.remove(ACTIVE_ORG_KEY);
    setOrgs([]);
    setActiveOrgIdState(null);
    setState({
      user: null,
      accessToken: null,
      refreshToken: null,
      loading: false,
    });
  }, [writeTokens]);

  // Refresh-on-401 hook: exchanges the stored refresh token for new tokens,
  // persists them, and returns the new access token (or null on failure).
  // Single-flighted: concurrent callers share one in-flight api.refresh().
  const onUnauthorized = useCallback<OnUnauthorized>(() => {
    const refreshToken = refreshRef.current;
    if (!refreshToken) return Promise.resolve(null);

    if (refreshInFlight.current) return refreshInFlight.current;

    const inFlight = (async () => {
      try {
        const res = await api.refresh(refreshToken);
        writeTokens(res.accessToken, res.refreshToken);
        safeLocalStorage.set(USER_KEY, JSON.stringify(res.user));
        setState((prev) => ({
          ...prev,
          user: res.user,
          accessToken: res.accessToken,
          refreshToken: res.refreshToken,
        }));
        return res.accessToken;
      } catch {
        // Refresh failed: tokens are no longer valid.
        logout();
        return null;
      } finally {
        refreshInFlight.current = null;
      }
    })();

    refreshInFlight.current = inFlight;
    return inFlight;
  }, [writeTokens, logout]);

  const authedCall = useCallback<AuthedCaller>(
    (fn, signal) => {
      const token = accessRef.current ?? "";
      return fn(token, onUnauthorized, signal);
    },
    [onUnauthorized],
  );

  // Load the user's orgs and pick a default active org.
  const loadOrgs = useCallback(async () => {
    try {
      const res = await api.listOrgs(accessRef.current ?? "", onUnauthorized);
      setOrgs(res.data);
      const stored = safeLocalStorage.get(ACTIVE_ORG_KEY);
      const valid =
        stored && res.data.some((o) => o.id === stored) ? stored : null;
      const next = valid ?? res.data[0]?.id ?? null;
      setActiveOrgIdState(next);
      if (next) {
        safeLocalStorage.set(ACTIVE_ORG_KEY, next);
      }
    } catch {
      // Leave orgs empty; the UI falls back to mock data.
    }
  }, [onUnauthorized]);

  const persist = useCallback(
    (res: AuthResponse) => {
      writeTokens(res.accessToken, res.refreshToken);
      safeLocalStorage.set(USER_KEY, JSON.stringify(res.user));
      setState({
        user: res.user,
        accessToken: res.accessToken,
        refreshToken: res.refreshToken,
        loading: false,
      });
    },
    [writeTokens],
  );

  // Hydrate from localStorage on mount, then load orgs if signed in.
  useEffect(() => {
    const accessToken = safeLocalStorage.get(ACCESS_KEY);
    const refreshToken = safeLocalStorage.get(REFRESH_KEY);
    const user = readStored<User>(USER_KEY);

    accessRef.current = accessToken;
    refreshRef.current = refreshToken;

    setState({
      user,
      accessToken,
      refreshToken,
      loading: false,
    });

    if (accessToken) {
      void loadOrgs();
      // Re-fetch the current user so fields like isAdmin stay fresh and are
      // available immediately after a hard reload.
      void api
        .me(accessToken, onUnauthorized)
        .then((freshUser) => {
          safeLocalStorage.set(USER_KEY, JSON.stringify(freshUser));
          setState((prev) => ({ ...prev, user: freshUser }));
        })
        .catch(() => {
          // Offline/unreachable: keep the hydrated user as-is.
        });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const login = useCallback(
    async (input: LoginInput) => {
      const res = await api.login(input);
      persist(res);
      await loadOrgs();
    },
    [persist, loadOrgs],
  );

  const signup = useCallback(
    async (input: SignupInput) => {
      const res = await api.signup(input);
      persist(res);
      await loadOrgs();
    },
    [persist, loadOrgs],
  );

  const value = useMemo<AuthContextValue>(
    () => ({
      ...state,
      orgs,
      activeOrgId,
      setActiveOrgId,
      authedCall,
      login,
      signup,
      logout,
    }),
    [
      state,
      orgs,
      activeOrgId,
      setActiveOrgId,
      authedCall,
      login,
      signup,
      logout,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return ctx;
}

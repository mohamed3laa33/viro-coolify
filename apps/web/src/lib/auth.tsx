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
  type LoginInput,
  type OnUnauthorized,
  type Org,
  type SignupInput,
  type User,
} from "@/lib/api";

// ---------------------------------------------------------------------------
// Cookie-based sessions.
//
// Auth tokens are issued by the API as HttpOnly + SameSite (Secure in prod)
// cookies and are NEVER reachable from JavaScript — so an XSS payload cannot
// exfiltrate them. The browser attaches them automatically (fetch uses
// `credentials: "include"`); the client holds NO tokens. The session is derived
// by calling `/v1/me`, refreshed via the rotating refresh cookie, and ended by
// `/v1/auth/logout` (which revokes the refresh token server-side).
//
// `accessToken` on the context is a presence sentinel ("session" when signed
// in, null otherwise) kept for the few call sites that use it as a boolean; it
// is not a real token. Only the active-org preference is persisted locally.
// ---------------------------------------------------------------------------

const SESSION = "session"; // non-secret presence sentinel for accessToken
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

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    user: null,
    accessToken: null,
    refreshToken: null,
    loading: true,
  });
  const [orgs, setOrgs] = useState<Org[]>([]);
  const [activeOrgId, setActiveOrgIdState] = useState<string | null>(null);

  // Single-flight refresh: concurrent 401s await ONE api.refresh() call (which
  // rotates the HttpOnly cookies) instead of stampeding the endpoint.
  const refreshInFlight = useRef<Promise<string | null> | null>(null);

  const setActiveOrgId = useCallback((id: string) => {
    setActiveOrgIdState(id);
    safeLocalStorage.set(ACTIVE_ORG_KEY, id);
  }, []);

  // applyUser marks the session as authenticated. accessToken is a non-secret
  // presence sentinel — the real token lives only in the HttpOnly cookie.
  const applyUser = useCallback((user: User) => {
    setState({
      user,
      accessToken: SESSION,
      refreshToken: null,
      loading: false,
    });
  }, []);

  const logout = useCallback(() => {
    refreshInFlight.current = null;
    setOrgs([]);
    setActiveOrgIdState(null);
    setState({
      user: null,
      accessToken: null,
      refreshToken: null,
      loading: false,
    });
    // Best-effort server-side revocation + cookie clear.
    void api.logout().catch(() => {});
  }, []);

  // Refresh-on-401 hook: rotates the session via the refresh cookie. Returns a
  // truthy sentinel so the caller retries (the refreshed cookie carries the new
  // session); null on failure. Single-flighted across concurrent 401s.
  const onUnauthorized = useCallback<OnUnauthorized>(() => {
    if (refreshInFlight.current) return refreshInFlight.current;

    const inFlight = (async () => {
      try {
        const res = await api.refresh();
        applyUser(res.user);
        return SESSION;
      } catch {
        logout();
        return null;
      } finally {
        refreshInFlight.current = null;
      }
    })();

    refreshInFlight.current = inFlight;
    return inFlight;
  }, [applyUser, logout]);

  const authedCall = useCallback<AuthedCaller>(
    (fn, signal) => fn("", onUnauthorized, signal),
    [onUnauthorized],
  );

  // Load the user's orgs and pick a default active org.
  const loadOrgs = useCallback(async () => {
    try {
      const res = await api.listOrgs("", onUnauthorized);
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
      // Leave orgs empty; the UI falls back to mock data (demo mode only).
    }
  }, [onUnauthorized]);

  // Derive the session from the cookie on mount: try /v1/me; if it 401s, try a
  // single cookie-based refresh and re-check before giving up.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const user = await api.me("");
        if (cancelled) return;
        applyUser(user);
        await loadOrgs();
      } catch {
        try {
          await api.refresh();
          const user = await api.me("");
          if (cancelled) return;
          applyUser(user);
          await loadOrgs();
        } catch {
          if (cancelled) return;
          setState({
            user: null,
            accessToken: null,
            refreshToken: null,
            loading: false,
          });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const login = useCallback(
    async (input: LoginInput) => {
      const res = await api.login(input);
      applyUser(res.user);
      await loadOrgs();
    },
    [applyUser, loadOrgs],
  );

  const signup = useCallback(
    async (input: SignupInput) => {
      const res = await api.signup(input);
      applyUser(res.user);
      await loadOrgs();
    },
    [applyUser, loadOrgs],
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

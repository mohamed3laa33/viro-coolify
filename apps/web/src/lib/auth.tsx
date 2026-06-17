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

const ACCESS_KEY = "viro.accessToken";
const REFRESH_KEY = "viro.refreshToken";
const USER_KEY = "viro.user";
const ACTIVE_ORG_KEY = "viro.activeOrgId";

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
 */
export type AuthedCaller = <T>(
  fn: (token: string, onUnauthorized: OnUnauthorized) => Promise<T>,
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
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(key);
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

  const writeTokens = useCallback(
    (access: string | null, refresh: string | null) => {
      accessRef.current = access;
      refreshRef.current = refresh;
      if (typeof window !== "undefined") {
        if (access) window.localStorage.setItem(ACCESS_KEY, access);
        else window.localStorage.removeItem(ACCESS_KEY);
        if (refresh) window.localStorage.setItem(REFRESH_KEY, refresh);
        else window.localStorage.removeItem(REFRESH_KEY);
      }
    },
    [],
  );

  const setActiveOrgId = useCallback((id: string) => {
    setActiveOrgIdState(id);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(ACTIVE_ORG_KEY, id);
    }
  }, []);

  const logout = useCallback(() => {
    writeTokens(null, null);
    if (typeof window !== "undefined") {
      window.localStorage.removeItem(USER_KEY);
      window.localStorage.removeItem(ACTIVE_ORG_KEY);
    }
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
  const onUnauthorized = useCallback<OnUnauthorized>(async () => {
    const refreshToken = refreshRef.current;
    if (!refreshToken) return null;
    try {
      const res = await api.refresh(refreshToken);
      writeTokens(res.accessToken, res.refreshToken);
      if (typeof window !== "undefined") {
        window.localStorage.setItem(USER_KEY, JSON.stringify(res.user));
      }
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
    }
  }, [writeTokens, logout]);

  const authedCall = useCallback<AuthedCaller>(
    (fn) => {
      const token = accessRef.current ?? "";
      return fn(token, onUnauthorized);
    },
    [onUnauthorized],
  );

  // Load the user's orgs and pick a default active org.
  const loadOrgs = useCallback(async () => {
    try {
      const res = await api.listOrgs(accessRef.current ?? "", onUnauthorized);
      setOrgs(res.data);
      const stored =
        typeof window !== "undefined"
          ? window.localStorage.getItem(ACTIVE_ORG_KEY)
          : null;
      const valid =
        stored && res.data.some((o) => o.id === stored) ? stored : null;
      const next = valid ?? res.data[0]?.id ?? null;
      setActiveOrgIdState(next);
      if (next && typeof window !== "undefined") {
        window.localStorage.setItem(ACTIVE_ORG_KEY, next);
      }
    } catch {
      // Leave orgs empty; the UI falls back to mock data.
    }
  }, [onUnauthorized]);

  const persist = useCallback(
    (res: AuthResponse) => {
      writeTokens(res.accessToken, res.refreshToken);
      if (typeof window !== "undefined") {
        window.localStorage.setItem(USER_KEY, JSON.stringify(res.user));
      }
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
    const accessToken =
      typeof window !== "undefined"
        ? window.localStorage.getItem(ACCESS_KEY)
        : null;
    const refreshToken =
      typeof window !== "undefined"
        ? window.localStorage.getItem(REFRESH_KEY)
        : null;
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
          if (typeof window !== "undefined") {
            window.localStorage.setItem(USER_KEY, JSON.stringify(freshUser));
          }
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

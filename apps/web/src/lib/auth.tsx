"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  type AuthResponse,
  type LoginInput,
  type SignupInput,
  type User,
} from "@/lib/api";

const ACCESS_KEY = "viro.accessToken";
const REFRESH_KEY = "viro.refreshToken";
const USER_KEY = "viro.user";

interface AuthState {
  user: User | null;
  accessToken: string | null;
  refreshToken: string | null;
  loading: boolean;
}

interface AuthContextValue extends AuthState {
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

  // Hydrate from localStorage on mount.
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

    setState({
      user,
      accessToken,
      refreshToken,
      loading: false,
    });
  }, []);

  const persist = useCallback((res: AuthResponse) => {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(ACCESS_KEY, res.accessToken);
      window.localStorage.setItem(REFRESH_KEY, res.refreshToken);
      window.localStorage.setItem(USER_KEY, JSON.stringify(res.user));
    }
    setState({
      user: res.user,
      accessToken: res.accessToken,
      refreshToken: res.refreshToken,
      loading: false,
    });
  }, []);

  const login = useCallback(
    async (input: LoginInput) => {
      const res = await api.login(input);
      persist(res);
    },
    [persist],
  );

  const signup = useCallback(
    async (input: SignupInput) => {
      const res = await api.signup(input);
      persist(res);
    },
    [persist],
  );

  const logout = useCallback(() => {
    if (typeof window !== "undefined") {
      window.localStorage.removeItem(ACCESS_KEY);
      window.localStorage.removeItem(REFRESH_KEY);
      window.localStorage.removeItem(USER_KEY);
    }
    setState({
      user: null,
      accessToken: null,
      refreshToken: null,
      loading: false,
    });
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ ...state, login, signup, logout }),
    [state, login, signup, logout],
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

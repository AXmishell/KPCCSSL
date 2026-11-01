import * as React from "react";
import { request, setCSRFToken } from "@/api/client";
import type { User } from "@/api/types";

interface AuthContextValue {
  user: User | null;
  csrf: string;
  loading: boolean;
  setupRequired: boolean;
  login: (email: string, password: string, totp?: string, recovery?: string) => Promise<void>;
  setup: (token: string, email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  setUser: (u: User | null) => void;
}

const AuthContext = React.createContext<AuthContextValue | null>(null);

interface MeResponse {
  user: User;
  csrf_token: string;
}

interface HealthResponse {
  setup_required: boolean;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUserState] = React.useState<User | null>(null);
  const [csrf, setCsrf] = React.useState("");
  const [loading, setLoading] = React.useState(true);
  const [setupRequired, setSetupRequired] = React.useState(false);

  const apply = React.useCallback((me: MeResponse | null) => {
    setUserState(me?.user ?? null);
    setCsrf(me?.csrf_token ?? "");
    setCSRFToken(me?.csrf_token ?? "");
  }, []);

  const refresh = React.useCallback(async () => {
    try {
      const me = await request<MeResponse>("/me");
      apply(me);
      setSetupRequired(false);
    } catch {
      apply(null);
      try {
        const health = await request<HealthResponse>("/system/health");
        setSetupRequired(Boolean(health.setup_required));
      } catch {
        setSetupRequired(false);
      }
    } finally {
      setLoading(false);
    }
  }, [apply]);

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  React.useEffect(() => {
    const onUnauthorized = () => apply(null);
    window.addEventListener("kpcc:unauthorized", onUnauthorized);
    return () => window.removeEventListener("kpcc:unauthorized", onUnauthorized);
  }, [apply]);

  const login = React.useCallback(
    async (email: string, password: string, totp?: string, recovery?: string) => {
      const me = await request<MeResponse>("/auth/login", {
        method: "POST",
        body: { email, password, totp_code: totp || "", recovery_code: recovery || "" },
      });
      apply(me);
    },
    [apply],
  );

  const setup = React.useCallback(
    async (token: string, email: string, password: string) => {
      const me = await request<MeResponse>("/setup", {
        method: "POST",
        body: { token, email, password },
      });
      apply(me);
      setSetupRequired(false);
    },
    [apply],
  );

  const logout = React.useCallback(async () => {
    try {
      await request<void>("/auth/logout", { method: "POST" });
    } finally {
      apply(null);
    }
  }, [apply]);

  const value: AuthContextValue = {
    user,
    csrf,
    loading,
    setupRequired,
    login,
    setup,
    logout,
    refresh,
    setUser: (u) => setUserState(u),
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = React.useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}

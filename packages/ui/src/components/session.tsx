"use client";

import { YatraxClient, type Role, type Session } from "@yatrax/api-client";
import { useRouter } from "next/navigation";
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

export interface AppIdentity {
  /** Short app name shown in the header, e.g. "Passenger". */
  name: string;
  tagline: string;
  /** Roles allowed to use this app. Admins are always allowed. */
  roles: Role[];
  /** Where sign-in lives (default "/"). */
  signInPath?: string;
  /** Where users land after signing in (default "/home"). */
  homePath?: string;
}

export const signInPath = (app: AppIdentity) => app.signInPath ?? "/";
export const homePath = (app: AppIdentity) => app.homePath ?? "/home";

/** Sign-in URL that returns to the current page afterwards. */
function signInHere(app: AppIdentity) {
  if (typeof window === "undefined") return signInPath(app);
  const here = window.location.pathname + window.location.search;
  return `${signInPath(app)}?next=${encodeURIComponent(here)}`;
}

/** The ?next= target after sign-in, accepted only if it is a path on this site. */
export function nextPath(app: AppIdentity) {
  if (typeof window === "undefined") return homePath(app);
  const next = new URLSearchParams(window.location.search).get("next");
  return next && next.startsWith("/") && !next.startsWith("//") ? next : homePath(app);
}

interface Ctx {
  client: YatraxClient;
  session: Session | null;
  ready: boolean;
  app: AppIdentity;
}

const SessionContext = createContext<Ctx | null>(null);

export function SessionProvider({ apiUrl, app, children }: { apiUrl: string; app: AppIdentity; children: ReactNode }) {
  const client = useMemo(() => new YatraxClient(apiUrl), [apiUrl]);
  const [session, setSession] = useState<Session | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    // The stored session is only readable in the browser, so load it after mount.
    setSession(client.current);
    setReady(true);
    return client.subscribe(setSession);
  }, [client]);

  return <SessionContext.Provider value={{ client, session, ready, app }}>{children}</SessionContext.Provider>;
}

export function useYatrax(): Ctx {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error("useYatrax must be used inside <SessionProvider>");
  return ctx;
}

export function roleAllowed(app: AppIdentity, role: Role) {
  return role === "admin" || app.roles.includes(role);
}

/** Renders children only for a signed-in user whose role this app accepts; otherwise returns to sign-in. */
export function RequireRole({ children }: { children: (s: Session) => ReactNode }) {
  const { session, ready, app, client } = useYatrax();
  const router = useRouter();
  const ok = session && roleAllowed(app, session.user.role);

  useEffect(() => {
    if (!ready) return;
    if (!ok) {
      router.replace(signInHere(app));
      return;
    }
    // Confirm the session is still valid server-side (this refreshes the access token if needed).
    client.me().catch(() => router.replace(signInHere(app)));
  }, [ready, ok, client, router, app]);

  if (!ready || !ok) return null;
  return <>{children(session)}</>;
}

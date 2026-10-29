"use client";

import type { Session } from "@yatrax/api-client";
import { useRouter } from "next/navigation";
import type { ReactNode } from "react";
import { roleLabel } from "./login";
import { Badge, Button, Card, Logo } from "./primitives";
import { signInPath, useYatrax } from "./session";
import { SimClockBadge } from "./live";
import { TextSizeToggle } from "./textsize";

export function AppShell({ session, children }: { session: Session; children: ReactNode }) {
  const { client, app } = useYatrax();
  const router = useRouter();
  const u = session.user;

  return (
    <div className="min-h-screen">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <header className="sticky top-0 z-10 border-b border-line bg-surface/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between gap-3 px-4">
          <div className="flex min-w-0 items-center gap-2.5">
            <Logo />
            <h1 className="truncate text-base font-semibold">
              YatraX <span className="text-[var(--accent)]">{app.name}</span>
            </h1>
          </div>
          <div className="flex items-center gap-3">
            <TextSizeToggle />
            <SimClockBadge />
            <div className="hidden text-right sm:block">
              <div className="text-sm font-medium leading-tight">{u.full_name}</div>
              <div className="text-xs text-muted">{u.employee_id ?? `+91 ${u.phone}`}</div>
            </div>
            <Badge tone={u.role === "admin" ? "saffron" : "brand"}>{roleLabel[u.role]}</Badge>
            <Button
              variant="ghost"
              size="sm"
              onClick={async () => {
                await client.logout();
                router.replace(signInPath(app));
              }}
            >
              Sign out
            </Button>
          </div>
        </div>
      </header>
      <main id="main" tabIndex={-1} className="mx-auto max-w-6xl space-y-6 px-4 py-6 focus:outline-none">
        {children}
      </main>
    </div>
  );
}

/** The signed-in user's identity and posting, shown at the top of every home screen. */
export function ProfileCard({ session, extra }: { session: Session; extra?: ReactNode }) {
  const u = session.user;
  const rows: [string, string | undefined][] = [
    ["Designation", u.designation],
    ["Zone / Division", u.zone ? `${u.zone} · ${u.division}` : undefined],
    ["Posted at", u.station],
    ["Home station", u.role === "passenger" ? u.home_station : undefined],
    ["Language", u.language],
  ];
  return (
    <Card>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="text-xs font-medium uppercase tracking-wide text-muted">Signed in as</div>
          <div className="mt-1 text-xl font-semibold">{u.full_name}</div>
        </div>
        {extra}
      </div>
      <dl className="mt-4 grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
        {rows
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k}>
              <dt className="text-xs text-muted">{k}</dt>
              <dd className="font-medium">{v}</dd>
            </div>
          ))}
      </dl>
    </Card>
  );
}

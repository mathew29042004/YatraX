"use client";

import type { Role, Session, User } from "@yatrax/api-client";
import {
  Alert, AppShell, Badge, Button, Card, CardTitle, Input, NetworkStatsGrid, ProfileCard, RequireRole, Spinner,
  cn, roleLabel, useAsync, useYatrax,
} from "@yatrax/ui";
import { useState } from "react";
import { Analytics } from "./analytics";
import { ChartViewer, FinanceTiles, RunTools, TDRQueue } from "./operations";
import { TatkalMonitor } from "./tatkal";

const ROLES: Role[] = ["passenger", "tte", "station", "vendor", "controller", "admin"];

function PeopleDirectory() {
  const { client } = useYatrax();
  const [role, setRole] = useState<Role>("tte");
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const { data, error } = useAsync<User[]>(() => client.adminUsers(role, query, 25), [client, role, query]);

  return (
    <Card>
      <CardTitle hint="Admin only · synthetic people">People directory</CardTitle>
      <div className="mb-3 flex flex-wrap gap-1">
        {ROLES.map((r) => (
          <button
            key={r}
            onClick={() => setRole(r)}
            className={cn(
              "rounded-md px-2.5 py-1 text-xs font-medium",
              r === role ? "bg-[var(--accent)] text-white" : "bg-canvas text-muted hover:text-ink",
            )}
          >
            {roleLabel[r]}
          </button>
        ))}
      </div>
      <form
        className="mb-3 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(q.trim());
        }}
      >
        <Input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search people" placeholder="Name, phone or employee ID" className="max-w-72" />
        <Button variant="secondary" type="submit">
          Search
        </Button>
      </form>
      {error && <Alert>{error}</Alert>}
      {!data && !error && <Spinner />}
      {data && (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full text-sm">
            <thead className="bg-canvas text-left text-xs text-muted">
              <tr>
                <th className="px-3 py-2">Name</th>
                <th className="px-3 py-2">ID / Phone</th>
                <th className="px-3 py-2">Posting / Home</th>
                <th className="px-3 py-2">Lang</th>
              </tr>
            </thead>
            <tbody>
              {data.map((u) => (
                <tr key={u.id} className="border-t border-line">
                  <td className="px-3 py-1.5 font-medium">{u.full_name}</td>
                  <td className="px-3 py-1.5 font-mono text-xs">{u.employee_id || u.phone}</td>
                  <td className="px-3 py-1.5 text-muted">
                    {u.designation ? `${u.designation} · ${u.zone} ${u.division} · ${u.station}` : u.home_station}
                  </td>
                  <td className="px-3 py-1.5 text-muted">{u.language}</td>
                </tr>
              ))}
              {data.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-3 py-6 text-center text-muted">
                    Nobody matches.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

// Each role's own app, linked from the portal.
const roleApp: Partial<Record<Role, { title: string; text: string; href: string }>> = {
  tte: { title: "TTE app", text: "Download the chart, scan QR tickets, mark no-shows and move RAC passengers onto freed berths, even with no network.", href: "http://localhost:3005" },
  station: { title: "Station app", text: "Boards, announcements, platforms, complaints, retiring rooms and group bookings for your station.", href: "http://localhost:3001" },
  vendor: { title: "Catering app", text: "Your outlet's live orders, menu and payouts.", href: "http://localhost:3004" },
  controller: { title: "Control Centre", text: "The live map, section chart, incidents, special trains, chaos drills and the replay of any day.", href: "http://localhost:3002" },
};

function StaffHome({ session }: { session: Session }) {
  const own = roleApp[session.user.role];
  return (
    <>
      {session.user.role !== "vendor" && <ChartViewer />}
      {own && (
        <Card>
          <CardTitle>{own.title}</CardTitle>
          <p className="text-sm text-muted">{own.text}</p>
          <a href={own.href} className="mt-2 inline-block text-sm font-medium text-[var(--accent)] hover:underline">
            Open the {own.title} →
          </a>
        </Card>
      )}
      {session.user.role !== "vendor" && <Analytics admin={false} />}
    </>
  );
}

export default function DashboardsHome() {
  return (
    <RequireRole>
      {(session) => (
        <AppShell session={session}>
          <ProfileCard
            session={session}
            extra={session.user.role === "admin" ? <Badge tone="saffron">Full access</Badge> : undefined}
          />
          <NetworkStatsGrid />
          {session.user.role === "admin" ? (
            <>
              <Analytics admin />
              <FinanceTiles />
              <div className="grid gap-5 lg:grid-cols-2">
                <RunTools />
                <TDRQueue />
              </div>
              <TatkalMonitor />
              <ChartViewer />
              <PeopleDirectory />
            </>
          ) : (
            <StaffHome session={session} />
          )}
        </AppShell>
      )}
    </RequireRole>
  );
}

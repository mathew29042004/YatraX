"use client";

import type { Zone } from "@yatrax/api-client";
import { Alert, AppShell, Badge, Card, CardTitle, ProfileCard, RequireRole, Spinner, TrainLookup, useAsync, useYatrax } from "@yatrax/ui";
import { LiveOperations } from "./live";

function ZoneMap({ highlight }: { highlight?: string }) {
  const { client } = useYatrax();
  const { data, error } = useAsync<Zone[]>(() => client.zones(), [client]);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading zones" />;
  return (
    <Card>
      <CardTitle hint={`${data.length} zones · ${data.reduce((n, z) => n + z.divisions.length, 0)} divisions`}>
        Network organisation
      </CardTitle>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {data.map((z) => (
          <div
            key={z.code}
            className={`rounded-lg border p-3 ${z.code === highlight ? "border-[var(--accent)] bg-[var(--accent)]/5" : "border-line"}`}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="font-semibold">{z.name}</span>
              <Badge tone="neutral">{z.code}</Badge>
            </div>
            <div className="mt-0.5 text-xs text-muted">
              HQ {z.hq} · {z.stations} stations
            </div>
            <div className="mt-2 flex flex-wrap gap-1">
              {z.divisions.map((d) => (
                <span key={d} className="rounded bg-canvas px-1.5 py-0.5 text-xs">
                  {d}
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}

export default function ControlHome() {
  return (
    <RequireRole>
      {(session) => (
        <AppShell session={session}>
          <ProfileCard session={session} />
          <LiveOperations />
          <ZoneMap highlight={session.user.zone} />
          <TrainLookup initial="12433" />
        </AppShell>
      )}
    </RequireRole>
  );
}

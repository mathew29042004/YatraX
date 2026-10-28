"use client";

import { AppShell, ComplaintQueue, ProfileCard, RequireRole, useAsync, useYatrax } from "@yatrax/ui";
import type { Station } from "@yatrax/api-client";
import Link from "next/link";
import { useState } from "react";
import { GroupDesk, StaysDesk } from "@/components/counter";
import { Announcements, Facilities, Platforms, TrainAtStation } from "@/components/desk";
import { DisplayBoard } from "@/components/display-board";

function Desk({ code }: { code: string }) {
  const { client } = useYatrax();
  const { data: st } = useAsync<Station>(() => client.station(code), [client, code]);
  const [picked, setPicked] = useState<{ train: string; date: string } | null>(null);
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">
          {st?.name ?? code} <span className="font-mono text-sm text-muted">{code}</span>
        </h2>
        <Link href={`/board/${code}`} target="_blank" className="text-sm text-[var(--accent)] hover:underline">
          Open the public display board ↗
        </Link>
      </div>
      <DisplayBoard code={code} />
      <ComplaintQueue title="Complaints and SOS alerts" hint="for trains reaching this station" />
      <div className="grid gap-5 xl:grid-cols-2">
        <Announcements code={code} />
        <div className="space-y-5">
          <Platforms code={code} onPick={(train, date) => setPicked({ train, date })} />
          {picked ? (
            <TrainAtStation key={picked.train + picked.date} code={code} train={picked.train} date={picked.date} platforms={st?.platforms ?? 1} />
          ) : (
            <p className="text-sm text-muted">Click a train on the platform chart to reassign it or see its coach position.</p>
          )}
        </div>
      </div>
      <div className="grid gap-5 xl:grid-cols-2">
        <StaysDesk code={code} />
        <GroupDesk />
      </div>
      <Facilities code={code} />
    </>
  );
}

export default function StationHome() {
  return (
    <RequireRole>
      {(session) => {
        // Admins have no posting, so show them the busiest demo station.
        const code = session.user.station || "MAS";
        return (
          <AppShell session={session}>
            <ProfileCard session={session} />
            <Desk code={code} />
          </AppShell>
        );
      }}
    </RequireRole>
  );
}

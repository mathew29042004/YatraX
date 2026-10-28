"use client";

import type { Booking, CoachPosition } from "@yatrax/api-client";
import { Card, CardTitle, DelayBadge, hhmm, useLiveTrain, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useEffect, useState } from "react";

/** Live running status on a confirmed booking: where the train is and when it reaches you. */
export function LiveCard({ b }: { b: Booking }) {
  const { client } = useYatrax();
  const { status } = useLiveTrain(b.train, b.origin_date);
  const [cp, setCp] = useState<CoachPosition | null>(null);
  useEffect(() => {
    client.coachPosition(b.from, b.train, b.origin_date).then(setCp).catch(() => setCp(null));
  }, [client, b.from, b.train, b.origin_date]);
  const coach = b.passengers.find((p) => p.coach)?.coach;
  const spot = coach ? cp?.spots.find((x) => x.coach === coach) : undefined;
  if (!status || !status.stops) return null;
  const board = status.stops.find((s) => s.station === b.from);
  const alight = status.stops.find((s) => s.station === b.to);
  const done = alight?.state === "departed" || (alight?.state === "here" && status.state === "arrived");
  return (
    <Card>
      <CardTitle hint={<DelayBadge minutes={status.delay_min} state={status.state} />}>Live status</CardTitle>
      <p className="text-sm">{status.message}</p>
      <div className="mt-3 grid gap-3 text-sm sm:grid-cols-2">
        {board && (
          <div className="rounded-md bg-canvas p-3">
            <div className="text-xs text-muted">At {board.name} (you board)</div>
            <div className="font-mono text-lg">
              {hhmm(board.exp_dep ?? board.exp_arr)}{" "}
              <span className="font-sans text-sm text-muted">platform</span>{" "}
              <span className={board.platform_changed ? "font-semibold text-danger" : ""}>{board.platform}</span>
            </div>
            {board.platform_changed && <div className="text-xs font-medium text-danger">Platform changed</div>}
            {spot && cp && (
              <div className="mt-1 text-xs text-muted">
                Coach {coach} stops at position {spot.marker} of {cp.markers} (engine end towards {cp.engine_toward})
              </div>
            )}
          </div>
        )}
        {alight && (
          <div className="rounded-md bg-canvas p-3">
            <div className="text-xs text-muted">At {alight.name} (you get off)</div>
            <div className="font-mono text-lg">{hhmm(alight.exp_arr)}</div>
            {done && <div className="text-xs text-ok">Arrived</div>}
          </div>
        )}
      </div>
      <Link href={`/live?train=${b.train}&date=${b.origin_date}&mark=${b.from},${b.to}`} className="mt-3 inline-block text-sm text-[var(--accent)] hover:underline">
        Full running status →
      </Link>
    </Card>
  );
}

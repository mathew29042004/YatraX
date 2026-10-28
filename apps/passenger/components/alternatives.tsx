"use client";

import type { RouteAlternative } from "@yatrax/api-client";
import { AvailabilityPill, Card, CardTitle, Spinner, useAsync, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { clockIST, duration, rupees } from "./format";

/**
 * Shown when no direct train has berths: the same train on two tickets (change berth on
 * the way), or two trains with time to change, plus the confirmation guarantee.
 */
export function Alternatives({ from, to, date, cls }: { from: string; to: string; date: string; cls: string }) {
  const { client } = useYatrax();
  const { data } = useAsync<RouteAlternative[]>(() => client.alternatives(from, to, date, cls), [client, from, to, date, cls]);
  return (
    <Card className="border-[var(--accent)]/40">
      <CardTitle hint={`${cls} · berths free on every leg`}>No direct berths? Other ways to go</CardTitle>
      {!data && <Spinner label="Looking for split journeys and connections" />}
      {data && data.length === 0 && <p className="text-sm text-muted">No split journey or connection with berths in {cls} this day.</p>}
      <ul className="space-y-2">
        {(data ?? []).map((a, i) => (
          <li key={i} className="rounded-lg border border-line p-3">
            <div className="flex flex-wrap items-baseline gap-2 text-sm">
              <span className="font-semibold">{a.kind === "split" ? "Split journey" : "Connection"}</span>
              <span className="text-muted">
                via {a.via.name} · {duration(a.duration_min)}
                {a.wait_min ? ` · ${duration(a.wait_min)} to change` : ""}
              </span>
              <span className="ml-auto font-semibold tabular-nums">{rupees(a.fare)}</span>
            </div>
            <ol className="mt-2 space-y-1.5">
              {a.legs.map((l, j) => (
                <li key={j} className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="font-mono">{l.train}</span>
                  <span className="min-w-0 truncate text-muted">{l.name}</span>
                  <span className="tabular-nums">
                    {l.from.code} {clockIST(l.departure)} → {l.to.code} {clockIST(l.arrival)}
                  </span>
                  <AvailabilityPill a={l.availability} />
                  <Link
                    href={`/book?train=${l.train}&from=${l.from.code}&to=${l.to.code}&date=${l.date}&class=${l.class}&quota=GN`}
                    className="ml-auto text-xs font-medium text-[var(--accent)] hover:underline"
                  >
                    Book this leg →
                  </Link>
                </li>
              ))}
            </ol>
            <p className="mt-1 text-xs text-muted">{a.note}</p>
          </li>
        ))}
      </ul>
      <p className="mt-3 text-sm">
        Or{" "}
        <Link href={`/guarantee?from=${from}&to=${to}&date=${date}&class=${cls}`} className="font-medium text-[var(--accent)] hover:underline">
          hold two waitlisted trains with the confirmation guarantee
        </Link>{" "}
        — the first to confirm is yours, the other is refunded in full.
      </p>
    </Card>
  );
}

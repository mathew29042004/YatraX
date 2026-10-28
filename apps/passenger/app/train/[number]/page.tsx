"use client";

import type { CoachLayout, TrainDetail } from "@yatrax/api-client";
import { Alert, Badge, Card, CardTitle, CoachMap, Rake, RunningDays, Spinner, cn, useAsync, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useParams, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { SiteHeader } from "@/components/site-header";

function CoachViewer({ train }: { train: TrainDetail }) {
  const { client } = useYatrax();
  const firstBookable = train.coaches.find((c) => c.class === "SL") ?? train.coaches.find((c) => c.bookable) ?? train.coaches[0]!;
  const [coachNo, setCoachNo] = useState(firstBookable.coach_no);
  const { data, error } = useAsync<CoachLayout>(() => client.coach(train.number, coachNo), [client, train.number, coachNo]);

  return (
    <Card>
      <CardTitle hint="Click a coach to see its berths. Position 1 is behind the locomotive.">Coaches</CardTitle>
      <Rake coaches={train.coaches} selected={coachNo} onSelect={setCoachNo} />
      <div className="mt-4">
        {error && <Alert>{error}</Alert>}
        {!data && !error && <Spinner />}
        {data && (
          <>
            <div className="mb-2 text-sm">
              <span className="font-semibold">{data.coach_no}</span> · {data.class_name} · position {data.position}
              {data.bookable && <span className="text-muted"> · {data.seats.length} {data.layout.startsWith("sleeper") ? "berths" : "seats"}</span>}
            </div>
            <CoachMap coach={data} />
          </>
        )}
      </div>
    </Card>
  );
}

function TrainPage() {
  const { client } = useYatrax();
  const { number } = useParams<{ number: string }>();
  const qs = useSearchParams();
  const from = qs.get("from");
  const to = qs.get("to");
  const { data, error } = useAsync<TrainDetail>(() => client.train(number), [client, number]);

  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading train" />;

  const fi = data.stops.findIndex((s) => s.station === from);
  const ti = data.stops.findIndex((s) => s.station === to);
  const inJourney = (i: number) => fi >= 0 && ti > fi && i >= fi && i <= ti;

  return (
    <div className="space-y-5">
      <div>
        <button onClick={() => history.back()} className="text-sm text-muted hover:text-ink">
          ← Back to results
        </button>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <span className="font-mono text-2xl font-semibold">{data.number}</span>
          <h1 className="text-2xl font-semibold">{data.name}</h1>
          <Badge tone="brand">{data.type_name}</Badge>
          {data.pantry && <Badge tone="ok">Pantry car</Badge>}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-muted">
          <span>
            {data.origin} → {data.destination} · {data.distance_km} km · {data.stops.length} stops
          </span>
          <RunningDays days={data.running_days} />
          {data.return_number && (
            <Link href={`/train/${data.return_number}`} className="text-[var(--accent)] hover:underline">
              Return train {data.return_number}
            </Link>
          )}
        </div>
      </div>

      <CoachViewer train={data} />

      <Card>
        <CardTitle hint={fi >= 0 && ti > fi ? `Your journey ${from} → ${to} is highlighted` : undefined}>Timetable</CardTitle>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted">
              <tr>
                <th className="px-3 py-2">#</th>
                <th className="px-3 py-2">Station</th>
                <th className="px-3 py-2">Arrives</th>
                <th className="px-3 py-2">Departs</th>
                <th className="px-3 py-2">Halt</th>
                <th className="px-3 py-2">Day</th>
                <th className="px-3 py-2 text-right">Km</th>
                <th className="px-3 py-2 text-right">Platform</th>
              </tr>
            </thead>
            <tbody className="tabular-nums">
              {data.stops.map((s, i) => (
                <tr key={s.seq} className={cn("border-t border-line", inJourney(i) && "bg-[var(--accent)]/5", (i === fi || i === ti) && "font-semibold")}>
                  <td className="px-3 py-1.5 text-muted">{s.seq}</td>
                  <td className="px-3 py-1.5">
                    <span className="font-mono font-semibold">{s.station}</span> {s.station_name}
                  </td>
                  <td className="px-3 py-1.5">{s.arrival ?? "Starts"}</td>
                  <td className="px-3 py-1.5">{s.departure ?? "Ends"}</td>
                  <td className="px-3 py-1.5 text-muted">{s.halt_min ? `${s.halt_min} min` : ""}</td>
                  <td className="px-3 py-1.5 text-muted">{s.day}</td>
                  <td className="px-3 py-1.5 text-right">{s.distance_km}</td>
                  <td className="px-3 py-1.5 text-right">{s.platform}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-6xl px-4 py-6">
        <Suspense fallback={<Spinner />}>
          <TrainPage />
        </Suspense>
      </main>
    </>
  );
}

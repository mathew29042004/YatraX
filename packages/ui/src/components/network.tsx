"use client";

import type { NetworkStats, StationCall, TrainDetail } from "@yatrax/api-client";
import { useEffect, useState, type FormEvent } from "react";
import { Alert, Badge, Button, Card, CardTitle, Input, Spinner, Stat, cn } from "./primitives";
import { useYatrax } from "./session";

const fmt = new Intl.NumberFormat("en-IN");
const DAYS = ["M", "T", "W", "T", "F", "S", "S"];

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    setData(null);
    setError(null);
    fn()
      .then((d) => live && setData(d))
      .catch((e: Error) => live && setError(e.message));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return { data, error };
}

export function NetworkStatsGrid() {
  const { client } = useYatrax();
  const { data, error } = useAsync<NetworkStats>(() => client.stats(), [client]);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading network" />;
  const people = Object.values(data.users).reduce((a, b) => a + (b ?? 0), 0);
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <Stat label="Zones" value={data.zones} sub={`${data.divisions} divisions`} />
      <Stat label="Stations" value={fmt.format(data.stations)} />
      <Stat label="Trains" value={fmt.format(data.trains)} sub={`${fmt.format(data.stops)} scheduled stops`} />
      <Stat label="Coaches" value={fmt.format(data.coaches)} />
      <Stat label="People" value={fmt.format(people)} sub={`${fmt.format(data.users.passenger ?? 0)} passengers`} />
      <Stat label="Vendors" value={fmt.format(data.vendors)} />
    </div>
  );
}

export function RunningDays({ days }: { days: boolean[] }) {
  return (
    <span className="inline-flex gap-0.5 font-mono text-[11px]" aria-label="Running days">
      {days.map((on, i) => (
        <span key={i} className={on ? "text-ink" : "text-line"}>
          {DAYS[i]}
        </span>
      ))}
    </span>
  );
}

const classTone: Record<string, string> = {
  "1A": "bg-violet-100 text-violet-800",
  EC: "bg-violet-100 text-violet-800",
  "2A": "bg-sky-100 text-sky-800",
  "3A": "bg-blue-100 text-blue-800",
  "3E": "bg-indigo-100 text-indigo-800",
  CC: "bg-teal-100 text-teal-800",
  SL: "bg-amber-100 text-amber-800",
  "2S": "bg-orange-100 text-orange-800",
};

/** The train's rake from the locomotive back, as coach chips. Pass onSelect to make coaches clickable. */
export function Rake({
  coaches, selected, onSelect,
}: { coaches: TrainDetail["coaches"]; selected?: string; onSelect?: (coachNo: string) => void }) {
  return (
    <div className="flex gap-1 overflow-x-auto pb-1">
      <span className="flex h-9 shrink-0 items-center rounded-l-xl rounded-r-sm bg-ink px-2 text-[10px] font-semibold text-white">LOCO</span>
      {coaches.map((c) => {
        const Tag = onSelect ? "button" : "span";
        return (
          <Tag
            key={c.coach_no}
            {...(onSelect ? { type: "button" as const, onClick: () => onSelect(c.coach_no), "aria-pressed": selected === c.coach_no } : {})}
            title={`${c.coach_no} · ${c.class}${c.bookable ? ` · ${c.seats} berths/seats` : ""}`}
            className={cn(
              "flex h-9 min-w-11 shrink-0 flex-col items-center justify-center rounded-sm px-1.5 text-[10px] font-semibold leading-tight",
              classTone[c.class] ?? "bg-canvas text-muted border border-line",
              onSelect && "cursor-pointer hover:ring-2 hover:ring-[var(--accent)]/40",
              selected === c.coach_no && "ring-2 ring-[var(--accent)]",
            )}
          >
            {c.coach_no}
            <span className="font-normal opacity-70">{c.class}</span>
          </Tag>
        );
      })}
    </div>
  );
}

export function TrainLookup({ initial = "12623" }: { initial?: string }) {
  const { client } = useYatrax();
  const [q, setQ] = useState(initial);
  const [number, setNumber] = useState(initial);
  const { data, error } = useAsync<TrainDetail>(() => client.train(number), [client, number]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setNumber(q.trim());
  };

  return (
    <Card>
      <CardTitle hint="Schedule and rake from the seeded timetable">Train lookup</CardTitle>
      <form onSubmit={submit} className="mb-4 flex gap-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Train number" placeholder="Train number, e.g. 12623" className="max-w-56" />
        <Button type="submit" variant="secondary">
          Show
        </Button>
      </form>
      {error && <Alert>{error}</Alert>}
      {!data && !error && <Spinner />}
      {data && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-lg font-semibold">{data.number}</span>
            <span className="text-lg font-semibold">{data.name}</span>
            <Badge tone="brand">{data.type_name}</Badge>
            {data.pantry && <Badge tone="ok">Pantry</Badge>}
            <span className="text-sm text-muted">
              {data.origin} → {data.destination} · {fmt.format(data.distance_km)} km ·
            </span>
            <RunningDays days={data.running_days} />
          </div>
          <Rake coaches={data.coaches} />
          <div className="max-h-80 overflow-auto rounded-lg border border-line">
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-canvas text-left text-xs text-muted">
                <tr>
                  <th className="px-3 py-2">Station</th>
                  <th className="px-3 py-2">Arr</th>
                  <th className="px-3 py-2">Dep</th>
                  <th className="px-3 py-2">Halt</th>
                  <th className="px-3 py-2">Day</th>
                  <th className="px-3 py-2 text-right">Km</th>
                  <th className="px-3 py-2 text-right">PF</th>
                </tr>
              </thead>
              <tbody className="tabular-nums">
                {data.stops.map((s) => (
                  <tr key={s.seq} className="border-t border-line">
                    <td className="px-3 py-1.5">
                      <span className="font-mono font-semibold">{s.station}</span>{" "}
                      <span className="text-muted">{s.station_name}</span>
                    </td>
                    <td className="px-3 py-1.5">{s.arrival ?? "—"}</td>
                    <td className="px-3 py-1.5">{s.departure ?? "—"}</td>
                    <td className="px-3 py-1.5 text-muted">{s.halt_min ? `${s.halt_min}m` : ""}</td>
                    <td className="px-3 py-1.5 text-muted">{s.day}</td>
                    <td className="px-3 py-1.5 text-right">{s.distance_km}</td>
                    <td className="px-3 py-1.5 text-right">{s.platform}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Card>
  );
}

/** Classic yellow-on-black departure board for one station. */
export function StationBoard({ code }: { code: string }) {
  const { client } = useYatrax();
  const { data, error } = useAsync<StationCall[]>(() => client.stationTrains(code), [client, code]);
  return (
    <div className="overflow-hidden rounded-xl bg-board shadow-lg">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2 font-mono text-sm text-board-amber">
        <span>{code} · ALL TRAINS (TIMETABLE)</span>
        <span className="opacity-70">{data ? `${data.length} services` : ""}</span>
      </div>
      {error && <div className="p-4 font-mono text-sm text-red-400">{error}</div>}
      {!data && !error && <div className="p-4 font-mono text-sm text-board-amber/70">LOADING…</div>}
      {data && (
        <div className="max-h-96 overflow-auto">
          <table className="w-full font-mono text-sm text-board-amber">
            <thead className="sticky top-0 bg-board text-left text-xs opacity-70">
              <tr>
                <th className="px-4 py-2">TRAIN</th>
                <th className="px-4 py-2">NAME</th>
                <th className="px-4 py-2">TO</th>
                <th className="px-4 py-2">ARR</th>
                <th className="px-4 py-2">DEP</th>
                <th className="px-4 py-2 text-right">PF</th>
              </tr>
            </thead>
            <tbody>
              {data.map((t) => (
                <tr key={t.number} className="border-t border-white/5">
                  <td className="px-4 py-1.5">{t.number}</td>
                  <td className="max-w-64 truncate px-4 py-1.5 uppercase">{t.name}</td>
                  <td className="px-4 py-1.5">{t.destination === code ? "TERM" : t.destination}</td>
                  <td className="px-4 py-1.5">{t.arrival ?? "--:--"}</td>
                  <td className="px-4 py-1.5">{t.departure ?? "--:--"}</td>
                  <td className="px-4 py-1.5 text-right">{t.platform}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

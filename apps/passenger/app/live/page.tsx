"use client";

import type { StationBoard } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, DelayBadge, Input, Label, LiveTimeline, Spinner, cn, dayShort, hhmm, useLiveTrain, useSimClock, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState, type FormEvent } from "react";
import { SiteHeader } from "@/components/site-header";

const eventText: Record<string, (e: Record<string, unknown>) => string> = {
  "train.departed": (e) => `Left ${e.station}`,
  "train.delayed": (e) => `Now ${e.delay} min late${e.reason ? ` · ${e.reason}` : ""}`,
  "platform.changed": (e) => `Platform at ${e.station} changed to ${e.platform}`,
  "train.approaching": (e) => `Approaching ${e.station} (PF ${e.platform}) at ${hhmm(e.eta as string)}`,
  "train.arrived": (e) => `Reached ${e.station}`,
};

function TrainLive({ train, date, mark }: { train: string; date?: string; mark: string[] }) {
  const { status, events, error } = useLiveTrain(train, date);
  if (error) return <Alert>{error}</Alert>;
  if (!status) return <Spinner label="Finding the train" />;
  return (
    <div className="space-y-4">
      <Card>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-lg font-semibold">{status.train}</span>
          <h2 className="text-lg font-semibold">{status.name}</h2>
          <span className="ml-auto">
            <DelayBadge minutes={status.delay_min} state={status.state} />
          </span>
        </div>
        <div className="mt-1 text-sm text-muted">Left its first station on {dayShort(`${status.origin_date}T12:00:00+05:30`)}</div>
        <p className="mt-3 font-medium">{status.message}</p>
        {status.reason && status.delay_min > 0 && <p className="text-sm text-muted">Reason: {status.reason}</p>}
        <div className="mt-3 h-2 overflow-hidden rounded-full bg-canvas">
          <div className="h-full rounded-full bg-[var(--accent)] transition-all duration-700" style={{ width: `${Math.round(status.progress * 100)}%` }} />
        </div>
        <div className="mt-1 text-xs text-muted">
          {Math.round(status.distance_km)} km covered · {Math.round(status.progress * 100)}%
        </div>
        {events.length > 0 && (
          <ul className="mt-3 space-y-1 border-t border-line pt-3 text-sm">
            {events.slice(0, 5).map((e, i) => (
              <li key={i} className="flex gap-2">
                <Badge tone="brand">live</Badge>
                {(eventText[e.type] ?? (() => e.type))(e)}
              </li>
            ))}
          </ul>
        )}
      </Card>
      {status.stops && (
        <Card>
          <CardTitle hint={`${status.stops.length} stops`}>Running status</CardTitle>
          <LiveTimeline status={status} highlight={mark} />
        </Card>
      )}
    </div>
  );
}

function Board({ code }: { code: string }) {
  const { client } = useYatrax();
  const [board, setBoard] = useState<StationBoard | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      client
        .stationBoard(code, 4)
        .then((b) => alive && setBoard(b))
        .catch((e) => alive && setError(e instanceof ApiError && e.status === 404 ? `No station ${code}` : "Board unavailable"));
    load();
    const t = setInterval(load, 10000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [client, code]);
  if (error) return <Alert>{error}</Alert>;
  if (!board) return <Spinner label="Loading the board" />;
  return (
    <Card>
      <CardTitle hint="next 4 hours">
        {board.station.name} <span className="font-mono text-sm text-muted">{board.station.code}</span>
      </CardTitle>
      {board.trains.length === 0 ? (
        <p className="text-sm text-muted">No trains in the next four hours.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted">
              <tr>
                <th className="py-1">Train</th>
                <th>From → To</th>
                <th>Arr</th>
                <th>Dep</th>
                <th>PF</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {board.trains.map((r) => (
                <tr key={r.train + r.origin_date} className={cn("border-t border-line", r.state === "departed" && "text-muted")}>
                  <td className="py-1.5">
                    <Link href={`/live?train=${r.train}&date=${r.origin_date}&mark=${code}`} className="font-mono font-semibold hover:underline">
                      {r.train}
                    </Link>{" "}
                    <span className="hidden md:inline">{r.name}</span>
                  </td>
                  <td className="text-xs">
                    {r.from} → {r.to}
                  </td>
                  <td className="font-mono">{r.exp_arr ? hhmm(r.exp_arr) : "—"}</td>
                  <td className="font-mono">{r.exp_dep ? hhmm(r.exp_dep) : "—"}</td>
                  <td className={cn("font-mono", r.platform_changed && "font-semibold text-danger")}>{r.platform}</td>
                  <td>
                    {r.state === "on_platform" ? (
                      <Badge tone="brand">On platform</Badge>
                    ) : r.state === "arriving" ? (
                      <Badge tone="saffron">Arriving</Badge>
                    ) : r.state === "departed" ? (
                      "Departed"
                    ) : r.state === "arrived" ? (
                      "Arrived"
                    ) : (
                      <DelayBadge minutes={r.delay_min} />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function LivePage() {
  const params = useSearchParams();
  const router = useRouter();
  const { now, simulated, clock } = useSimClock();
  const train = params.get("train");
  const station = params.get("station");
  const [num, setNum] = useState(train ?? "");
  const [code, setCode] = useState(station ?? "");
  const go = (e: FormEvent, q: string) => {
    e.preventDefault();
    router.push(`/live?${q}`);
  };
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl space-y-5 px-4 py-6">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h1 className="text-xl font-semibold">Live train status</h1>
          {now && (
            <span className="text-sm text-muted">
              {simulated ? "Railway simulation time" : "Now"}: {dayShort(now)} <span className="font-mono">{hhmm(now)}</span>
              {clock && simulated && !clock.paused && clock.speed !== 1 && ` · ${clock.speed}× speed`}
            </span>
          )}
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <form onSubmit={(e) => go(e, `train=${encodeURIComponent(num.trim())}`)} className="flex items-end gap-2">
            <div className="flex-1">
              <Label htmlFor="live-train">Train number</Label>
              <Input id="live-train" value={num} onChange={(e) => setNum(e.target.value)} placeholder="12623" inputMode="numeric" />
            </div>
            <Button type="submit" disabled={!num.trim()}>
              Track
            </Button>
          </form>
          <form onSubmit={(e) => go(e, `station=${encodeURIComponent(code.trim().toUpperCase())}`)} className="flex items-end gap-2">
            <div className="flex-1">
              <Label htmlFor="live-station">Station code</Label>
              <Input id="live-station" value={code} onChange={(e) => setCode(e.target.value)} placeholder="MAS" />
            </div>
            <Button type="submit" variant="secondary" disabled={!code.trim()}>
              Board
            </Button>
          </form>
        </div>
        {train && <TrainLive key={train + (params.get("date") ?? "")} train={train} date={params.get("date") ?? undefined} mark={(params.get("mark") ?? "").split(",").filter(Boolean)} />}
        {!train && station && <Board key={station} code={station.toUpperCase()} />}
        {!train && !station && (
          <p className="text-sm text-muted">
            Track a train by number, or open a station&apos;s arrivals and departures. Try{" "}
            <Link href="/live?train=12623" className="text-[var(--accent)] hover:underline">
              12623 Trivandrum Mail
            </Link>{" "}
            or{" "}
            <Link href="/live?station=NDLS" className="text-[var(--accent)] hover:underline">
              New Delhi
            </Link>
            .
          </p>
        )}
      </main>
    </>
  );
}

export default function Page() {
  return (
    <Suspense>
      <LivePage />
    </Suspense>
  );
}

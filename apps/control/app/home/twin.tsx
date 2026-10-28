"use client";

import type { ChaosDrill, ChaosRun, ReplayDay } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, Spinner, Stat, cn, hhmm, useAsync, useSimClock, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";

const istDate = (d: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Kolkata" }).format(d);

// ── Digital twin: replay any past day ──────────────────────────────────────

const eventLabel: Record<ReplayDay["events"][number]["kind"], { text: string; tone: "danger" | "saffron" | "brand" | "neutral" }> = {
  breakdown: { text: "Breakdown", tone: "danger" },
  disruption: { text: "Disruption", tone: "danger" },
  incident: { text: "Held", tone: "saffron" },
  platform: { text: "Platform", tone: "brand" },
  late: { text: "Late", tone: "neutral" },
};

/**
 * Winds the railway clock back to a past day and plays it. The live model is deterministic
 * for a run and its recorded incidents, so the map, section chart and boards show the day
 * exactly as it happened.
 */
export function ReplayCard({ onPick }: { onPick: (train: string, originDate: string) => void }) {
  const { client } = useYatrax();
  const { clock, simulated } = useSimClock(5000);
  const yesterday = istDate(new Date(Date.now() - 86_400_000));
  const [date, setDate] = useState(yesterday);
  const [day, setDay] = useState<ReplayDay | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    (d: string) => {
      setBusy(true);
      setError(null);
      client
        .replayDay(d)
        .then(setDay)
        .catch((e: Error) => setError(e.message))
        .finally(() => setBusy(false));
    },
    [client],
  );
  useEffect(() => load(date), [load, date]);

  const play = (at: string, speed: number) =>
    client.setSimClock({ jump_to: at, speed, paused: false }).catch((e) => setError(e instanceof ApiError ? e.message : "Could not move the clock"));

  return (
    <Card>
      <CardTitle hint="the network as it ran that day, from the same model as live">Replay a day</CardTitle>
      <div className="flex flex-wrap items-end gap-3">
        <div>
          <Label htmlFor="replay-date">Day</Label>
          <Input id="replay-date" type="date" max={yesterday} value={date} onChange={(e) => e.target.value && setDate(e.target.value)} />
        </div>
        <Button onClick={() => play(new Date(`${date}T05:00:00+05:30`).toISOString(), 144)}>Play from 05:00 at 144×</Button>
        <Button variant="secondary" onClick={() => play(new Date(`${date}T05:00:00+05:30`).toISOString(), 600)}>
          600×
        </Button>
        {simulated && (
          <Button variant="ghost" onClick={() => client.setSimClock({ reset: true })}>
            Back to live
          </Button>
        )}
        {clock && simulated && <span className="text-xs text-muted">Replaying at {clock.paused ? "pause" : `${clock.speed}×`} — the map, section chart and boards follow</span>}
      </div>
      {error && <div className="mt-3"><Alert>{error}</Alert></div>}
      {busy && !day && <Spinner label="Reading the day" />}
      {day && (
        <>
          <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="On time" value={`${(day.on_time * 100).toFixed(1)}%`} sub={`${day.runs.toLocaleString("en-IN")} runs that left that day`} />
            <Stat label="Average delay" value={`${day.avg_delay.toFixed(1)} min`} />
            <Stat label="Over an hour late" value={day.late_60} />
            <Stat label="Cancelled" value={day.cancelled} />
          </div>
          <div className="mt-4">
            <div className="mb-1 text-xs font-medium text-muted">What happened (click to jump the clock there)</div>
            {day.events.length === 0 ? (
              <p className="text-sm text-muted">A quiet day: no control actions recorded.</p>
            ) : (
              <ul className="max-h-72 divide-y divide-line overflow-y-auto rounded-md border border-line">
                {day.events.map((e, i) => (
                  <li key={i}>
                    <button
                      className="flex w-full items-start gap-2 px-3 py-1.5 text-left text-sm hover:bg-canvas"
                      onClick={() => {
                        play(new Date(new Date(e.at).getTime() - 10 * 60_000).toISOString(), 60);
                        onPick(e.train, e.origin_date);
                      }}
                    >
                      <span className="w-14 shrink-0 font-mono text-xs leading-5 text-muted">
                        {hhmm(e.at)}
                        {istDate(new Date(e.at)) > day.date && <sup title="the next day">+1</sup>}
                      </span>
                      <Badge tone={eventLabel[e.kind].tone} className="shrink-0">{eventLabel[e.kind].text}</Badge>
                      <span className="min-w-0">
                        <span className="font-mono">{e.train}</span> {e.name} · <span className="text-muted">{e.text}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}
    </Card>
  );
}

// ── Chaos mode ─────────────────────────────────────────────────────────────

const stepTone: Record<ChaosRun["steps"][number]["kind"], string> = {
  info: "text-muted",
  fault: "text-danger font-medium",
  ok: "text-ink",
  recovered: "text-ok font-semibold",
  failed: "text-danger font-semibold",
};
const stepMark: Record<ChaosRun["steps"][number]["kind"], string> = { info: "·", fault: "✕", ok: "✓", recovered: "✓", failed: "!" };

function RunTimeline({ run }: { run: ChaosRun }) {
  const start = new Date(run.started_at).getTime();
  return (
    <ol className="space-y-1 text-sm" aria-live="polite">
      {run.steps.map((s, i) => (
        <li key={i} className={cn("flex gap-2", stepTone[s.kind])}>
          <span className="w-12 shrink-0 text-right font-mono text-xs leading-5 text-muted">+{((new Date(s.at).getTime() - start) / 1000).toFixed(1)}s</span>
          <span aria-hidden className="w-3 shrink-0 text-center">{stepMark[s.kind]}</span>
          <span>{s.text}</span>
        </li>
      ))}
      {run.status === "running" && (
        <li className="flex gap-2 text-muted">
          <span className="w-12" />
          <span className="animate-pulse">Watching for recovery…</span>
        </li>
      )}
    </ol>
  );
}

/** Break something on purpose and watch the system heal itself. */
export function ChaosCard() {
  const { client } = useYatrax();
  const { data: drills } = useAsync<ChaosDrill[]>(() => client.chaosDrills(), [client]);
  const [runs, setRuns] = useState<ChaosRun[] | null>(null);
  const [open, setOpen] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.chaosRuns().then(setRuns).catch(() => undefined), [client]);
  const running = runs?.find((r) => r.status === "running");
  useEffect(() => {
    load();
    const t = setInterval(load, running ? 1500 : 10000);
    return () => clearInterval(t);
  }, [load, running]);

  async function start(kind: string) {
    setError(null);
    try {
      const r = await client.startChaos(kind);
      setOpen(r.id);
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not start the drill");
    }
  }

  const shown = runs?.find((r) => r.id === open) ?? running ?? runs?.[0];
  return (
    <Card>
      <CardTitle hint="faults are real; nothing is repaired by hand">Chaos mode</CardTitle>
      {error && <div className="mb-3"><Alert>{error}</Alert></div>}
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {(drills ?? []).map((d) => (
          <div key={d.kind} className="flex flex-col rounded-lg border border-line p-3">
            <div className="text-sm font-semibold">{d.title}</div>
            <p className="mt-1 text-xs text-muted">
              <span className="font-medium text-danger">Breaks: </span>
              {d.breaks}
            </p>
            <p className="mt-1 flex-1 text-xs text-muted">
              <span className="font-medium text-ok">Should: </span>
              {d.expect}
            </p>
            <Button size="sm" variant="danger" className="mt-3" disabled={!!running} onClick={() => start(d.kind)}>
              Run drill
            </Button>
          </div>
        ))}
      </div>
      {shown && (
        <div className="mt-4 rounded-lg bg-canvas p-3">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <span className="text-sm font-semibold">{shown.title}</span>
            <Badge tone={shown.status === "recovered" ? "ok" : shown.status === "failed" ? "danger" : "saffron"}>
              {shown.status === "recovered" ? `Recovered in ${shown.recovered_in?.toFixed(1)} s` : shown.status === "failed" ? "Did not recover" : "Running"}
            </Badge>
            <span className="text-xs text-muted">started {hhmm(shown.started_at)} by {shown.started_by || "—"}</span>
          </div>
          <RunTimeline run={shown} />
        </div>
      )}
      {runs && runs.length > 1 && (
        <div className="mt-4">
          <div className="mb-1 text-xs font-medium text-muted">Earlier drills</div>
          <table className="w-full text-sm">
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className={cn("cursor-pointer border-t border-line hover:bg-canvas", r.id === shown?.id && "bg-canvas")} onClick={() => setOpen(r.id)}>
                  <td className="py-1 text-xs text-muted">{new Date(r.started_at).toLocaleString("en-IN", { dateStyle: "short", timeStyle: "short" })}</td>
                  <td className="py-1">{r.title}</td>
                  <td className="py-1 text-right">
                    {r.status === "recovered" ? <span className="text-ok">{r.recovered_in?.toFixed(1)} s</span> : <span className={r.status === "failed" ? "text-danger" : "text-muted"}>{r.status}</span>}
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

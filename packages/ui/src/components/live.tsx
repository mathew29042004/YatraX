"use client";

import type { LiveMessage, LiveStatus, SimClock } from "@yatrax/api-client";
import { useEffect, useRef, useState } from "react";
import { Badge, cn } from "./primitives";
import { useYatrax } from "./session";

const istClock = new Intl.DateTimeFormat("en-IN", { timeZone: "Asia/Kolkata", hour: "2-digit", minute: "2-digit", hour12: false });
const istDay = new Intl.DateTimeFormat("en-IN", { timeZone: "Asia/Kolkata", weekday: "short", day: "numeric", month: "short" });

export const hhmm = (iso?: string | Date) => (iso ? istClock.format(typeof iso === "string" ? new Date(iso) : iso) : "—");
export const dayShort = (iso?: string | Date) => (iso ? istDay.format(typeof iso === "string" ? new Date(iso) : iso) : "");

/**
 * The railway's simulated clock, ticking locally between fetches. Train movement and
 * alerts follow it; bookings and payments use real time.
 */
export function useSimClock(refreshMs = 15000) {
  const { client } = useYatrax();
  const [clock, setClock] = useState<SimClock | null>(null);
  const fetchedAt = useRef(0);
  const [, tick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      client
        .simClock()
        .then((c) => {
          if (!alive) return;
          fetchedAt.current = Date.now();
          setClock(c);
        })
        .catch(() => undefined);
    load();
    const t = setInterval(load, refreshMs);
    const s = setInterval(() => tick((n) => n + 1), 1000);
    return () => {
      alive = false;
      clearInterval(t);
      clearInterval(s);
    };
  }, [client, refreshMs]);

  const now = clock
    ? new Date(new Date(clock.sim_now).getTime() + (clock.paused ? 0 : (Date.now() - fetchedAt.current) * clock.speed))
    : null;
  const simulated = !!clock && (clock.paused || clock.speed !== 1 || Math.abs(new Date(clock.sim_now).getTime() - new Date(clock.real_now).getTime()) > 120_000);
  return { clock, now, simulated };
}

/** Shows railway time when the simulation clock isn't simply real time. */
export function SimClockBadge() {
  const { clock, now, simulated } = useSimClock();
  if (!clock || !now || !simulated) return null;
  return (
    <span
      title="Railway simulation clock: trains move and alerts follow this time"
      className="hidden items-center gap-1.5 rounded-md bg-saffron-100 px-2 py-1 font-mono text-xs font-medium text-saffron-700 sm:flex"
    >
      <span aria-hidden>⏱</span>
      {dayShort(now)} {hhmm(now)}
      <span className="font-sans">{clock.paused ? "· paused" : `· ${clock.speed}×`}</span>
    </span>
  );
}

export function DelayBadge({ minutes, state }: { minutes: number; state?: string }) {
  if (state === "not_running") return <Badge tone="neutral">Not running</Badge>;
  if (state === "cancelled") return <Badge tone="danger">Cancelled</Badge>;
  if (minutes <= 0) return <Badge tone="ok">On time</Badge>;
  return <Badge tone={minutes >= 60 ? "danger" : "saffron"}>{minutes} min late</Badge>;
}

/**
 * Follows one run live: a WebSocket pushes its status every second (and its events as
 * they happen); if the socket can't connect, it polls instead.
 */
export function useLiveTrain(train: string | null, originDate?: string) {
  const { client } = useYatrax();
  const [status, setStatus] = useState<LiveStatus | null>(null);
  const [clock, setClock] = useState<SimClock | null>(null);
  const [events, setEvents] = useState<Array<Record<string, unknown> & { type: string }>>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!train) return;
    let alive = true;
    let ws: WebSocket | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    setStatus(null);
    setEvents([]);
    setError(null);
    const fallback = () => {
      if (poll) return;
      const load = () =>
        client
          .liveTrain(train, originDate)
          .then((s) => alive && setStatus(s))
          .catch((e) => alive && setError(e?.message ?? "Live status unavailable"));
      load();
      poll = setInterval(load, 5000);
    };
    // Resolve the run first (so an unknown train shows a clear error), then stream it.
    client
      .liveTrain(train, originDate)
      .then((s) => {
        if (!alive) return;
        setStatus(s);
        try {
          ws = new WebSocket(client.liveSocketURL({ train, date: s.origin_date }));
          ws.onmessage = (e) => {
            const m = JSON.parse(e.data) as LiveMessage;
            if (m.kind === "status") {
              setStatus(m.status);
              setClock(m.clock);
            } else if (m.kind === "event") {
              setEvents((xs) => [m.event, ...xs].slice(0, 20));
            }
          };
          ws.onerror = fallback;
        } catch {
          fallback();
        }
      })
      .catch((e) => alive && setError(e?.message ?? "No such train"));
    return () => {
      alive = false;
      ws?.close();
      if (poll) clearInterval(poll);
    };
  }, [client, train, originDate]);

  return { status, clock, events, error };
}

/** The classic running-status timeline: every stop, scheduled vs expected, and where the train is. */
export function LiveTimeline({ status, highlight = [] }: { status: LiveStatus; highlight?: string[] }) {
  const stops = status.stops ?? [];
  return (
    <ol className="relative">
      {stops.map((s, i) => {
        const late = Math.max(s.arr_delay, s.dep_delay);
        const gone = s.state === "cancelled" || s.state === "skipped";
        const between = status.state === "running" && i === status.last_idx;
        const mark = highlight.includes(s.station);
        return (
          <li key={s.seq} className="relative pl-8">
            <span
              aria-hidden
              className={cn(
                "absolute left-[11px] top-0 h-full w-0.5",
                i === stops.length - 1 ? "h-3" : "",
                s.state === "departed" ? "bg-[var(--accent)]" : "bg-line",
              )}
            />
            <span
              aria-hidden
              className={cn(
                "absolute left-1.5 top-2.5 h-3.5 w-3.5 rounded-full border-2",
                s.state === "here" ? "border-[var(--accent)] bg-[var(--accent)] ring-4 ring-[var(--accent)]/20" : "",
                s.state === "departed" ? "border-[var(--accent)] bg-surface" : "",
                s.state === "upcoming" ? "border-line bg-surface" : "",
              )}
            />
            <div className={cn("flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 py-2", mark && "rounded-md bg-[var(--accent)]/5 px-2")}>
              <div className="min-w-0">
                <span className={cn("font-medium", s.state === "upcoming" && "text-ink", s.state === "departed" && "text-muted", gone && "text-muted line-through")}>{s.name}</span>{" "}
                {gone && <span className="ml-1 text-xs font-medium text-danger">{s.state === "skipped" ? "not calling (diverted)" : "cancelled"}</span>}
                <span className="font-mono text-xs text-muted">{s.station}</span>
                {mark && <span className="ml-2 text-xs font-medium text-[var(--accent)]">your stop</span>}
                <div className="text-xs text-muted">
                  {s.distance_km} km · PF{" "}
                  <span className={cn(s.platform_changed && "font-semibold text-danger")}>
                    {s.platform}
                    {s.platform_changed && " (changed)"}
                  </span>
                </div>
              </div>
              <div className="text-right text-sm">
                {s.sched_arr && (
                  <div>
                    <span className="text-xs text-muted">arr </span>
                    <span className="font-mono">{hhmm(s.exp_arr)}</span>
                    {s.arr_delay > 0 && <span className="ml-1 font-mono text-xs text-muted line-through">{hhmm(s.sched_arr)}</span>}
                  </div>
                )}
                {s.sched_dep && (
                  <div>
                    <span className="text-xs text-muted">dep </span>
                    <span className="font-mono">{hhmm(s.exp_dep)}</span>
                    {s.dep_delay > 0 && <span className="ml-1 font-mono text-xs text-muted line-through">{hhmm(s.sched_dep)}</span>}
                  </div>
                )}
                {!gone && (
                  <div className={cn("text-xs", late === 0 ? "text-ok" : late >= 60 ? "text-danger" : "text-warn")}>
                    {late === 0 ? "on time" : `+${late} min`}
                  </div>
                )}
              </div>
            </div>
            {between && (
              <div className="relative -ml-8 flex items-center gap-2 py-1 pl-8 text-xs font-medium text-[var(--accent)]">
                <span aria-hidden className="absolute left-[5px] h-4 w-4 animate-pulse rounded-full bg-[var(--accent)]" />
                🚆 {status.message}
              </div>
            )}
          </li>
        );
      })}
    </ol>
  );
}

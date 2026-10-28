"use client";

import type { LiveBrief, LiveIncident, LiveMessage, SimClock } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import {
  Alert, Badge, Button, Card, CardTitle, ComplaintQueue, DelayBadge, Input, Label, LiveTimeline, Spinner, Stat, cn, dayShort, hhmm,
  useLiveTrain, useSimClock, useYatrax,
} from "@yatrax/ui";
import { useEffect, useMemo, useState } from "react";
import { ConflictsCard, DisruptionLog, IncidentPanel, SectionChartCard, WaitlistsCard, WhatIfPanel } from "./control";
import { SpecialsCard } from "./specials";
import { ChaosCard, ReplayCard } from "./twin";

const SPEEDS = [1, 10, 60, 144, 600];
const ZONES = ["", "CR", "ER", "ECR", "ECoR", "KR", "NR", "NCR", "NER", "NFR", "NWR", "SR", "SCR", "SER", "SECR", "SWR", "WR", "WCR"];

/** Railway clock: real time, faster (144× = a day in ten minutes), paused, or jumped. */
export function ClockControls() {
  const { client } = useYatrax();
  const { clock, now, simulated } = useSimClock(5000);
  const [jump, setJump] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const [, refresh] = useState(0);
  const set = async (change: Parameters<typeof client.setSimClock>[0]) => {
    setMsg(null);
    try {
      await client.setSimClock(change);
      refresh((n) => n + 1);
      setMsg("Clock updated — every screen follows within a few seconds.");
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Could not change the clock");
    }
  };
  return (
    <Card>
      <CardTitle hint="trains, live status and alerts follow it; bookings stay on real time">Railway clock</CardTitle>
      <div className="flex flex-wrap items-center gap-4">
        <div>
          <div className="font-mono text-3xl font-semibold">{now ? hhmm(now) : "—"}</div>
          <div className="text-sm text-muted">
            {now && dayShort(now)} · {clock ? (clock.paused ? "paused" : `${clock.speed}× ${clock.speed === 144 ? "(a day in 10 min)" : ""}`) : ""}
            {!simulated && " · real time"}
          </div>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {SPEEDS.map((sp) => (
            <Button key={sp} size="sm" variant={clock?.speed === sp && !clock.paused ? "primary" : "secondary"} onClick={() => set({ speed: sp, paused: false })}>
              {sp}×
            </Button>
          ))}
          <Button size="sm" variant="secondary" onClick={() => set({ paused: !clock?.paused })}>
            {clock?.paused ? "Resume" : "Pause"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => set({ reset: true })}>
            Back to real time
          </Button>
        </div>
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (jump) set({ jump_to: new Date(jump + ":00+05:30").toISOString() });
          }}
        >
          <div>
            <Label htmlFor="jump">Jump to (IST)</Label>
            <Input id="jump" type="datetime-local" value={jump} onChange={(e) => setJump(e.target.value)} />
          </div>
          <Button type="submit" size="sm" variant="secondary" disabled={!jump}>
            Jump
          </Button>
        </form>
      </div>
      {msg && <p className="mt-2 text-xs text-muted">{msg}</p>}
    </Card>
  );
}

// India fits lon 68–98, lat 6–37; squash longitude by cos(22°) so the shape looks right.
const W = 600;
const H = 640;
const x = (lon: number) => ((lon - 67.5) / 31) * W;
const y = (lat: number) => H - ((lat - 6) / 31.5) * H;
const tone = (d: number) => (d >= 60 ? "#b91c1c" : d >= 15 ? "#f29a0e" : "#15803d");

/** Every running train on the network, streamed over a WebSocket every two seconds. */
export function LiveMap({ onPick, picked }: { onPick: (t: LiveBrief) => void; picked?: string }) {
  const { client } = useYatrax();
  const [zone, setZone] = useState("");
  const [trains, setTrains] = useState<LiveBrief[] | null>(null);
  const [clock, setClock] = useState<SimClock | null>(null);
  const [geo, setGeo] = useState<Array<[string, number, number, number]>>([]);

  useEffect(() => {
    client.stationsGeo().then(setGeo).catch(() => undefined);
  }, [client]);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    const fallback = () => {
      if (poll) return;
      const load = () => client.liveMap({ zone }).then((m) => setTrains(m.trains)).catch(() => undefined);
      load();
      poll = setInterval(load, 4000);
    };
    try {
      ws = new WebSocket(client.liveSocketURL({ map: true, zone: zone || undefined }));
      ws.onmessage = (e) => {
        const m = JSON.parse(e.data) as LiveMessage;
        if (m.kind === "map") {
          setTrains(m.trains);
          setClock(m.clock);
        }
      };
      ws.onerror = fallback;
    } catch {
      fallback();
    }
    return () => {
      ws?.close();
      if (poll) clearInterval(poll);
    };
  }, [client, zone]);

  const late = trains?.filter((t) => t.delay_min >= 15).length ?? 0;
  const worst = useMemo(() => [...(trains ?? [])].sort((a, b) => b.delay_min - a.delay_min).slice(0, 8), [trains]);

  return (
    <Card>
      <CardTitle
        hint={
          <select aria-label="Zone" value={zone} onChange={(e) => setZone(e.target.value)} className="rounded-md border border-line bg-surface px-2 py-1 text-xs">
            {ZONES.map((z) => (
              <option key={z} value={z}>
                {z || "All zones"}
              </option>
            ))}
          </select>
        }
      >
        Live network
      </CardTitle>
      {!trains ? (
        <Spinner label="Connecting to the live feed" />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[1fr_260px]">
          <svg viewBox={`0 0 ${W} ${H}`} className="w-full rounded-lg bg-canvas" role="img" aria-label={`Map of ${trains.length} running trains`}>
            {geo.map(([code, lat, lon, imp]) => (
              <circle key={code} cx={x(lon)} cy={y(lat)} r={imp === 1 ? 1.6 : 0.9} fill="#cbd5e1" />
            ))}
            {trains.map((t) => (
              <circle
                key={t.train + t.origin_date}
                cx={x(t.lon)}
                cy={y(t.lat)}
                r={t.train === picked ? 6 : 2.8}
                fill={tone(t.delay_min)}
                stroke={t.train === picked ? "#0f172a" : "none"}
                strokeWidth={2}
                className="cursor-pointer transition-all duration-1000"
                onClick={() => onPick(t)}
              >
                <title>{`${t.train} ${t.name} · ${t.delay_min ? `${t.delay_min} min late` : "on time"} · ${t.message}`}</title>
              </circle>
            ))}
          </svg>
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2">
              <Stat label="Running" value={trains.length.toLocaleString("en-IN")} sub={clock ? `${hhmm(clock.sim_now)} railway time` : undefined} />
              <Stat label="Late 15+ min" value={late.toLocaleString("en-IN")} sub={trains.length ? `${Math.round((late / trains.length) * 100)}%` : ""} />
            </div>
            <div className="flex gap-3 text-xs text-muted">
              <span><span className="mr-1 inline-block h-2 w-2 rounded-full bg-ok" />on time</span>
              <span><span className="mr-1 inline-block h-2 w-2 rounded-full bg-saffron-500" />15–59 min</span>
              <span><span className="mr-1 inline-block h-2 w-2 rounded-full bg-danger" />60+ min</span>
            </div>
            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted">Most delayed</h3>
              <ul className="space-y-1 text-sm">
                {worst.map((t) => (
                  <li key={t.train + t.origin_date}>
                    <button type="button" onClick={() => onPick(t)} className={cn("flex w-full items-center justify-between gap-2 rounded px-1.5 py-1 text-left hover:bg-canvas", t.train === picked && "bg-canvas")}>
                      <span className="truncate">
                        <span className="font-mono font-semibold">{t.train}</span> {t.name}
                      </span>
                      <span className="shrink-0 font-mono text-xs text-danger">+{t.delay_min}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      )}
    </Card>
  );
}

/** One run's running status, with a form to hold it at a station. */
export function TrainDesk(props: { train: string; originDate?: string; onHeld: () => void }) {
  const { status } = useLiveTrain(props.train, props.originDate);
  return (
    <div className="space-y-5">
      <TrainStatus {...props} />
      {status && (
        <div className="grid gap-5 xl:grid-cols-2">
          <WhatIfPanel status={status} onApplied={props.onHeld} />
          <IncidentPanel status={status} onDone={props.onHeld} />
        </div>
      )}
    </div>
  );
}

function TrainStatus({ train, originDate, onHeld }: { train: string; originDate?: string; onHeld: () => void }) {
  const { client } = useYatrax();
  const { status, error } = useLiveTrain(train, originDate);
  const [station, setStation] = useState("");
  const [minutes, setMinutes] = useState(15);
  const [reason, setReason] = useState("Held for a crossing");
  const [msg, setMsg] = useState<{ tone: "info" | "danger"; text: string } | null>(null);
  if (error) return <Alert>{error}</Alert>;
  if (!status) return <Spinner label="Loading the train" />;
  const departs = (status.stops ?? []).filter((s) => s.sched_dep && s.state !== "departed");
  const first = departs[0]?.station ?? "";
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-lg font-semibold">{status.train}</span>
        <h2 className="font-semibold">{status.name}</h2>
        <Badge>{status.origin_date}</Badge>
        <span className="ml-auto">
          <DelayBadge minutes={status.delay_min} state={status.state} />
        </span>
      </div>
      <p className="mt-2 text-sm">{status.message}</p>
      {departs.length > 0 && (
        <form
          className="mt-3 flex flex-wrap items-end gap-2 rounded-lg border border-line p-3"
          onSubmit={async (e) => {
            e.preventDefault();
            setMsg(null);
            try {
              const s = await client.addIncident(status.train, status.origin_date, station || first, minutes, reason);
              setMsg({ tone: "info", text: `Held. ${s.message}. Passengers are being told.` });
              onHeld();
            } catch (err) {
              setMsg({ tone: "danger", text: err instanceof ApiError ? err.message : "Could not hold the train" });
            }
          }}
        >
          <div>
            <Label htmlFor="hold-at">Hold at</Label>
            <select id="hold-at" value={station || first} onChange={(e) => setStation(e.target.value)} className="h-9 rounded-md border border-line bg-surface px-2 text-sm">
              {departs.map((s) => (
                <option key={s.station} value={s.station}>
                  {s.name} ({hhmm(s.exp_dep)})
                </option>
              ))}
            </select>
          </div>
          <div>
            <Label htmlFor="hold-min">Minutes</Label>
            <Input id="hold-min" type="number" min={1} max={1440} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} className="w-24" />
          </div>
          <div className="flex-1">
            <Label htmlFor="hold-why">Reason</Label>
            <Input id="hold-why" value={reason} onChange={(e) => setReason(e.target.value)} />
          </div>
          <Button type="submit" variant="secondary">
            Hold train
          </Button>
        </form>
      )}
      {msg && (
        <div className="mt-3">
          <Alert tone={msg.tone}>{msg.text}</Alert>
        </div>
      )}
      <div className="mt-4 max-h-[520px] overflow-y-auto pr-1">
        <LiveTimeline status={status} />
      </div>
    </Card>
  );
}

export function IncidentLog({ tick }: { tick: number }) {
  const { client } = useYatrax();
  const [rows, setRows] = useState<LiveIncident[] | null>(null);
  useEffect(() => {
    client.incidents().then(setRows).catch(() => setRows([]));
  }, [client, tick]);
  if (!rows || rows.length === 0) return null;
  return (
    <Card>
      <CardTitle hint="most recent first">Holds ordered by control</CardTitle>
      <table className="w-full text-sm">
        <tbody>
          {rows.slice(0, 10).map((r) => (
            <tr key={r.id} className="border-t border-line">
              <td className="py-1.5 font-mono font-semibold">{r.train}</td>
              <td>{r.origin_date}</td>
              <td className="font-mono">{r.station}</td>
              <td className="font-mono">+{r.delay_min} min</td>
              <td className="text-muted">{r.reason}</td>
              <td className="text-xs text-muted">{r.by}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}

/** The control room's live desk: clock, map, the picked train and the incident log. */
export function LiveOperations() {
  const [picked, setPicked] = useState<{ train: string; origin_date: string } | null>(null);
  const [tick, setTick] = useState(0);
  const pick = (train: string, origin_date: string) => setPicked({ train, origin_date });
  return (
    <div className="space-y-5">
      <ClockControls />
      <LiveMap picked={picked?.train} onPick={(t) => pick(t.train, t.origin_date)} />
      <SectionChartCard picked={picked?.train} onPick={pick} />
      {picked ? (
        <TrainDesk key={picked.train + picked.origin_date + tick} train={picked.train} originDate={picked.origin_date} onHeld={() => setTick((n) => n + 1)} />
      ) : (
        <p className="text-sm text-muted">Click a train on the map or the section chart to see its running status, simulate a hold, or handle an incident.</p>
      )}
      <div className="grid gap-5 xl:grid-cols-2">
        <ConflictsCard />
        <WaitlistsCard />
      </div>
      <SpecialsCard />
      <DisruptionLog tick={tick} />
      <IncidentLog tick={tick} />
      <ComplaintQueue title="RailMadad — all complaints" hint="whole network" />
      <ReplayCard onPick={pick} />
      <ChaosCard />
    </div>
  );
}

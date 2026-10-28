"use client";

import type { DisruptionResult, DisruptionRow, LiveStatus, LongWaitlist, NetworkConflict, Ripple, SectionChart } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, Spinner, cn, hhmm, useYatrax } from "@yatrax/ui";
import { useEffect, useMemo, useState } from "react";

const err = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

// ── The section chart: the controller's time-distance "master chart" ───────

const W = 960;
const H = 440;
const L = 130; // room for station names
const T = 16;
const B = 28;

export function SectionChartCard({ onPick, picked }: { onPick: (train: string, originDate: string) => void; picked?: string }) {
  const { client } = useYatrax();
  const [from, setFrom] = useState("MAS");
  const [to, setTo] = useState("SA");
  const [q, setQ] = useState({ from: "MAS", to: "SA" });
  const [chart, setChart] = useState<SectionChart | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      client
        .sectionChart(q.from, q.to, 4)
        .then((c) => alive && (setChart(c), setError(null)))
        .catch((e) => alive && setError(err(e, "Section unavailable")));
    load();
    const t = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [client, q]);

  const geo = useMemo(() => {
    if (!chart) return null;
    const t0 = new Date(chart.start).getTime();
    const t1 = new Date(chart.end).getTime();
    const maxKm = chart.stations[chart.stations.length - 1]?.km || 1;
    const x = (iso: string) => L + ((new Date(iso).getTime() - t0) / (t1 - t0)) * (W - L - 10);
    const y = (km: number) => T + (km / maxKm) * (H - T - B);
    const hours: number[] = [];
    for (let h = Math.ceil(t0 / 3600_000) * 3600_000; h <= t1; h += 3600_000) hours.push(h);
    return { x, y, t0, t1, hours };
  }, [chart]);

  return (
    <Card>
      <CardTitle hint="time → · distance ↓ · solid = expected, dashed = timetable">Section chart</CardTitle>
      <form className="mb-3 flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); setQ({ from: from.trim().toUpperCase(), to: to.trim().toUpperCase() }); }}>
        <div>
          <Label htmlFor="sec-from">From</Label>
          <Input id="sec-from" value={from} onChange={(e) => setFrom(e.target.value)} className="w-24" />
        </div>
        <div>
          <Label htmlFor="sec-to">To</Label>
          <Input id="sec-to" value={to} onChange={(e) => setTo(e.target.value)} className="w-24" />
        </div>
        <Button type="submit" size="sm" variant="secondary">
          Show section
        </Button>
        {chart && <span className="text-xs text-muted">{chart.trains.length} trains · {chart.stations.length} stations</span>}
      </form>
      {error && <Alert>{error}</Alert>}
      {!chart || !geo ? (
        !error && <Spinner label="Drawing the section" />
      ) : (
        <div className="overflow-x-auto">
          <svg viewBox={`0 0 ${W} ${H}`} className="min-w-[720px] rounded-lg bg-canvas" role="img" aria-label={`Section chart ${chart.from} to ${chart.to}`}>
            {chart.stations.map((s) => (
              <g key={s.code}>
                <line x1={L} x2={W - 10} y1={geo.y(s.km)} y2={geo.y(s.km)} stroke="#e2e8f0" />
                <text x={L - 6} y={geo.y(s.km) + 3} textAnchor="end" fontSize="10" fill="#64748b">
                  {s.code} {s.name.length > 14 ? s.name.slice(0, 13) + "…" : s.name}
                </text>
              </g>
            ))}
            {geo.hours.map((h) => (
              <g key={h}>
                <line x1={geo.x(new Date(h).toISOString())} x2={geo.x(new Date(h).toISOString())} y1={T} y2={H - B} stroke="#e2e8f0" strokeDasharray="2 3" />
                <text x={geo.x(new Date(h).toISOString())} y={H - 10} textAnchor="middle" fontSize="10" fill="#64748b">
                  {hhmm(new Date(h))}
                </text>
              </g>
            ))}
            {chart.trains.map((p) => {
              const sched: string[] = [];
              const exp: string[] = [];
              for (const pt of p.points) {
                for (const tt of [pt.sched_arr, pt.sched_dep]) if (tt) sched.push(`${geo.x(tt)},${geo.y(pt.km)}`);
                for (const tt of [pt.exp_arr, pt.exp_dep]) if (tt) exp.push(`${geo.x(tt)},${geo.y(pt.km)}`);
              }
              const color = p.state === "cancelled" ? "#94a3b8" : p.delay_min >= 60 ? "#b91c1c" : p.delay_min >= 15 ? "#f29a0e" : p.direction === "down" ? "#0f766e" : "#2348b5";
              const sel = p.train === picked;
              const nowX = geo.x(chart.sim_now);
              const first = p.points[0];
              const firstT = first?.exp_dep ?? first?.exp_arr;
              return (
                <g key={p.train + p.origin_date} className="cursor-pointer" onClick={() => onPick(p.train, p.origin_date)}>
                  <title>{`${p.train} ${p.name} · ${p.delay_min ? `${p.delay_min} min late` : "on time"}`}</title>
                  <polyline points={sched.join(" ")} fill="none" stroke="#94a3b8" strokeWidth={0.8} strokeDasharray="3 3" />
                  <polyline points={exp.join(" ")} fill="none" stroke={color} strokeWidth={sel ? 3.5 : 1.6} strokeDasharray={p.state === "cancelled" ? "1 4" : undefined} />
                  {p.now_km !== undefined && p.now_km !== null && <circle cx={nowX} cy={geo.y(p.now_km)} r={sel ? 6 : 4} fill={color} stroke="#fff" strokeWidth={1.5} />}
                  {firstT && (
                    <text x={Math.max(L + 2, geo.x(firstT) + 2)} y={geo.y(first!.km) + (p.direction === "down" ? -4 : 12)} fontSize="9" fill={color}>
                      {p.train}
                    </text>
                  )}
                </g>
              );
            })}
            <line x1={geo.x(chart.sim_now)} x2={geo.x(chart.sim_now)} y1={T} y2={H - B} stroke="#b91c1c" strokeWidth={1.5} />
            <text x={geo.x(chart.sim_now) + 3} y={T + 10} fontSize="10" fill="#b91c1c">
              now {hhmm(chart.sim_now)}
            </text>
          </svg>
        </div>
      )}
    </Card>
  );
}

// ── What-if: hold a train, see the ripple, then decide ─────────────────────

export function WhatIfPanel({ status, onApplied }: { status: LiveStatus; onApplied: () => void }) {
  const { client } = useYatrax();
  const departs = (status.stops ?? []).filter((s) => s.sched_dep && (s.state === "upcoming" || s.state === "here"));
  const [station, setStation] = useState(departs[0]?.station ?? "");
  const [minutes, setMinutes] = useState(30);
  const [ripple, setRipple] = useState<Ripple | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  if (departs.length === 0 || status.disruption === "cancelled") return null;
  const run = async () => {
    setBusy(true);
    setMsg(null);
    try {
      setRipple(await client.whatIf(status.train, status.origin_date, station || departs[0]!.station, minutes));
    } catch (e) {
      setMsg(err(e, "Simulation failed"));
    } finally {
      setBusy(false);
    }
  };
  const apply = async () => {
    try {
      await client.addIncident(status.train, status.origin_date, station || departs[0]!.station, minutes, `Held ${minutes} min by control`);
      setMsg(`Hold applied. ${ripple?.total_passengers ?? 0} passengers on the affected trains will be told by their alerts.`);
      setRipple(null);
      onApplied();
    } catch (e) {
      setMsg(err(e, "Could not apply the hold"));
    }
  };
  return (
    <Card>
      <CardTitle hint="nothing changes until you apply it">What if we hold {status.train}?</CardTitle>
      <div className="flex flex-wrap items-end gap-2">
        <div>
          <Label htmlFor="wi-at">Hold at</Label>
          <select id="wi-at" value={station} onChange={(e) => { setStation(e.target.value); setRipple(null); }} className="h-9 rounded-md border border-line bg-surface px-2 text-sm">
            {departs.map((s) => (
              <option key={s.station} value={s.station}>
                {s.name} ({hhmm(s.exp_dep)})
              </option>
            ))}
          </select>
        </div>
        <div>
          <Label htmlFor="wi-min">Minutes</Label>
          <Input id="wi-min" type="number" min={1} max={600} value={minutes} onChange={(e) => { setMinutes(Number(e.target.value)); setRipple(null); }} className="w-24" />
        </div>
        <Button onClick={run} disabled={busy} variant="secondary">
          Simulate
        </Button>
        {ripple && (
          <Button onClick={apply} variant="danger">
            Apply this hold
          </Button>
        )}
      </div>
      {msg && <p className="mt-2 text-sm text-muted">{msg}</p>}
      {ripple && (
        <div className="mt-3 space-y-2 text-sm">
          <p>
            {ripple.train} would reach its destination at <b>{hhmm(ripple.arrival_after)}</b> instead of {hhmm(ripple.arrival_now)}.{" "}
            {ripple.affected.length === 0 ? (
              <span className="text-ok">No other train is delayed.</span>
            ) : (
              <span className="text-danger">
                {ripple.affected.length} other trains lose {ripple.total_added_min} minutes in all; {ripple.total_passengers.toLocaleString("en-IN")} booked passengers affected.
              </span>
            )}
          </p>
          {ripple.affected.length > 0 && (
            <table className="w-full text-xs">
              <thead className="text-left text-muted">
                <tr>
                  <th className="py-1">Train</th>
                  <th>Delay</th>
                  <th>Why</th>
                  <th>Pax</th>
                </tr>
              </thead>
              <tbody>
                {ripple.affected.map((e) => (
                  <tr key={e.train + e.origin_date} className="border-t border-line">
                    <td className="py-1">
                      <span className="font-mono font-semibold">{e.train}</span> {e.name.slice(0, 22)}
                      {e.hop > 1 && <Badge className="ml-1">knock-on ×{e.hop}</Badge>}
                    </td>
                    <td className="font-mono text-danger">+{e.added_min}</td>
                    <td>{e.cause}</td>
                    <td className="font-mono">{e.passengers}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </Card>
  );
}

// ── Incidents: cancel, cut short or divert, with refunds and rebooking ─────

export function IncidentPanel({ status, onDone }: { status: LiveStatus; onDone: () => void }) {
  const { client } = useYatrax();
  const stops = status.stops ?? [];
  const middle = stops.slice(1, -1).filter((s) => s.state === "upcoming");
  const [kind, setKind] = useState<"cancelled" | "short_terminated" | "diverted">("short_terminated");
  const [at, setAt] = useState(middle[0]?.station ?? "");
  const [skip, setSkip] = useState<string[]>([]);
  const [extra, setExtra] = useState(60);
  const [reason, setReason] = useState("Engine failure");
  const [res, setRes] = useState<DisruptionResult | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (status.disruption) {
    return (
      <Card>
        <CardTitle>Incident</CardTitle>
        <Alert tone="info">This run is already {status.disruption.replace("_", " ")}: {status.reason}</Alert>
      </Card>
    );
  }
  const go = async () => {
    const what = kind === "cancelled" ? "Cancel this run" : kind === "short_terminated" ? `Short-terminate at ${at}` : `Divert, skipping ${skip.join(", ")}`;
    if (!window.confirm(`${what}? Every affected passenger is refunded and told at once.`)) return;
    setBusy(true);
    setMsg(null);
    try {
      setRes(await client.disruptRun(status.train, status.origin_date, { kind, station: at, skip, extra_delay: extra, reason }));
      onDone();
    } catch (e) {
      setMsg(err(e, "Could not apply the incident"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card>
      <CardTitle hint="passengers are refunded and offered another train automatically">Incident on {status.train}</CardTitle>
      <div className="mb-2 flex flex-wrap gap-1.5">
        {(
          [
            ["short_terminated", "Short-terminate"],
            ["diverted", "Divert"],
            ["cancelled", "Cancel"],
          ] as const
        ).map(([k, label]) => (
          <Button key={k} size="sm" variant={kind === k ? "primary" : "secondary"} onClick={() => setKind(k)}>
            {label}
          </Button>
        ))}
      </div>
      <div className="flex flex-wrap items-end gap-2">
        {kind === "short_terminated" && (
          <div>
            <Label htmlFor="inc-at">Terminate at</Label>
            <select id="inc-at" value={at} onChange={(e) => setAt(e.target.value)} className="h-9 rounded-md border border-line bg-surface px-2 text-sm">
              {middle.map((s) => (
                <option key={s.station} value={s.station}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        )}
        {kind === "diverted" && (
          <div className="w-full">
            <Label>Stations the diversion misses</Label>
            <div className="flex flex-wrap gap-1">
              {middle.map((s) => (
                <button key={s.station} type="button" onClick={() => setSkip((xs) => (xs.includes(s.station) ? xs.filter((x) => x !== s.station) : [...xs, s.station]))}
                  className={cn("rounded border px-2 py-0.5 font-mono text-xs", skip.includes(s.station) ? "border-danger bg-red-50 text-danger" : "border-line")}>
                  {s.station}
                </button>
              ))}
            </div>
            <div className="mt-2 w-40">
              <Label htmlFor="inc-extra">Extra minutes on the longer route</Label>
              <Input id="inc-extra" type="number" min={0} max={600} value={extra} onChange={(e) => setExtra(Number(e.target.value))} />
            </div>
          </div>
        )}
        <div className="min-w-[14rem] flex-1">
          <Label htmlFor="inc-why">Reason (passengers see it)</Label>
          <Input id="inc-why" value={reason} onChange={(e) => setReason(e.target.value)} />
        </div>
        <Button variant="danger" onClick={go} disabled={busy || !reason.trim() || (kind === "diverted" && skip.length === 0) || (kind === "short_terminated" && !at)}>
          Apply
        </Button>
      </div>
      {msg && <Alert>{msg}</Alert>}
      {res && (
        <div className="mt-3 text-sm">
          <Alert tone="info">
            {res.bookings} bookings affected · ₹{res.refunded.toLocaleString("en-IN")} refunded · {res.rebooked} offered seats on another train
            {res.no_alternative > 0 && ` · ${res.no_alternative} with no alternative`}
            {res.simulated > 0 && ` · ${res.simulated} simulated bookings released`}
          </Alert>
          <ul className="mt-2 space-y-1">
            {res.affected.slice(0, 8).map((a) => (
              <li key={a.booking_id} className="text-xs">
                PNR {a.pnr}: ₹{a.refund} {a.partial ? "for the distance not travelled" : "refunded"}
                {a.alternative && ` · held ${a.alternative.status} on ${a.alternative.train} (${hhmm(a.alternative.departure)})`}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Card>
  );
}

// ── Network views ──────────────────────────────────────────────────────────

export function ConflictsCard() {
  const { client } = useYatrax();
  const [zone, setZone] = useState("");
  const [list, setList] = useState<NetworkConflict[] | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () => client.networkConflicts(zone || undefined, 2).then((l) => alive && setList(l)).catch(() => alive && setList([]));
    load();
    const t = setInterval(load, 30000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [client, zone]);
  return (
    <Card>
      <CardTitle hint={<Input aria-label="Zone" placeholder="zone, e.g. SR" value={zone} onChange={(e) => setZone(e.target.value.toUpperCase())} className="h-7 w-28 text-xs" />}>
        Platform conflicts · next 2 h
      </CardTitle>
      {!list ? (
        <Spinner label="Checking major stations" />
      ) : list.length === 0 ? (
        <p className="text-sm text-muted">No clashes at major stations.</p>
      ) : (
        <ul className="max-h-72 space-y-1 overflow-y-auto text-sm">
          {list.slice(0, 40).map((c) => (
            <li key={c.station + c.platform + c.a + c.b} className="border-t border-line py-1">
              <span className="font-mono font-semibold">{c.station}</span> PF {c.platform}: {c.a} and {c.b} overlap {c.overlap_min} min
              {c.suggest > 0 && <span className="text-muted"> · suggest {c.b} to PF {c.suggest} (station master)</span>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

export function WaitlistsCard() {
  const { client } = useYatrax();
  const [list, setList] = useState<LongWaitlist[] | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const load = () => client.longWaitlists(30).then(setList).catch(() => setList([]));
  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);
  const add = async (w: LongWaitlist) => {
    setMsg(null);
    try {
      const r = await client.addExtraCoaches(w.train, w.origin_date, w.class, 1);
      setMsg(`${w.train} ${w.origin_date}: coach ${r.coaches.join(", ")} added · ${r.promotions.length} passengers moved up · ${r.still_waitlisted} still waitlisted`);
      load();
    } catch (e) {
      setMsg(err(e, "Could not add a coach"));
    }
  };
  return (
    <Card>
      <CardTitle hint="before the chart">Longest waitlists</CardTitle>
      {msg && <Alert tone="info">{msg}</Alert>}
      {!list ? (
        <Spinner label="Loading waitlists" />
      ) : list.length === 0 ? (
        <p className="text-sm text-muted">No waitlists in the coming days.</p>
      ) : (
        <table className="w-full text-sm">
          <tbody>
            {list.map((w) => (
              <tr key={w.train + w.origin_date + w.class} className="border-t border-line">
                <td className="py-1.5">
                  <span className="font-mono font-semibold">{w.train}</span> {w.name.slice(0, 20)}
                  <div className="text-xs text-muted">
                    {w.origin_date} · {w.class}
                    {w.extra_coaches > 0 && ` · ${w.extra_coaches} extra coach${w.extra_coaches > 1 ? "es" : ""} added`}
                  </div>
                </td>
                <td className="font-mono text-danger">WL {w.waitlisted}</td>
                <td className="text-right">
                  <Button size="sm" variant="secondary" onClick={() => add(w)}>
                    Add {w.class} coach
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

export function DisruptionLog({ tick }: { tick: number }) {
  const { client } = useYatrax();
  const [rows, setRows] = useState<DisruptionRow[]>([]);
  useEffect(() => {
    client.disruptions().then(setRows).catch(() => setRows([]));
  }, [client, tick]);
  if (rows.length === 0) return null;
  return (
    <Card>
      <CardTitle>Incidents</CardTitle>
      <ul className="space-y-1 text-sm">
        {rows.slice(0, 12).map((r) => (
          <li key={r.train + r.origin_date} className="border-t border-line py-1">
            <span className="font-mono font-semibold">{r.train}</span> {r.origin_date}:{" "}
            <Badge tone="danger">{r.kind.replace("_", " ")}</Badge> {r.station && `at ${r.station}`} {r.skip.length > 0 && `skipping ${r.skip.join(", ")}`} · {r.reason}
            <span className="text-xs text-muted"> · {r.by}</span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

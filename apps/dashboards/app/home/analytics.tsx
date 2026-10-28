"use client";

import type {
  AnalyticsOccupancy, AnalyticsOverview, AnalyticsPunctuality, AnalyticsScope, AnalyticsZones, FraudReport, OccupancyCell, PunctualityRow,
} from "@yatrax/api-client";
import { Alert, Badge, Card, CardTitle, Spinner, Stat, cn, useAsync, useYatrax } from "@yatrax/ui";
import { useState, type ReactNode } from "react";

// One data hue for single-series marks, and a validated five-step ordinal ramp of it for
// the occupancy heatmap (light end clears the surface at 2:1).
const MARK = "#2a78d6";
const RAMP = ["#86b6ef", "#5598e7", "#256abf", "#184f95", "#0d366b"];
const BUCKETS = ["under 50%", "50–70%", "70–85%", "85–95%", "95% and over"];
const bucket = (load: number) => (load < 0.5 ? 0 : load < 0.7 ? 1 : load < 0.85 ? 2 : load < 0.95 ? 3 : 4);

const inr = (n: number) => "₹" + n.toLocaleString("en-IN");
const crore = (n: number) => (n >= 1e7 ? `₹${(n / 1e7).toFixed(2)} Cr` : n >= 1e5 ? `₹${(n / 1e5).toFixed(1)} L` : inr(n));
const pct = (x: number, digits = 0) => `${(x * 100).toFixed(digits)}%`;
const num = (n: number) => n.toLocaleString("en-IN");
const shortDate = (iso: string) => new Date(iso.length === 10 ? iso + "T00:00:00+05:30" : iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "Asia/Kolkata" });

// ── Hover tooltip shared by every chart ────────────────────────────────────

function useTip() {
  const [tip, setTip] = useState<{ x: number; y: number; body: ReactNode } | null>(null);
  const bind = (body: ReactNode) => ({
    onMouseMove: (e: React.MouseEvent) => setTip({ x: e.clientX, y: e.clientY, body }),
    onMouseLeave: () => setTip(null),
  });
  const node = tip && (
    <div
      role="tooltip"
      className="pointer-events-none fixed z-50 max-w-64 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs shadow-lg"
      style={{ left: tip.x + 14, top: tip.y + 14 }}
    >
      {tip.body}
    </div>
  );
  return { bind, node };
}

/** Horizontal bars, one series; the value is written beside each bar in ink. */
function BarList<T>({ rows, value, label, format, detail }: {
  rows: T[]; value: (r: T) => number; label: (r: T) => ReactNode; format: (n: number) => string; detail?: (r: T) => ReactNode;
}) {
  const { bind, node } = useTip();
  const top = Math.max(1, ...rows.map(value));
  if (rows.length === 0) return <p className="text-sm text-muted">Nothing to show.</p>;
  return (
    <div className="space-y-1.5">
      {rows.map((r, i) => (
        <div key={i} className="grid grid-cols-[minmax(6rem,11rem)_1fr_auto] items-center gap-2 text-sm" {...bind(detail ? detail(r) : format(value(r)))}>
          <div className="truncate">{label(r)}</div>
          <div className="h-3.5 rounded-sm bg-canvas">
            <div className="h-full rounded-r" style={{ width: `${(value(r) / top) * 100}%`, background: MARK }} />
          </div>
          <div className="w-20 text-right tabular-nums text-muted">{format(value(r))}</div>
        </div>
      ))}
      {node}
    </div>
  );
}

/** Vertical columns over time, one series; only the peak is labelled. */
function Columns<T>({ rows, value, x, format, detail, label, max }: {
  rows: T[]; value: (r: T) => number; x: (r: T) => string; format: (n: number) => string; detail: (r: T) => ReactNode;
  label: (r: T) => string; // the same facts as the tooltip, for screen readers
  max?: number;
}) {
  const { bind, node } = useTip();
  if (rows.length === 0) return <p className="text-sm text-muted">Nothing to show.</p>;
  const top = max ?? Math.max(1, ...rows.map(value));
  const peak = rows.reduce((b, r) => (value(r) > value(b) ? r : b));
  const every = Math.ceil(rows.length / 8);
  return (
    <div>
      <div className="relative mt-4 flex h-40 items-end gap-[2px] border-b border-line">
        {max !== undefined && <span className="absolute -top-4 left-0 text-[11px] text-muted">{format(max)}</span>}
        {rows.map((r, i) => (
          <div key={i} role="img" aria-label={label(r)} className="group relative flex h-full flex-1 items-end" {...bind(detail(r))}>
            <div
              className="w-full rounded-t opacity-90 group-hover:opacity-100"
              style={{ height: `${(value(r) / top) * 100}%`, minHeight: value(r) > 0 ? 2 : 0, background: MARK }}
            />
            {r === peak && (
              <span className="absolute left-1/2 -translate-x-1/2 text-[11px] font-medium tabular-nums" style={{ bottom: `calc(${(value(r) / top) * 100}% + 2px)` }}>
                {format(value(r))}
              </span>
            )}
          </div>
        ))}
      </div>
      <div className="relative mt-1 h-4 text-[10px] text-muted">
        {rows.map((r, i) =>
          i % every === 0 ? (
            <span key={i} className="absolute -translate-x-1/2 whitespace-nowrap" style={{ left: `${((i + 0.5) / rows.length) * 100}%` }}>
              {x(r)}
            </span>
          ) : null,
        )}
      </div>
      {node}
    </div>
  );
}

// ── Revenue ────────────────────────────────────────────────────────────────

const quotaName: Record<string, string> = { GN: "General", TQ: "Tatkal", PT: "Premium Tatkal", LD: "Ladies", SS: "Senior citizen" };

function Revenue({ sc }: { sc: AnalyticsScope }) {
  const { client } = useYatrax();
  const { data: o, error } = useAsync<AnalyticsOverview>(() => client.analyticsOverview(sc), [client, sc.zone, sc.division]);
  if (error) return <Alert>{error}</Alert>;
  if (!o) return <Spinner label="Adding up the ledger" />;
  const k = o.kpis;
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Net revenue" value={crore(k.net)} sub={`${crore(k.gross)} fares − ${crore(k.refunds)} refunds`} />
        <Stat label="Passengers" value={num(k.passengers)} sub={`${num(k.bookings)} bookings · avg fare ${inr(k.avg_fare)}`} />
        <Stat label="Berth at booking" value={pct(k.booked_confirm)} sub={`${num(k.waitlisted)} still waitlisted`} />
        <Stat label="Cancelled" value={num(k.cancelled)} sub={`${pct(k.cancelled / Math.max(1, k.passengers), 1)} of passengers`} />
        <Stat label="Tatkal share" value={pct(k.tatkal_share, 1)} sub="of fares" />
        <Stat label="TTE excess fares" value={inr(k.penalties)} sub="fares + penalties collected on board" />
        <Stat label="Food sales" value={inr(k.food_sales)} sub={`${inr(k.food_commission)} commission`} />
        <Stat
          label="Complaints"
          value={num(o.complaints.total)}
          sub={`${num(o.complaints.open)} open${o.complaints.urgent_open ? ` (${o.complaints.urgent_open} urgent)` : ""} · resolved in ${o.complaints.avg_resolve_min.toFixed(0)} min on average`}
        />
      </div>
      <Card>
        <CardTitle hint="Passengers booked, by the day they travel">Demand by travel date</CardTitle>
        <Columns
          rows={o.by_travel_date}
          value={(d) => d.passengers}
          x={(d) => shortDate(d.date)}
          format={num}
          label={(d) => `${shortDate(d.date)}: ${num(d.passengers)} passengers, ${crore(d.revenue)}`}
          detail={(d) => (
            <>
              <div className="font-semibold">{shortDate(d.date)}</div>
              <div>{num(d.passengers)} passengers</div>
              <div className="text-muted">{crore(d.revenue)} in fares</div>
            </>
          )}
        />
      </Card>
      <div className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardTitle hint="Fares">By class</CardTitle>
          <BarList rows={o.by_class} value={(r) => r.revenue} label={(r) => r.key} format={crore} detail={(r) => `${r.key}: ${crore(r.revenue)} · ${num(r.passengers)} passengers`} />
        </Card>
        <Card>
          <CardTitle hint="Fares">By quota</CardTitle>
          <BarList rows={o.by_quota} value={(r) => r.revenue} label={(r) => quotaName[r.key] ?? r.key} format={crore} detail={(r) => `${quotaName[r.key] ?? r.key}: ${crore(r.revenue)} · ${num(r.passengers)} passengers`} />
        </Card>
        <Card>
          <CardTitle hint="Fares">Top trains</CardTitle>
          <BarList
            rows={o.top_trains}
            value={(r) => r.revenue}
            label={(r) => <><span className="font-mono">{r.number}</span> <span className="text-muted">{r.name}</span></>}
            format={crore}
            detail={(r) => `${r.number} ${r.name}: ${crore(r.revenue)} · ${num(r.passengers)} passengers`}
          />
        </Card>
        <Card className="lg:col-span-2">
          <CardTitle hint={`acknowledged in ${o.complaints.avg_ack_min.toFixed(0)} min, resolved in ${o.complaints.avg_resolve_min.toFixed(0)} min on average`}>
            Complaints (RailMadad)
          </CardTitle>
          {o.complaints.by_category.length === 0 ? (
            <p className="text-sm text-muted">No complaints in this area.</p>
          ) : (
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted">
                <tr>
                  <th className="py-1">Category</th>
                  <th className="py-1">Department</th>
                  <th className="py-1 text-right">Reported</th>
                  <th className="py-1 text-right">Still open</th>
                  <th className="py-1 text-right">Avg time to resolve</th>
                </tr>
              </thead>
              <tbody>
                {o.complaints.by_category.map((c) => (
                  <tr key={c.key} className="border-t border-line">
                    <td className="py-1">{c.key.replace("_", " ")}</td>
                    <td className="py-1 text-muted">{c.department}</td>
                    <td className="py-1 text-right tabular-nums">{num(c.count)}</td>
                    <td className={cn("py-1 text-right tabular-nums", c.open > 0 && "text-danger")}>{num(c.open)}</td>
                    <td className="py-1 text-right tabular-nums">{c.avg_resolve_min ? `${c.avg_resolve_min.toFixed(0)} min` : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
        <Card>
          <CardTitle hint="Passengers">Busiest journeys</CardTitle>
          <BarList
            rows={o.top_routes}
            value={(r) => r.passengers}
            label={(r) => <span className="font-mono">{r.from} → {r.to}</span>}
            format={num}
            detail={(r) => `${r.from} → ${r.to}: ${num(r.passengers)} passengers · ${crore(r.revenue)}`}
          />
        </Card>
      </div>
    </div>
  );
}

// ── Occupancy ──────────────────────────────────────────────────────────────

function HeatCell({ c, bind }: { c: OccupancyCell; bind: (b: ReactNode) => object }) {
  const date = shortDate(c.date);
  if (!c.runs) return <td className="p-[1px]"><div role="img" aria-label={`Doesn't run on ${date}`} className="h-7 rounded-sm" title={`Doesn't run on ${date}`} /></td>;
  if (c.load === undefined)
    return (
      <td className="p-[1px]">
        <div role="img" aria-label={`${date}: not open for booking yet`} className="h-7 rounded-sm border border-dashed border-line" {...bind(<><div className="font-semibold">{date}</div><div className="text-muted">Not open for booking yet</div></>)} />
      </td>
    );
  const b = bucket(c.load);
  return (
    <td className="p-[1px]">
      <div
        role="img"
        aria-label={`${date}: ${pct(c.load)} of berths sold, ${c.bookings} bookings${c.wl ? `, ${c.wl} waitlisted` : ""}`}
        className={cn("flex h-7 items-center justify-center rounded-sm text-[10px] tabular-nums", b >= 2 ? "text-white" : "text-ink")}
        style={{ background: RAMP[b] }}
        {...bind(
          <>
            <div className="font-semibold">{date}</div>
            <div>{pct(c.load)} of berths sold</div>
            <div className="text-muted">{num(c.bookings)} bookings{c.wl ? ` · ${c.wl} waitlisted` : ""}</div>
          </>,
        )}
      >
        {Math.round(c.load * 100)}
      </div>
    </td>
  );
}

function Occupancy({ sc }: { sc: AnalyticsScope }) {
  const { client } = useYatrax();
  const [days, setDays] = useState(14);
  const { data: o, error } = useAsync<AnalyticsOccupancy>(() => client.analyticsOccupancy(sc, days), [client, sc.zone, sc.division, days]);
  const { bind, node } = useTip();
  if (error) return <Alert>{error}</Alert>;
  if (!o) return <Spinner label="Counting berths" />;
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Average occupancy" value={pct(o.avg_load)} sub={`${num(o.runs)} runs open for booking`} />
        <Stat label="Runs over 90%" value={num(o.over_90)} />
        <Stat label="Waitlisted" value={num(o.waitlisted)} sub="passengers on these runs" />
        <Stat label="Trains" value={num(o.trains)} sub="with a run open" />
      </div>
      <Card>
        <CardTitle
          hint={
            <span className="inline-flex items-center gap-1">
              {[7, 14, 30].map((d) => (
                <button key={d} onClick={() => setDays(d)} className={cn("rounded px-2 py-0.5", d === days ? "bg-canvas font-semibold text-ink" : "hover:text-ink")}>
                  {d} days
                </button>
              ))}
            </span>
          }
        >
          Occupancy by run
        </CardTitle>
        <p className="mb-3 text-xs text-muted">
          Share of berth-segments sold (a berth sold Chennai–Salem and again Salem–Ernakulam is full). Trains with the fullest runs first
          {o.trains > o.max_rows ? `; top ${o.max_rows} of ${o.trains}` : ""}.
        </p>
        {o.heatmap.length === 0 ? (
          <p className="text-sm text-muted">No runs open for booking in this period.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-separate border-spacing-0 text-xs">
              <thead>
                <tr>
                  <th className="sticky left-0 bg-surface pr-2 text-left font-medium text-muted">Train</th>
                  {o.dates.map((d) => (
                    <th key={d} className="min-w-9 px-[1px] pb-1 text-center font-normal text-muted">{shortDate(d)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {o.heatmap.map((r) => (
                  <tr key={r.number}>
                    <td className="sticky left-0 max-w-44 truncate bg-surface pr-2">
                      <span className="font-mono">{r.number}</span> <span className="text-muted">{r.name}</span>
                    </td>
                    {r.cells.map((c) => <HeatCell key={c.date} c={c} bind={bind} />)}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-3 text-xs text-muted">
          {BUCKETS.map((b, i) => (
            <span key={b} className="inline-flex items-center gap-1">
              <span className="h-3 w-4 rounded-sm" style={{ background: RAMP[i] }} /> {b}
            </span>
          ))}
          <span className="inline-flex items-center gap-1"><span className="h-3 w-4 rounded-sm border border-dashed border-line" /> not open yet</span>
          <span>blank: doesn't run that day</span>
        </div>
        {node}
      </Card>
      <div className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardTitle hint="Across the runs above">By class</CardTitle>
          <BarList rows={o.by_class} value={(r) => r.load} label={(r) => `${r.class} · ${r.name}`} format={(x) => pct(x)} detail={(r) => `${r.name}: ${pct(r.load, 1)} of ${num(r.berths)} berths`} />
        </Card>
        <Card>
          <CardTitle>Fullest runs</CardTitle>
          <table className="w-full text-sm">
            <tbody>
              {o.busiest.map((r) => (
                <tr key={r.number + r.date} className="border-t border-line first:border-0">
                  <td className="py-1"><span className="font-mono">{r.number}</span> <span className="text-muted">{r.name}</span></td>
                  <td className="py-1 text-muted">{shortDate(r.date)}</td>
                  <td className="py-1 text-right tabular-nums">{pct(r.load)}</td>
                  <td className="py-1 text-right">{r.wl > 0 && <Badge tone="danger">WL {r.wl}</Badge>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      </div>
    </div>
  );
}

// ── Punctuality ────────────────────────────────────────────────────────────

function League({ rows, highlight, onTimeMin }: { rows: PunctualityRow[]; highlight?: string; onTimeMin: number }) {
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs text-muted">
        <tr>
          <th className="py-1">#</th>
          <th className="py-1"></th>
          <th className="py-1">On time (≤{onTimeMin} min)</th>
          <th className="py-1 text-right">Avg delay</th>
          <th className="py-1 text-right">&gt;1 h late</th>
          <th className="py-1 text-right">Runs</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={r.key} className={cn("border-t border-line", r.key === highlight && "bg-canvas font-semibold")}>
            <td className="py-1 text-muted">{i + 1}</td>
            <td className="py-1">{r.key.match(/^\d+$/) ? r.name : <><span className="font-mono">{r.key}</span> <span className="text-muted">{r.name}</span></>}</td>
            <td className="py-1">
              <div className="flex items-center gap-2">
                <div className="h-2.5 w-24 rounded-sm bg-canvas">
                  <div className="h-full rounded-r" style={{ width: pct(r.on_time), background: MARK }} />
                </div>
                <span className="tabular-nums">{pct(r.on_time, 1)}</span>
              </div>
            </td>
            <td className="py-1 text-right tabular-nums">{r.avg_delay.toFixed(1)} min</td>
            <td className="py-1 text-right tabular-nums">{num(r.late_60)}</td>
            <td className="py-1 text-right tabular-nums text-muted">{num(r.runs)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Punctuality({ sc, onTimeMin }: { sc: AnalyticsScope; onTimeMin: number }) {
  const { client } = useYatrax();
  const [days, setDays] = useState(7);
  const { data: p, error } = useAsync<AnalyticsPunctuality>(() => client.analyticsPunctuality(sc, days), [client, sc.zone, sc.division, days]);
  if (error) return <Alert>{error}</Alert>;
  if (!p) return <Spinner label="Replaying the runs" />;
  const s = p.summary;
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
        <span>
          Runs that left {shortDate(p.from)} – {shortDate(p.to)}, measured at their destination.
        </span>
        <span className="inline-flex gap-1">
          {[7, 14, 30].map((d) => (
            <button key={d} onClick={() => setDays(d)} className={cn("rounded px-2 py-0.5", d === days ? "bg-surface font-semibold text-ink shadow-sm" : "hover:text-ink")}>
              {d} days
            </button>
          ))}
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="On time" value={pct(s.on_time, 1)} sub={`within ${onTimeMin} min at the destination`} />
        <Stat label="Average delay" value={`${s.avg_delay.toFixed(1)} min`} sub="over all runs" />
        <Stat label="Over an hour late" value={num(s.late_60)} sub={`of ${num(s.runs)} runs`} />
        <Stat label="Cancelled" value={num(p.cancelled)} />
      </div>
      <Card>
        <CardTitle hint="Share of runs on time, by the day they left">Day by day</CardTitle>
        <Columns
          rows={p.daily}
          value={(d) => d.on_time}
          x={(d) => d.name}
          max={1}
          label={(d) => `${d.name}: ${pct(d.on_time, 1)} on time, average ${d.avg_delay.toFixed(1)} min late`}
          format={(x) => pct(x)}
          detail={(d) => (
            <>
              <div className="font-semibold">{d.name}</div>
              <div>{pct(d.on_time, 1)} on time</div>
              <div className="text-muted">avg {d.avg_delay.toFixed(1)} min · {d.runs} runs</div>
            </>
          )}
        />
      </Card>
      <Card>
        <CardTitle hint="All India">Zone league table</CardTitle>
        <League rows={p.zones} highlight={sc.zone} onTimeMin={onTimeMin} />
      </Card>
      {p.divisions.length > 0 && (
        <Card>
          <CardTitle hint={sc.zone}>Divisions</CardTitle>
          <League rows={p.divisions} highlight={String(sc.division)} onTimeMin={onTimeMin} />
        </Card>
      )}
      <div className="grid gap-5 lg:grid-cols-2">
        <Card className="lg:col-span-2">
          <CardTitle hint="Where late trains lost the most time">Why trains were late</CardTitle>
          <BarList rows={p.reasons.slice(0, 8)} value={(r) => r.count} label={(r) => r.key} format={(n) => `${n} runs`} />
        </Card>
        <Card>
          <CardTitle hint="Average delay at the destination">Least punctual trains</CardTitle>
          <TrainTable rows={p.worst} />
        </Card>
        <Card>
          <CardTitle hint="Most often on time">Most punctual trains</CardTitle>
          <TrainTable rows={p.best} />
        </Card>
      </div>
    </div>
  );
}

function TrainTable({ rows }: { rows: AnalyticsPunctuality["worst"] }) {
  if (rows.length === 0) return <p className="text-sm text-muted">Nothing to show.</p>;
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs text-muted">
        <tr>
          <th className="py-1">Train</th>
          <th className="py-1 text-right">On time</th>
          <th className="py-1 text-right">Avg</th>
          <th className="py-1 text-right">Worst</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.number} className="border-t border-line">
            <td className="max-w-52 truncate py-1"><span className="font-mono">{r.number}</span> <span className="text-muted">{r.name}</span></td>
            <td className="py-1 text-right tabular-nums">{pct(r.on_time)}</td>
            <td className="py-1 text-right tabular-nums">{r.avg_delay.toFixed(0)} min</td>
            <td className="py-1 text-right tabular-nums text-muted">{r.worst} min</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// ── Fraud (admins) ─────────────────────────────────────────────────────────

function Leads({ title, hint, head, rows, empty = "Nothing flagged." }: { title: string; hint?: string; head: string[]; rows: ReactNode[][]; empty?: string }) {
  return (
    <Card>
      <CardTitle hint={hint}>{title}</CardTitle>
      {rows.length === 0 ? (
        <p className="text-sm text-muted">{empty}</p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-muted">
            <tr>{head.map((h, i) => <th key={h} className={cn("py-1", i > 0 && "text-right")}>{h}</th>)}</tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i} className="border-t border-line">
                {r.map((c, j) => <td key={j} className={cn("py-1", j > 0 && "text-right tabular-nums")}>{c}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

function Fraud() {
  const { client } = useYatrax();
  const { data: f, error } = useAsync<FraudReport>(() => client.fraudReport(), [client]);
  if (error) return <Alert>{error}</Alert>;
  if (!f) return <Spinner label="Gathering leads" />;
  const b = f.bots;
  return (
    <div className="space-y-5">
      <p className="text-sm text-muted">Leads for the vigilance team from signals the system already records. A lead is something to look into, not proof.</p>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Tatkal sessions scored" value={num(b.checked)} sub={`${num(b.blocked)} blocked · ${num(b.challenged)} challenged`} />
        <Stat label="Payments unresolved" value={num(f.payments.unresolved)} sub="money taken, booking not settled" />
        <Stat label="Refunds failing" value={num(f.payments.refunds_failed)} />
        <Stat label="Paid after hold expired" value={num(f.payments.late_refunds)} sub="refunded in full" />
      </div>
      <div className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardTitle hint="Blocked and challenged sessions">Why the bot detector stepped in</CardTitle>
          <BarList rows={b.top_reasons} value={(r) => r.count} label={(r) => r.key} format={num} />
        </Card>
        <Leads
          title="Possible touts"
          hint="Same traveller on Tatkal from 3+ accounts"
          head={["Traveller", "Accounts", "Bookings"]}
          rows={f.touts.map((t) => [`${t.name}, ${t.age}`, t.accounts, t.bookings])}
        />
        <Leads
          title="Shared networks"
          hint="IP addresses used by 3+ accounts"
          head={["IP", "Accounts", "Blocked"]}
          rows={b.shared_ips.map((x) => [<span key="k" className="font-mono">{x.key}</span>, x.accounts, x.blocked])}
        />
        <Leads
          title="Shared devices"
          hint="Device fingerprints used by 3+ accounts"
          head={["Device", "Accounts", "Blocked"]}
          rows={b.shared_devices.map((x) => [<span key="k" className="font-mono">{x.key}</span>, x.accounts, x.blocked])}
        />
        <Leads
          title="Heavy Tatkal bookers"
          head={["Account", "Bookings", "Fares", "Bot flags"]}
          rows={f.tatkal_heavy.map((u) => [`${u.name} · ${u.phone}`, u.count, inr(u.amount), u.bot_flags || "—"])}
        />
        <Leads
          title="Frequent cancellers"
          hint="3+ cancelled bookings"
          head={["Account", "Cancelled", "Refunded"]}
          rows={f.cancellers.map((u) => [`${u.name} · ${u.phone}`, u.count, inr(u.amount)])}
        />
        <Leads
          title="Ticketless travel hot spots"
          head={["Train", "Ticketless", "Wrong class", "Collected"]}
          rows={f.fine_spots.map((x) => [<><span className="font-mono">{x.train}</span> <span className="text-muted">{x.name}</span></>, x.ticketless, x.wrong_class, inr(x.collected)])}
        />
        <Leads
          title="Cash collected by TTEs"
          hint="To reconcile against deposits"
          head={["TTE", "Receipts", "Collected"]}
          rows={f.tte_cash.map((x) => [`${x.name} · ${x.employee_id}`, x.fines, inr(x.collected)])}
        />
        <Leads
          title="Catering vendors to inspect"
          hint="Hygiene complaints or repeated failed orders"
          head={["Vendor", "Hygiene", "Rejected", "Missed", "Rating"]}
          rows={f.vendors.map((v) => [`${v.name} · ${v.station}`, v.hygiene_complaints, v.rejected, pct(v.missed_rate), `★ ${v.rating.toFixed(1)}`])}
        />
      </div>
    </div>
  );
}

// ── The dashboard ──────────────────────────────────────────────────────────

const sel = "h-9 rounded-lg border border-line bg-surface px-2 text-sm";

export function Analytics({ admin }: { admin: boolean }) {
  const { client } = useYatrax();
  const { data: z, error } = useAsync<AnalyticsZones>(() => client.analyticsZones(), [client]);
  const [picked, setPicked] = useState<AnalyticsScope | null>(null);
  const tabs = admin ? (["Revenue", "Occupancy", "Punctuality", "Fraud"] as const) : (["Revenue", "Occupancy", "Punctuality"] as const);
  const [tab, setTab] = useState<(typeof tabs)[number]>("Revenue");
  if (error) return <Alert>{error}</Alert>;
  if (!z) return <Spinner label="Loading zones" />;
  const sc = picked ?? { zone: z.home_zone, division: 0 };
  const zone = z.zones.find((x) => x.code === sc.zone);
  const where = zone ? (sc.division ? `${zone.divisions.find((d) => d.id === sc.division)?.name} division, ${zone.name}` : zone.name) : "All India";
  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">{where}</h2>
          <p className="text-xs text-muted">Trains count in the zone and division they start from. Synthetic bookings on the real network.</p>
        </div>
        <div className="flex gap-2">
          <select
            aria-label="Zone"
            className={sel}
            value={sc.zone}
            disabled={!!z.home_zone}
            onChange={(e) => setPicked({ zone: e.target.value, division: 0 })}
          >
            {!z.home_zone && <option value="">All India</option>}
            {z.zones.map((x) => <option key={x.code} value={x.code}>{x.code} · {x.name}</option>)}
          </select>
          {zone && (
            <select aria-label="Division" className={sel} value={sc.division} onChange={(e) => setPicked({ zone: sc.zone, division: Number(e.target.value) })}>
              <option value={0}>All divisions</option>
              {zone.divisions.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
          )}
        </div>
      </div>
      <div className="flex gap-1 border-b border-line">
        {tabs.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={cn("-mb-px border-b-2 px-3 py-2 text-sm", tab === t ? "border-[var(--accent)] font-semibold" : "border-transparent text-muted hover:text-ink")}
          >
            {t}
          </button>
        ))}
      </div>
      {tab === "Revenue" && <Revenue sc={sc} />}
      {tab === "Occupancy" && <Occupancy sc={sc} />}
      {tab === "Punctuality" && <Punctuality sc={sc} onTimeMin={z.on_time_min} />}
      {tab === "Fraud" && <Fraud />}
    </section>
  );
}

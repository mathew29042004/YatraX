"use client";

import type { Chart, Finance, TDR } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Spinner, Stat, useAsync, useYatrax } from "@yatrax/ui";
import { useState } from "react";

const inr = (paise: number) => "₹" + new Intl.NumberFormat("en-IN").format(Math.round(paise / 100));

function todayIST() {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Kolkata" }).format(new Date());
}

export function FinanceTiles() {
  const { client } = useYatrax();
  const [tick, setTick] = useState(0);
  const { data, error } = useAsync<Finance>(() => client.finance(), [client, tick]);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading ledger" />;
  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-sm font-semibold">Money (from the double-entry ledger)</h2>
        <button onClick={() => setTick(tick + 1)} className="text-xs text-[var(--accent)] hover:underline">
          Refresh
        </button>
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Stat label="Held at gateway" value={inr(data.gateway_held)} />
        <Stat label="Fares not yet travelled" value={inr(data.fares_unearned)} />
        <Stat label="Refunds owed" value={inr(data.refunds_owed)} sub={`${data.refunds_pending} pending · ${data.refunds_processed} paid`} />
        <Stat label="Cancellation charges" value={inr(data.cancellation_revenue)} />
        <Stat label="Wallet balances" value={inr(data.wallets_total)} />
        <Stat
          label="Trial balance"
          value={data.trial_balance === 0 ? "₹0 ✓" : inr(data.trial_balance)}
          sub={`${data.payments_unresolved} payments unresolved · ${data.tdrs_pending} TDRs pending`}
        />
      </div>
    </div>
  );
}

export function TDRQueue() {
  const { client } = useYatrax();
  const [tick, setTick] = useState(0);
  const { data, error } = useAsync<TDR[]>(() => client.tdrs(), [client, tick]);
  const [amounts, setAmounts] = useState<Record<string, number>>({});
  const [msg, setMsg] = useState<string | null>(null);

  async function decide(t: TDR, approve: boolean) {
    setMsg(null);
    try {
      await client.decideTDR(t.id, approve, approve ? (amounts[t.id] ?? Math.round(t.max_refund / 2)) : 0);
      setTick(tick + 1);
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Could not decide");
    }
  }

  return (
    <Card>
      <CardTitle hint="Refunds claimed after the chart">TDR claims</CardTitle>
      {msg && <Alert>{msg}</Alert>}
      {error && <Alert>{error}</Alert>}
      {!data && !error && <Spinner />}
      {data && data.length === 0 && <p className="text-sm text-muted">No claims.</p>}
      {data && data.length > 0 && (
        <ul className="divide-y divide-line text-sm">
          {data.map((t) => (
            <li key={t.id} className="flex flex-wrap items-center gap-2 py-2">
              <span className="font-mono">{t.pnr}</span>
              <span className="text-muted">
                {t.train} · {t.journey_date}
              </span>
              <span className="min-w-0 flex-1">{t.reason_text}{t.details ? ` — “${t.details}”` : ""}</span>
              {t.status === "PENDING" ? (
                <>
                  <Input
                    type="number"
                    aria-label="Refund amount"
                    className="h-8 w-24"
                    min={1}
                    max={t.max_refund}
                    defaultValue={Math.round(t.max_refund / 2)}
                    onChange={(e) => setAmounts({ ...amounts, [t.id]: Number(e.target.value) })}
                  />
                  <span className="text-xs text-muted">of ₹{t.max_refund}</span>
                  <Button size="sm" onClick={() => decide(t, true)}>
                    Approve
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => decide(t, false)}>
                    Reject
                  </Button>
                </>
              ) : (
                <Badge tone={t.status === "APPROVED" ? "ok" : "danger"}>
                  {t.status}
                  {t.refund ? ` · ₹${t.refund}` : ""}
                </Badge>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/** Admin tools for driving a train run through its lifecycle in a demo. */
export function RunTools() {
  const { client } = useYatrax();
  const [train, setTrain] = useState("12623");
  const [date, setDate] = useState(todayIST());
  const [cls, setCls] = useState("SL");
  const [count, setCount] = useState(5);
  const [out, setOut] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function run(fn: () => Promise<string>) {
    setBusy(true);
    setErr(null);
    setOut(null);
    try {
      setOut(await fn());
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Request failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardTitle hint="Origin date = the day the train leaves its first station">Run tools</CardTitle>
      <div className="grid gap-2 sm:grid-cols-4">
        <Input aria-label="Train number" value={train} onChange={(e) => setTrain(e.target.value)} placeholder="Train" />
        <Input aria-label="Origin date" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        <Input aria-label="Class" value={cls} onChange={(e) => setCls(e.target.value.toUpperCase())} placeholder="Class" />
        <Input aria-label="How many" type="number" min={1} max={200} value={count} onChange={(e) => setCount(Number(e.target.value))} />
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const r = await client.simulateCancellations(train, date, cls, count);
              return `${r.cancelled} bookings cancelled by other travellers; ${r.promotions.length} passengers moved up${r.promotions.length ? ": " + r.promotions.slice(0, 5).map((p) => `${p.from} → ${p.to}`).join(", ") : ""}.`;
            })
          }
        >
          Simulate {count} cancellations
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const c = await client.prepareChart(train, date, 1);
              return `Chart 1 prepared: ${c.promotions} promotions, ${c.auto_cancelled_bookings} waitlisted bookings cancelled and refunded, ${c.classes.reduce((n, k) => n + k.passengers.length, 0)} passengers on the chart.`;
            })
          }
        >
          Prepare chart now
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const r = await client.reconcile();
              return `Reconciled: ${r.checked} payments checked, ${r.captured} found paid, ${r.abandoned} abandoned, ${r.refunds_processed} refunds paid out.`;
            })
          }
        >
          Reconcile payments now
        </Button>
      </div>
      {out && (
        <div className="mt-3">
          <Alert tone="info">{out}</Alert>
        </div>
      )}
      {err && (
        <div className="mt-3">
          <Alert>{err}</Alert>
        </div>
      )}
    </Card>
  );
}

/** Read a prepared chart: who sits where, per class. */
export function ChartViewer() {
  const { client } = useYatrax();
  const [train, setTrain] = useState("12623");
  const [date, setDate] = useState(todayIST());
  const [chart, setChart] = useState<Chart | null>(null);
  const [err, setErr] = useState<string | null>(null);
  return (
    <Card>
      <CardTitle hint="Published 4 hours and 30 minutes before departure">Reservation chart</CardTitle>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr(null);
          try {
            setChart(await client.chart(train, date, 1));
          } catch (x) {
            setChart(null);
            setErr(x instanceof ApiError && x.status === 404 ? "No chart prepared for that train and date yet." : "Could not load the chart");
          }
        }}
      >
        <Input aria-label="Train number" value={train} onChange={(e) => setTrain(e.target.value)} className="max-w-32" />
        <Input aria-label="Origin date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="max-w-44" />
        <Button type="submit" variant="secondary">
          Show chart
        </Button>
      </form>
      {err && (
        <div className="mt-3">
          <Alert>{err}</Alert>
        </div>
      )}
      {chart && (
        <div className="mt-4 space-y-4">
          <p className="text-xs text-muted">
            Chart {chart.version} · prepared {new Date(chart.prepared_at).toLocaleString("en-IN")}
          </p>
          {chart.classes.map((c) => (
            <div key={c.class}>
              <div className="mb-1 text-sm font-semibold">
                {c.class} · {c.passengers.length} passengers · {c.vacant_berths} berths vacant for the whole run
              </div>
              <div className="max-h-64 overflow-auto rounded-lg border border-line">
                <table className="w-full text-xs">
                  <thead className="sticky top-0 bg-canvas text-left text-muted">
                    <tr>
                      <th className="px-2 py-1">Berth</th>
                      <th className="px-2 py-1">Passenger</th>
                      <th className="px-2 py-1">Journey</th>
                      <th className="px-2 py-1">PNR</th>
                      <th className="px-2 py-1">Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {c.passengers.slice(0, 300).map((p, i) => (
                      <tr key={i} className="border-t border-line">
                        <td className="px-2 py-1 font-mono">{p.coach ? `${p.coach}/${p.berth} ${p.berth_type}` : "—"}</td>
                        <td className="px-2 py-1">
                          {p.name} · {p.age}
                          {p.gender}
                        </td>
                        <td className="px-2 py-1">
                          {p.from}→{p.to}
                        </td>
                        <td className="px-2 py-1 font-mono">{p.pnr}</td>
                        <td className="px-2 py-1">{p.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

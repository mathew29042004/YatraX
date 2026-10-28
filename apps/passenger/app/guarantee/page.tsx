"use client";

import type { PassengerInput, SearchResult, SearchTrain } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, AvailabilityPill, Button, Card, CardTitle, Input, RequireRole, Spinner, cn, useAsync, useYatrax } from "@yatrax/ui";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { clockIST, dayLabel } from "@/components/format";
import { SiteHeader } from "@/components/site-header";

type Choice = { train: SearchTrain; date: string };

/**
 * Confirmation guarantee: when every train is waitlisted, hold the trip on two of them. The
 * first to confirm keeps the trip; the other is cancelled with a full refund.
 */
function GuaranteeForm() {
  const { client } = useYatrax();
  const router = useRouter();
  const qs = useSearchParams();
  const from = qs.get("from") ?? "MAS";
  const to = qs.get("to") ?? "ERN";
  const date = qs.get("date") ?? new Date(Date.now() + 14 * 86_400_000).toISOString().slice(0, 10);
  const cls = qs.get("class") ?? "SL";
  const { data, error } = useAsync<SearchResult>(() => client.search({ from, to, date, class: cls, flex: 1 }), [client, from, to, date, cls]);
  const [picks, setPicks] = useState<Choice[]>([]);
  const [pax, setPax] = useState<PassengerInput[]>([{ name: "", age: 30, gender: "F", berth_pref: "" }]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const waiting = (data?.trains ?? []).filter((t) => t.runs_on_date && t.classes.some((c) => c.code === cls && c.availability.code !== "AVL" && c.availability.code !== "REGRET"));
  const key = (c: Choice) => c.train.number + c.date;
  const toggle = (c: Choice) =>
    setPicks((p) => (p.some((x) => key(x) === key(c)) ? p.filter((x) => key(x) !== key(c)) : p.length < 2 ? [...p, c] : [p[1]!, c]));

  async function submit() {
    if (picks.length !== 2) return setErr("Pick two trains.");
    if (pax.some((p) => p.name.trim().length < 2)) return setErr("Every passenger needs a name.");
    setBusy(true);
    setErr(null);
    try {
      const hold = (c: Choice) => ({ train: c.train.number, from: c.train.from.code, to: c.train.to.code, date: c.date, class: cls, quota: "GN" as const, passengers: pax, together: true, only_confirmed: false });
      const pair = await client.createGuarantee(hold(picks[0]!), hold(picks[1]!), crypto.randomUUID());
      router.push(`/booking/${pair.first.id}?guarantee=1`);
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Could not hold both trains");
      setBusy(false);
    }
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-xl font-semibold">Confirmation guarantee</h1>
        <p className="text-sm text-muted">
          {from} → {to} · {dayLabel(date)} · {cls}. Waitlisted on every train? Hold two. The first to confirm is your ticket; the other is
          cancelled automatically with a <b>full refund</b>. If neither confirms by chart time, both are refunded.
        </p>
      </div>
      <Card>
        <CardTitle hint={`${picks.length}/2 picked`}>Pick two waitlisted trains</CardTitle>
        {error && <Alert>{error}</Alert>}
        {!data && !error && <Spinner label="Finding trains" />}
        {data && waiting.length === 0 && <p className="text-sm text-muted">No waitlisted {cls} trains on this route: book one directly.</p>}
        <ul className="divide-y divide-line">
          {waiting.map((t) => {
            const c = { train: t, date };
            const on = picks.some((x) => key(x) === key(c));
            const a = t.classes.find((x) => x.code === cls)!.availability;
            return (
              <li key={t.number}>
                <label className={cn("flex cursor-pointer items-center gap-3 py-2 text-sm", on && "font-medium")}>
                  <input type="checkbox" checked={on} onChange={() => toggle(c)} className="h-4 w-4 accent-[var(--accent)]" />
                  <span className="font-mono">{t.number}</span>
                  <span className="min-w-0 flex-1 truncate">{t.name}</span>
                  <span className="tabular-nums text-muted">
                    {clockIST(t.departure)} → {clockIST(t.arrival)}
                  </span>
                  <AvailabilityPill a={a} />
                </label>
              </li>
            );
          })}
        </ul>
      </Card>
      <Card>
        <CardTitle>Passengers</CardTitle>
        <div className="space-y-2">
          {pax.map((p, i) => (
            <div key={i} className="grid grid-cols-[1fr_5rem_7rem] gap-2">
              <Input aria-label={`Passenger ${i + 1} name`} placeholder="Full name" value={p.name} onChange={(e) => setPax(pax.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
              <Input aria-label="Age" type="number" min={5} max={125} value={p.age} onChange={(e) => setPax(pax.map((x, j) => (j === i ? { ...x, age: Number(e.target.value) } : x)))} />
              <select aria-label="Gender" className="h-10 rounded-lg border border-line bg-surface px-2 text-sm" value={p.gender} onChange={(e) => setPax(pax.map((x, j) => (j === i ? { ...x, gender: e.target.value as PassengerInput["gender"] } : x)))}>
                <option value="F">Female</option>
                <option value="M">Male</option>
                <option value="O">Other</option>
              </select>
            </div>
          ))}
        </div>
        <Button variant="secondary" size="sm" className="mt-2" disabled={pax.length >= 6} onClick={() => setPax([...pax, { name: "", age: 30, gender: "F", berth_pref: "" }])}>
          + Add passenger
        </Button>
      </Card>
      {err && <Alert>{err}</Alert>}
      <Button size="lg" onClick={submit} disabled={busy || picks.length !== 2}>
        {busy ? "Holding both…" : "Hold both trains"}
      </Button>
      <p className="text-xs text-muted">You pay for both now; the one that loses is refunded in full to your original payment.</p>
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl px-4 py-6">
        <RequireRole>
          {() => (
            <Suspense fallback={<Spinner />}>
              <GuaranteeForm />
            </Suspense>
          )}
        </RequireRole>
      </main>
    </>
  );
}

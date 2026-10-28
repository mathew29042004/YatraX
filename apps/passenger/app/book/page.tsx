"use client";

import type { CalendarResult, PassengerInput, Quota, SavedPassenger, SeatMap } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, AvailabilityPill, Badge, Button, Card, CardTitle, Input, RequireRole, Spinner, cn, useAsync, useYatrax } from "@yatrax/ui";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo, useRef, useState } from "react";
import { dayLabel, rupees } from "@/components/format";
import { SeatPicker, type Picked } from "@/components/seat-picker";
import { SiteHeader } from "@/components/site-header";

const PREFS: Record<string, [string, string][]> = {
  sleeper_8: [["", "No preference"], ["LB", "Lower"], ["MB", "Middle"], ["UB", "Upper"], ["SL", "Side lower"], ["SU", "Side upper"]],
  sleeper_6: [["", "No preference"], ["LB", "Lower"], ["UB", "Upper"], ["SL", "Side lower"], ["SU", "Side upper"]],
  sleeper_4: [["", "No preference"], ["LB", "Lower"], ["UB", "Upper"]],
  sitting: [["", "No preference"], ["W", "Window"], ["A", "Aisle"]],
};

const blank = (): PassengerInput => ({ name: "", age: 30, gender: "F", berth_pref: "" });

function BookForm() {
  const { client } = useYatrax();
  const router = useRouter();
  const qs = useSearchParams();
  const train = qs.get("train") ?? "";
  const from = qs.get("from") ?? "";
  const to = qs.get("to") ?? "";
  const date = qs.get("date") ?? "";
  const cls = qs.get("class") ?? "";
  const quota = (qs.get("quota") ?? "GN") as Quota;
  const maxPax = quota === "TQ" || quota === "PT" ? 4 : quota === "SS" || quota === "HP" ? 2 : 6;
  // Quotas that need a document number: for divyangjan only the first passenger (the escort travels on theirs).
  const idLabel = { HP: "UDID card number", DF: "Warrant number", FT: "Passport number" }[quota as "HP" | "DF" | "FT"] as string | undefined;

  const info = useAsync<CalendarResult>(() => client.calendar(train, from, to, cls, quota, date, 1), [client, train, from, to, cls, quota, date]);
  const saved = useAsync<SavedPassenger[]>(() => client.savedPassengers(), [client]).data ?? [];
  const [pax, setPax] = useState<PassengerInput[]>([blank()]);
  const [together, setTogether] = useState(true);
  const [onlyConfirmed, setOnlyConfirmed] = useState(false);
  const [autoUpgrade, setAutoUpgrade] = useState(true);
  const [insurance, setInsurance] = useState(true);
  const [choose, setChoose] = useState(false);
  const [map, setMap] = useState<SeatMap | null>(null);
  const [picked, setPicked] = useState<Picked[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // One key per booking attempt: retrying after a network error can't create a second booking.
  const idemKey = useRef(crypto.randomUUID());

  const prefOptions = useMemo(() => {
    const cc = info.data?.class ?? cls;
    if (["CC", "EC", "2S"].includes(cc)) return PREFS.sitting!;
    if (cc === "2A") return PREFS.sleeper_6!;
    if (cc === "1A") return PREFS.sleeper_4!;
    return PREFS.sleeper_8!;
  }, [info.data, cls]);

  const update = (i: number, patch: Partial<PassengerInput>) => setPax(pax.map((p, j) => (j === i ? { ...p, ...patch } : p)));

  async function openSeatMap() {
    setChoose(true);
    if (map) return;
    try {
      setMap(await client.seatMap({ train, from, to, date, class: cls, quota }));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not load the seat map");
      setChoose(false);
    }
  }

  async function submit() {
    setError(null);
    if (pax.some((p) => p.name.trim().length < 2)) return setError("Every passenger needs a name.");
    if (idLabel && pax.some((p, i) => (quota !== "HP" || i === 0) && !p.id_number?.trim())) return setError(`Enter the ${idLabel.toLowerCase()}.`);
    if (choose && picked.length > 0 && picked.length !== pax.length) {
      return setError(`Pick ${pax.length} berths on the seat map, or clear your picks to let us choose.`);
    }
    setBusy(true);
    try {
      const passengers = pax.map((p, i) => (choose && picked[i] ? { ...p, coach: picked[i]!.coach, berth: picked[i]!.berth } : p));
      const b = await client.createBooking(
        { train, from, to, date, class: cls, quota, passengers, together, only_confirmed: onlyConfirmed, auto_upgrade: autoUpgrade, insurance },
        idemKey.current,
      );
      router.push(`/booking/${b.id}`);
    } catch (e) {
      if (!(e instanceof ApiError)) {
        // Network error: keep the same idempotency key so a retry can't double-book.
        setError("Could not reach YatraX. Try again.");
        return;
      }
      setError(e.message);
      idemKey.current = crypto.randomUUID(); // the server answered; the next try is a new attempt
      if (e.code === "berth_taken") {
        setPicked([]);
        setMap(await client.seatMap({ train, from, to, date, class: cls, quota }).catch(() => null));
      }
    } finally {
      setBusy(false);
    }
  }

  const perHead = info.data?.fare?.total ?? 0;
  const avail = info.data?.days[0]?.availability;

  return (
    <div className="grid gap-5 lg:grid-cols-[1fr_20rem]">
      <div className="space-y-5">
        <Card>
          <CardTitle>Passengers</CardTitle>
          {saved.length > 0 && (
            <div className="mb-3 flex flex-wrap items-center gap-1.5 text-sm">
              <span className="text-xs text-muted">Add saved:</span>
              {saved.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  disabled={pax.length >= maxPax}
                  onClick={() => {
                    const p = { name: s.name, age: s.age, gender: s.gender, berth_pref: s.berth_pref };
                    setPax(pax.length === 1 && !pax[0]!.name ? [p] : [...pax, p]);
                  }}
                  className="rounded-md border border-line px-2 py-0.5 text-xs hover:border-[var(--accent)] disabled:opacity-40"
                >
                  + {s.name}
                </button>
              ))}
            </div>
          )}
          <div className="space-y-2">
            {pax.map((p, i) => (
              <div key={i} className="grid grid-cols-2 gap-2 rounded-lg border border-line p-2 sm:grid-cols-[1fr_5rem_7rem_9rem_auto]">
                <Input aria-label={`Passenger ${i + 1} name`} placeholder="Full name" value={p.name} onChange={(e) => update(i, { name: e.target.value })} className="col-span-2 sm:col-span-1" />
                <Input aria-label="Age" type="number" min={5} max={125} value={p.age} onChange={(e) => update(i, { age: Number(e.target.value) })} />
                <select aria-label="Gender" value={p.gender} onChange={(e) => update(i, { gender: e.target.value as PassengerInput["gender"] })} className="h-10 rounded-lg border border-line bg-surface px-2 text-sm">
                  <option value="F">Female</option>
                  <option value="M">Male</option>
                  <option value="O">Other</option>
                </select>
                <select aria-label="Berth preference" value={p.berth_pref} onChange={(e) => update(i, { berth_pref: e.target.value })} className="h-10 rounded-lg border border-line bg-surface px-2 text-sm">
                  {prefOptions.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
                {idLabel && (quota !== "HP" || i === 0) && (
                  <Input
                    aria-label={`Passenger ${i + 1} ${idLabel}`}
                    placeholder={idLabel}
                    value={p.id_number ?? ""}
                    onChange={(e) => update(i, { id_number: e.target.value })}
                    className="col-span-2 sm:col-span-4"
                  />
                )}
                {quota === "HP" && i === 1 && <p className="col-span-2 self-center text-xs text-muted sm:col-span-4">Escort: travels at the same concession.</p>}
                <button type="button" onClick={() => setPax(pax.filter((_, j) => j !== i))} disabled={pax.length === 1} className="text-sm text-muted hover:text-danger disabled:opacity-30" aria-label={`Remove passenger ${i + 1}`}>
                  Remove
                </button>
              </div>
            ))}
          </div>
          <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
            <Button variant="secondary" size="sm" onClick={() => setPax([...pax, blank()])} disabled={pax.length >= maxPax}>
              + Add passenger ({pax.length}/{maxPax})
            </Button>
            <span className="text-xs text-muted">Children under 5 travel free without a ticket.</span>
          </div>
        </Card>

        <Card>
          <CardTitle>Preferences</CardTitle>
          <div className="space-y-2 text-sm">
            <label className="flex cursor-pointer items-center gap-2">
              <input type="checkbox" checked={together} onChange={(e) => setTogether(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
              Seat us together (same bay)
            </label>
            <label className="flex cursor-pointer items-center gap-2">
              <input type="checkbox" checked={onlyConfirmed} onChange={(e) => setOnlyConfirmed(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
              Book only if every passenger gets a confirmed berth
            </label>
            <label className="flex cursor-pointer items-center gap-2">
              <input type="checkbox" checked={autoUpgrade} onChange={(e) => setAutoUpgrade(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
              Upgrade me to a higher class for free if berths are left when the chart is prepared
            </label>
            <label className="flex cursor-pointer items-center gap-2">
              <input type="checkbox" checked={insurance} onChange={(e) => setInsurance(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
              Travel insurance: ₹1 per passenger, cover up to ₹10 lakh
            </label>
          </div>
        </Card>

        <Card>
          <CardTitle hint="Optional: we pick the best berths if you skip this">Choose berths</CardTitle>
          {!choose ? (
            <Button variant="secondary" onClick={openSeatMap}>
              Open seat map
            </Button>
          ) : !map ? (
            <Spinner label="Loading seat map" />
          ) : (
            <>
              <SeatPicker map={map} max={pax.length} picked={picked} onChange={setPicked} />
              {picked.length > 0 && (
                <p className="mt-2 text-sm">
                  {picked.map((p, i) => `${pax[i]?.name || `Passenger ${i + 1}`} → ${p.coach}/${p.berth}`).join(" · ")}
                </p>
              )}
            </>
          )}
        </Card>
      </div>

      <div className="lg:sticky lg:top-20 lg:self-start">
        <Card>
          {!info.data && !info.error && <Spinner />}
          {info.error && <Alert>{info.error}</Alert>}
          {info.data && (
            <>
              <div className="flex items-center gap-2">
                <span className="font-mono text-lg font-semibold">{train}</span>
                <Badge tone="brand">
                  {cls} · {quota}
                </Badge>
              </div>
              <div className="mt-1 text-sm">
                {from} → {to}
              </div>
              <div className="text-sm text-muted">{dayLabel(date)}</div>
              {avail && (
                <div className="mt-2">
                  <AvailabilityPill a={avail} />
                </div>
              )}
              <dl className="mt-4 space-y-1 border-t border-line pt-3 text-sm">
                <div className="flex justify-between">
                  <dt className="text-muted">Fare per passenger</dt>
                  <dd className="tabular-nums">{rupees(perHead)}</dd>
                </div>
                <div className="flex justify-between">
                  <dt className="text-muted">Passengers</dt>
                  <dd>× {pax.length}</dd>
                </div>
                {insurance && (
                  <div className="flex justify-between">
                    <dt className="text-muted">Travel insurance</dt>
                    <dd className="tabular-nums">{rupees(pax.length)}</dd>
                  </div>
                )}
                <div className="flex justify-between border-t border-line pt-2 text-base font-semibold">
                  <dt>Total</dt>
                  <dd className="tabular-nums">{rupees(perHead * pax.length + (insurance ? pax.length : 0))}</dd>
                </div>
              </dl>
            </>
          )}
          {error && (
            <div className="mt-3">
              <Alert>{error}</Alert>
            </div>
          )}
          <Button size="lg" className={cn("mt-4 w-full")} disabled={busy || !info.data} onClick={submit}>
            {busy ? "Holding berths…" : "Hold berths & continue"}
          </Button>
          <p className="mt-2 text-xs text-muted">Berths are held for 10 minutes while you pay.</p>
        </Card>
      </div>
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-6xl px-4 py-6">
        <h1 className="mb-4 text-xl font-semibold">Book your journey</h1>
        <RequireRole>
          {() => (
            <Suspense fallback={<Spinner />}>
              <BookForm />
            </Suspense>
          )}
        </RequireRole>
      </main>
    </>
  );
}

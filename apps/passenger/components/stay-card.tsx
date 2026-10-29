"use client";

import type { Booking, Stay, StayOption, StayUnit } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, cn, useT, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useMemo, useState } from "react";
import { rupees } from "./format";

// datetime-local wants "YYYY-MM-DDTHH:mm" in local (IST) time.
const toLocal = (iso: string) => new Date(new Date(iso).getTime() + 5.5 * 3600_000).toISOString().slice(0, 16);
const fromLocal = (v: string) => new Date(v + ":00+05:30").toISOString();
const when = (iso: string) => new Date(iso).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short", timeZone: "Asia/Kolkata" });

/** Retiring rooms, dormitory beds and cloak-room lockers at either end of the journey. */
export function StayCard({ b }: { b: Booking }) {
  const { client } = useYatrax();
  const { t } = useT();
  const ends = [
    { code: b.from, name: b.from_name, at: new Date(new Date(b.departure).getTime() - 6 * 3600_000).toISOString(), hint: "before you leave" },
    { code: b.to, name: b.to_name, at: b.arrival, hint: "after you arrive" },
  ];
  const [end, setEnd] = useState(0);
  const [checkIn, setCheckIn] = useState(toLocal(ends[0]!.at));
  const [hours, setHours] = useState<12 | 24 | 48>(12);
  const [unit, setUnit] = useState<StayUnit>("ac_room");
  const [units, setUnits] = useState(1);
  const [payment, setPayment] = useState<"counter" | "wallet">("counter");
  const [options, setOptions] = useState<StayOption[] | null>(null);
  const [mine, setMine] = useState<Stay[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const station = ends[end]!;

  const loadMine = useCallback(() => client.myStays().then((all) => setMine(all.filter((s) => s.pnr === b.pnr))).catch(() => undefined), [client, b.pnr]);
  useEffect(() => {
    loadMine();
  }, [loadMine]);
  useEffect(() => {
    client.stayOptions(station.code, fromLocal(checkIn), hours).then(setOptions).catch(() => setOptions(null));
  }, [client, station.code, checkIn, hours]);
  const opt = useMemo(() => options?.find((o) => o.type === unit), [options, unit]);
  const price = (opt?.price[String(hours)] ?? 0) * units;

  async function book() {
    setError(null);
    setMsg(null);
    try {
      const s = await client.bookStay({ booking_id: b.id, station: station.code, unit, units, check_in: fromLocal(checkIn), hours, payment });
      setMsg(`Booked ${s.code}: show it at the ${s.station_name} counter.`);
      loadMine();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not book");
    }
  }

  return (
    <Card>
      <CardTitle hint="booked against this ticket">{t("ticket.stay")}</CardTitle>
      <div className="flex flex-wrap gap-2">
        {ends.map((e, i) => (
          <button
            key={e.code}
            onClick={() => {
              setEnd(i);
              setCheckIn(toLocal(e.at));
            }}
            aria-pressed={end === i}
            className={cn("rounded-md border px-3 py-1.5 text-left text-sm", end === i ? "border-[var(--accent)] bg-canvas" : "border-line")}
          >
            <span className="font-medium">{e.name}</span> <span className="text-xs text-muted">{e.hint}</span>
          </button>
        ))}
      </div>
      <div className="mt-3 grid gap-3 sm:grid-cols-4">
        <div className="sm:col-span-2">
          <Label htmlFor="stay-in">Check in</Label>
          <Input id="stay-in" type="datetime-local" value={checkIn} onChange={(e) => e.target.value && setCheckIn(e.target.value)} />
        </div>
        <div>
          <Label htmlFor="stay-hours">For</Label>
          <select id="stay-hours" className="h-10 w-full rounded-lg border border-line bg-surface px-2 text-sm" value={hours} onChange={(e) => setHours(Number(e.target.value) as 12 | 24 | 48)}>
            <option value={12}>12 hours</option>
            <option value={24}>24 hours</option>
            <option value={48}>48 hours</option>
          </select>
        </div>
        <div>
          <Label htmlFor="stay-units">How many</Label>
          <Input id="stay-units" type="number" min={1} max={6} value={units} onChange={(e) => setUnits(Math.max(1, Math.min(6, Number(e.target.value))))} />
        </div>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        {(options ?? []).map((o) => (
          <label key={o.type} className={cn("cursor-pointer rounded-lg border p-2.5 text-sm", unit === o.type ? "border-[var(--accent)] bg-canvas" : "border-line", o.free === 0 && "opacity-50")}>
            <input type="radio" name="stay-unit" className="sr-only" checked={unit === o.type} disabled={o.free === 0} onChange={() => setUnit(o.type)} />
            <div className="font-medium">{o.name}</div>
            <div className="text-xs text-muted">
              {rupees(o.price[String(hours)] ?? 0)} for {hours} h · {o.free} of {o.total} free
            </div>
          </label>
        ))}
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-4 text-sm">
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={payment === "counter"} onChange={() => setPayment("counter")} /> Pay at the counter
        </label>
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={payment === "wallet"} onChange={() => setPayment("wallet")} /> YatraX Pay wallet
        </label>
        <Button className="ml-auto" onClick={book} disabled={!opt || opt.free < units}>
          Book · {rupees(price)}
        </Button>
      </div>
      {msg && <div className="mt-2"><Alert tone="info">{msg}</Alert></div>}
      {error && <div className="mt-2"><Alert>{error}</Alert></div>}
      {mine.length > 0 && (
        <ul className="mt-3 space-y-1.5 text-sm">
          {mine.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-2 rounded-md bg-canvas px-3 py-2">
              <span className="font-mono text-xs">{s.code}</span>
              <span>
                {s.units} × {s.unit_name} at {s.station_name}
              </span>
              <span className="text-xs text-muted">
                {when(s.check_in)} – {when(s.check_out)} · {rupees(s.amount)} {s.payment === "counter" ? "at the counter" : "paid"}
              </span>
              <Badge tone={s.status === "cancelled" ? "neutral" : s.status === "booked" ? "brand" : "ok"} className="ml-auto">
                {s.status.replace("_", " ")}
              </Badge>
              {s.status === "booked" && (
                <button className="text-xs text-danger hover:underline" onClick={() => client.cancelStay(s.id).then(loadMine).catch(() => undefined)}>
                  Cancel
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

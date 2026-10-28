"use client";

import type { Quota, SearchParams, Station } from "@yatrax/api-client";
import { Alert, Button, cn, useYatrax } from "@yatrax/ui";
import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { addDays, todayIST } from "./format";
import { StationPicker } from "./station-picker";

export const CLASSES: [string, string][] = [
  ["", "All classes"], ["1A", "First AC (1A)"], ["2A", "AC 2 Tier (2A)"], ["3A", "AC 3 Tier (3A)"], ["3E", "AC 3 Economy (3E)"],
  ["EC", "Exec. Chair Car (EC)"], ["CC", "AC Chair Car (CC)"], ["SL", "Sleeper (SL)"], ["2S", "Second Sitting (2S)"],
];

export const QUOTAS: [Quota, string][] = [
  ["GN", "General"], ["TQ", "Tatkal"], ["PT", "Premium Tatkal"], ["LD", "Ladies"], ["SS", "Senior / Lower berth"], ["HP", "Divyangjan"], ["DF", "Defence"], ["FT", "Foreign tourist"],
];

/** Booking opens 60 days ahead; search looks a little further so it can say when booking opens. */
const SEARCH_DAYS = 120;

export function searchHref(p: SearchParams) {
  const q = new URLSearchParams({ from: p.from, to: p.to, date: p.date });
  if (p.quota && p.quota !== "GN") q.set("quota", p.quota);
  if (p.class) q.set("class", p.class);
  if (p.nearby) q.set("nearby", "1");
  if (p.flex) q.set("flex", String(p.flex));
  return `/search?${q}`;
}

const selectClass =
  "h-12 w-full rounded-lg border border-line bg-surface px-3 text-sm text-ink focus:border-[var(--accent)] focus:outline-none focus:ring-2 focus:ring-[var(--accent)]/20";

export function SearchForm({ initial, compact }: { initial?: Partial<SearchParams>; compact?: boolean }) {
  const { client } = useYatrax();
  const router = useRouter();
  const today = todayIST();
  const [from, setFrom] = useState<Station | null>(null);
  const [to, setTo] = useState<Station | null>(null);
  const [date, setDate] = useState(initial?.date ?? addDays(today, 7));
  const [cls, setCls] = useState(initial?.class ?? "");
  const [quota, setQuota] = useState<Quota>(initial?.quota ?? "GN");
  const [nearby, setNearby] = useState(initial?.nearby ?? false);
  const [flex, setFlex] = useState((initial?.flex ?? 0) > 0);
  const [error, setError] = useState<string | null>(null);

  // Resolve station codes from the URL into full stations for the pickers.
  useEffect(() => {
    if (initial?.from) client.station(initial.from).then(setFrom).catch(() => undefined);
    if (initial?.to) client.station(initial.to).then(setTo).catch(() => undefined);
  }, [client, initial?.from, initial?.to]);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!from || !to) return setError("Pick both stations from the list.");
    if (from.code === to.code) return setError("From and To must be different stations.");
    setError(null);
    router.push(searchHref({ from: from.code, to: to.code, date, quota, class: cls, nearby, flex: flex ? 3 : 0 }));
  }

  return (
    <form onSubmit={submit} className={cn("rounded-2xl border border-line bg-surface shadow-sm", compact ? "p-4" : "p-5 sm:p-6")}>
      <div className="grid gap-3 md:grid-cols-[1fr_auto_1fr_11rem]">
        <StationPicker label="From" value={from} onChange={setFrom} placeholder="Station or city, e.g. Chennai" />
        <button
          type="button"
          aria-label="Swap stations"
          onClick={() => {
            setFrom(to);
            setTo(from);
          }}
          className="mx-auto flex h-10 w-10 items-center justify-center self-end rounded-full border border-line bg-surface text-muted hover:text-ink md:mb-1"
        >
          ⇄
        </button>
        <StationPicker label="To" value={to} onChange={setTo} placeholder="e.g. Ernakulam" />
        <div>
          <label htmlFor="date" className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-muted">
            Date
          </label>
          <input
            id="date"
            type="date"
            value={date}
            min={today}
            max={addDays(today, SEARCH_DAYS)}
            onChange={(e) => setDate(e.target.value)}
            className={selectClass}
          />
        </div>
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-2 md:grid-cols-[1fr_1fr_auto]">
        <div>
          <label htmlFor="cls" className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-muted">
            Class
          </label>
          <select id="cls" value={cls} onChange={(e) => setCls(e.target.value)} className={selectClass}>
            {CLASSES.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="quota" className="mb-1.5 block text-xs font-medium uppercase tracking-wide text-muted">
            Quota
          </label>
          <select id="quota" value={quota} onChange={(e) => setQuota(e.target.value as Quota)} className={selectClass}>
            {QUOTAS.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </div>
        <Button type="submit" size="lg" className="self-end px-8">
          Search trains
        </Button>
      </div>

      <div className="mt-3 flex flex-wrap gap-x-5 gap-y-2 text-sm">
        <label className="inline-flex cursor-pointer items-center gap-2">
          <input type="checkbox" checked={nearby} onChange={(e) => setNearby(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
          Include nearby stations <span className="text-muted">(e.g. all Chennai stations)</span>
        </label>
        <label className="inline-flex cursor-pointer items-center gap-2">
          <input type="checkbox" checked={flex} onChange={(e) => setFlex(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
          Flexible dates <span className="text-muted">(±3 days)</span>
        </label>
      </div>
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
    </form>
  );
}

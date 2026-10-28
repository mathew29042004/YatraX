"use client";

import type { CalendarResult, ClassOption, Festival, Quota, SearchTrain } from "@yatrax/api-client";
import { Alert, AvailabilityPill, Badge, Button, Spinner, availabilityTone, cn, useAsync, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { DAY_LETTERS, clockIST, dayLabel, duration, rupees } from "./format";

const typeTone: Record<string, "brand" | "saffron" | "ok" | "neutral"> = {
  RAJDHANI: "saffron", DURONTO: "saffron", VANDE_BHARAT: "brand", SHATABDI: "brand", SUPERFAST: "neutral", MAIL_EXPRESS: "neutral", PASSENGER: "neutral",
};

export function TrainCard({
  t, quota, date, selected, onSelect, festivals,
}: {
  t: SearchTrain;
  quota: Quota;
  date: string;
  selected: string | null;
  onSelect: (cls: string | null) => void;
  festivals: Festival[];
}) {
  const open = t.classes.find((c) => c.code === selected) ?? null;
  return (
    <article className="overflow-hidden rounded-xl border border-line bg-surface shadow-sm">
      <div className="p-4 sm:p-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-base font-semibold">{t.number}</span>
          <h2 className="text-base font-semibold">{t.name}</h2>
          <Badge tone={typeTone[t.type] ?? "neutral"}>{t.type_name}</Badge>
          {t.pantry && <Badge tone="ok">Pantry</Badge>}
          {t.flexi && <Badge tone="neutral">Flexi fare</Badge>}
          <span className="ml-auto inline-flex items-center gap-3">
            <span className="inline-flex gap-0.5 font-mono text-[11px]" aria-label="Running days">
              {t.running_days.map((on, i) => (
                <span key={i} className={on ? "font-semibold text-ink" : "text-line"}>
                  {DAY_LETTERS[i]}
                </span>
              ))}
            </span>
            <Link
              href={`/train/${t.number}?from=${t.from.code}&to=${t.to.code}`}
              className="text-sm font-medium text-[var(--accent)] hover:underline"
            >
              Schedule & coaches
            </Link>
          </span>
        </div>

        <div className="mt-4 grid grid-cols-[1fr_auto_1fr] items-center gap-3">
          <div>
            <div className="text-2xl font-semibold tabular-nums">{clockIST(t.departure)}</div>
            <div className="text-sm">
              <span className="font-mono font-semibold">{t.from.code}</span> <span className="text-muted">{t.from.name}</span>
            </div>
            <div className="text-xs text-muted">
              {dayLabel(t.departure)} · PF {t.from_platform}
            </div>
          </div>
          <div className="flex min-w-24 flex-col items-center text-xs text-muted">
            <span>{duration(t.duration_min)}</span>
            <span className="my-1 h-px w-full bg-line" />
            <span>{t.distance_km} km</span>
          </div>
          <div className="text-right">
            <div className="text-2xl font-semibold tabular-nums">{clockIST(t.arrival)}</div>
            <div className="text-sm">
              <span className="text-muted">{t.to.name}</span> <span className="font-mono font-semibold">{t.to.code}</span>
            </div>
            <div className="text-xs text-muted">{dayLabel(t.arrival)}</div>
          </div>
        </div>

        {!t.runs_on_date && (
          <div className="mt-3">
            <Alert tone="info">Does not run on {dayLabel(date)}. See the dates grid above for the days it runs.</Alert>
          </div>
        )}

        <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
          {t.classes.map((c) => (
            <button
              key={c.code}
              type="button"
              onClick={() => onSelect(selected === c.code ? null : c.code)}
              aria-expanded={selected === c.code}
              className={cn(
                "rounded-lg border p-2.5 text-left transition-colors hover:border-[var(--accent)]",
                selected === c.code ? "border-[var(--accent)] ring-2 ring-[var(--accent)]/20" : "border-line",
              )}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-semibold">{c.code}</span>
                <span className="text-sm font-semibold tabular-nums">{c.fare ? rupees(c.fare.total) : "—"}</span>
              </div>
              <div className="mt-1.5 flex items-center justify-between gap-1">
                <AvailabilityPill a={c.availability} />
                {c.availability.code === "WL" && <span className="text-[11px] text-muted">{c.availability.confirm_chance}% est.</span>}
              </div>
            </button>
          ))}
        </div>
      </div>
      {open && <ClassPanel t={t} c={open} quota={quota} date={date} festivals={festivals} />}
    </article>
  );
}

function FareBreakdown({ c }: { c: ClassOption }) {
  const f = c.fare;
  if (!f) return <p className="text-sm text-muted">No {c.code} fare in this quota.</p>;
  const rows: [string, number][] = [
    ["Base fare", f.base],
    ["Reservation charge", f.reservation],
    ["Superfast charge", f.superfast],
    [f.quota === "PT" ? "Premium Tatkal charge" : "Tatkal charge", f.tatkal],
    ["GST (5%)", f.gst],
  ];
  return (
    <div>
      <table className="w-full text-sm">
        <tbody>
          {rows
            .filter(([, v]) => v > 0)
            .map(([k, v]) => (
              <tr key={k}>
                <td className="py-0.5 text-muted">{k}</td>
                <td className="py-0.5 text-right tabular-nums">{rupees(v)}</td>
              </tr>
            ))}
          <tr className="border-t border-line font-semibold">
            <td className="pt-1.5">Total per passenger</td>
            <td className="pt-1.5 text-right tabular-nums">{rupees(f.total)}</td>
          </tr>
        </tbody>
      </table>
      <ul className="mt-2 space-y-0.5 text-xs text-muted">
        <li>
          Charged for {f.charged_km} km{f.charged_km > f.distance_km ? ` (minimum for ${c.code}; journey is ${f.distance_km} km)` : ""}.
        </li>
        {f.catering_included && <li>Meals included in this train&apos;s fare.</li>}
        {f.flexi && <li>Flexi fare: the base fare rises up to 50% as seats sell. Shown at the opening price.</li>}
      </ul>
    </div>
  );
}

const BOOKABLE = new Set(["AVL", "RAC", "WL"]);

function BookButton({ t, c, quota, date }: { t: SearchTrain; c: ClassOption; quota: Quota; date: string }) {
  const href = `/book?${new URLSearchParams({ train: t.number, from: t.from.code, to: t.to.code, date, class: c.code, quota })}`;
  if (!BOOKABLE.has(c.availability.code)) {
    return (
      <Button className="mt-4 w-full" disabled>
        {c.availability.label}
      </Button>
    );
  }
  return (
    <Link href={href} className="mt-4 flex h-10 w-full items-center justify-center rounded-lg bg-[var(--accent)] text-sm font-medium text-white hover:brightness-110">
      {c.availability.code === "AVL" ? "Book now" : `Book (${c.availability.label})`}
    </Link>
  );
}

function ClassPanel({ t, c, quota, date, festivals }: { t: SearchTrain; c: ClassOption; quota: Quota; date: string; festivals: Festival[] }) {
  const { client } = useYatrax();
  const { data, error } = useAsync<CalendarResult>(
    () => client.calendar(t.number, t.from.code, t.to.code, c.code, quota, date, 14),
    [client, t.number, t.from.code, t.to.code, c.code, quota, date],
  );
  // Only mark festivals that drive travel on this journey (nationwide, or celebrated at either end).
  const relevant = festivals.filter((f) => !f.states?.length || f.states.includes(t.from.state) || f.states.includes(t.to.state));
  const festivalOn = new Map(relevant.map((f) => [f.date.slice(0, 10), f.name]));

  return (
    <div className="grid gap-5 border-t border-line bg-canvas/60 p-4 sm:p-5 lg:grid-cols-[18rem_1fr]">
      <div>
        <h4 className="mb-2 text-sm font-semibold">
          {c.name} · {quota} fare
        </h4>
        <FareBreakdown c={c} />
        <BookButton t={t} c={c} quota={quota} date={date} />
      </div>
      <div>
        <h4 className="mb-2 text-sm font-semibold">Availability, next 14 days</h4>
        {error && <Alert>{error}</Alert>}
        {!data && !error && <Spinner />}
        {data && (
          <div className="grid grid-cols-3 gap-1.5 sm:grid-cols-5 lg:grid-cols-7">
            {data.days.map((d) => (
              <div
                key={d.date}
                className={cn("rounded-lg border p-2 text-center", availabilityTone(d.availability.code), d.date === date && "ring-2 ring-[var(--accent)]")}
              >
                <div className="text-[11px] font-medium opacity-80">{dayLabel(d.date)}</div>
                <div className="mt-0.5 text-xs font-semibold leading-tight">{d.availability.label}</div>
                {festivalOn.has(d.date) && <div className="mt-0.5 text-[10px] font-medium text-saffron-700">🪔 {festivalOn.get(d.date)}</div>}
              </div>
            ))}
          </div>
        )}
        <p className="mt-2 text-xs text-muted">
          Waitlist estimates are rough. The trained confirmation model arrives in Phase 10.
        </p>
      </div>
    </div>
  );
}

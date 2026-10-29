"use client";

import type { Festival, Quota, SearchParams, SearchResult, SearchTrain } from "@yatrax/api-client";
import { Alert, AvailabilityPill, Spinner, cn, useAsync, useT, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useMemo, useState } from "react";
import { dayLabel, hourIST } from "./format";
import { searchHref } from "./search-form";
import { Alternatives } from "./alternatives";
import { TrainCard } from "./train-card";

type Sort = "departure" | "arrival" | "duration" | "fare" | "availability";

const TIME_SLOTS: [string, number, number][] = [
  ["Night 00–06", 0, 6], ["Morning 06–12", 6, 12], ["Afternoon 12–18", 12, 18], ["Evening 18–24", 18, 24],
];

const goodStatus = new Set(["AVL", "RAC"]);

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={cn(
        "rounded-md border px-2.5 py-1 text-xs font-medium",
        on ? "border-[var(--accent)] bg-[var(--accent)] text-white" : "border-line bg-surface text-muted hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

function toggle<T>(set: Set<T>, v: T) {
  const next = new Set(set);
  if (next.has(v)) next.delete(v);
  else next.add(v);
  return next;
}

export function Results({ params }: { params: SearchParams }) {
  const { client } = useYatrax();
  const { t } = useT();
  const { data, error } = useAsync<SearchResult>(() => client.search(params), [client, JSON.stringify(params)]);
  const festivals = useAsync<Festival[]>(() => client.festivals(), [client]).data ?? [];

  const [classes, setClasses] = useState<Set<string>>(new Set(params.class ? [params.class] : []));
  const [types, setTypes] = useState<Set<string>>(new Set());
  const [slots, setSlots] = useState<Set<number>>(new Set());
  const [onlyAvailable, setOnlyAvailable] = useState(false);
  const [sort, setSort] = useState<Sort>("departure");
  const [open, setOpen] = useState<Record<string, string | null>>({});

  const allClasses = useMemo(() => [...new Set(data?.trains.flatMap((t) => t.classes.map((c) => c.code)))], [data]);
  const allTypes = useMemo(() => [...new Map(data?.trains.map((t) => [t.type, t.type_name])).entries()], [data]);

  const visible = useMemo(() => {
    if (!data) return [];
    const relevant = (t: SearchTrain) => t.classes.filter((c) => classes.size === 0 || classes.has(c.code));
    const list = data.trains.filter((t) => {
      if (types.size && !types.has(t.type)) return false;
      if (slots.size && ![...slots].some((i) => hourIST(t.departure) >= TIME_SLOTS[i]![1] && hourIST(t.departure) < TIME_SLOTS[i]![2])) return false;
      const cs = relevant(t);
      if (cs.length === 0) return false;
      if (onlyAvailable && !cs.some((c) => goodStatus.has(c.availability.code))) return false;
      return true;
    });
    const minFare = (t: SearchTrain) => Math.min(...relevant(t).map((c) => c.fare?.total ?? Infinity));
    const bestChance = (t: SearchTrain) => Math.max(...relevant(t).map((c) => c.availability.confirm_chance));
    const key: Record<Sort, (t: SearchTrain) => number> = {
      departure: (t) => +new Date(t.departure),
      arrival: (t) => +new Date(t.arrival),
      duration: (t) => t.duration_min,
      fare: minFare,
      availability: (t) => -bestChance(t),
    };
    return [...list].sort((a, b) => key[sort](a) - key[sort](b));
  }, [data, classes, types, slots, onlyAvailable, sort]);

  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Searching trains" />;
  // Offer other ways when no running train has a free berth in the class asked for (sleeper by default).
  const wanted = params.class || "SL";
  const runningWith = data.trains.filter((t) => t.runs_on_date && t.classes.some((c) => c.code === wanted));
  const altClass = params.quota && params.quota !== "GN" ? null
    : runningWith.length > 0 && !runningWith.some((t) => t.classes.some((c) => c.code === wanted && c.availability.code === "AVL")) ? wanted : null;

  const gridClass = params.class || [...classes][0] || allClasses[0];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h1 className="text-lg font-semibold">
          {data.from.name} → {data.to.name}
          <span className="ml-2 text-sm font-normal text-muted">
            {dayLabel(data.date)} · {t("results.quota", { quota: t(`quota.${data.quota}`) })} · {t("results.count", { shown: visible.length, total: data.trains.length })}
          </span>
        </h1>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
          aria-label={t("results.sort")}
          className="h-9 rounded-lg border border-line bg-surface px-2 text-sm"
        >
          <option value="departure">{t("results.sort.departure")}</option>
          <option value="arrival">{t("results.sort.arrival")}</option>
          <option value="duration">{t("results.sort.duration")}</option>
          <option value="fare">{t("results.sort.fare")}</option>
          <option value="availability">{t("results.sort.availability")}</option>
        </select>
      </div>

      {altClass && <Alternatives from={data.from.code} to={data.to.code} date={data.date} cls={altClass} />}

      {(data.from_stations.length > 1 || data.to_stations.length > 1) && (
        <Alert tone="info">
          {t("results.nearby", { from: data.from_stations.map((s) => s.name).join(", "), to: data.to_stations.map((s) => s.name).join(", ") })}
        </Alert>
      )}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-surface p-3">
        <div className="flex flex-wrap items-center gap-1">
          <span className="mr-1 text-xs font-medium uppercase tracking-wide text-muted">{t("filter.class")}</span>
          {allClasses.map((c) => (
            <Chip key={c} on={classes.has(c)} onClick={() => setClasses(toggle(classes, c))}>
              {c}
            </Chip>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-1">
          <span className="mr-1 text-xs font-medium uppercase tracking-wide text-muted">{t("filter.type")}</span>
          {allTypes.map(([code, name]) => (
            <Chip key={code} on={types.has(code)} onClick={() => setTypes(toggle(types, code))}>
              {name}
            </Chip>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-1">
          <span className="mr-1 text-xs font-medium uppercase tracking-wide text-muted">{t("filter.departs")}</span>
          {TIME_SLOTS.map(([label], i) => (
            <Chip key={label} on={slots.has(i)} onClick={() => setSlots(toggle(slots, i))}>
              {t(`slot.${i}`)}
            </Chip>
          ))}
        </div>
        <label className="inline-flex cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" checked={onlyAvailable} onChange={(e) => setOnlyAvailable(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
          {t("results.onlyAvailable")}
        </label>
      </div>

      {data.dates && gridClass && <FlexGrid data={data} trains={visible} cls={gridClass} params={params} />}

      {visible.length === 0 ? (
        <div className="rounded-xl border border-dashed border-line p-8 text-center text-sm text-muted">
          {data.trains.length === 0 ? (
            <>
              No direct trains on this route{params.nearby ? "" : ". Try including nearby stations"}. Connecting journeys arrive with alternate routes in a later phase.
            </>
          ) : (
            "No trains match these filters."
          )}
        </div>
      ) : (
        visible.map((t) => (
          <TrainCard
            key={t.number}
            t={t}
            quota={data.quota as Quota}
            date={data.date}
            festivals={festivals}
            selected={open[t.number] ?? null}
            onSelect={(cls) => setOpen({ ...open, [t.number]: cls })}
          />
        ))
      )}
    </div>
  );
}

/** Trains × the ±3 days around the search date, for one class. Click a date to search it. */
function FlexGrid({ data, trains, cls, params }: { data: SearchResult; trains: SearchTrain[]; cls: string; params: SearchParams }) {
  const dates = data.dates!;
  return (
    <div className="overflow-x-auto rounded-xl border border-line bg-surface">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-line text-xs text-muted">
            <th className="px-3 py-2 text-left font-medium">{cls} availability, ±3 days</th>
            {dates.map((d) => (
              <th key={d} className={cn("px-2 py-2 text-center font-medium", d === data.date && "text-[var(--accent)]")}>
                <Link href={searchHref({ ...params, date: d })} className="hover:underline">
                  {dayLabel(d)}
                </Link>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {trains.map((t) => {
            const c = t.classes.find((x) => x.code === cls);
            return (
              <tr key={t.number} className="border-b border-line last:border-0">
                <td className="whitespace-nowrap px-3 py-1.5">
                  <span className="font-mono font-semibold">{t.number}</span> <span className="text-muted">{t.name}</span>
                </td>
                {dates.map((d, i) => (
                  <td key={d} className={cn("px-2 py-1.5 text-center", d === data.date && "bg-[var(--accent)]/5")}>
                    {c?.by_date?.[i] ? <AvailabilityPill a={c.by_date[i]!.availability} /> : <span className="text-xs text-muted">—</span>}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

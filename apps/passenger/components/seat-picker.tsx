"use client";

import type { SeatMap, SeatState } from "@yatrax/api-client";
import { cn } from "@yatrax/ui";
import { useState, type ReactNode } from "react";

export interface Picked {
  coach: string;
  berth: number;
  type: string;
}

const names: Record<string, string> = {
  LB: "Lower", MB: "Middle", UB: "Upper", SL: "Side lower", SU: "Side upper", W: "Window", M: "Middle", A: "Aisle",
};

function Berth({ s, coach, picked, onToggle }: { s: SeatState; coach: string; picked: boolean; onToggle: () => void }) {
  const free = s.status === "free";
  const label = `${coach} ${s.berth}, ${names[s.type]}: ${picked ? "selected" : free ? "free" : s.status === "booked" ? "booked" : `${s.rac ? "RAC" : s.quota} quota`}`;
  return (
    <button
      type="button"
      disabled={!free && !picked}
      onClick={onToggle}
      aria-pressed={picked}
      aria-label={label}
      title={label}
      className={cn(
        "flex h-9 w-10 flex-col items-center justify-center rounded border text-[10px] leading-none transition-colors",
        picked && "border-[var(--accent)] bg-[var(--accent)] text-white",
        !picked && free && "border-green-300 bg-green-50 text-green-900 hover:border-[var(--accent)]",
        !picked && s.status === "booked" && "cursor-not-allowed border-zinc-200 bg-zinc-200 text-zinc-400",
        !picked && s.status === "quota" && "cursor-not-allowed border-dashed border-zinc-300 bg-surface text-zinc-400",
      )}
    >
      <span className="font-semibold">{s.berth}</span>
      <span className="mt-0.5 opacity-80">{s.type}</span>
    </button>
  );
}

function Bay({ n, children }: { n: number; children: ReactNode }) {
  return (
    <div className="flex shrink-0 flex-col items-center gap-1 border-r border-dashed border-line px-2 last:border-r-0">
      <span className="text-[10px] text-muted">Bay {n}</span>
      {children}
    </div>
  );
}

/** Pick up to `max` berths on live occupancy for this journey. */
export function SeatPicker({ map, max, picked, onChange }: { map: SeatMap; max: number; picked: Picked[]; onChange: (p: Picked[]) => void }) {
  const [coach, setCoach] = useState(map.coaches[0]?.coach ?? "");
  const current = map.coaches.find((c) => c.coach === coach);
  const isPicked = (c: string, b: number) => picked.some((p) => p.coach === c && p.berth === b);
  const toggle = (s: SeatState) => {
    if (isPicked(coach, s.berth)) onChange(picked.filter((p) => !(p.coach === coach && p.berth === s.berth)));
    else if (picked.length < max) onChange([...picked, { coach, berth: s.berth, type: s.type }]);
  };

  const bays = new Map<number, SeatState[]>();
  for (const s of current?.seats ?? []) bays.set(s.bay, [...(bays.get(s.bay) ?? []), s]);
  const sleeper = map.layout === "sleeper_8" || map.layout === "sleeper_6";
  const per = map.layout === "sleeper_8" ? 3 : 2;
  const left = map.layout === "sitting_4" ? 2 : 3;

  return (
    <div>
      <div className="mb-3 flex flex-wrap gap-1">
        {map.coaches.map((c) => (
          <button
            key={c.coach}
            type="button"
            onClick={() => setCoach(c.coach)}
            className={cn(
              "rounded-md border px-2.5 py-1 text-xs font-medium",
              c.coach === coach ? "border-[var(--accent)] bg-[var(--accent)] text-white" : "border-line bg-surface hover:border-[var(--accent)]",
            )}
          >
            {c.coach} <span className="opacity-75">· {c.free} free</span>
          </button>
        ))}
      </div>
      <div className="flex overflow-x-auto rounded-lg border border-line bg-surface py-2">
        {[...bays.entries()].map(([n, seats]) =>
          sleeper ? (
            <Bay key={n} n={n}>
              <div className="flex gap-1">
                <div className="flex flex-col gap-1">
                  {seats.slice(0, per).map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
                </div>
                <div className="flex flex-col gap-1">
                  {seats.slice(per, per * 2).map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
                </div>
              </div>
              <div className="my-1 h-3 w-full rounded bg-canvas" />
              <div className="flex gap-1">
                {seats.slice(per * 2).map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
              </div>
            </Bay>
          ) : map.layout === "sleeper_4" ? (
            <Bay key={n} n={n}>
              <div className="grid grid-cols-2 gap-1">
                {seats.map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
              </div>
            </Bay>
          ) : (
            <Bay key={n} n={n}>
              <div className="flex flex-col gap-1">
                {seats.slice(0, left).map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
              </div>
              <div className="h-3 w-full rounded bg-canvas" />
              <div className="flex flex-col gap-1">
                {seats.slice(left).map((s) => <Berth key={s.berth} s={s} coach={coach} picked={isPicked(coach, s.berth)} onToggle={() => toggle(s)} />)}
              </div>
            </Bay>
          ),
        )}
      </div>
      <div className="mt-2 flex flex-wrap gap-3 text-xs text-muted">
        <span className="inline-flex items-center gap-1"><span className="h-3 w-3 rounded border border-green-300 bg-green-50" /> Free for your journey</span>
        <span className="inline-flex items-center gap-1"><span className="h-3 w-3 rounded bg-zinc-200" /> Booked</span>
        <span className="inline-flex items-center gap-1"><span className="h-3 w-3 rounded border border-dashed border-zinc-300" /> Other quota / RAC</span>
        <span>
          {picked.length} of {max} picked
        </span>
      </div>
    </div>
  );
}

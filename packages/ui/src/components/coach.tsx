"use client";

import type { Availability, CoachLayout, CoachSeat } from "@yatrax/api-client";
import type { ReactNode } from "react";
import { cn } from "./primitives";

/** Colour family for an availability status: green available, amber RAC, red waitlist, grey otherwise. */
export function availabilityTone(code: Availability["code"]) {
  switch (code) {
    case "AVL":
      return "bg-green-50 text-green-800 border-green-200";
    case "RAC":
      return "bg-amber-50 text-amber-800 border-amber-200";
    case "WL":
      return "bg-red-50 text-red-700 border-red-200";
    case "REGRET":
      return "bg-zinc-100 text-zinc-500 border-zinc-200 line-through decoration-zinc-400";
    default:
      return "bg-canvas text-muted border-line";
  }
}

export function AvailabilityPill({ a, className }: { a: Availability; className?: string }) {
  return (
    <span className={cn("inline-flex items-center rounded-md border px-1.5 py-0.5 text-xs font-semibold tabular-nums", availabilityTone(a.code), className)}>
      {a.label}
    </span>
  );
}

const berthName: Record<CoachSeat["type"], string> = {
  LB: "Lower", MB: "Middle", UB: "Upper", SL: "Side lower", SU: "Side upper", W: "Window", M: "Middle", A: "Aisle",
};

function Seat({ s, wide }: { s: CoachSeat; wide?: boolean }) {
  const lower = s.type === "LB" || s.type === "SL" || s.type === "W";
  return (
    <div
      title={`${s.number} · ${berthName[s.type]}`}
      className={cn(
        "flex h-8 flex-col items-center justify-center rounded border text-[10px] leading-none",
        wide ? "w-14" : "w-9",
        lower ? "border-[var(--accent)]/40 bg-[var(--accent)]/10" : "border-line bg-surface",
      )}
    >
      <span className="font-semibold text-ink">{s.number}</span>
      <span className="mt-0.5 text-muted">{s.type}</span>
    </div>
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

function groupBy(seats: CoachSeat[]) {
  const bays = new Map<number, CoachSeat[]>();
  for (const s of seats) bays.set(s.bay, [...(bays.get(s.bay) ?? []), s]);
  return [...bays.entries()];
}

/**
 * Top-down map of one coach. Sleeper bays show the six main berths facing each
 * other across the bay and the two side berths across the aisle.
 */
export function CoachMap({ coach }: { coach: CoachLayout }) {
  if (!coach.bookable || coach.seats.length === 0) {
    return <p className="text-sm text-muted">{coach.class_name} is not reserved, so it has no seat map.</p>;
  }
  const bays = groupBy(coach.seats);

  let body: ReactNode;
  if (coach.layout === "sleeper_8" || coach.layout === "sleeper_6") {
    const per = coach.layout === "sleeper_8" ? 3 : 2;
    body = bays.map(([n, seats]) => {
      const main = seats.slice(0, per * 2);
      const side = seats.slice(per * 2);
      return (
        <Bay key={n} n={n}>
          <div className="flex gap-1">
            <div className="flex flex-col gap-1">{main.slice(0, per).map((s) => <Seat key={s.number} s={s} />)}</div>
            <div className="flex flex-col gap-1">{main.slice(per).map((s) => <Seat key={s.number} s={s} />)}</div>
          </div>
          <div className="my-1 h-3 w-full rounded bg-canvas text-center text-[9px] leading-3 text-muted">aisle</div>
          <div className="flex gap-1">{side.map((s) => <Seat key={s.number} s={s} />)}</div>
        </Bay>
      );
    });
  } else if (coach.layout === "sleeper_4") {
    body = bays.map(([n, seats]) => (
      <Bay key={n} n={n}>
        <div className="grid grid-cols-2 gap-1 rounded border border-line p-1">
          {seats.map((s) => <Seat key={s.number} s={s} wide />)}
        </div>
        <span className="text-[10px] text-muted">Cabin {String.fromCharCode(64 + n)}</span>
      </Bay>
    ));
  } else {
    // Chair cars: one column per row, the aisle splitting the seats 3+3, 3+2 or 2+2.
    const left = coach.layout === "sitting_4" ? 2 : 3;
    body = bays.map(([n, seats]) => (
      <Bay key={n} n={n}>
        <div className="flex flex-col gap-1">{seats.slice(0, left).map((s) => <Seat key={s.number} s={s} />)}</div>
        <div className="h-3 w-full rounded bg-canvas" />
        <div className="flex flex-col gap-1">{seats.slice(left).map((s) => <Seat key={s.number} s={s} />)}</div>
      </Bay>
    ));
  }

  return (
    <div>
      <div className="flex overflow-x-auto rounded-lg border border-line bg-surface py-2">{body}</div>
      <div className="mt-2 flex flex-wrap gap-3 text-xs text-muted">
        <span className="inline-flex items-center gap-1">
          <span className="h-3 w-3 rounded border border-[var(--accent)]/40 bg-[var(--accent)]/10" /> Lower / window
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="h-3 w-3 rounded border border-line bg-surface" /> Other berths
        </span>
        <span>Free and booked berths show on the seat map when you book.</span>
      </div>
    </div>
  );
}

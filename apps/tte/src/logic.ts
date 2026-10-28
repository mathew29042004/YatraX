// Offline rules. The device applies its own operations to the downloaded bundle so the
// TTE sees the result at once; on sync the server applies the same operations and its
// answer replaces this view (server-side allocation wins).
import type { TTEBundle, TTEFine, TTEOp, TTEPassenger } from "@yatrax/api-client";

/** Stations past the boarding point before a no-show's berth can be released. */
export const NO_SHOW_AFTER = 2;

export type View = {
  passengers: TTEPassenger[];
  fines: TTEFine[];
  notes: string[]; // complaints and hand-offs made offline
};

export const fareIndex = (n: number, i: number, j: number) => (i * (2 * n - i - 1)) / 2 + (j - i - 1);

export function fareFor(b: TTEBundle, cls: string, from: number, to: number): number {
  const table = b.fares[cls];
  if (!table || to <= from) return 0;
  return table[fareIndex(b.stops.length, from, to)] ?? 0;
}

export const place = (p: { coach?: string; berth?: number }) => (p.coach ? `${p.coach}/${p.berth}` : "");

/** Stops [from, to) a passenger holds on their berth. */
function span(p: TTEPassenger, stops: Record<string, number>): [number, number] {
  if (p.status === "NOSHOW") return [p.from_idx, p.no_show_at ? (stops[p.no_show_at] ?? p.to_idx) : p.from_idx];
  return [p.from_idx, p.to_idx];
}

/** Whether a berth is free over stops [from, to), given the current view. */
export function berthFree(v: View, b: TTEBundle, coach: string, berth: number, from: number, to: number, ignore?: string) {
  const idx = Object.fromEntries(b.stops.map((s) => [s.code, s.seq]));
  return !v.passengers.some((p) => {
    if (p.id === ignore || p.coach !== coach || p.berth !== berth || p.status === "RAC" || p.status === "WL") return false;
    const [f, t] = span(p, idx);
    return f < to && from < t;
  });
}

/** Berths of a class free for the rest of the run from stop k: the TTE can allot them. */
export function vacantBerths(v: View, b: TTEBundle, cls: string, k: number, to = b.stops.length - 1) {
  const out: Array<{ coach: string; berth: number; type: string }> = [];
  for (const c of b.coaches) {
    if (c.class !== cls) continue;
    for (const x of c.berths) {
      if (!x.rac && berthFree(v, b, c.coach, x.berth, k, to)) out.push({ coach: c.coach, berth: x.berth, type: x.type });
    }
  }
  return out;
}

/** The RAC passenger who should get a berth released at stop k: lowest RAC number still travelling on. */
/** `releasing` is the no-show whose berth this is (still shown as holding it until the op is applied). */
export function nextRAC(v: View, b: TTEBundle, cls: string, coach: string, berth: number, k: number, releasing?: string): TTEPassenger | undefined {
  return v.passengers
    .filter((p) => p.status === "RAC" && p.class === cls && p.to_idx > k)
    .sort((a, c) => (a.rac_no ?? 0) - (c.rac_no ?? 0))
    .find((p) => berthFree(v, b, coach, berth, Math.max(k, p.from_idx), p.to_idx, releasing));
}

type Check = { ok: true } | { ok: false; why: string };

export function canNoShow(b: TTEBundle, p: TTEPassenger, k: number): Check {
  if (p.status !== "CNF") return { ok: false, why: `${p.name} is ${p.status}, not confirmed` };
  if (p.boarded_at) return { ok: false, why: `${p.name} has boarded` };
  if (k < p.from_idx + NO_SHOW_AFTER)
    return { ok: false, why: `Boards at ${b.stops[p.from_idx]?.code}; wait until the train has passed ${NO_SHOW_AFTER} stations beyond it` };
  if (k >= p.to_idx) return { ok: false, why: "Their journey has ended" };
  return { ok: true };
}

/** Applies queued operations to the server's bundle, as the server will (barring conflicts). */
export function applyLocal(b: TTEBundle, ops: TTEOp[]): View {
  const v: View = { passengers: b.passengers.map((p) => ({ ...p })), fines: [...b.fines], notes: [] };
  const idx = Object.fromEntries(b.stops.map((s) => [s.code, s.seq]));
  const byId = (id: unknown) => v.passengers.find((p) => p.id === id);
  let localReceipt = 0;
  for (const op of ops) {
    const x = op.payload as Record<string, unknown>;
    switch (op.kind) {
      case "board": {
        const p = byId(x.passenger_id);
        if (p && p.status !== "NOSHOW") p.boarded_at ??= op.at;
        break;
      }
      case "no_show": {
        const p = byId(x.passenger_id);
        const k = idx[x.station as string];
        if (!p || k === undefined || !canNoShow(b, p, k).ok) break;
        p.status = "NOSHOW";
        p.no_show_at = x.station as string;
        const chosen = x.assign_to ? byId(x.assign_to) : undefined;
        const q = chosen?.status === "RAC" ? chosen : nextRAC(v, b, p.class, p.coach!, p.berth!, k);
        if (q && q.status === "RAC" && berthFree(v, b, p.coach!, p.berth!, Math.max(k, q.from_idx), q.to_idx)) {
          Object.assign(q, { status: "CNF", coach: p.coach, berth: p.berth, berth_type: p.berth_type, rac_no: undefined });
        }
        break;
      }
      case "assign_berth": {
        const q = byId(x.passenger_id);
        const k = idx[x.station as string] ?? 0;
        if (q && q.status === "RAC" && berthFree(v, b, x.coach as string, x.berth as number, Math.max(k, q.from_idx), q.to_idx)) {
          Object.assign(q, { status: "CNF", coach: x.coach, berth: x.berth, rac_no: undefined });
        }
        break;
      }
      case "fine": {
        const from = idx[x.from as string] ?? 0;
        const to = idx[x.to as string] ?? 0;
        let fare = fareFor(b, x.class as string, from, to);
        if (x.reason === "wrong_class") fare = Math.max(0, fare - fareFor(b, x.paid_class as string, from, to));
        v.fines.push({
          receipt_no: `PENDING-${++localReceipt}`, name: x.name as string, from: x.from as string, to: x.to as string,
          class: x.class as string, reason: x.reason as string, fare, penalty: b.penalty, total: fare + b.penalty,
          coach: x.coach as string | undefined, berth: x.berth as number | undefined, created_at: op.at,
        });
        if (x.coach) {
          v.passengers.push({
            id: `local-${op.op_id}`, booking_id: "", pnr: "pending", seq: 1, name: x.name as string, age: x.age as number,
            gender: x.gender as string, status: "CNF", coach: x.coach as string, berth: x.berth as number, class: x.class as string,
            quota: "GN", from_idx: from, to_idx: to, source: "tte", boarded_at: op.at,
          });
        }
        break;
      }
      case "complaint":
        v.notes.push(`Complaint (${x.category}): ${x.description}`);
        break;
      case "handoff":
        v.notes.push(`Chart handed to ${x.to_employee_id} at ${x.station}`);
        break;
    }
  }
  return v;
}

/** A sensible default for "where is the train": the last stop whose departure is behind the clock. */
export function guessStop(b: TTEBundle, now: Date): number {
  let k = 0;
  for (const s of b.stops) {
    const t = s.dep ?? s.arr;
    if (t && new Date(t) <= now) k = s.seq;
  }
  return k;
}

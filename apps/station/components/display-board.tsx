"use client";

// The classic yellow-on-black arrivals and departures board, following the live simulation.
import type { BoardRow, StationBoard } from "@yatrax/api-client";
import { cn, hhmm, useYatrax } from "@yatrax/ui";
import { useEffect, useState } from "react";

function status(r: BoardRow, side: "arr" | "dep") {
  if (r.state === "cancelled") return { text: "CANCELLED", tone: "text-red-400" };
  if (r.state === "on_platform") return { text: side === "dep" ? "ON PLATFORM" : "ARRIVED", tone: "text-green-400" };
  if (r.state === "arriving") return { text: "ARRIVING", tone: "text-green-400 animate-pulse" };
  if (r.state === "departed") return { text: "DEPARTED", tone: "text-white/40" };
  if (r.state === "arrived") return { text: "ARRIVED", tone: "text-white/40" };
  if (r.delay_min >= 5) return { text: `LATE ${r.delay_min}M`, tone: "text-red-400" };
  return { text: "RIGHT TIME", tone: "text-board-amber" };
}

function Rows({ rows, side }: { rows: BoardRow[]; side: "arr" | "dep" }) {
  return (
    <table aria-label={side === "arr" ? "Arrivals" : "Departures"} className="w-full font-mono text-[13px] uppercase sm:text-sm">
      <thead className="text-left text-xs text-white/50">
        <tr>
          <th className="px-3 py-1.5 font-normal">Train</th>
          <th className="font-normal">{side === "arr" ? "From" : "To"}</th>
          <th className="px-1 font-normal">Sch</th>
          <th className="px-1 font-normal">Exp</th>
          <th className="px-1 font-normal">PF</th>
          <th className="pr-3 font-normal">Status</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const sch = side === "arr" ? r.sched_arr : r.sched_dep;
          const exp = side === "arr" ? r.exp_arr : r.exp_dep;
          const st = status(r, side);
          return (
            <tr key={r.train + r.origin_date} className={cn("border-t border-white/10 text-board-amber", r.state === "departed" && "opacity-50")}>
              <td className="px-3 py-1.5">
                <span className="font-bold">{r.train}</span> <span className="hidden md:inline">{r.name.slice(0, 22)}</span>
              </td>
              <td className="max-w-[9rem] truncate px-1">{side === "arr" ? r.from_name : r.to_name}</td>
              <td className="px-1">{hhmm(sch)}</td>
              <td className={cn("px-1", exp && sch && exp !== sch && "text-red-400")}>{hhmm(exp)}</td>
              <td className={cn("px-1 text-lg font-bold", r.platform_changed && "animate-pulse text-red-400")}>{r.platform}</td>
              <td className={cn("pr-3", st.tone)}>{st.text}</td>
            </tr>
          );
        })}
        {rows.length === 0 && (
          <tr>
            <td colSpan={6} className="px-3 py-4 text-center text-white/40">
              No trains
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

/** Live arrivals and departures for a station, refreshed every 10 seconds. */
export function DisplayBoard({ code, hours = 3, big = false }: { code: string; hours?: number; big?: boolean }) {
  const { client } = useYatrax();
  const [board, setBoard] = useState<StationBoard | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () =>
      client
        .stationBoard(code, hours)
        .then((b) => alive && (setBoard(b), setError(null)))
        .catch(() => alive && setError("Board offline"));
    load();
    const t = setInterval(load, 10000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [client, code, hours]);

  const arrivals = (board?.trains ?? []).filter((r) => r.exp_arr).slice(0, big ? 14 : 10);
  const departures = (board?.trains ?? []).filter((r) => r.exp_dep).slice(0, big ? 14 : 10);
  return (
    <div className={cn("overflow-hidden rounded-xl bg-board shadow-lg", big && "rounded-none")}>
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2 font-mono text-board-amber">
        <span className={cn("font-bold uppercase tracking-wider", big ? "text-2xl" : "text-sm")}>
          {board?.station.name ?? code} <span className="text-white/50">{code}</span>
        </span>
        <span className={cn(big ? "text-2xl" : "text-sm")}>{board ? hhmm(board.sim_now) : "--:--"}</span>
      </div>
      {error && <div className="px-4 py-2 font-mono text-sm text-red-400">{error}</div>}
      <div className={cn("grid gap-px bg-white/10", big ? "lg:grid-cols-2" : "xl:grid-cols-2")}>
        <div className="bg-board">
          <div className="px-3 pt-2 font-mono text-xs font-bold tracking-widest text-white/70">ARRIVALS · आगमन</div>
          <Rows rows={arrivals} side="arr" />
        </div>
        <div className="bg-board">
          <div className="px-3 pt-2 font-mono text-xs font-bold tracking-widest text-white/70">DEPARTURES · प्रस्थान</div>
          <Rows rows={departures} side="dep" />
        </div>
      </div>
    </div>
  );
}

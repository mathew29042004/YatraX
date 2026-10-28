"use client";

import type { BotFlag, Integrity, RoomMonitor, WaitRoom } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, Stat, useYatrax } from "@yatrax/ui";
import { useEffect, useState } from "react";

function tomorrowIST() {
  const d = new Date(Date.now() + 24 * 3600_000);
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Kolkata" }).format(d);
}

const clock = (iso: string) =>
  new Intl.DateTimeFormat("en-IN", { timeZone: "Asia/Kolkata", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(
    new Date(iso),
  );

/** Tatkal waiting rooms: create a drill, then watch the queue, sell-out and the bot detector live. */
export function TatkalMonitor() {
  const { client } = useYatrax();
  const [rooms, setRooms] = useState<WaitRoom[]>([]);
  const [roomId, setRoomId] = useState<string | null>(null);
  const [mon, setMon] = useState<RoomMonitor | null>(null);
  const [bots, setBots] = useState<BotFlag[]>([]);
  const [integ, setInteg] = useState<Integrity | null>(null);
  const [date, setDate] = useState(tomorrowIST());
  const [ac, setAc] = useState(false);
  const [opensIn, setOpensIn] = useState(60);
  const [rate, setRate] = useState(100);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => {
    const load = () =>
      client
        .waitrooms()
        .then((rs) => {
          setRooms(rs);
          setRoomId((cur) => cur ?? rs.find((r) => r.state !== "closed")?.id ?? rs[0]?.id ?? null);
        })
        .catch(() => {});
    load();
    const t = setInterval(load, 15_000);
    return () => clearInterval(t);
  }, [client]);

  useEffect(() => {
    if (!roomId) return;
    let alive = true;
    const load = async () => {
      try {
        const [m, b, i] = await Promise.all([client.roomMonitor(roomId), client.roomBots(roomId), client.integrity()]);
        if (!alive) return;
        setMon(m);
        setBots(b);
        setInteg(i);
      } catch {
        /* keep the last good numbers */
      }
    };
    load();
    const t = setInterval(load, 2000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [client, roomId]);

  async function drill() {
    setMsg(null);
    try {
      const r = await client.createDrill(date, ac, opensIn, rate);
      setRooms((rs) => [r, ...rs]);
      setRoomId(r.id);
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Could not create the drill");
    }
  }

  const room = mon?.room;
  return (
    <Card>
      <CardTitle hint="refreshes every 2s">Tatkal waiting rooms</CardTitle>
      <div className="flex flex-wrap items-end gap-2 text-sm">
        <div>
          <Label htmlFor="dr-date">Journey date</Label>
          <Input id="dr-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </div>
        <div>
          <Label htmlFor="dr-open">Opens in (s)</Label>
          <Input id="dr-open" type="number" min={10} value={opensIn} onChange={(e) => setOpensIn(Number(e.target.value))} className="w-24" />
        </div>
        <div>
          <Label htmlFor="dr-rate">Admit / s</Label>
          <Input id="dr-rate" type="number" min={1} value={rate} onChange={(e) => setRate(Number(e.target.value))} className="w-24" />
        </div>
        <label className="flex items-center gap-1.5 pb-2">
          <input type="checkbox" checked={ac} onChange={(e) => setAc(e.target.checked)} /> AC classes
        </label>
        <Button onClick={drill}>Create drill room</Button>
      </div>
      {msg && (
        <div className="mt-3">
          <Alert>{msg}</Alert>
        </div>
      )}
      {rooms.length > 0 && (
        <select
          aria-label="Room"
          value={roomId ?? ""}
          onChange={(e) => setRoomId(e.target.value)}
          className="mt-4 w-full rounded-md border border-line bg-surface px-2 py-1.5 text-sm"
        >
          {rooms.map((r) => (
            <option key={r.id} value={r.id}>
              {r.kind} · {r.ac ? "AC" : "non-AC"} · {r.journey_date} · opens {clock(r.opens_at)} · {r.state}
            </option>
          ))}
        </select>
      )}
      {room && mon && (
        <div className="mt-4 space-y-4">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="State" value={<Badge tone={room.state === "open" ? "ok" : "neutral"}>{room.state}</Badge>} sub={`opens ${clock(room.opens_at)}`} />
            <Stat label="Still waiting" value={mon.waiting.toLocaleString("en-IN")} sub={`${room.admitted.toLocaleString("en-IN")} admitted of ${room.queue.toLocaleString("en-IN")}`} />
            <Stat label="Blocked" value={mon.blocked.toLocaleString("en-IN")} sub={`${mon.challenged} verifying · ${mon.verified} verified`} />
            <Stat
              label="Integrity"
              value={integ && integ.overlapping_allocations === 0 && integ.paid_without_ticket === 0 && integ.trial_balance === 0 ? "OK ✓" : "CHECK"}
              sub={integ ? `${integ.overlapping_allocations} overlaps · ${integ.paid_without_ticket} paid w/o ticket` : ""}
            />
          </div>
          {Object.keys(mon.stats).length > 0 && (
            <div className="flex flex-wrap gap-2 text-xs">
              {Object.entries(mon.stats)
                .sort()
                .map(([k, v]) => (
                  <Badge key={k}>
                    {k.replaceAll("_", " ")}: {v.toLocaleString("en-IN")}
                  </Badge>
                ))}
            </div>
          )}
          {Object.keys(mon.sold_out_seconds).length > 0 && (
            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted">Sold out after opening</h3>
              <div className="flex flex-wrap gap-2 text-sm">
                {Object.entries(mon.sold_out_seconds)
                  .sort((a, b) => a[1] - b[1])
                  .map(([k, s]) => (
                    <span key={k} className="rounded-md bg-canvas px-2 py-1 font-mono">
                      {k} {s.toFixed(0)}s
                    </span>
                  ))}
              </div>
            </div>
          )}
          {Object.keys(mon.top_reasons).length > 0 && (
            <div>
              <h3 className="mb-1 text-xs font-semibold text-muted">Why accounts were flagged</h3>
              <ul className="space-y-0.5 text-sm">
                {Object.entries(mon.top_reasons)
                  .sort((a, b) => b[1] - a[1])
                  .map(([why, n]) => (
                    <li key={why} className="flex justify-between">
                      <span>{why}</span>
                      <span className="font-mono">{n.toLocaleString("en-IN")}</span>
                    </li>
                  ))}
              </ul>
            </div>
          )}
          {bots.length > 0 && (
            <div className="max-h-64 overflow-auto">
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-surface text-left text-muted">
                  <tr>
                    <th className="py-1">Account</th>
                    <th>Score</th>
                    <th>Action</th>
                    <th>Device / IP</th>
                  </tr>
                </thead>
                <tbody>
                  {bots.map((b) => (
                    <tr key={b.user_id} className="border-t border-line" title={b.reasons.join(", ")}>
                      <td className="py-1">{b.name}</td>
                      <td className="font-mono">{b.score}</td>
                      <td>
                        <Badge tone={b.action === "block" ? "danger" : "saffron"}>{b.action}</Badge>
                      </td>
                      <td className="font-mono text-muted">
                        {b.device.slice(0, 14)} · {b.ip}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

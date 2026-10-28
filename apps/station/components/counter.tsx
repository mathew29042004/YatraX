"use client";

import type { Group, Stay } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, cn, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";

const when = (iso: string) => new Date(iso).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: "Asia/Kolkata" });

/** The station counter: guests checking in to retiring rooms, dormitories and lockers. */
export function StaysDesk({ code }: { code: string }) {
  const { client } = useYatrax();
  const [list, setList] = useState<Stay[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.stayDesk(code).then(setList).catch((e: Error) => setError(e.message)), [client, code]);
  useEffect(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [load]);
  const act = (s: Stay, action: "check_in" | "check_out") =>
    client.advanceStay(code, s.id, action).then(load).catch((e) => setError(e instanceof ApiError ? e.message : "Could not update"));
  return (
    <Card>
      <CardTitle hint="booked online against a ticket">Retiring rooms and cloak room</CardTitle>
      {error && <Alert>{error}</Alert>}
      {list && list.length === 0 && <p className="text-sm text-muted">No stays booked for today or the coming days.</p>}
      <ul className="divide-y divide-line text-sm">
        {(list ?? []).map((s) => (
          <li key={s.id} className={cn("flex flex-wrap items-center gap-2 py-2", s.status === "completed" && "opacity-60")}>
            <span className="font-mono text-xs">{s.code}</span>
            <span className="font-medium">{s.guest}</span>
            <span className="text-muted">
              {s.units} × {s.unit_name} · {when(s.check_in)} – {when(s.check_out)} · PNR {s.pnr}
            </span>
            <Badge tone={s.payment === "counter" && s.status === "booked" ? "saffron" : "neutral"} className="ml-auto">
              {s.payment === "counter" ? `collect ₹${s.amount}` : "paid online"}
            </Badge>
            {s.status === "booked" && (
              <Button size="sm" onClick={() => act(s, "check_in")}>
                Check in
              </Button>
            )}
            {s.status === "checked_in" && (
              <Button size="sm" variant="secondary" onClick={() => act(s, "check_out")}>
                Check out
              </Button>
            )}
            {s.status === "completed" && <span className="text-xs text-ok">Checked out</span>}
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** Commercial desk: group requests for trains leaving this station. */
export function GroupDesk() {
  const { client } = useYatrax();
  const [list, setList] = useState<Group[] | null>(null);
  const [notes, setNotes] = useState<Record<number, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const load = useCallback(() => client.groupQueue().then(setList).catch((e: Error) => setError(e.message)), [client]);
  useEffect(() => {
    load();
  }, [load]);
  async function decide(g: Group, approve: boolean) {
    setBusy(g.id);
    setError(null);
    try {
      await client.decideGroup(g.id, approve, notes[g.id] ?? (approve ? "Approved: pay within 48 hours" : ""));
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not decide");
    } finally {
      setBusy(null);
    }
  }
  const pending = (list ?? []).filter((g) => g.status === "pending").length;
  return (
    <Card>
      <CardTitle hint={pending ? `${pending} waiting` : "weddings, pilgrimages, school trips"}>Group bookings</CardTitle>
      {error && <Alert>{error}</Alert>}
      {list && list.length === 0 && <p className="text-sm text-muted">No group requests for trains from this station.</p>}
      <ul className="divide-y divide-line text-sm">
        {(list ?? []).map((g) => (
          <li key={g.id} className="py-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-muted">#{g.id}</span>
              <span className="font-medium">
                {g.passengers.length} × {g.class} · {g.train} {g.train_name}
              </span>
              <span className="text-muted">
                {g.from} → {g.to} · {g.date} · {g.purpose} · {g.leader} ({g.contact})
              </span>
              <Badge tone={g.status === "pending" ? "saffron" : g.status === "approved" ? "ok" : "neutral"} className="ml-auto">
                {g.status}
              </Badge>
            </div>
            {g.status === "pending" && (
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <Input
                  className="h-8 max-w-72 text-sm"
                  aria-label="Note to the group leader"
                  placeholder="Note to the leader (optional)"
                  value={notes[g.id] ?? ""}
                  onChange={(e) => setNotes({ ...notes, [g.id]: e.target.value })}
                />
                <Button size="sm" disabled={busy === g.id} onClick={() => decide(g, true)}>
                  Approve & hold berths
                </Button>
                <Button size="sm" variant="ghost" disabled={busy === g.id} onClick={() => decide(g, false)}>
                  Reject
                </Button>
              </div>
            )}
            {g.note && g.status !== "pending" && <p className="mt-1 text-xs text-muted">{g.note}</p>}
          </li>
        ))}
      </ul>
    </Card>
  );
}

"use client";

import type { Group, PassengerInput } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, Label, RequireRole, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { SiteHeader } from "@/components/site-header";

const PURPOSES = ["wedding", "pilgrimage", "school trip", "sports team", "office trip", "other"];
const statusTone = { pending: "saffron", approved: "ok", rejected: "danger", cancelled: "neutral" } as const;

/** "Name, age, M/F" per line → passengers; bad lines are reported, not guessed. */
function parsePassengers(text: string): { pax: PassengerInput[]; bad: number[] } {
  const pax: PassengerInput[] = [];
  const bad: number[] = [];
  text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
    .forEach((line, i) => {
      const [name, age, gender] = line.split(",").map((x) => x.trim());
      const g = (gender ?? "").toUpperCase()[0];
      if (!name || !Number(age) || !["M", "F", "O"].includes(g ?? "")) bad.push(i + 1);
      else pax.push({ name, age: Number(age), gender: g as PassengerInput["gender"], berth_pref: "" });
    });
  return { pax, bad };
}

function GroupDesk() {
  const { client } = useYatrax();
  const [form, setForm] = useState({ train: "12623", from: "MAS", to: "ERN", date: new Date(Date.now() + 30 * 86_400_000).toISOString().slice(0, 10), class: "SL", purpose: "wedding", contact: "" });
  const [list, setList] = useState("");
  const [mine, setMine] = useState<Group[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.myGroups().then(setMine).catch(() => undefined), [client]);
  useEffect(() => {
    load();
  }, [load]);
  const { pax, bad } = parsePassengers(list);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit() {
    setError(null);
    setMsg(null);
    if (bad.length) return setError(`Fix line ${bad.join(", ")}: write "Name, age, M or F".`);
    try {
      const g = await client.requestGroup({ ...form, from: form.from.toUpperCase(), to: form.to.toUpperCase(), passengers: pax });
      setMsg(`Request #${g.id} sent to the commercial desk at ${g.from}. You'll be notified when it's decided.`);
      setList("");
      load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not send the request");
    }
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-xl font-semibold">Group booking desk</h1>
        <p className="text-sm text-muted">
          For weddings, pilgrimages and school trips: 7 to 60 passengers on one train. The commercial desk at the boarding station checks the
          berths and holds them together for 48 hours while you pay.
        </p>
      </div>
      <Card>
        <CardTitle>New request</CardTitle>
        <div className="grid gap-3 sm:grid-cols-4">
          <div>
            <Label htmlFor="g-train">Train</Label>
            <Input id="g-train" value={form.train} onChange={set("train")} />
          </div>
          <div>
            <Label htmlFor="g-from">From (code)</Label>
            <Input id="g-from" value={form.from} onChange={set("from")} />
          </div>
          <div>
            <Label htmlFor="g-to">To (code)</Label>
            <Input id="g-to" value={form.to} onChange={set("to")} />
          </div>
          <div>
            <Label htmlFor="g-date">Date</Label>
            <Input id="g-date" type="date" value={form.date} onChange={set("date")} />
          </div>
          <div>
            <Label htmlFor="g-class">Class</Label>
            <select id="g-class" className="h-10 w-full rounded-lg border border-line bg-surface px-2 text-sm" value={form.class} onChange={set("class")}>
              {["SL", "3A", "2A", "3E", "CC", "2S"].map((c) => (
                <option key={c}>{c}</option>
              ))}
            </select>
          </div>
          <div>
            <Label htmlFor="g-purpose">Purpose</Label>
            <select id="g-purpose" className="h-10 w-full rounded-lg border border-line bg-surface px-2 text-sm" value={form.purpose} onChange={set("purpose")}>
              {PURPOSES.map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </div>
          <div className="sm:col-span-2">
            <Label htmlFor="g-contact">Contact phone</Label>
            <Input id="g-contact" value={form.contact} onChange={set("contact")} placeholder="98765 43210" />
          </div>
        </div>
        <div className="mt-3">
          <Label htmlFor="g-pax">Passengers: one per line, "Name, age, M or F"</Label>
          <textarea
            id="g-pax"
            rows={6}
            className="w-full rounded-lg border border-line bg-surface px-3 py-2 font-mono text-sm"
            placeholder={"Lakshmi Iyer, 52, F\nSuresh Iyer, 55, M\n…"}
            value={list}
            onChange={(e) => setList(e.target.value)}
          />
          <p className="text-xs text-muted">
            {pax.length} passengers{bad.length ? ` · check line ${bad.join(", ")}` : ""}
          </p>
        </div>
        {error && <div className="mt-2"><Alert>{error}</Alert></div>}
        {msg && <div className="mt-2"><Alert tone="info">{msg}</Alert></div>}
        <Button className="mt-3" onClick={submit} disabled={pax.length < 7 || pax.length > 60 || form.contact.length < 10}>
          Send request
        </Button>
      </Card>
      {mine.length > 0 && (
        <Card>
          <CardTitle>Your requests</CardTitle>
          <ul className="divide-y divide-line text-sm">
            {mine.map((g) => (
              <li key={g.id} className="py-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs text-muted">#{g.id}</span>
                  <span className="font-medium">
                    {g.train} {g.train_name}
                  </span>
                  <span className="text-muted">
                    {g.from} → {g.to} · {g.date} · {g.class} · {g.passengers.length} passengers · {g.purpose}
                  </span>
                  <Badge tone={statusTone[g.status]} className="ml-auto">
                    {g.status}
                  </Badge>
                  {g.status === "pending" && (
                    <button className="text-xs text-danger hover:underline" onClick={() => client.cancelGroup(g.id).then(load).catch(() => undefined)}>
                      Withdraw
                    </button>
                  )}
                </div>
                {g.note && <p className="mt-1 text-xs text-muted">Desk: {g.note}</p>}
                {g.bookings.length > 0 && (
                  <p className="mt-1 text-xs">
                    Held for 48 hours — pay for each:{" "}
                    {g.bookings.map((id, i) => (
                      <Link key={id} href={`/booking/${id}`} className="mr-2 text-[var(--accent)] hover:underline">
                        party {i + 1}
                      </Link>
                    ))}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl px-4 py-6">
        <RequireRole>{() => <GroupDesk />}</RequireRole>
      </main>
    </>
  );
}

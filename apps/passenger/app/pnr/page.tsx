"use client";

import type { Booking } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, Input, useYatrax } from "@yatrax/ui";
import { useState, type FormEvent } from "react";
import { clockIST, dayLabel } from "@/components/format";
import { SiteHeader } from "@/components/site-header";

export default function PnrPage() {
  const { client } = useYatrax();
  const [pnr, setPnr] = useState("");
  const [b, setB] = useState<Booking | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function check(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setB(null);
    try {
      setB(await client.pnr(pnr.replace(/\D/g, "")));
    } catch (err) {
      setError(err instanceof ApiError && err.status === 404 ? "No booking with that PNR." : "Could not check the PNR right now.");
    }
  }

  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-2xl space-y-5 px-4 py-6">
        <h1 className="text-xl font-semibold">PNR status</h1>
        <form onSubmit={check} className="flex gap-2">
          <Input value={pnr} onChange={(e) => setPnr(e.target.value)} placeholder="10-digit PNR" inputMode="numeric" maxLength={12} aria-label="PNR" />
          <Button type="submit">Check</Button>
        </form>
        {error && <Alert>{error}</Alert>}
        {b && (
          <Card>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-lg font-semibold">{b.pnr}</span>
              <Badge tone={b.status === "CONFIRMED" ? "ok" : "neutral"}>{b.status}</Badge>
            </div>
            <div className="mt-2 text-sm">
              <span className="font-mono font-semibold">{b.train}</span> {b.train_name} · {b.class} · {b.quota}
            </div>
            <div className="text-sm text-muted">
              {b.from_name} {clockIST(b.departure)} → {b.to_name} {clockIST(b.arrival)} · {dayLabel(b.departure)}
            </div>
            <table className="mt-4 w-full text-sm">
              <thead className="text-left text-xs text-muted">
                <tr>
                  <th className="py-1">Passenger</th>
                  <th className="py-1">Current status</th>
                </tr>
              </thead>
              <tbody>
                {b.passengers.map((p) => (
                  <tr key={p.seq} className="border-t border-line">
                    <td className="py-1.5">
                      {p.seq}. {p.name}
                    </td>
                    <td className="py-1.5 font-medium">
                      {p.status === "CNF" ? `CNF ${p.coach}/${p.berth}` : p.status === "RAC" ? `RAC ${p.rac_no}` : p.status === "NOSHOW" ? "Did not board" : p.status === "CAN" ? "Cancelled" : `WL ${p.wl_no}`}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="mt-3 text-xs text-muted">Names are shown as initials to protect passengers&apos; privacy.</p>
          </Card>
        )}
      </main>
    </>
  );
}

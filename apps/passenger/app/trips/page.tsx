"use client";

import type { Booking } from "@yatrax/api-client";
import { Alert, Badge, RequireRole, Spinner, useAsync, useT, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { clockIST, dayLabel, rupees } from "@/components/format";
import { SiteHeader } from "@/components/site-header";

const label: Record<Booking["status"], [string, "ok" | "saffron" | "danger" | "neutral"]> = {
  CONFIRMED: ["Confirmed", "ok"],
  HELD: ["Awaiting payment", "saffron"],
  PAYMENT_PENDING: ["Payment in progress", "saffron"],
  FAILED: ["Failed · refund due", "danger"],
  CANCELLED: ["Cancelled", "neutral"],
  EXPIRED: ["Expired", "neutral"],
};

function Trips() {
  const { client } = useYatrax();
  const { data, error } = useAsync<Booking[]>(() => client.bookings(), [client]);
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading trips" />;
  if (data.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-line p-8 text-center text-sm text-muted">
        No trips yet.{" "}
        <Link href="/" className="text-[var(--accent)] hover:underline">
          Search trains
        </Link>
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {data.map((b) => (
        <Link key={b.id} href={`/booking/${b.id}`} className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-xl border border-line bg-surface p-4 hover:border-[var(--accent)]">
          <div className="min-w-0 flex-1">
            <div className="font-medium">
              <span className="font-mono">{b.train}</span> {b.train_name}
            </div>
            <div className="text-sm text-muted">
              {b.from} {clockIST(b.departure)} → {b.to} {clockIST(b.arrival)} · {dayLabel(b.departure)} · {b.class} ·{" "}
              {b.passengers.map((p) => (p.status === "CNF" ? `${p.coach}/${p.berth}` : p.status === "CAN" ? "CAN" : p.status === "NOSHOW" ? "no-show" : `${p.status} ${p.position ?? ""}`)).join(", ")}
            </div>
          </div>
          {b.pnr && <span className="font-mono text-sm">PNR {b.pnr}</span>}
          <span className="tabular-nums">{rupees(b.total_fare)}</span>
          <Badge tone={label[b.status][1]}>{label[b.status][0]}</Badge>
        </Link>
      ))}
    </div>
  );
}

export default function Page() {
  const { t } = useT();
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl px-4 py-6">
        <h1 className="mb-4 text-xl font-semibold">{t("trips.title")}</h1>
        <RequireRole>{() => <Trips />}</RequireRole>
      </main>
    </>
  );
}

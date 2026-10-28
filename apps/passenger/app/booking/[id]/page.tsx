"use client";

import type { Booking, BookingPassenger } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, RequireRole, Spinner, cn, useYatrax } from "@yatrax/ui";
import QRCode from "qrcode";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { BoardingPanel, CancelPanel, RefundList, TDRPanel } from "@/components/booking-actions";
import { FoodCard } from "@/components/food-card";
import { HelpCard } from "@/components/help-card";
import { StayCard } from "@/components/stay-card";
import { clockIST, dayLabel, duration, rupees } from "@/components/format";
import { LiveCard } from "@/components/live-card";
import { SiteHeader } from "@/components/site-header";

function place(p: BookingPassenger) {
  if (p.status === "CNF") return `${p.coach} / ${p.berth} ${p.berth_type ?? ""}`;
  if (p.status === "RAC") return `RAC ${p.position ?? p.rac_no} · ${p.coach} / ${p.berth} (shared side-lower)`;
  if (p.status === "CAN") return p.refund ? `Cancelled · refund ${rupees(p.refund)}` : "Cancelled";
  if (p.status === "NOSHOW") return "Did not board (berth released by the TTE)";
  return `Waitlist ${p.position ?? p.wl_no}`;
}

const statusTone = { CNF: "ok", RAC: "saffron", WL: "danger", CAN: "neutral", NOSHOW: "neutral" } as const;

function Countdown({ until, onExpire }: { until: string; onExpire: () => void }) {
  const [left, setLeft] = useState(() => new Date(until).getTime() - Date.now());
  const fired = useRef(false);
  useEffect(() => {
    const t = setInterval(() => {
      const ms = new Date(until).getTime() - Date.now();
      setLeft(ms);
      if (ms <= 0 && !fired.current) {
        fired.current = true;
        onExpire();
      }
    }, 500);
    return () => clearInterval(t);
  }, [until, onExpire]);
  const s = Math.max(0, Math.floor(left / 1000));
  return (
    <span className={cn("font-mono text-2xl font-semibold tabular-nums", s < 120 && "text-danger")}>
      {String(Math.floor(s / 60)).padStart(2, "0")}:{String(s % 60).padStart(2, "0")}
    </span>
  );
}

function Journey({ b }: { b: Booking }) {
  return (
    <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3">
      <div>
        <div className="text-2xl font-semibold tabular-nums">{clockIST(b.departure)}</div>
        <div className="text-sm">
          <span className="font-mono font-semibold">{b.from}</span> {b.from_name}
        </div>
        <div className="text-xs text-muted">{dayLabel(b.departure)}</div>
      </div>
      <div className="text-center text-xs text-muted">
        {duration(Math.round((new Date(b.arrival).getTime() - new Date(b.departure).getTime()) / 60000))}
        <div className="my-1 h-px w-20 bg-line" />
        {b.distance_km} km
      </div>
      <div className="text-right">
        <div className="text-2xl font-semibold tabular-nums">{clockIST(b.arrival)}</div>
        <div className="text-sm">
          {b.to_name} <span className="font-mono font-semibold">{b.to}</span>
        </div>
        <div className="text-xs text-muted">{dayLabel(b.arrival)}</div>
      </div>
    </div>
  );
}

function PassengerTable({ b }: { b: Booking }) {
  return (
    <table className="w-full text-sm">
      <thead className="text-left text-xs text-muted">
        <tr>
          <th className="py-1.5">#</th>
          <th className="py-1.5">Passenger</th>
          <th className="py-1.5">Booked as</th>
          <th className="py-1.5">Now</th>
          <th className="py-1.5">Coach / berth</th>
          <th className="py-1.5 text-right">Fare</th>
        </tr>
      </thead>
      <tbody>
        {b.passengers.map((p) => (
          <tr key={p.seq} className="border-t border-line">
            <td className="py-1.5 text-muted">{p.seq}</td>
            <td className="py-1.5">
              {p.name} <span className="text-muted">· {p.age}{p.gender}</span>
            </td>
            <td className="py-1.5 text-xs text-muted">{p.booked_status ?? "—"}</td>
            <td className="py-1.5">
              <Badge tone={statusTone[p.status]}>{p.status}</Badge>
            </td>
            <td className="py-1.5 font-medium">
              {place(p)}
              {p.upgraded_from && <div className="text-xs font-normal text-ok">Upgraded free from {p.upgraded_from}</div>}
              {p.id_number && <div className="text-xs font-normal text-muted">ID {p.id_number}</div>}
            </td>
            <td className="py-1.5 text-right tabular-nums">{rupees(p.fare)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Ticket({ b }: { b: Booking }) {
  const { client } = useYatrax();
  const [svg, setSvg] = useState<string | null>(null);
  useEffect(() => {
    client
      .ticket(b.id)
      .then((t) => QRCode.toString(t.qr, { type: "svg", errorCorrectionLevel: "M", margin: 1 }))
      .then(setSvg)
      .catch(() => setSvg(null));
  }, [client, b.id]);
  return (
    <div className="grid gap-5 sm:grid-cols-[1fr_11rem]">
      <div>
        <div className="text-xs font-medium uppercase tracking-wide text-muted">PNR</div>
        <div className="font-mono text-3xl font-semibold tracking-wider">{b.pnr}</div>
        <div className="mt-1 text-sm text-muted">
          {b.class_name} · {b.quota} quota · booked {b.confirmed_at ? new Date(b.confirmed_at).toLocaleString("en-IN") : ""}
        </div>
        <div className="mt-3 flex flex-wrap gap-1.5 text-xs">
          {b.carbon && b.carbon.saved_kg > 0 && (
            <Badge tone="ok" title={`About ${b.carbon.train_kg} kg CO₂ by train against ${b.carbon.flight_kg} kg flying`}>
              🌱 {b.carbon.saved_kg} kg CO₂ saved vs flying
            </Badge>
          )}
          {b.insurance_premium > 0 && <Badge tone="brand">Insured · ₹10 lakh cover</Badge>}
          {b.points_earned > 0 && <Badge tone="saffron">+{b.points_earned} Rewards points</Badge>}
          {b.auto_upgrade && <Badge tone="neutral">Free upgrade if berths are left at chart time</Badge>}
        </div>
        <Link href={`/booking/${b.id}/invoice`} className="mt-2 inline-block text-sm text-[var(--accent)] hover:underline">
          GST invoice →
        </Link>
      </div>
      <div className="flex flex-col items-center">
        {svg ? (
          // The SVG comes from the qrcode library encoding our own signed token.
          <div className="w-40 rounded-lg border border-line bg-white p-1" dangerouslySetInnerHTML={{ __html: svg }} />
        ) : (
          <div className="flex h-40 w-40 items-center justify-center rounded-lg border border-dashed border-line text-xs text-muted">QR code</div>
        )}
        <span className="mt-1 text-center text-[11px] text-muted">Signed; the TTE can verify it offline</span>
      </div>
    </div>
  );
}

function BookingView() {
  const { client } = useYatrax();
  const router = useRouter();
  const { id } = useParams<{ id: string }>();
  const [b, setB] = useState<Booking | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const verified = useRef(false);

  const load = useCallback(() => client.booking(id).then(setB).catch((e: Error) => setError(e.message)), [client, id]);

  // Returning from the gateway: verify the redirect once, then clean the URL.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    const order = q.get("order_id");
    if (!order || verified.current) {
      load();
      return;
    }
    verified.current = true;
    const paymentId = q.get("payment_id") ?? "";
    client
      .verifyPayment(id, { order_id: order, payment_id: paymentId, signature: q.get("signature") ?? "" })
      .then((nb) => {
        setB(nb);
        if (q.get("status") === "failed") setNotice("Your bank declined the payment. Your berths are still held; you can try again.");
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => router.replace(`/booking/${id}`));
  }, [client, id, load, router]);

  // While payment is pending, the webhook may confirm at any moment; while anyone is
  // on RAC or the waitlist, cancellations elsewhere can move them up.
  const queued = b?.passengers.some((p) => p.status === "RAC" || p.status === "WL") && b.status === "CONFIRMED";
  useEffect(() => {
    if (b?.status !== "PAYMENT_PENDING" && !queued) return;
    const t = setInterval(load, b?.status === "PAYMENT_PENDING" ? 3000 : 8000);
    return () => clearInterval(t);
  }, [b?.status, queued, load]);

  const [walletBalance, setWalletBalance] = useState<number | null>(null);
  useEffect(() => {
    if (b?.status === "HELD" || b?.status === "PAYMENT_PENDING") client.wallet().then((w) => setWalletBalance(w.balance)).catch(() => undefined);
  }, [client, b?.status]);

  async function payWallet() {
    setBusy(true);
    setError(null);
    try {
      setB(await client.payWithWallet(id));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not pay from the wallet");
    } finally {
      setBusy(false);
    }
  }

  const done = (msg: string) => {
    setNotice(msg);
    load();
  };

  async function pay() {
    setBusy(true);
    setError(null);
    try {
      const { checkout_url } = await client.pay(id);
      window.location.href = checkout_url;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not start payment");
      setBusy(false);
      load();
    }
  }

  async function release() {
    setBusy(true);
    await client.release(id).catch(() => undefined);
    await load();
    setBusy(false);
  }

  if (error && !b) return <Alert>{error}</Alert>;
  if (!b) return <Spinner label="Loading booking" />;

  const held = b.status === "HELD" || b.status === "PAYMENT_PENDING";
  return (
    <div className="space-y-5">
      <Card>
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <span className="font-mono text-lg font-semibold">{b.train}</span>
          <h1 className="text-lg font-semibold">{b.train_name}</h1>
          <Badge
            tone={b.status === "CONFIRMED" ? "ok" : held ? "saffron" : "danger"}
            className="ml-auto"
          >
            {{ HELD: "Held, awaiting payment", PAYMENT_PENDING: "Payment in progress", CONFIRMED: "Confirmed", EXPIRED: "Hold expired", FAILED: "Failed", CANCELLED: "Cancelled" }[b.status]}
          </Badge>
        </div>
        <Journey b={b} />
      </Card>

      {notice && <Alert tone="info">{notice}</Alert>}
      {error && <Alert>{error}</Alert>}
      {b.guarantee_with && (
        <Alert tone="info">
          Confirmation guarantee: this ticket is paired with{" "}
          <Link href={`/booking/${b.guarantee_with}`} className="font-medium underline">
            your backup booking
          </Link>
          . Pay for both; the first to confirm is yours and the other is refunded in full.
        </Alert>
      )}

      {b.status === "CONFIRMED" && <LiveCard b={b} />}

      {b.status === "CONFIRMED" && (
        <Card>
          <Ticket b={b} />
        </Card>
      )}

      {b.status === "CONFIRMED" && <FoodCard b={b} />}

      {b.status === "CONFIRMED" && <HelpCard b={b} />}

      {b.status === "CONFIRMED" && <StayCard b={b} />}

      {held && b.hold_expires_at && (
        <Card className="border-saffron-400/60">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div>
              <div className="text-sm font-semibold">Your berths are held</div>
              <div className="text-sm text-muted">Pay before the timer runs out, or they go back on sale.</div>
            </div>
            <Countdown until={b.hold_expires_at} onExpire={load} />
          </div>
          {b.payment?.status === "FAILED" && !notice && (
            <div className="mt-3">
              <Alert>The last payment attempt was declined. You can try again.</Alert>
            </div>
          )}
          {b.status === "PAYMENT_PENDING" && (
            <p className="mt-3 text-sm text-muted">Waiting for the payment gateway to confirm. This page updates by itself.</p>
          )}
          <div className="mt-4 flex flex-wrap gap-2">
            <Button size="lg" onClick={pay} disabled={busy}>
              Pay {rupees(b.total_fare)}
            </Button>
            {walletBalance !== null && walletBalance >= b.total_fare * 100 && (
              <Button size="lg" variant="secondary" onClick={payWallet} disabled={busy}>
                Pay from wallet ({rupees(Math.floor(walletBalance / 100))})
              </Button>
            )}
            <Button size="lg" variant="ghost" onClick={release} disabled={busy}>
              Release berths
            </Button>
          </div>
        </Card>
      )}

      {b.status === "EXPIRED" && (
        <Alert>The hold expired before payment, so the berths went back on sale. No money was taken.</Alert>
      )}
      {b.status === "CANCELLED" && <Alert tone="info">This booking is cancelled.</Alert>}
      {b.status === "FAILED" && (
        <Alert>
          Your payment arrived after the hold expired and no berths were left. The payment has been marked for a full refund.
        </Alert>
      )}

      <Card>
        <PassengerTable b={b} />
        {b.insurance_premium > 0 && (
          <div className="mt-3 flex justify-between border-t border-line pt-3 text-sm">
            <span className="text-muted">Travel insurance</span>
            <span className="tabular-nums">{rupees(b.insurance_premium)}</span>
          </div>
        )}
        <div className="mt-3 flex justify-between border-t border-line pt-3 font-semibold">
          <span>Total</span>
          <span className="tabular-nums">{rupees(b.total_fare)}</span>
        </div>
        {queued && (
          <p className="mt-2 text-xs text-muted">RAC and waitlisted passengers move up automatically as others cancel. This page updates by itself.</p>
        )}
      </Card>

      {b.refunds && b.refunds.length > 0 && <RefundList refunds={b.refunds} />}

      {b.status === "CONFIRMED" && (
        <div className="grid gap-5 md:grid-cols-2">
          {!b.chart_prepared && <CancelPanel key={b.passengers.map((p) => p.status).join()} b={b} onDone={done} />}
          {!b.chart_prepared && !b.original_from && <BoardingPanel b={b} onDone={done} />}
          {b.chart_prepared && <TDRPanel b={b} onDone={done} />}
          {b.chart_prepared && (
            <Alert tone="info">The chart is prepared, so the final berths above won't change. Cancelling now refunds nothing; a TDR is the way to claim.</Alert>
          )}
        </div>
      )}
      {b.original_from && <p className="text-xs text-muted">Boarding point changed from {b.original_from} to {b.from}.</p>}

      <div className="flex flex-wrap gap-3 text-sm">
        <Link href="/trips" className="text-[var(--accent)] hover:underline">
          ← My trips
        </Link>
        {b.status === "CONFIRMED" && (
          <button onClick={() => window.print()} className="text-[var(--accent)] hover:underline">
            Print ticket
          </button>
        )}
      </div>
    </div>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <main id="main" tabIndex={-1} className="mx-auto max-w-3xl px-4 py-6">
        <RequireRole>{() => <BookingView />}</RequireRole>
      </main>
    </>
  );
}

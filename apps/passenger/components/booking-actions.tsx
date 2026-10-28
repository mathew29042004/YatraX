"use client";

import type { Booking, CancelQuote, RefundInfo, TrainDetail } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, cn, useAsync, useYatrax } from "@yatrax/ui";
import { useEffect, useState } from "react";
import { rupees } from "./format";

/** Cancel some or all passengers, with a live refund preview. */
export function CancelPanel({ b, onDone }: { b: Booking; onDone: (msg: string) => void }) {
  const { client } = useYatrax();
  const live = b.passengers.filter((p) => p.status !== "CAN");
  const [picked, setPicked] = useState<number[]>(live.map((p) => p.seq));
  const [refundTo, setRefundTo] = useState<"source" | "wallet">("source");
  const [quote, setQuote] = useState<CancelQuote | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (picked.length === 0) return setQuote(null);
    client
      .cancelQuote(b.id, picked)
      .then(setQuote)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not price the cancellation"));
  }, [client, b.id, picked]);

  async function cancel() {
    setBusy(true);
    setError(null);
    try {
      const r = await client.cancel(b.id, picked, refundTo);
      const n = r.promotions?.length ?? 0;
      const moved = n ? ` ${n} waiting passenger${n > 1 ? "s" : ""} moved up.` : "";
      onDone(
        r.quote.refund > 0
          ? `Cancelled. ${rupees(r.quote.refund)} refund ${r.refund_to === "wallet" ? "added to your YatraX Pay wallet" : "on its way to your original payment method"}.${moved}`
          : `Cancelled. No refund is due under the rules.${moved}`,
      );
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not cancel");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardTitle hint="Indian Railways refund rules">Cancel passengers</CardTitle>
      <div className="space-y-1.5">
        {live.map((p) => {
          const r = quote?.passengers.find((x) => x.seq === p.seq);
          return (
            <label key={p.seq} className="flex cursor-pointer items-start gap-2 rounded-lg border border-line p-2 text-sm">
              <input
                type="checkbox"
                className="mt-0.5 h-4 w-4 accent-[var(--accent)]"
                checked={picked.includes(p.seq)}
                onChange={(e) => setPicked(e.target.checked ? [...picked, p.seq].sort() : picked.filter((x) => x !== p.seq))}
              />
              <span className="flex-1">
                <span className="font-medium">{p.name}</span> <span className="text-muted">· {p.status}</span>
                {r && <span className="block text-xs text-muted">{r.rule}</span>}
              </span>
              {r && (
                <span className="text-right tabular-nums">
                  {rupees(r.refund)}
                  <span className="block text-xs text-muted">−{rupees(r.deduction)}</span>
                </span>
              )}
            </label>
          );
        })}
      </div>
      {quote && (
        <div className="mt-3 flex items-center justify-between border-t border-line pt-3 text-sm">
          <span>
            Refund <span className="text-muted">(charges {rupees(quote.deduction)})</span>
          </span>
          <span className="text-lg font-semibold tabular-nums">{rupees(quote.refund)}</span>
        </div>
      )}
      {quote && quote.refund > 0 && !quote.paid_by_wallet && (
        <div className="mt-3 grid grid-cols-2 gap-2 text-sm">
          {(["source", "wallet"] as const).map((d) => (
            <button
              key={d}
              type="button"
              onClick={() => setRefundTo(d)}
              className={cn("rounded-lg border p-2 text-left", refundTo === d ? "border-[var(--accent)] ring-2 ring-[var(--accent)]/20" : "border-line")}
            >
              <span className="font-medium">{d === "wallet" ? "YatraX Pay wallet" : "Original payment"}</span>
              <span className="block text-xs text-muted">{d === "wallet" ? "Instant, use on your next booking" : "Back to UPI / card in a few minutes"}</span>
            </button>
          ))}
        </div>
      )}
      {error && (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      )}
      <Button variant="danger" className="mt-3 w-full" disabled={busy || picked.length === 0} onClick={cancel}>
        {picked.length === live.length ? "Cancel booking" : `Cancel ${picked.length} passenger${picked.length === 1 ? "" : "s"}`}
      </Button>
    </Card>
  );
}

/** Move the boarding point to a later station (once, up to 24 h before departure). */
export function BoardingPanel({ b, onDone }: { b: Booking; onDone: (msg: string) => void }) {
  const { client } = useYatrax();
  const { data: train } = useAsync<TrainDetail>(() => client.train(b.train), [client, b.train]);
  const [station, setStation] = useState("");
  const [error, setError] = useState<string | null>(null);
  if (!train) return null;
  const fi = train.stops.findIndex((s) => s.station === b.from);
  const ti = train.stops.findIndex((s) => s.station === b.to);
  const options = train.stops.slice(fi + 1, ti);
  if (options.length === 0) return null;
  return (
    <Card>
      <CardTitle hint="Once, up to 24 hours before departure">Change boarding point</CardTitle>
      <div className="flex gap-2">
        <select value={station} onChange={(e) => setStation(e.target.value)} className="h-10 flex-1 rounded-lg border border-line bg-surface px-2 text-sm" aria-label="New boarding station">
          <option value="">Board later at…</option>
          {options.map((s) => (
            <option key={s.station} value={s.station}>
              {s.station_name} ({s.station}) · {s.departure}
            </option>
          ))}
        </select>
        <Button
          variant="secondary"
          disabled={!station}
          onClick={async () => {
            setError(null);
            try {
              await client.changeBoarding(b.id, station);
              onDone(`You now board at ${station}. The fare is not refunded.`);
            } catch (e) {
              setError(e instanceof ApiError ? e.message : "Could not change the boarding point");
            }
          }}
        >
          Change
        </Button>
      </div>
      {error && (
        <div className="mt-2">
          <Alert>{error}</Alert>
        </div>
      )}
    </Card>
  );
}

/** File a TDR claim once the chart is prepared. */
export function TDRPanel({ b, onDone }: { b: Booking; onDone: (msg: string) => void }) {
  const { client } = useYatrax();
  const { data: reasons } = useAsync<Record<string, string>>(() => client.tdrReasons(), [client]);
  const [reason, setReason] = useState("");
  const [details, setDetails] = useState("");
  const [error, setError] = useState<string | null>(null);
  if (b.tdr) {
    return (
      <Card>
        <CardTitle>TDR claim</CardTitle>
        <div className="flex items-center gap-2 text-sm">
          <Badge tone={b.tdr.status === "APPROVED" ? "ok" : b.tdr.status === "REJECTED" ? "danger" : "saffron"}>{b.tdr.status}</Badge>
          <span>{b.tdr.reason_text}</span>
          {b.tdr.refund ? <span className="ml-auto font-medium">{rupees(b.tdr.refund)}</span> : null}
        </div>
      </Card>
    );
  }
  return (
    <Card>
      <CardTitle hint="Cancellation no longer refunds after the chart">File a TDR</CardTitle>
      <select value={reason} onChange={(e) => setReason(e.target.value)} className="mb-2 h-10 w-full rounded-lg border border-line bg-surface px-2 text-sm" aria-label="TDR reason">
        <option value="">Reason…</option>
        {Object.entries(reasons ?? {}).map(([k, v]) => (
          <option key={k} value={k}>
            {v}
          </option>
        ))}
      </select>
      <textarea
        value={details}
        onChange={(e) => setDetails(e.target.value)}
        maxLength={500}
        placeholder="Details (optional)"
        className="h-20 w-full rounded-lg border border-line bg-surface p-2 text-sm"
        aria-label="TDR details"
      />
      {error && <Alert>{error}</Alert>}
      <Button
        variant="secondary"
        className="mt-2"
        disabled={!reason}
        onClick={async () => {
          setError(null);
          try {
            await client.fileTDR(b.id, reason, details);
            onDone("TDR filed. You'll be notified when it is decided.");
          } catch (e) {
            setError(e instanceof ApiError ? e.message : "Could not file the TDR");
          }
        }}
      >
        File TDR
      </Button>
    </Card>
  );
}

export function RefundList({ refunds }: { refunds: RefundInfo[] }) {
  return (
    <Card>
      <CardTitle>Refunds</CardTitle>
      <ul className="divide-y divide-line text-sm">
        {refunds.map((r) => (
          <li key={r.id} className="flex flex-wrap items-center gap-2 py-2">
            <span className="font-medium tabular-nums">{rupees(r.amount)}</span>
            <span className="text-muted">to {r.destination === "wallet" ? "YatraX Pay wallet" : "original payment"}</span>
            <span className="text-xs text-muted">· {r.reason}</span>
            <Badge className="ml-auto" tone={r.status === "PROCESSED" ? "ok" : r.status === "FAILED" ? "danger" : "saffron"}>
              {r.status === "PROCESSED" ? "Refunded" : r.status === "PENDING" ? "Processing" : "Failed"}
            </Badge>
          </li>
        ))}
      </ul>
    </Card>
  );
}

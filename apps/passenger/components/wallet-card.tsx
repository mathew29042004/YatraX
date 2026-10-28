"use client";

import type { Wallet } from "@yatrax/api-client";
import { Alert, Card, CardTitle, Spinner, useAsync, useYatrax } from "@yatrax/ui";
import Link from "next/link";
import { rupees } from "./format";

const kindLabel: Record<string, string> = { refund: "Refund", wallet_payment: "Paid for a booking", food_order: "Food order", food_refund: "Food refund", loyalty_redeem: "Rewards points redeemed" };

export function WalletCard() {
  const { client } = useYatrax();
  const { data, error } = useAsync<Wallet>(() => client.wallet(), [client]);
  return (
    <Card>
      <CardTitle hint="Refunds land here instantly; spend them on your next booking">YatraX Pay</CardTitle>
      {error && <Alert>{error}</Alert>}
      {!data && !error && <Spinner />}
      {data && (
        <>
          <div className="text-3xl font-semibold tabular-nums">{rupees(Math.floor(data.balance / 100))}</div>
          {data.history.length > 0 ? (
            <ul className="mt-3 divide-y divide-line text-sm">
              {data.history.slice(0, 8).map((h, i) => (
                <li key={i} className="flex items-center justify-between py-1.5">
                  <span>
                    {kindLabel[h.kind] ?? h.kind}
                    <span className="block text-xs text-muted">{new Date(h.at).toLocaleString("en-IN")}</span>
                  </span>
                  <span className={h.amount > 0 ? "font-medium text-ok" : "font-medium"}>
                    {h.amount > 0 ? "+" : "−"}
                    {rupees(Math.abs(h.amount) / 100)}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-sm text-muted">
              No activity yet. Cancel a ticket and choose “YatraX Pay wallet” to get an instant refund here. <Link href="/trips" className="text-[var(--accent)] hover:underline">My trips</Link>
            </p>
          )}
        </>
      )}
    </Card>
  );
}

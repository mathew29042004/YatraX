"use client";

import type { Loyalty } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Button, Card, CardTitle, Spinner, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";
import { rupees } from "./format";

const reasonText = { earned: "Earned on", reversed: "Taken back (cancelled)", redeemed: "Redeemed for wallet credit" } as const;

/** YatraX Rewards: points on every ticket, turned into wallet credit for the next one. */
export function LoyaltyCard() {
  const { client } = useYatrax();
  const [l, setL] = useState<Loyalty | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.loyalty().then(setL).catch((e: Error) => setError(e.message)), [client]);
  useEffect(() => {
    load();
  }, [load]);
  if (error) return <Alert>{error}</Alert>;
  if (!l) return <Spinner label="Loading points" />;
  async function redeem() {
    setError(null);
    try {
      const before = l!.credit_value;
      setL(await client.redeemPoints(l!.redeemable));
      setMsg(`${rupees(before)} added to your YatraX Pay wallet.`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not redeem");
    }
  }
  return (
    <Card>
      <CardTitle hint={l.rules}>YatraX Rewards</CardTitle>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="text-3xl font-semibold tabular-nums">{l.balance.toLocaleString("en-IN")}</div>
          <div className="text-sm text-muted">points</div>
        </div>
        <Button onClick={redeem} disabled={l.redeemable === 0}>
          {l.redeemable ? `Redeem ${l.redeemable} for ${rupees(l.credit_value)}` : "Not enough points yet"}
        </Button>
      </div>
      {msg && <p className="mt-2 text-sm text-ok">{msg}</p>}
      {l.history.length > 0 && (
        <ul className="mt-3 divide-y divide-line text-sm">
          {l.history.slice(0, 8).map((h, i) => (
            <li key={i} className="flex justify-between py-1.5">
              <span>
                {reasonText[h.reason]} {h.pnr && <span className="font-mono text-xs text-muted">PNR {h.pnr}</span>}
              </span>
              <span className={h.delta < 0 ? "tabular-nums text-danger" : "tabular-nums text-ok"}>
                {h.delta > 0 ? "+" : ""}
                {h.delta}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

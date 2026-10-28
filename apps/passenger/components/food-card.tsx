"use client";

import type { Booking, FoodOrder, FoodStatus, FoodStop, MenuItem, VendorBrief } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Badge, Button, Card, CardTitle, Input, cn, hhmm, useYatrax } from "@yatrax/ui";
import { useCallback, useEffect, useMemo, useState } from "react";
import { rupees } from "./format";

const statusLabel: Record<FoodStatus, string> = {
  placed: "Waiting for the vendor",
  accepted: "Accepted",
  preparing: "Being cooked",
  handed_over: "On its way to your berth",
  delivered: "Delivered",
  rejected: "Rejected by the vendor",
  cancelled: "Cancelled",
  missed: "Missed",
};
const statusTone = {
  placed: "saffron", accepted: "brand", preparing: "brand", handed_over: "brand",
  delivered: "ok", rejected: "danger", cancelled: "neutral", missed: "danger",
} as const;
const closedUnpaid = (s: FoodStatus) => s === "rejected" || s === "cancelled" || s === "missed";
const open = (s: FoodStatus) => s === "placed" || s === "accepted" || s === "preparing" || s === "handed_over";

function DietDot({ diet }: { diet: MenuItem["diet"] }) {
  // The FSSAI marks: green square for veg (and Jain), brown for egg, red for non-veg.
  const c = diet === "nonveg" ? "border-red-600 after:bg-red-600" : diet === "egg" ? "border-amber-700 after:bg-amber-700" : "border-green-600 after:bg-green-600";
  return <span title={diet} className={cn("relative inline-block h-3 w-3 shrink-0 border after:absolute after:inset-[2px] after:rounded-full", c)} />;
}

function Stars({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  return (
    <span className="inline-flex gap-0.5">
      {[1, 2, 3, 4, 5].map((n) => (
        <button key={n} type="button" aria-label={`${n} stars`} onClick={() => onChange(n)} className={n <= value ? "text-saffron-500" : "text-line"}>
          ★
        </button>
      ))}
    </span>
  );
}

function OrderRow({ o, onChange }: { o: FoodOrder; onChange: () => void }) {
  const { client } = useYatrax();
  const [stars, setStars] = useState(0);
  const [review, setReview] = useState("");
  const [hygiene, setHygiene] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const act = (p: Promise<unknown>) => p.then(onChange).catch((e: Error) => setError(e.message));
  return (
    <div className="rounded-md border border-line p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono font-semibold">{o.code}</span>
        <span>{o.vendor_name}</span>
        <Badge tone={statusTone[o.status]} className="ml-auto">{statusLabel[o.status]}</Badge>
      </div>
      <div className="mt-1 text-muted">
        {o.items.map((i) => `${i.qty}× ${i.name}`).join(", ")} · {rupees(o.total)} {o.payment === "cod" ? "cash on delivery" : "paid from wallet"}
      </div>
      {open(o.status) && (
        <div className="mt-1">
          At {o.station_name}, to {o.coach}/{o.berth}, around <span className="font-mono font-semibold">{hhmm(o.eta)}</span>
          {o.delay_min > 0 && <span className="text-danger"> (train {o.delay_min} min late)</span>}
          {o.platform > 0 && <span className="text-muted"> · platform {o.platform}</span>}
        </div>
      )}
      {(o.note || (o.payment === "wallet" && closedUnpaid(o.status))) && (
        <div className="mt-1 text-xs text-muted">
          {o.note}
          {o.payment === "wallet" && closedUnpaid(o.status) && `${o.note ? " · " : ""}${rupees(o.total)} refunded to your wallet`}
        </div>
      )}
      {(o.status === "placed" || o.status === "accepted") && (
        <Button size="sm" variant="ghost" className="mt-2" onClick={() => act(client.cancelFoodOrder(o.id))}>
          Cancel order
        </Button>
      )}
      {o.status === "delivered" && !o.rating && (
        <div className="mt-2 space-y-2 border-t border-line pt-2">
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted">Rate it</span>
            <Stars value={stars} onChange={setStars} />
          </div>
          {stars > 0 && (
            <>
              <Input aria-label="Review" placeholder="A few words (optional)" value={review} onChange={(e) => setReview(e.target.value)} />
              <label className="flex items-center gap-2 text-xs">
                <input type="checkbox" checked={hygiene} onChange={(e) => setHygiene(e.target.checked)} />
                Report a hygiene problem to the railway
              </label>
              <Button size="sm" onClick={() => act(client.rateFoodOrder(o.id, stars, review, hygiene))}>
                Send rating
              </Button>
            </>
          )}
        </div>
      )}
      {o.rating && (
        <div className="mt-1 text-xs text-muted">
          You rated it {"★".repeat(o.rating)}
          {o.review ? ` — “${o.review}”` : ""}
        </div>
      )}
      {error && <div className="mt-2 text-xs text-danger">{error}</div>}
    </div>
  );
}

function Menu({ b, stop, vendor, onPlaced }: { b: Booking; stop: FoodStop; vendor: VendorBrief; onPlaced: (o: FoodOrder) => void }) {
  const { client } = useYatrax();
  const [menu, setMenu] = useState<MenuItem[] | null>(null);
  const [cart, setCart] = useState<Record<number, number>>({});
  const [diet, setDiet] = useState<"all" | "veg">("all");
  const [payment, setPayment] = useState<"wallet" | "cod">("wallet");
  const [wallet, setWallet] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setMenu(null);
    setCart({});
    client.vendorMenu(vendor.id).then(setMenu).catch((e: Error) => setError(e.message));
    client.wallet().then((w) => setWallet(Math.floor(w.balance / 100))).catch(() => setWallet(0));
  }, [client, vendor.id]);
  const total = useMemo(() => (menu ?? []).reduce((t, m) => t + (cart[m.id] ?? 0) * m.price, 0), [menu, cart]);
  const shown = (menu ?? []).filter((m) => m.available && (diet === "all" || m.diet === "veg" || m.diet === "jain"));
  const add = (id: number, d: number) => setCart((c) => ({ ...c, [id]: Math.max(0, Math.min(20, (c[id] ?? 0) + d)) }));
  const short = wallet !== null && wallet < total;
  useEffect(() => {
    if (short) setPayment("cod");
  }, [short]);

  async function place() {
    setBusy(true);
    setError(null);
    try {
      const items = Object.entries(cart).filter(([, q]) => q > 0).map(([id, qty]) => ({ item_id: Number(id), qty }));
      onPlaced(await client.placeFoodOrder({ booking_id: b.id, vendor_id: vendor.id, station: stop.station, items, payment }));
      setCart({});
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not place the order");
    } finally {
      setBusy(false);
    }
  }

  if (!menu) return error ? <Alert>{error}</Alert> : <div className="text-sm text-muted">Loading the menu…</div>;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-xs">
        {!vendor.veg_only && (
          <>
            <button className={cn("rounded px-2 py-1", diet === "all" ? "bg-canvas font-semibold" : "text-muted")} onClick={() => setDiet("all")}>All</button>
            <button className={cn("rounded px-2 py-1", diet === "veg" ? "bg-canvas font-semibold" : "text-muted")} onClick={() => setDiet("veg")}>Veg only</button>
          </>
        )}
        <span className="ml-auto text-muted">Prices include GST</span>
      </div>
      <ul className="divide-y divide-line">
        {shown.map((m) => (
          <li key={m.id} className="flex items-center gap-3 py-2">
            <DietDot diet={m.diet} />
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{m.name}</div>
              {m.description && <div className="truncate text-xs text-muted">{m.description}</div>}
            </div>
            <span className="text-sm tabular-nums">{rupees(m.price)}</span>
            {cart[m.id] ? (
              <span className="inline-flex items-center rounded-md border border-line">
                <button className="px-2" aria-label="Remove one" onClick={() => add(m.id, -1)}>−</button>
                <span className="w-5 text-center text-sm tabular-nums">{cart[m.id]}</span>
                <button className="px-2" aria-label="Add one" onClick={() => add(m.id, 1)}>+</button>
              </span>
            ) : (
              <Button size="sm" variant="secondary" onClick={() => add(m.id, 1)}>Add</Button>
            )}
          </li>
        ))}
      </ul>
      {total > 0 && (
        <div className="space-y-2 rounded-md bg-canvas p-3">
          <div className="flex items-center gap-4 text-sm">
            <label className={cn("flex items-center gap-1.5", short && "opacity-50")}>
              <input type="radio" checked={payment === "wallet"} disabled={short} onChange={() => setPayment("wallet")} />
              Wallet {wallet !== null && <span className="text-muted">({rupees(wallet)})</span>}
            </label>
            <label className="flex items-center gap-1.5">
              <input type="radio" checked={payment === "cod"} onChange={() => setPayment("cod")} />
              Cash on delivery
            </label>
          </div>
          <Button className="w-full" onClick={place} disabled={busy}>
            Order for {stop.name} · {rupees(total)}
          </Button>
          <p className="text-xs text-muted">
            Delivered to {b.passengers.find((p) => p.coach)?.coach}/{b.passengers.find((p) => p.coach)?.berth}. You can cancel until the vendor starts cooking.
          </p>
        </div>
      )}
      {error && <Alert>{error}</Alert>}
    </div>
  );
}

/** Food to your berth: pick a station on the journey, a vendor there, and order. */
export function FoodCard({ b }: { b: Booking }) {
  const { client } = useYatrax();
  const [stops, setStops] = useState<FoodStop[] | null>(null);
  const [orders, setOrders] = useState<FoodOrder[]>([]);
  const [pick, setPick] = useState<{ stop: string; vendor: string } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const load = useCallback(() => {
    client.foodOptions(b.id).then(setStops).catch(() => setStops([]));
    client.myFoodOrders(b.id).then(setOrders).catch(() => undefined);
  }, [client, b.id]);
  useEffect(load, [load]);
  // Live: order status and the ETA move as the vendor and the train do.
  const active = orders.some((o) => open(o.status));
  useEffect(() => {
    if (!active) return;
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [active, load]);

  if (!stops || (stops.length === 0 && orders.length === 0)) return null;
  const stop = stops.find((s) => s.station === pick?.stop);
  const vendor = stop?.vendors.find((v) => v.id === pick?.vendor);
  return (
    <Card>
      <CardTitle hint="Delivered to your berth at a halt">Order food</CardTitle>
      {orders.length > 0 && (
        <div className="mb-4 space-y-2">
          {orders.map((o) => <OrderRow key={o.id} o={o} onChange={load} />)}
        </div>
      )}
      {notice && <div className="mb-3"><Alert tone="info">{notice}</Alert></div>}
      <div className="space-y-1.5">
        {stops.map((s) => (
          <div key={s.station} className={cn("rounded-md border border-line p-2.5", !s.open && "opacity-60")}>
            <div className="flex flex-wrap items-baseline gap-2 text-sm">
              <span className="font-mono font-semibold">{s.station}</span>
              <span className="font-medium">{s.name}</span>
              <span className="text-xs text-muted">around {hhmm(s.eta)} · {s.halt_min} min halt</span>
              {!s.open && <span className="ml-auto text-xs text-muted">{s.reason}</span>}
            </div>
            {s.open && (
              <div className="mt-1.5 flex flex-wrap gap-1.5">
                {s.vendors.map((v) => (
                  <button
                    key={v.id}
                    onClick={() => setPick(pick?.vendor === v.id && pick.stop === s.station ? null : { stop: s.station, vendor: v.id })}
                    className={cn(
                      "rounded-md border px-2.5 py-1 text-left text-xs",
                      pick?.vendor === v.id && pick.stop === s.station ? "border-[var(--accent)] bg-canvas" : "border-line hover:bg-canvas",
                    )}
                  >
                    <span className="font-medium">{v.name}</span> <span className="text-saffron-600">★ {v.rating.toFixed(1)}</span>
                    <div className="text-muted">{v.veg_only ? "Pure veg · " : ""}{v.cuisines.join(", ")}</div>
                  </button>
                ))}
              </div>
            )}
            {stop && vendor && stop.station === s.station && (
              <div className="mt-3 border-t border-line pt-3">
                <Menu
                  b={b}
                  stop={stop}
                  vendor={vendor}
                  onPlaced={(o) => {
                    setNotice(`Order ${o.code} placed. ${o.vendor_name} will deliver it at ${o.station_name} around ${hhmm(o.eta)}.`);
                    setPick(null);
                    load();
                  }}
                />
              </div>
            )}
          </div>
        ))}
      </div>
    </Card>
  );
}

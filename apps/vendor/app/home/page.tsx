"use client";

import type { FoodOrder, FoodStatus, MenuEdit, MenuItem, VendorPayouts, VendorProfile } from "@yatrax/api-client";
import {
  Alert, AppShell, Badge, Button, Card, CardTitle, Input, ProfileCard, RequireRole, SimClockBadge, Spinner, Stat, StationBoard, cn, hhmm,
  useAsync, useSimClock, useYatrax,
} from "@yatrax/ui";
import { useCallback, useEffect, useState } from "react";

const rupees = (n: number) => `${n < 0 ? "−" : ""}₹${Math.abs(n).toLocaleString("en-IN")}`;
const next: Partial<Record<FoodStatus, { to: FoodStatus; label: string }>> = {
  placed: { to: "accepted", label: "Accept" },
  accepted: { to: "preparing", label: "Start cooking" },
  preparing: { to: "handed_over", label: "Hand to delivery" },
  handed_over: { to: "delivered", label: "Delivered at the berth" },
};
const statusTone = {
  placed: "saffron", accepted: "brand", preparing: "brand", handed_over: "brand",
  delivered: "ok", rejected: "danger", cancelled: "neutral", missed: "danger",
} as const;
const statusLabel: Record<FoodStatus, string> = {
  placed: "New", accepted: "Accepted", preparing: "Cooking", handed_over: "Out for delivery",
  delivered: "Delivered", rejected: "Rejected", cancelled: "Cancelled by passenger", missed: "Missed the train",
};

function Outlet({ data }: { data: VendorProfile }) {
  return (
    <Card>
      <CardTitle hint={`FSSAI ${data.fssai_license}`}>Your outlet</CardTitle>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xl font-semibold">{data.name}</span>
        <Badge tone={data.veg_only ? "ok" : "danger"}>{data.veg_only ? "Pure veg" : "Veg & non-veg"}</Badge>
        <Badge tone="saffron">★ {data.rating.toFixed(1)}</Badge>
      </div>
      <div className="mt-1 text-sm text-muted">
        {data.station_name} ({data.station}) · {data.cuisines.join(", ")}
      </div>
    </Card>
  );
}

/** Minutes from railway time `now` to `iso`; negative when past. */
const minsTo = (iso: string, now: Date) => Math.round((new Date(iso).getTime() - now.getTime()) / 60000);
const span = (m: number) => (m < 90 ? `${m} min` : m < 48 * 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${Math.round(m / 1440)} days`);

function OrderCard({ o, now, onChange }: { o: FoodOrder; now: Date; onChange: () => void }) {
  const { client } = useYatrax();
  const [busy, setBusy] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const step = next[o.status];
  const startIn = minsTo(o.start_by, now);
  const etaIn = minsTo(o.eta, now);
  const late = (o.status === "placed" || o.status === "accepted") && startIn < 0;

  async function advance(to: FoodStatus, note = "") {
    setBusy(true);
    setError(null);
    try {
      await client.advanceFoodOrder(o.id, to, note);
      onChange();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not update the order");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={cn("rounded-lg border p-3", late ? "border-danger" : "border-line")}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-base font-semibold">{o.code}</span>
        <Badge tone={statusTone[o.status]}>{statusLabel[o.status]}</Badge>
        <span className="ml-auto font-mono text-sm">
          {o.train} <span className="font-sans text-muted">{o.train_name}</span>
        </span>
      </div>
      <div className="mt-2 grid gap-3 sm:grid-cols-[1fr_auto]">
        <div className="text-sm">
          <ul>
            {o.items.map((i) => (
              <li key={i.item_id}>
                <span className="font-semibold tabular-nums">{i.qty}×</span> {i.name}
              </li>
            ))}
          </ul>
          <div className="mt-1 text-xs text-muted">
            Coach <span className="font-semibold text-ink">{o.coach}</span> berth <span className="font-semibold text-ink">{o.berth}</span> · {o.passenger} ·{" "}
            {o.payment === "cod" ? <span className="font-semibold text-ink">collect {rupees(o.total)} cash</span> : `prepaid ${rupees(o.total)}`}
          </div>
        </div>
        <div className="grid grid-cols-2 gap-3 text-right sm:text-left">
          <div>
            <div className="text-[11px] uppercase tracking-wide text-muted">Train arrives</div>
            <div className="font-mono text-lg font-semibold">{hhmm(o.eta)}</div>
            <div className="text-xs text-muted">
              {etaIn >= 0 ? `in ${span(etaIn)}` : "at the platform"}
              {o.platform > 0 && ` · PF ${o.platform}`}
              {o.delay_min > 0 && <span className="text-danger"> · {o.delay_min} late</span>}
            </div>
          </div>
          {(o.status === "placed" || o.status === "accepted") && (
            <div>
              <div className="text-[11px] uppercase tracking-wide text-muted">Start cooking</div>
              <div className={cn("font-mono text-lg font-semibold", late && "text-danger")}>{hhmm(o.start_by)}</div>
              <div className={cn("text-xs", late ? "text-danger" : "text-muted")}>{startIn >= 0 ? `in ${span(startIn)}` : `${span(-startIn)} overdue`}</div>
            </div>
          )}
        </div>
      </div>
      {o.note && <div className="mt-1 text-xs text-muted">{o.note}</div>}
      {step && (
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => advance(step.to)} disabled={busy}>
            {step.label}
          </Button>
          {(o.status === "placed" || o.status === "accepted") && !rejecting && (
            <Button size="sm" variant="ghost" onClick={() => setRejecting(true)}>
              Reject
            </Button>
          )}
          {rejecting && (
            <>
              <Input className="h-8 max-w-56 text-sm" aria-label="Reason for rejecting" placeholder="Reason, e.g. sold out" value={reason} onChange={(e) => setReason(e.target.value)} />
              <Button size="sm" variant="danger" disabled={busy || !reason.trim()} onClick={() => advance("rejected", reason.trim())}>
                Reject order
              </Button>
            </>
          )}
        </div>
      )}
      {error && <div className="mt-2 text-xs text-danger">{error}</div>}
    </div>
  );
}

function Orders() {
  const { client } = useYatrax();
  const { now } = useSimClock(10000);
  const [orders, setOrders] = useState<FoodOrder[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.vendorOrders().then(setOrders).catch((e: Error) => setError(e.message)), [client]);
  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);
  if (error) return <Alert>{error}</Alert>;
  if (!orders) return <Spinner label="Loading orders" />;
  const fresh = orders.filter((o) => o.status === "placed").length;
  return (
    <Card>
      <CardTitle hint={<span className="inline-flex items-center gap-2">{fresh > 0 && <Badge tone="saffron">{fresh} new</Badge>}<SimClockBadge /></span>}>
        Live orders
      </CardTitle>
      {orders.length === 0 ? (
        <p className="text-sm text-muted">No open orders. They appear here as passengers order for trains calling at your station; times follow the train's live running.</p>
      ) : (
        <div className="space-y-3">
          {orders.map((o) => <OrderCard key={o.id} o={o} now={now ?? new Date()} onChange={load} />)}
        </div>
      )}
    </Card>
  );
}

const blank: MenuEdit = { name: "", description: "", category: "meal", diet: "veg", price: 100, prep_min: 15 };

function MenuForm({ initial, onSave, onCancel, vegOnly }: { initial: MenuEdit; onSave: (m: MenuEdit) => Promise<void>; onCancel: () => void; vegOnly: boolean }) {
  const [m, setM] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const set = <K extends keyof MenuEdit>(k: K, v: MenuEdit[K]) => setM((x) => ({ ...x, [k]: v }));
  const sel = "h-9 rounded-lg border border-line bg-surface px-2 text-sm";
  return (
    <form
      className="grid gap-2 rounded-md bg-canvas p-3 sm:grid-cols-6"
      onSubmit={(e) => {
        e.preventDefault();
        onSave(m).catch((err: Error) => setError(err.message));
      }}
    >
      <Input className="sm:col-span-3" aria-label="Dish name" placeholder="Dish name" value={m.name} onChange={(e) => set("name", e.target.value)} required />
      <Input className="sm:col-span-3" aria-label="Description" placeholder="Description" value={m.description} onChange={(e) => set("description", e.target.value)} />
      <select aria-label="Category" className={sel} value={m.category} onChange={(e) => set("category", e.target.value as MenuEdit["category"])}>
        {["meal", "snack", "sweet", "beverage"].map((c) => <option key={c}>{c}</option>)}
      </select>
      <select aria-label="Diet" className={sel} value={m.diet} onChange={(e) => set("diet", e.target.value as MenuEdit["diet"])}>
        {(vegOnly ? ["veg", "jain"] : ["veg", "jain", "egg", "nonveg"]).map((c) => <option key={c}>{c}</option>)}
      </select>
      <label className="flex items-center gap-1 text-xs text-muted">
        ₹<Input type="number" aria-label="Price in rupees" min={1} value={m.price} onChange={(e) => set("price", Number(e.target.value))} />
      </label>
      <label className="flex items-center gap-1 text-xs text-muted">
        <Input type="number" aria-label="Preparation time in minutes" min={1} max={120} value={m.prep_min} onChange={(e) => set("prep_min", Number(e.target.value))} />
        min
      </label>
      <div className="flex gap-2 sm:col-span-2">
        <Button size="sm" type="submit">Save</Button>
        <Button size="sm" type="button" variant="ghost" onClick={onCancel}>Cancel</Button>
      </div>
      {error && <div className="text-xs text-danger sm:col-span-6">{error}</div>}
    </form>
  );
}

function MenuEditor({ vegOnly }: { vegOnly: boolean }) {
  const { client } = useYatrax();
  const [menu, setMenu] = useState<MenuItem[] | null>(null);
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const load = useCallback(() => client.myMenu().then(setMenu).catch(() => setMenu([])), [client]);
  useEffect(() => {
    load();
  }, [load]);
  if (!menu) return <Spinner label="Loading menu" />;
  const save = (id: number | "new") => async (m: MenuEdit) => {
    if (id === "new") await client.addMenuItem(m);
    else await client.editMenuItem(id, m);
    setEditing(null);
    await load();
  };
  return (
    <Card>
      <CardTitle hint={`${menu.filter((m) => m.available).length} of ${menu.length} available`}>Menu</CardTitle>
      <ul className="divide-y divide-line">
        {menu.map((m) =>
          editing === m.id ? (
            <li key={m.id} className="py-2">
              <MenuForm initial={m} vegOnly={vegOnly} onSave={save(m.id)} onCancel={() => setEditing(null)} />
            </li>
          ) : (
            <li key={m.id} className={cn("flex items-center gap-3 py-2 text-sm", !m.available && "opacity-50")}>
              <Badge tone={m.diet === "nonveg" ? "danger" : m.diet === "egg" ? "saffron" : "ok"}>{m.diet}</Badge>
              <div className="min-w-0 flex-1">
                <div className="font-medium">{m.name}</div>
                <div className="text-xs text-muted">{m.category} · {m.prep_min} min to make{m.description ? ` · ${m.description}` : ""}</div>
              </div>
              <span className="tabular-nums">{rupees(m.price)}</span>
              <Button size="sm" variant="ghost" onClick={() => client.editMenuItem(m.id, { ...m, available: !m.available }).then(load)}>
                {m.available ? "Sold out" : "Back on"}
              </Button>
              <Button size="sm" variant="secondary" onClick={() => setEditing(m.id)}>Edit</Button>
            </li>
          ),
        )}
      </ul>
      <div className="mt-3">
        {editing === "new" ? (
          <MenuForm initial={blank} vegOnly={vegOnly} onSave={save("new")} onCancel={() => setEditing(null)} />
        ) : (
          <Button size="sm" variant="secondary" onClick={() => setEditing("new")}>Add a dish</Button>
        )}
      </div>
    </Card>
  );
}

function Payouts() {
  const { client } = useYatrax();
  const [p, setP] = useState<VendorPayouts | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => client.vendorPayouts().then(setP).catch((e: Error) => setError(e.message)), [client]);
  useEffect(() => {
    load();
  }, [load]);
  if (error) return <Alert>{error}</Alert>;
  if (!p) return <Spinner label="Loading payouts" />;
  async function payout() {
    setError(null);
    try {
      const r = await client.requestPayout();
      setNotice(`${rupees(r.amount)} sent to your bank account (${r.reference}).`);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Payout failed");
    }
  }
  return (
    <Card>
      <CardTitle hint={`YatraX keeps 12%`}>Payouts</CardTitle>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Balance due" value={rupees(p.balance)} sub={p.balance < 0 ? "you owe commission on cash orders" : undefined} />
        <Stat label="Delivered" value={p.delivered.toLocaleString("en-IN")} />
        <Stat label="Sales" value={rupees(p.sales)} />
        <Stat label="Commission" value={rupees(p.commission)} />
      </div>
      <div className="mt-3 flex items-center gap-3">
        <Button size="sm" onClick={payout} disabled={p.balance <= 0}>Pay out {rupees(Math.max(0, p.balance))}</Button>
        {notice && <span className="text-sm text-ok">{notice}</span>}
      </div>
      {p.recent.length > 0 && (
        <table className="mt-4 w-full text-sm">
          <thead className="text-left text-xs text-muted">
            <tr>
              <th className="py-1">When</th>
              <th className="py-1">Entry</th>
              <th className="py-1 text-right">Amount</th>
            </tr>
          </thead>
          <tbody>
            {p.recent.map((r, i) => (
              <tr key={i} className="border-t border-line">
                <td className="py-1 text-xs text-muted">{new Date(r.at).toLocaleString("en-IN", { dateStyle: "short", timeStyle: "short" })}</td>
                <td className="py-1">{r.memo}</td>
                <td className={cn("py-1 text-right tabular-nums", r.amount < 0 && "text-danger")}>{rupees(r.amount)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  );
}

function History() {
  const { client } = useYatrax();
  const { data } = useAsync<FoodOrder[]>(() => client.vendorOrders(true), [client]);
  if (!data) return null;
  const rated = data.filter((o) => o.rating);
  return (
    <Card>
      <CardTitle hint={rated.length ? `${rated.length} rated · avg ★ ${(rated.reduce((t, o) => t + (o.rating ?? 0), 0) / rated.length).toFixed(1)}` : undefined}>
        Past orders and reviews
      </CardTitle>
      {data.length === 0 ? (
        <p className="text-sm text-muted">Nothing yet.</p>
      ) : (
        <ul className="divide-y divide-line text-sm">
          {data.slice(0, 30).map((o) => (
            <li key={o.id} className="flex flex-wrap items-center gap-2 py-1.5">
              <span className="font-mono">{o.code}</span>
              <Badge tone={statusTone[o.status]}>{statusLabel[o.status]}</Badge>
              <span className="text-muted">{o.train} · {rupees(o.total)}</span>
              {o.rating && <span className="text-saffron-600">{"★".repeat(o.rating)}</span>}
              {o.review && <span className="text-xs italic text-muted">“{o.review}”</span>}
              {!o.rating && o.note && <span className="text-xs text-muted">{o.note}</span>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

const tabs = ["Orders", "Menu", "Payouts", "Trains"] as const;

function Vendor() {
  const { client } = useYatrax();
  const { data, error } = useAsync<VendorProfile>(() => client.vendorMe(), [client]);
  const [tab, setTab] = useState<(typeof tabs)[number]>("Orders");
  if (error) return <Alert>{error}</Alert>;
  if (!data) return <Spinner label="Loading outlet" />;
  return (
    <>
      <Outlet data={data} />
      <div className="flex gap-1 border-b border-line">
        {tabs.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={cn("-mb-px border-b-2 px-3 py-2 text-sm", tab === t ? "border-[var(--accent)] font-semibold" : "border-transparent text-muted hover:text-ink")}
          >
            {t}
          </button>
        ))}
      </div>
      {tab === "Orders" && <Orders />}
      {tab === "Menu" && <MenuEditor vegOnly={data.veg_only} />}
      {tab === "Payouts" && (
        <>
          <Payouts />
          <History />
        </>
      )}
      {tab === "Trains" && <StationBoard code={data.station} />}
    </>
  );
}

export default function VendorHome() {
  return (
    <RequireRole>
      {(session) => (
        <AppShell session={session}>
          <ProfileCard session={session} />
          {session.user.role === "vendor" ? <Vendor /> : <Alert tone="info">Admins have no outlet. Sign in as a vendor to see one.</Alert>}
        </AppShell>
      )}
    </RequireRole>
  );
}

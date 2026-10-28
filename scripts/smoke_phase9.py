#!/usr/bin/env python3
"""Phase 9 smoke test: catering. Delivery options along a journey, menus, ordering (wallet
or cash on delivery) with cut-offs, the plan's demo (a biryani for Salem; the train runs
40 minutes late; the vendor's timing and the passenger's ETA move with it), the order flow
to the berth, cancellation, rejection, a missed train, ratings, payouts and the ledger.
Run with the stack up:  python scripts/smoke_phase9.py"""
import datetime as dt
import json
import os
import random
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

API = os.environ.get("API_URL", "http://localhost:8080")
GW = os.environ.get("GATEWAY_URL", "http://localhost:8081")
FAILED = []
sys.stdout.reconfigure(encoding="utf-8")  # Windows consoles default to cp1252
IST = dt.timezone(dt.timedelta(hours=5, minutes=30))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def call(method, url, body=None, token=None, form=None, headers=None):
    data, hdrs = None, dict(headers or {})
    if body is not None:
        data, hdrs["Content-Type"] = json.dumps(body).encode(), "application/json"
    if form is not None:
        data, hdrs["Content-Type"] = urllib.parse.urlencode(form).encode(), "application/x-www-form-urlencoded"
    if token:
        hdrs["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.build_opener(NoRedirect).open(req) as r:
            raw = r.read()
            ok = r.headers.get_content_type() == "application/json" and raw
            return r.status, (json.loads(raw) if ok else raw), r.headers
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw), e.headers
        except Exception:
            return e.code, raw, e.headers


def ok(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        FAILED.append(msg)


def passenger():
    phone = random.choice("6789") + "".join(random.choices("0123456789", k=9))
    _, r, _ = call("POST", API + "/v1/auth/otp/request", {"phone": phone})
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Hungry Traveller"})
    return s["access_token"]


def staff(emp):
    _, s, _ = call("POST", API + "/v1/auth/login", {"identifier": emp, "password": "yatrax123"})
    return s["access_token"]


def pay(tok, booking_id):
    _, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=tok)
    order = r["checkout_url"].rsplit("/", 1)[1]
    _, _, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": "success"})
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    _, b, _ = call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                   {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, tok)
    return b


def book(tok, d, cls, to="ERN"):
    s, b, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": to, "date": str(d), "class": cls, "quota": "GN",
                                                    "passengers": [{"name": "Hungry Traveller", "age": 33, "gender": "M", "berth_pref": ""}],
                                                    "together": True, "only_confirmed": False}, tok, headers={"Idempotency-Key": str(uuid.uuid4())})
    return pay(tok, b["id"]) if s == 201 else None


def wait_note(tok, pred, timeout=60):
    end = time.time() + timeout
    while time.time() < end:
        _, n, _ = call("GET", API + "/v1/me/notifications", token=tok)
        hit = next((x for x in n["items"] if pred(x)), None)
        if hit:
            return hit
        time.sleep(2)
    return None


def wallet(tok):
    _, w, _ = call("GET", API + "/v1/me/wallet", token=tok)
    return w["balance"] // 100


def intact(d):
    """Earlier smoke tests disrupt or chart some 12623 runs; skip those (a charted run refunds nothing on cancel)."""
    _, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={d}")
    _, run, _ = call("GET", f"{API}/v1/tte/runs/12623/{d}", token=TTE)
    return isinstance(st, dict) and not st.get("disruption") and isinstance(run, dict) and run.get("chart") is None


TTE = staff("TTE00001")
CTL, VND = staff("CTRL00001"), staff("VND00001")  # VND00001: Salem Biryani House at Salem Jn
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)
today = dt.datetime.now(IST).date()
D, CLS = None, None
for days in random.sample(range(5, 40), 35):
    d = today + dt.timedelta(days=days)
    if not intact(d):
        continue
    _, r, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={d}")
    mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
    cls = next((c["code"] for c in (mail or {}).get("classes", []) if c["availability"]["code"] == "AVL"), None)
    if cls:
        D, CLS = d, cls
        break

# ── 1. A passenger with a wallet balance, and where they can eat ──
p = passenger()
spare = book(p, D, "3A") or book(p, D, CLS)  # a pricier ticket, so the refund covers the food orders below
call("POST", f"{API}/v1/bookings/{spare['id']}/cancel", {"refund_to": "wallet"}, p)  # puts money in the wallet
b = book(p, D, CLS)
ok(b and b["status"] == "CONFIRMED" and b["passengers"][0].get("coach") and wallet(p) > 300,
   f"passenger on 12623 {D} in {b['passengers'][0]['coach']}/{b['passengers'][0]['berth']}, ₹{wallet(p)} in the wallet")
s, opts, _ = call("GET", f"{API}/v1/bookings/{b['id']}/food", token=p)
sa = next((o for o in opts if o["station"] == "SA"), None) if s == 200 else None
ok(sa and sa["open"] and any(v["name"] == "Salem Biryani House" for v in sa["vendors"]),
   f"food along the journey: {[o['station'] for o in opts]}; Salem open, halt {sa and sa['halt_min']} min")
vendor = next(v for v in sa["vendors"] if v["name"] == "Salem Biryani House")
s, menu, _ = call("GET", f"{API}/v1/vendors/{vendor['id']}/menu", token=p)
biryani = next((m for m in menu if m["name"] == "Chicken Dum Biryani"), None)
veg = next((m for m in menu if m["diet"] == "veg" and m["category"] == "meal"), None)
water = next((m for m in menu if "Water" in m["name"]), None)
ok(biryani and veg and water, f"menu: {len(menu)} dishes, e.g. {biryani and biryani['name']} ₹{biryani and biryani['price']}")

# ── 2. Orders ──
def order(tok, items, payment, station="SA", vid=None):
    return call("POST", API + "/v1/food-orders", {"booking_id": b["id"], "vendor_id": vid or vendor["id"], "station": station,
                                                  "items": items, "payment": payment}, tok)


before = wallet(p)
s, o1, _ = order(p, [{"item_id": biryani["id"], "qty": 1}, {"item_id": water["id"], "qty": 2}], "wallet")
ok(s == 201 and o1["status"] == "placed" and o1["total"] == biryani["price"] + 2 * water["price"] and wallet(p) == before - o1["total"],
   f"order {o1.get('code')}: biryani + 2 water = ₹{o1.get('total')}, paid from the wallet, to {o1.get('coach')}/{o1.get('berth')} at Salem")
s, o2, _ = order(p, [{"item_id": veg["id"], "qty": 1}], "cod")
ok(s == 201 and o2["payment"] == "cod", f"second order {o2.get('code')}, cash on delivery")
s, err, _ = order(p, [{"item_id": biryani["id"], "qty": 1}], "cod", station="KPD")
ok(s == 409, f"wrong station for the vendor: {err.get('message', '')[:60]}")
s, err, _ = order(p, [{"item_id": 999999, "qty": 1}], "cod")
ok(s == 409, "an item not on the menu is refused")

s, vo, _ = call("GET", API + "/v1/vendor/orders", token=VND)
mine = {o["id"]: o for o in vo}
v1 = mine.get(o1["id"])
ok(s == 200 and v1 and v1["start_by"] < v1["eta"], f"the vendor sees it: ETA {v1 and v1['eta'][11:16]}Z, start cooking by {v1 and v1['start_by'][11:16]}Z")

# ── 3. The demo: the train runs 40 minutes late ──
s, _, _ = call("POST", API + "/v1/admin/live/incidents", {"train": "12623", "origin_date": str(D), "station": "MAS", "delay_min": 40,
                                                         "reason": "Late arrival of the rake"}, CTL)
_, vo, _ = call("GET", API + "/v1/vendor/orders", token=VND)
v1b = next(o for o in vo if o["id"] == o1["id"])
shift = (dt.datetime.fromisoformat(v1b["eta"].replace("Z", "+00:00")) - dt.datetime.fromisoformat(v1["eta"].replace("Z", "+00:00"))).total_seconds() / 60
ok(s == 201 and shift >= 25, f"the vendor's ETA and start time moved by {shift:.0f} min on their own (start by {v1b['start_by'][11:16]}Z)")
n = wait_note(p, lambda x: x["kind"] == "food" and "now arrives" in x["title"], 70)
ok(n is not None, f"the passenger is told: {n and n['title']} — {n and n['body'][:80]}")

# ── 4. To the berth, and the other endings ──
for step in ["accepted", "preparing", "handed_over", "delivered"]:
    s, x, _ = call("POST", f"{API}/v1/vendor/orders/{o1['id']}/status", {"status": step}, VND)
ok(s == 200 and x["status"] == "delivered", "order 1: accepted → preparing → handed over → delivered")
s, _, _ = call("POST", f"{API}/v1/vendor/orders/{o1['id']}/status", {"status": "preparing"}, VND)
ok(s == 409, "a delivered order can't go backwards")
for step in ["accepted", "preparing", "handed_over", "delivered"]:
    call("POST", f"{API}/v1/vendor/orders/{o2['id']}/status", {"status": step}, VND)
s, o3, _ = order(p, [{"item_id": water["id"], "qty": 1}], "wallet")
mid = wallet(p)
s, c3, _ = call("POST", f"{API}/v1/food-orders/{o3['id']}/cancel", token=p)
ok(s == 200 and c3["status"] == "cancelled" and wallet(p) == mid + o3["total"], "order 3 cancelled before cooking: refunded to the wallet")
s, o4, _ = order(p, [{"item_id": veg["id"], "qty": 2}], "cod")
s, r4, _ = call("POST", f"{API}/v1/vendor/orders/{o4['id']}/status", {"status": "rejected", "note": "Out of veg biryani"}, VND)
ok(s == 200 and r4["status"] == "rejected" and r4["note"] == "Out of veg biryani", "order 4 rejected by the vendor, with the reason")
s, err, _ = call("POST", f"{API}/v1/food-orders/{o1['id']}/cancel", token=p)
ok(s == 409, "a delivered order can't be cancelled")

# A missed train: the train leaves Salem before the food arrives → refunded automatically.
s, o5, _ = order(p, [{"item_id": water["id"], "qty": 3}], "wallet")
pre = wallet(p)
_, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
sa_stop = next(v for v in st["stops"] if v["station"] == "SA")
call("POST", API + "/v1/admin/sim/clock", {"jump_to": sa_stop["exp_dep"], "speed": 60}, CTL)
s, err, _ = order(p, [{"item_id": water["id"], "qty": 1}], "cod")
ok(s == 409 and ("reached" in err.get("message", "") or "close" in err.get("message", "")), f"too late to order for Salem: {err.get('message', '')[:70]}")
missed = None
end = time.time() + 70
while time.time() < end and not missed:
    _, mo, _ = call("GET", f"{API}/v1/food-orders?booking_id={b['id']}", token=p)
    missed = next((o for o in mo if o["id"] == o5["id"] and o["status"] == "missed"), None)
    time.sleep(3)
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)
ok(missed is not None and wallet(p) == pre + o5["total"], f"order 5 missed (the train left Salem): {missed and missed["note"][:60]} — refunded")

# ── 5. Ratings and payouts ──
s, rated, _ = call("POST", f"{API}/v1/food-orders/{o1['id']}/rate", {"stars": 5, "review": "Hot and on time despite the delay"}, p)
ok(s == 200 and rated["rating"] == 5, "rated 5 stars")
s, _, _ = call("POST", f"{API}/v1/food-orders/{o4['id']}/rate", {"stars": 1}, p)
ok(s == 409, "can't rate an order that never came")
s, pay_, _ = call("GET", API + "/v1/vendor/payouts", token=VND)
expect = (o1["total"] - o1["commission"]) - o2["commission"]
ok(s == 200 and pay_["delivered"] >= 2, f"payout ledger: balance ₹{pay_['balance']} (this run adds ₹{expect}: prepaid sale less commission, minus cash-order commission)")
if pay_["balance"] > 0:
    s, po, _ = call("POST", API + "/v1/vendor/payouts", token=VND)
    _, after, _ = call("GET", API + "/v1/vendor/payouts", token=VND)
    ok(s == 201 and po["reference"].startswith("UTR") and after["balance"] == 0, f"payout of ₹{po.get('amount')} ({po.get('reference')}); balance now ₹{after['balance']}")
s, _, _ = call("GET", API + "/v1/vendor/orders", token=p)
ok(s == 403, "passengers can't see vendors' orders")
_, integ, _ = call("GET", API + "/v1/admin/integrity", token=staff("ADM00001"))
ok(integ["trial_balance"] == 0, f"ledger balances with escrow, commissions, refunds and payouts: trial balance {integ['trial_balance']}")

sys.exit(1 if FAILED else 0)

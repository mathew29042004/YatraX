#!/usr/bin/env python3
"""Phase 3 smoke test: cancellation and refunds, the WL → RAC → CNF promotion engine,
the wallet, reconciliation of a payment killed mid-flow, chart preparation, TDRs,
boarding-point changes, notifications and the ledger.
Run with the stack up:  python scripts/smoke_phase3.py"""
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
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Three"})
    return s["access_token"]


def admin():
    _, s, _ = call("POST", API + "/v1/auth/login", {"identifier": "ADM00001", "password": "yatrax123"})
    return s["access_token"]


def hold(tok, train, frm, to, date, cls, pax=1, quota="GN", **kw):
    people = [{"name": f"Traveller {i + 1}", "age": 30 + i, "gender": "MF"[i % 2], "berth_pref": ""} for i in range(pax)]
    body = {"train": train, "from": frm, "to": to, "date": str(date), "class": cls, "quota": quota, "passengers": people,
            "together": True, "only_confirmed": False}
    body.update(kw)
    return call("POST", API + "/v1/bookings", body, tok, headers={"Idempotency-Key": str(uuid.uuid4())})


def pay(tok, booking_id):
    s, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=tok)
    order = r["checkout_url"].rsplit("/", 1)[1]
    s, _, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": "success"})
    if s != 303:
        return order, None
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    _, b, _ = call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                   {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, tok)
    return order, b


def get(tok, booking_id):
    return call("GET", f"{API}/v1/bookings/{booking_id}", token=tok)[1]


IST = dt.timezone(dt.timedelta(hours=5, minutes=30))
today = dt.datetime.now(IST).date()
ADM = admin()
call("POST", GW + "/admin/mode", {"mode": "normal"})

# ── 1. The plan's demo: cancellations elsewhere move a waitlisted passenger up ──
# Find a waitlisted journey: Delhi → Patna in the weeks around Diwali and Chhath.
target = None
for days in range(0, 25):
    d = dt.date(2026, 11, 1) + dt.timedelta(days=days)
    if d <= today:
        continue
    s, r, _ = call("GET", f"{API}/v1/search?from=NDLS&to=PNBE&date={d}")
    for t in r.get("trains", []):
        if t["from"]["code"] != "NDLS" or t["to"]["code"] != "PNBE":
            continue
        for c in t["classes"]:
            if c["availability"]["code"] == "WL" and c["code"] in ("SL", "3A", "2A") and c["availability"]["count"] <= 40:
                target = (t["number"], d, c["code"])
                break
        if target:
            break
    if target:
        break
ok(target is not None, f"found a waitlisted journey: {target}")
w = passenger()
train, date, wcls = target
s, b, _ = hold(w, train, "NDLS", "PNBE", date, wcls)
ok(s == 201 and b["passengers"][0]["status"] in ("WL", "RAC"), f"booked onto the queue: {b['passengers'][0]['status']} "
   f"{b['passengers'][0].get('wl_no') or b['passengers'][0].get('rac_no')}")
_, b = pay(w, b["id"])
ok(b["status"] == "CONFIRMED", f"waitlisted ticket paid: PNR {b.get('pnr')}, booked as {b['passengers'][0].get('booked_status')}")
origin = date - dt.timedelta(days=0)  # NDLS is the origin of the Delhi → Patna trains
seen = [b["passengers"][0]["status"]]
for _ in range(120):
    # A few cancellations at a time, so each step of the queue is visible.
    s, r, _ = call("POST", API + "/v1/admin/runs/simulate-cancellations",
                   {"train": train, "origin_date": str(origin), "class": wcls, "count": 3}, ADM)
    if s != 200:
        ok(False, f"simulate cancellations: {r}")
        break
    st = get(w, b["id"])["passengers"][0]
    if st["status"] != seen[-1]:
        seen.append(st["status"])
    if st["status"] == "CNF":
        break
ok(seen[-1] == "CNF" and ("RAC" in seen), f"queue moved {' → '.join(seen)}; now {st.get('coach')}/{st.get('berth')}, "
   f"ticket still says {st.get('booked_status')}")

def intact(d):
    """Earlier runs of the control-centre smoke test cancel, divert or cut short some 12623 runs; skip those."""
    _, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={d}")
    return isinstance(st, dict) and not st.get("disruption")


# ── 2. Cancel a confirmed ticket: refund by the rules, paid back through the gateway ──
c = passenger()
# A day whose run isn't charted yet (earlier runs of this test chart days 45–55 out).
_, tte_s, _ = call("POST", API + "/v1/auth/login", {"identifier": "TTE00001", "password": "yatrax123"})
for _ in range(20):
    far = today + dt.timedelta(days=random.randint(20, 40))
    _, run_, _ = call("GET", f"{API}/v1/tte/runs/12623/{far}", token=tte_s["access_token"])
    if isinstance(run_, dict) and run_.get("chart") is None and intact(far):
        break
s, b, _ = hold(c, "12627", "SBC", "NDLS", far, "3A", pax=2)
_, b = pay(c, b["id"])
s, q, _ = call("GET", f"{API}/v1/bookings/{b['id']}/cancel-quote?passengers=2", token=c)
want = 180 if q["passengers"][0]["status"] == "CNF" else 60
ok(s == 200 and q["passengers"][0]["deduction"] == want,
   f"quote for cancelling passenger 2 ({q['passengers'][0]['status']}): refund ₹{q['refund']}, charge ₹{q['passengers'][0]['deduction']} — {q['passengers'][0]['rule']}")
s, r, _ = call("POST", f"{API}/v1/bookings/{b['id']}/cancel", {"passengers": [2], "refund_to": "source"}, c)
ok(s == 200 and r["booking"]["status"] == "CONFIRMED" and r["booking"]["passengers"][1]["status"] == "CAN",
   "partial cancellation: passenger 2 cancelled, passenger 1 still travels")
s, rep, _ = call("POST", API + "/v1/admin/reconcile", token=ADM)
bk = get(c, b["id"])
ok(bk["refunds"][0]["status"] == "PROCESSED" and bk["refunds"][0]["destination"] == "source",
   f"refund ₹{bk['refunds'][0]['amount']} paid back to the original payment by the refund worker")

# ── 3. Wallet: refund instantly, then spend it ──
s, r, _ = call("POST", f"{API}/v1/bookings/{b['id']}/cancel", {"passengers": [], "refund_to": "wallet"}, c)
ok(r["booking"]["status"] == "CANCELLED" and r["refund_to"] == "wallet", f"rest of the booking cancelled; ₹{r['quote']['refund']} to the wallet")
_, wl, _ = call("GET", API + "/v1/me/wallet", token=c)
ok(wl["balance"] == r["quote"]["refund"] * 100, f"wallet balance ₹{wl['balance'] // 100}")
s, cheap, _ = hold(c, "12627", "SBC", "YNK", far, "SL")
s, paid, _ = call("POST", f"{API}/v1/bookings/{cheap['id']}/pay-wallet", token=c)
_, wl2, _ = call("GET", API + "/v1/me/wallet", token=c)
ok(s == 200 and paid["status"] == "CONFIRMED" and wl2["balance"] == wl["balance"] - cheap["total_fare"] * 100,
   f"paid ₹{cheap['total_fare']} from the wallet; ₹{wl2['balance'] // 100} left")

# ── 4. The plan's demo: kill a payment mid-flow → automatic refund ──
k = passenger()
call("POST", GW + "/admin/mode", {"mode": "timeout"})  # money taken, redirect and webhook lost
s, h, _ = hold(k, "12627", "SBC", "NDLS", far + dt.timedelta(days=1), "2A")
order, res = pay(k, h["id"])
call("POST", GW + "/admin/mode", {"mode": "normal"})
ok(res is None and get(k, h["id"])["status"] == "PAYMENT_PENDING", "payment killed mid-flow: money taken, YatraX never told")
call("POST", f"{API}/v1/dev/bookings/{h['id']}/expire")
s, rep, _ = call("POST", API + "/v1/admin/reconcile?stale_after_sec=0", token=ADM)
hb = get(k, h["id"])
ok(rep["captured"] >= 1 and hb["status"] == "FAILED" and hb["refunds"] and hb["refunds"][0]["status"] == "PROCESSED",
   f"reconciliation found the payment and refunded ₹{hb['refunds'][0]['amount'] if hb.get('refunds') else '?'} automatically "
   f"(payment {hb['payment']['status']})")

# ── 5. Boarding-point change ──
s, bb, _ = hold(c, "12623", "MAS", "ERN", far, "SL")
_, bb = pay(c, bb["id"])
s, moved, _ = call("POST", f"{API}/v1/bookings/{bb['id']}/boarding", {"station": "SA"}, c)
ok(s == 200 and moved["from"] == "SA" and moved["original_from"] == "MAS", "boarding point changed MAS → SA" + ("" if s == 200 else f" ({s} {moved})"))
s, again, _ = call("POST", f"{API}/v1/bookings/{bb['id']}/boarding", {"station": "ED"}, c)
ok(s == 409, "a second change is refused")

# ── 6. Chart preparation and TDR ──
# Charted days come from their own range (45–55 days out), so a later run's `far` day (20–40) is never
# one an earlier run already charted — a charted run rightly refuses boarding changes.
chart_day = today + dt.timedelta(days=random.randint(45, 55))
s, cb, _ = hold(c, "12623", "MAS", "ERN", chart_day, "SL")
_, cb = pay(c, cb["id"])
s, ch, _ = call("POST", API + "/v1/admin/runs/chart", {"train": "12623", "origin_date": str(chart_day), "version": 1}, ADM)
ok(s == 200 and ch["version"] == 1 and any(p["pnr"] == cb["pnr"] for k_ in ch["classes"] for p in k_["passengers"]),
   f"chart 1 prepared: {sum(len(x['passengers']) for x in ch['classes'])} passengers, our PNR on it")
s, q, _ = call("GET", f"{API}/v1/bookings/{cb['id']}/cancel-quote", token=c)
ok(q["chart_prepared"] and q["refund"] == 0, "after the chart a confirmed cancellation refunds nothing")
s, td, _ = call("POST", f"{API}/v1/bookings/{cb['id']}/tdr", {"reason": "train_late", "details": "smoke test"}, c)
ok(s == 201 and td["status"] == "PENDING", "TDR filed")
s, dec, _ = call("POST", f"{API}/v1/admin/tdrs/{td['id']}/decide", {"approve": True, "refund": td["max_refund"] // 2}, ADM)
call("POST", API + "/v1/admin/reconcile", token=ADM)
cbb = get(c, cb["id"])
ok(dec["status"] == "APPROVED" and cbb["refunds"][-1]["status"] == "PROCESSED", f"TDR approved; ₹{dec['refund']} refunded")
s, late, _ = hold(c, "12623", "MAS", "ERN", chart_day, "SL", pax=1)
ok(s in (201, 409), "booking after the chart is current booking only (free berths, no waitlist)")

# ── 7. Notifications and the ledger ──
time.sleep(3)  # the outbox relay and consumers run every half second
_, n, _ = call("GET", API + "/v1/me/notifications", token=w)
kinds = {x["kind"] for x in n["items"]}
ok({"confirmed", "promoted"} <= kinds, f"waitlisted passenger notified: {sorted(kinds)} ({n['unread']} unread)")
_, n, _ = call("GET", API + "/v1/me/notifications", token=k)
ok(any(x["kind"] == "refund" for x in n["items"]), "killed-payment passenger told about the refund")
_, f, _ = call("GET", API + "/v1/admin/finance", token=ADM)
ok(f["trial_balance"] == 0, f"ledger balances: trial balance ₹{f['trial_balance']}, refunds owed ₹{f['refunds_owed'] // 100}, "
   f"cancellation charges ₹{f['cancellation_revenue'] // 100}")

sys.exit(1 if FAILED else 0)

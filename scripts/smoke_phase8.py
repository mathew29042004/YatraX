#!/usr/bin/env python3
"""Phase 8 smoke test: the control centre. The section chart, the what-if ripple, the
plan's demo (a train breaks down → see the ripple → divert it → its passengers are
refunded, offered another train and told), cancelling and short-terminating, extra
coaches clearing a waitlist, network conflicts, and the money still balancing.
Run with the stack up:  python scripts/smoke_phase8.py"""
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
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Eight"})
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


def intact(d):
    """Earlier runs of the control-centre smoke test cancel, divert or cut short some 12623 runs; skip those."""
    _, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={d}")
    return isinstance(st, dict) and not st.get("disruption")


def free_day(to, avoid=()):
    """A day 5–45 days out with an AVL class on 12623 to `to`, not used yet in this run."""
    today = dt.datetime.now(IST).date()
    for days in random.sample(range(5, 46), 41):
        d = today + dt.timedelta(days=days)
        if d in avoid or not intact(d):
            continue
        _, r, _ = call("GET", f"{API}/v1/search?from=MAS&to={to}&date={d}")
        mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
        cls = next((c["code"] for c in (mail or {}).get("classes", []) if c["availability"]["code"] == "AVL"), None)
        if cls:
            return d, cls
    return None, None


def book(tok, to, d, cls):
    s, b, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": to, "date": str(d), "class": cls, "quota": "GN",
                                                    "passengers": [{"name": "Disrupted Traveller", "age": 45, "gender": "F", "berth_pref": ""}],
                                                    "together": True, "only_confirmed": False}, tok, headers={"Idempotency-Key": str(uuid.uuid4())})
    return pay(tok, b["id"]) if s == 201 else None


def notice(tok, timeout=30):
    end = time.time() + timeout
    while time.time() < end:
        _, n, _ = call("GET", API + "/v1/me/notifications", token=tok)
        hit = next((x for x in n["items"] if x["kind"] == "disruption"), None)
        if hit:
            return hit
        time.sleep(1)
    return None


CTL = staff("CTRL00001")
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)

# ── 1. Section chart and access ──
s, sec, _ = call("GET", API + "/v1/control/section?from=MAS&to=SA&hours=4", token=CTL)
dirs = {t["direction"] for t in sec.get("trains", [])}
ok(s == 200 and len(sec["stations"]) >= 5 and len(sec["trains"]) >= 10 and dirs == {"up", "down"},
   f"section chart Chennai – Salem: {len(sec['stations'])} stations, {len(sec['trains'])} trains both ways")
s, _, _ = call("GET", API + "/v1/control/section?from=MAS&to=MAS", token=CTL)
ok(s == 400, "a section needs two stations")
s, _, _ = call("GET", API + "/v1/control/section?from=MAS&to=SA", token=passenger())
ok(s == 403, "passengers can't open the control room")

# ── 2. The demo: 12623 breaks down; its passengers ──
D, CLS = free_day("SA")
p_sa, p_ern = passenger(), passenger()
b_sa = book(p_sa, "SA", D, CLS)
b_ern = book(p_ern, "ERN", D, CLS)
ok(b_sa and b_ern and b_sa["status"] == b_ern["status"] == "CONFIRMED", f"two passengers on 12623 on {D}: to Salem and to Ernakulam Town ({CLS})")

s, rip, _ = call("POST", API + "/v1/control/what-if", {"train": "12623", "origin_date": str(D), "station": "MAS", "delay_min": 60}, CTL)
ok(s == 200 and rip["arrival_after"] > rip["arrival_now"] and rip["passengers"] >= 2,
   f"what-if: a 60-minute hold at Chennai → {len(rip['affected'])} other trains lose {rip['total_added_min']} min; {rip['total_passengers']} passengers affected")
s, _, _ = call("POST", API + "/v1/control/what-if", {"train": "12623", "origin_date": str(D), "station": "TVC", "delay_min": 10}, CTL)
ok(s == 400, "can't hold a train at its terminus")
_, st0, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
ok(st0["state"] == "scheduled" and not st0.get("disruption"), "nothing changed: the what-if saved nothing")

# Divert around Salem (the line is blocked): Salem passengers are refunded and rebooked.
s, res, _ = call("POST", API + "/v1/control/runs/disrupt", {"train": "12623", "origin_date": str(D), "kind": "diverted", "skip": ["SA"],
                                                             "extra_delay": 75, "reason": "Engine failure blocks the line at Salem"}, CTL)
mine = next((a for a in res.get("affected", []) if a["booking_id"] == b_sa["id"]), None)
ok(s == 200 and mine and mine["refund"] == b_sa["total_fare"] and not mine["partial"],
   f"diverted: {res.get('bookings')} bookings affected, ₹{res.get('refunded')} refunded (the Salem passenger in full)")
ok(mine and mine.get("alternative") and mine["alternative"]["train"] != "12623",
   f"the Salem passenger has seats held on {mine and mine.get('alternative', {}).get('train')}: {mine and mine.get('alternative', {}).get('status')}")
ok(all(a["booking_id"] != b_ern["id"] for a in res.get("affected", [])), "the Ernakulam passenger isn't refunded: the diversion still gets them there")
n = notice(p_sa)
ok(n is not None and "diverted" in n["title"] and "held" in n["body"], f"the Salem passenger is told: {n and n['title']} — {n and n['body'][:90]}")
_, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
sa = next(v for v in st["stops"] if v["station"] == "SA")
ok(st.get("disruption") == "diverted" and sa["state"] == "skipped" and st["stops"][-1]["arr_delay"] >= st0["stops"][-1]["arr_delay"] + 30,
   f"live status: Salem skipped, the longer route adds time ({st0['stops'][-1]['arr_delay']} → {st['stops'][-1]['arr_delay']} min late at TVC)")
s, err, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": "SA", "date": str(D), "class": CLS, "quota": "GN",
                                                 "passengers": [{"name": "Too Late", "age": 30, "gender": "M", "berth_pref": ""}],
                                                 "together": True, "only_confirmed": False}, passenger(), headers={"Idempotency-Key": str(uuid.uuid4())})
ok(s == 409 and err.get("error") == "train_disrupted", "new bookings to Salem on that run are refused")
s, _, _ = call("POST", API + "/v1/control/runs/disrupt", {"train": "12623", "origin_date": str(D), "kind": "cancelled", "reason": "again"}, CTL)
ok(s == 409, "one disruption per run")

# ── 3. Cancel and short-terminate ──
D2, C2 = free_day("ERN", avoid={D})
p2 = passenger()
b2 = book(p2, "ERN", D2, C2)
s, res, _ = call("POST", API + "/v1/control/runs/disrupt", {"train": "12623", "origin_date": str(D2), "kind": "cancelled", "reason": "Flooding at Arakkonam"}, CTL)
a2 = next((a for a in res.get("affected", []) if a["booking_id"] == b2["id"]), None)
ok(s == 200 and a2 and a2["refund"] == b2["total_fare"], f"cancelled {D2}: full refund ₹{a2 and a2['refund']}, {res.get('rebooked')} rebooked, {res.get('simulated')} simulated bookings released")
_, g2, _ = call("GET", f"{API}/v1/bookings/{b2['id']}", token=p2)
ok(g2["status"] == "CANCELLED" and g2["refunds"] and g2["refunds"][0]["amount"] == b2["total_fare"], "the booking shows cancelled with its refund")
_, st2, _ = call("GET", f"{API}/v1/live/trains/12623?date={D2}")
ok(st2["state"] == "cancelled", f"live status: {st2['message']}")

D3, C3 = free_day("TVC", avoid={D, D2})
p3 = passenger()
b3 = book(p3, "TVC", D3, C3)
s, res, _ = call("POST", API + "/v1/control/runs/disrupt", {"train": "12623", "origin_date": str(D3), "kind": "short_terminated", "station": "ERN",
                                                             "reason": "Landslide beyond Ernakulam"}, CTL)
a3 = next((a for a in res.get("affected", []) if a["booking_id"] == b3["id"]), None)
ok(s == 200 and a3 and a3["partial"] and 0 < a3["refund"] < b3["total_fare"],
   f"short-terminated at ERN: the Trivandrum passenger gets ₹{a3 and a3['refund']} of ₹{b3['total_fare']} back and keeps the trip to Ernakulam")
_, g3, _ = call("GET", f"{API}/v1/bookings/{b3['id']}", token=p3)
ok(g3["status"] == "CONFIRMED", "their booking stays confirmed")

# ── 4. Extra coaches and the network ──
s, wl, _ = call("GET", API + "/v1/control/waitlists?days=60", token=CTL)
ok(s == 200 and isinstance(wl, list), f"longest waitlists: {len(wl)} runs")
target = next((w for w in wl if w["waitlisted"] >= 2), None)
if target:
    s, ex, _ = call("POST", API + "/v1/control/runs/extra-coaches", {"train": target["train"], "origin_date": target["origin_date"], "class": target["class"], "count": 1}, CTL)
    ok(s == 200 and ex["coaches"] and len(ex["promotions"]) > 0 and ex["still_waitlisted"] < target["waitlisted"],
       f"extra {target['class']} coach {ex.get('coaches')} on {target['train']} {target['origin_date']}: {len(ex.get('promotions', []))} moved up, WL {target['waitlisted']} → {ex.get('still_waitlisted')}")
else:
    print("SKIP no waitlisted run to add a coach to")
s, cf, _ = call("GET", API + "/v1/control/conflicts?hours=2", token=CTL)
ok(s == 200 and isinstance(cf, list) and all(c["overlap_min"] > 0 for c in cf), f"platform conflicts at major stations: {len(cf)}")
s, dl, _ = call("GET", API + "/v1/control/disruptions", token=CTL)
ok(s == 200 and sum(1 for x in dl if x["train"] == "12623" and x["origin_date"] in (str(D), str(D2), str(D3))) == 3, "the incident log lists all three")

_, integ, _ = call("GET", API + "/v1/admin/integrity", token=staff("ADM00001"))
ok(integ["overlapping_allocations"] == 0 and integ["trial_balance"] == 0,
   f"integrity after disruptions and extra coaches: {integ['overlapping_allocations']} overlaps, trial balance {integ['trial_balance']}")

sys.exit(1 if FAILED else 0)

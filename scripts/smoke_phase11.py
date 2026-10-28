#!/usr/bin/env python3
"""Phase 11 smoke test: zone and division dashboards. Revenue that adds up and moves with a
new booking (in its own zone only), occupancy per run, punctuality from the live delay model
reacting to a delay, the fraud report, and who may see which zone.
Run with the stack up:  python scripts/smoke_phase11.py"""
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


def staff(emp):
    _, s, _ = call("POST", API + "/v1/auth/login", {"identifier": emp, "password": "yatrax123"})
    return s["access_token"]


def passenger():
    phone = random.choice("6789") + "".join(random.choices("0123456789", k=9))
    _, r, _ = call("POST", API + "/v1/auth/otp/request", {"phone": phone})
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Data Point"})
    return s["access_token"]


def pay(tok, booking_id):
    _, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=tok)
    order = r["checkout_url"].rsplit("/", 1)[1]
    _, _, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": "success"})
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    _, b, _ = call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                   {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, tok)
    return b


ADM, CTL, SM = staff("ADM00001"), staff("CTRL00001"), staff("SM00001")
A = API + "/v1/analytics"
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)

# ── 1. Zones and the shape of the numbers ──
s, z, _ = call("GET", A + "/zones", token=ADM)
sr = next((x for x in z.get("zones", []) if x["code"] == "SR"), None) if s == 200 else None
ok(sr and len(z["zones"]) == 17 and z["home_zone"] == "", f"{len(z.get('zones', []))} zones; Southern Railway has {len(sr['divisions']) if sr else 0} divisions; admin sees all India")
s, india, _ = call("GET", A + "/overview", token=ADM)
k = india["kpis"]
ok(s == 200 and k["gross"] > 0 and sum(c["revenue"] for c in india["by_class"]) == k["gross"] == sum(q["revenue"] for q in india["by_quota"]),
   f"all India: ₹{k['gross']:,} in fares from {k['passengers']:,} passengers; class and quota splits add up to it")
ok(sum(d["passengers"] for d in india["by_travel_date"]) == k["passengers"] and k["net"] == k["gross"] - k["refunds"],
   f"demand by travel date covers every passenger; net ₹{k['net']:,} = fares − refunds")
_, srv, _ = call("GET", A + "/overview?zone=SR", token=ADM)
_, nr, _ = call("GET", A + "/overview?zone=NR", token=ADM)
ok(0 < srv["kpis"]["gross"] < k["gross"] and srv["kpis"]["gross"] + nr["kpis"]["gross"] <= k["gross"],
   f"zones partition India: SR ₹{srv['kpis']['gross']:,} + NR ₹{nr['kpis']['gross']:,} ≤ ₹{k['gross']:,}")
chennai = next(d for d in sr["divisions"] if d["name"] == "Chennai")
_, chn, _ = call("GET", f"{A}/overview?zone=SR&division={chennai['id']}", token=ADM)
ok(any(t["number"] == "12623" for t in chn["top_trains"]) and chn["kpis"]["gross"] <= srv["kpis"]["gross"],
   f"Chennai division ₹{chn['kpis']['gross']:,}, Trivandrum Mail among its top trains")

# ── 2. A new booking moves its own zone's numbers, nobody else's ──
today = dt.datetime.now(IST).date()
p = passenger()
booked = None
for days in random.sample(range(3, 13), 10):
    d = today + dt.timedelta(days=days)
    s, b, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": "ERN", "date": str(d), "class": "SL", "quota": "GN",
                                                  "passengers": [{"name": "Data Point", "age": 30, "gender": "F", "berth_pref": ""}],
                                                  "together": True, "only_confirmed": True}, p, headers={"Idempotency-Key": str(uuid.uuid4())})
    if s == 201:
        booked = pay(p, b["id"])
        break
ok(booked and booked["status"] == "CONFIRMED", f"booked 12623 MAS→ERN SL on {booked and booked['journey_date']} for ₹{booked and booked['total_fare']}")
_, srv2, _ = call("GET", A + "/overview?zone=SR", token=ADM)
_, nr2, _ = call("GET", A + "/overview?zone=NR", token=ADM)
ok(srv2["kpis"]["gross"] - srv["kpis"]["gross"] >= booked["total_fare"] and srv2["kpis"]["passengers"] > srv["kpis"]["passengers"],
   f"SR fares up ₹{srv2['kpis']['gross'] - srv['kpis']['gross']:,} (other people may be booking too)")
ok(nr2["kpis"]["gross"] == nr["kpis"]["gross"], "Northern Railway unchanged")

# ── 3. Occupancy ──
days_out = (dt.date.fromisoformat(booked["journey_date"]) - today).days
s, occ, _ = call("GET", f"{A}/occupancy?zone=SR&days={days_out + 1}", token=ADM)
row = next((r for r in occ.get("heatmap", []) if r["number"] == "12623"), None)
cell = next((c for c in (row or {}).get("cells", []) if c["date"] == booked["journey_date"]), None)
ok(s == 200 and cell and cell.get("load", 0) > 0 and cell["bookings"] > 0,
   f"12623 on {booked['journey_date']}: {cell and round(cell['load'] * 100)}% of berth-segments sold, {cell and cell['bookings']} bookings")
ok(0 < occ["avg_load"] <= 1 and all(0 < c["load"] <= 1 for c in occ["by_class"]) and len(occ["dates"]) == days_out + 1,
   f"SR: {occ['runs']} runs open, average {round(occ['avg_load'] * 100)}%; classes {[(c['class'], round(c['load'] * 100)) for c in occ['by_class']]}")

# ── 4. Punctuality, and a delay showing up in it ──
s, pu, _ = call("GET", A + "/punctuality?zone=SR", token=ADM)
ok(s == 200 and len(pu["zones"]) == 17 and sum(d["runs"] for d in pu["daily"]) == pu["summary"]["runs"] > 0 and 0 < pu["summary"]["on_time"] < 1,
   f"SR {pu['from']}–{pu['to']}: {round(pu['summary']['on_time'] * 1000) / 10}% on time over {pu['summary']['runs']} runs; league table of 17 zones")
ok(pu["divisions"] and all(r["runs"] > 0 for r in pu["divisions"]) and pu["reasons"], f"{len(pu['divisions'])} SR divisions ranked; top reason for delays: {pu['reasons'][0]['key']}")
# Hold the Trivandrum Mail for two hours at Chennai on a run inside the window: wind the railway
# clock back to that morning, add the incident, and come back to today.
past = dt.date.fromisoformat(pu["from"]) + dt.timedelta(days=random.randrange(7))
s1, _, _ = call("POST", API + "/v1/admin/sim/clock", {"jump_to": f"{past}T08:00:00+05:30", "speed": 1}, CTL)
s2, _, _ = call("POST", API + "/v1/admin/live/incidents", {"train": "12623", "origin_date": str(past), "station": "MAS", "delay_min": 150,
                                                         "reason": "Smoke test: rake failure"}, CTL)
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)
added = 0
end = time.time() + 75
while time.time() < end and added < 100:
    time.sleep(5)
    _, pu2, _ = call("GET", A + "/punctuality?zone=SR", token=ADM)
    added = pu2["summary"]["avg_delay"] * pu2["summary"]["runs"] - pu["summary"]["avg_delay"] * pu["summary"]["runs"]
ok(s1 == 200 and s2 == 201 and added >= 100,
   f"a 150-min hold of 12623 on {past} adds {added:.0f} min of arrival delay to SR (some is made up on the way); "
   f"average {pu['summary']['avg_delay']:.2f} → {pu2['summary']['avg_delay']:.2f} min")

# ── 5. Fraud report ──
s, f, _ = call("GET", A + "/fraud", token=ADM)
ok(s == 200 and f["bots"]["checked"] >= f["bots"]["blocked"] and all(x["accounts"] >= 3 for x in f["bots"]["shared_ips"] + f["bots"]["shared_devices"]),
   f"fraud: {f['bots']['blocked']} bot sessions blocked; {len(f['bots']['shared_devices'])} devices shared by 3+ accounts; {len(f['touts'])} possible touts")
ok(all(t["accounts"] >= 3 for t in f["touts"]) and f["payments"]["unresolved"] >= 0, "every tout lead spans 3+ accounts")

# ── 6. Who sees what ──
s, smz, _ = call("GET", A + "/zones", token=SM)
s2, smo, _ = call("GET", A + "/overview", token=SM)
ok(s == 200 and smz["home_zone"] == "SR" and s2 == 200 and smo["scope"]["zone"] == "SR", "station master SM00001 lands on their own zone (SR)")
s, _, _ = call("GET", A + "/overview?zone=NR", token=SM)
ok(s == 403, "…and can't open another zone's dashboards")
s, _, _ = call("GET", A + "/fraud", token=SM)
ok(s == 403, "the fraud report is for headquarters (admins) only")
s, _, _ = call("GET", A + "/overview?zone=NR", token=CTL)
ok(s == 200, "controllers see every zone")
s, _, _ = call("GET", A + "/overview", token=p)
ok(s == 403, "passengers can't see the dashboards")
s, _, _ = call("GET", A + "/overview?zone=XX", token=ADM)
s2, _, _ = call("GET", A + "/overview?zone=SR&division=9", token=ADM)  # division 9 is Mumbai, in CR
ok(s == 400 and s2 == 400, "unknown zones and divisions outside their zone are refused")

sys.exit(1 if FAILED else 0)

#!/usr/bin/env python3
"""Smoke test for the features that fill the plan's gaps: special trains, auto-upgrade at chart
time, the divyangjan / defence / foreign-tourist quotas, travel insurance, GST invoices, carbon
saved, loyalty points, split journeys and connections, the confirmation guarantee, RailMadad
complaints and the passenger's SOS, retiring rooms and cloak rooms, and the group booking desk.
Run with the stack up:  python scripts/smoke_extras.py"""
import datetime as dt
import json
import os
import random
import sys
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


def passenger(name="Extra Traveller"):
    phone = random.choice("6789") + "".join(random.choices("0123456789", k=9))
    _, r, _ = call("POST", API + "/v1/auth/otp/request", {"phone": phone})
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": name})
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


def hold(tok, **req):
    base = {"quota": "GN", "together": True, "only_confirmed": False}
    return call("POST", API + "/v1/bookings", {**base, **req}, tok, headers={"Idempotency-Key": str(uuid.uuid4())})


ADM, CTL, SM = staff("ADM00001"), staff("CTRL00001"), staff("SM00001")
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)
today = dt.datetime.now(IST).date()
p = passenger()

# ── 1. A special train, run on a date, booked with auto-upgrade, charted ──
s, specials, _ = call("GET", API + "/v1/control/specials", token=CTL)
sp = next((x for x in specials if x["number"] == "02623"), None) if s == 200 else None
ok(sp and len(specials) >= 5 and sp["base"] == "12623", f"{len(specials or [])} special trains ready, e.g. {sp and sp['number']} {sp and sp['name']} leaving {sp and sp['departure']}")
used = set(sp["runs"]) if sp else set()
day = next(today + dt.timedelta(days=d) for d in random.sample(range(10, 56), 46) if str(today + dt.timedelta(days=d)) not in used)
s, r, _ = call("POST", API + "/v1/control/specials", {"train": "02623", "date": str(day), "reason": "Smoke test"}, CTL)
ok(s == 201 and str(day) in r["runs"], f"02623 runs on {day}")
_, srch, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={day}")
special = next((t for t in srch["trains"] if t["number"] == "02623"), None)
ok(special and special["runs_on_date"] and any(c["availability"]["code"] == "AVL" for c in special["classes"]), "it shows in search with berths")
s, _, _ = call("POST", API + "/v1/control/specials", {"train": "12623", "date": str(day)}, CTL)
ok(s == 400, "only special trains can be added that way")
s, b, _ = hold(p, train="02623", **{"from": "MAS"}, to="ERN", date=str(day), **{"class": "SL"}, auto_upgrade=True, insurance=True,
               passengers=[{"name": "Upgrade Hopeful", "age": 40, "gender": "F", "berth_pref": ""}, {"name": "Second Hopeful", "age": 42, "gender": "M", "berth_pref": ""}])
b = pay(p, b["id"]) if s == 201 else {}
ok(b.get("status") == "CONFIRMED" and b["auto_upgrade"] and b["insurance_premium"] == 2 and b["total_fare"] == b["fare"]["total"] * 2 + 2,
   f"booked the special in SL with auto-upgrade and insurance: ₹{b.get('total_fare')} (₹{b.get('insurance_premium')} insurance)")
ok(b.get("points_earned", 0) == (b["total_fare"] - 2) // 100 and b.get("carbon", {}).get("saved_kg", 0) > 0,
   f"+{b.get('points_earned')} points; {b.get('carbon', {}).get('saved_kg')} kg CO₂ saved vs flying")
s, ch, _ = call("POST", API + "/v1/admin/runs/chart", {"train": "02623", "origin_date": str(day), "version": 1}, ADM)
_, after, _ = call("GET", f"{API}/v1/bookings/{b['id']}", token=p)
ok(s == 200 and ch["upgrades"] >= 2 and after["class"] == "3A" and after["passengers"][0].get("upgraded_from") == "SL" and after["total_fare"] == b["total_fare"],
   f"chart prepared: {ch.get('upgrades')} upgraded; our party now in {after.get('class')} {after['passengers'][0].get('coach')}, same fare")

# ── 2. Invoice and loyalty ──
s, inv, _ = call("GET", f"{API}/v1/bookings/{b['id']}/invoice", token=p)
ok(s == 200 and inv["pnr"] == b["pnr"] and inv["total"] == b["total_fare"] and inv["taxable"] == sum(l["amount"] for l in inv["lines"]) and inv["sac"] == "996421",
   f"GST invoice {inv.get('number')}: taxable ₹{inv.get('taxable')}, total ₹{inv.get('total')} (lines add up)")
s, loy, _ = call("GET", API + "/v1/me/loyalty", token=p)
ok(s == 200 and loy["balance"] == b["points_earned"], f"loyalty: {loy.get('balance')} points ({loy.get('rules')})")
s, _, _ = call("POST", API + "/v1/me/loyalty/redeem", {"points": loy["balance"] + 100}, p)
ok(s in (400, 409, 422), "can't redeem more points than you have")

# ── 3. Quotas that need a document ──
_, qs, _ = call("GET", API + "/v1/quotas")
codes = [q["code"] for q in qs["quotas"]]
ok(all(c in codes for c in ("HP", "DF", "FT")), f"quotas: {', '.join(codes)}")
hp = dict(train="12623", **{"from": "MAS"}, to="ERN", date=str(today + dt.timedelta(days=random.randint(15, 50))), **{"class": "SL"}, quota="HP",
          passengers=[{"name": "Divya Raman", "age": 30, "gender": "F", "berth_pref": ""}, {"name": "Escort Raman", "age": 34, "gender": "M", "berth_pref": ""}])
s, err, _ = hold(p, **hp)
ok(s == 400, f"divyangjan quota without a UDID card is refused: {err.get('message', '')[:60]}")
hp["passengers"][0]["id_number"] = "TN0120230012345"
s, hb, _ = hold(p, **hp)
ok(s == 201 and hb["fare"]["concession"] > 0 and hb["passengers"][0]["id_number"] == "••••2345",
   f"with the card: concession ₹{hb.get('fare', {}).get('concession')} per head, ID shown as {hb.get('passengers', [{}])[0].get('id_number')}")
if s == 201:
    call("POST", f"{API}/v1/bookings/{hb['id']}/release", token=p)

# ── 4. Split journeys and connections ──
s, alts, _ = call("GET", f"{API}/v1/search/alternatives?from=MAS&to=ERN&date={today + dt.timedelta(days=25)}&class=3A")
good = s == 200 and all(a["legs"][0]["from"]["code"] == "MAS" and a["legs"][-1]["to"]["code"] == "ERN" and a["fare"] == sum(l["fare"] for l in a["legs"])
                        and all(l["availability"]["code"] == "AVL" for l in a["legs"]) for a in alts)
ok(good, f"{len(alts or [])} alternatives MAS→ERN in 3A, every leg with berths" + (f", e.g. via {alts[0]['via']['name']}" if alts else ""))

# ── 5. Confirmation guarantee: refused when the first choice has berths ──
one = [{"name": "G Test", "age": 30, "gender": "F", "berth_pref": ""}]
s, err, _ = call("POST", API + "/v1/bookings/guarantee", {
    "first": {"train": "02623", "from": "MAS", "to": "ERN", "date": str(day), "class": "2A", "quota": "GN", "passengers": one, "together": True, "only_confirmed": False},
    "second": {"train": "12623", "from": "MAS", "to": "ERN", "date": str(day), "class": "2A", "quota": "GN", "passengers": one, "together": True, "only_confirmed": False}}, p)
ok(s in (400, 409) and "book it directly" in (err.get("message", "") if isinstance(err, dict) else ""),
   f"guarantee refused when the first choice has berths: {err.get('message', '')[:70] if isinstance(err, dict) else err}")

# ── 6. RailMadad complaints and SOS ──
s, c, _ = call("POST", f"{API}/v1/bookings/{b['id']}/complaints", {"category": "water", "description": "No water in the B1 toilet"}, p)
ok(s == 201 and c["department"] == "mechanical" and c["priority"] == "high" and c["station"], f"complaint #{c.get('id')} → {c.get('department')}, {c.get('priority')}, act at {c.get('station')}")
s, sos, _ = call("POST", f"{API}/v1/bookings/{b['id']}/sos", {"kind": "female_tte"}, p)
ok(s == 201 and sos["complaint"]["priority"] == "urgent" and sos["complaint"]["department"] == "rpf", f"SOS: {sos.get('message', '')[:90]}")
s, sos2, _ = call("POST", f"{API}/v1/bookings/{b['id']}/sos", {"kind": "teleport"}, p)
ok(s == 409, "unknown SOS kinds are refused")
_, queue, _ = call("GET", API + "/v1/complaints", token=SM)
mine = next((x for x in queue if x["id"] == c["id"]), None)
ok(mine is not None and queue[0]["priority"] == "urgent", f"Chennai Central's queue has it ({len(queue)} open, urgent first)")
s, done, _ = call("POST", f"{API}/v1/complaints/{c['id']}", {"action": "resolve", "resolution": "Tank refilled before departure"}, SM)
ok(s == 200 and done["status"] == "resolved" and done["resolved_at"], "resolved by the station master")
_, ov, _ = call("GET", API + "/v1/analytics/overview?zone=SR", token=CTL)
ok(ov["complaints"]["total"] >= 2 and any(x["key"] == "water" for x in ov["complaints"]["by_category"]), f"dashboards: {ov['complaints']['total']} complaints, {ov['complaints']['open']} open")

# ── 7. Retiring room and cloak room ──
dep = dt.datetime.fromisoformat(b["departure"])
s, opts, _ = call("GET", f"{API}/v1/stations/MAS/stays?from={urllib.parse.quote((dep - dt.timedelta(hours=8)).isoformat())}&hours=12")
room = next((o for o in opts if o["type"] == "ac_room"), None) if s == 200 else None
ok(room and room["total"] >= room["free"] > 0, f"MAS: {room and room['free']} of {room and room['total']} AC rooms free, ₹{room and room['price']['12']} for 12 h")
s, stay, _ = call("POST", API + "/v1/stays", {"booking_id": b["id"], "station": "MAS", "unit": "ac_room", "units": 1,
                                              "check_in": (dep - dt.timedelta(hours=8)).isoformat(), "hours": 12, "payment": "counter"}, p)
ok(s == 201 and stay["code"].startswith("RR") and stay["amount"] == room["price"]["12"], f"booked {stay.get('code')} (pay ₹{stay.get('amount')} at the counter)")
s, err, _ = call("POST", API + "/v1/stays", {"booking_id": b["id"], "station": "SA", "unit": "locker", "units": 1,
                                             "check_in": dep.isoformat(), "hours": 24, "payment": "counter"}, p)
ok(s == 409, "a stay must be at a station the ticket starts or ends at")
_, desk, _ = call("GET", API + "/v1/station-master/MAS/stays", token=SM)
s, checked, _ = call("POST", f"{API}/v1/station-master/MAS/stays/{stay['id']}", {"action": "check_in"}, SM)
ok(any(x["id"] == stay["id"] for x in desk) and s == 200 and checked["status"] == "checked_in", "the counter checks the guest in")
s, _, _ = call("POST", f"{API}/v1/stays/{stay['id']}/cancel", token=p)
ok(s == 409, "a stay can't be cancelled once checked in")

# ── 8. Group booking desk ──
for _ in range(20):
    gday = today + dt.timedelta(days=random.randint(20, 55))
    _, r, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={gday}&class=SL")
    mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
    a = next((c["availability"] for c in (mail or {}).get("classes", []) if c["code"] == "SL"), {})
    _, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={gday}")
    if a.get("code") == "AVL" and a.get("count", 0) > 25 and not st.get("disruption"):
        break
group = [{"name": f"Pilgrim {i}", "age": 30 + i, "gender": "F" if i % 2 else "M", "berth_pref": ""} for i in range(8)]
s, g, _ = call("POST", API + "/v1/groups", {"train": "12623", "from": "MAS", "to": "ERN", "date": str(gday), "class": "SL",
                                            "purpose": "pilgrimage", "contact": "9876543210", "passengers": group}, p)
ok(s == 201 and g["status"] == "pending", f"group request #{g.get('id')} for 8 pilgrims on {gday}")
s, err, _ = call("POST", API + "/v1/groups", {"train": "12623", "from": "MAS", "to": "ERN", "date": str(gday), "class": "SL",
                                              "purpose": "pilgrimage", "contact": "9876543210", "passengers": group[:3]}, p)
ok(s == 400, "fewer than 7 passengers is not a group")
s, dg, _ = call("POST", f"{API}/v1/group-desk/{g['id']}", {"approve": True, "note": "Approved"}, SM)
ok(s == 200 and dg["status"] == "approved" and len(dg["bookings"]) == 2, f"approved: berths held in {len(dg.get('bookings', []))} parties")
if s == 200:
    _, gb, _ = call("GET", f"{API}/v1/bookings/{dg['bookings'][0]}", token=p)
    hours = (dt.datetime.fromisoformat(gb["hold_expires_at"].replace("Z", "+00:00")) - dt.datetime.now(dt.timezone.utc)).total_seconds() / 3600
    ok(gb["status"] == "HELD" and 40 < hours <= 48 and len({x["coach"] for x in gb["passengers"]}) == 1, f"party 1: {len(gb['passengers'])} together in {gb['passengers'][0]['coach']}, held {hours:.0f} h")

_, integ, _ = call("GET", API + "/v1/admin/integrity", token=ADM)
ok(integ["trial_balance"] == 0 and integ["overlapping_allocations"] == 0, "ledger balances and no berth is sold twice")
sys.exit(1 if FAILED else 0)

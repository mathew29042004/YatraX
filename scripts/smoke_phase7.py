#!/usr/bin/env python3
"""Phase 7 smoke test: the station app. Multilingual announcements, coach positions, the
station master's platform chart (clashes, crowd), reassigning a platform (and the live
status, board and passenger alert that follow), and the facilities desk.
Run with the stack up:  python scripts/smoke_phase7.py"""
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
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Seven"})
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


SM = staff("SM00001")  # Chennai Central
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, staff("CTRL00001"))

# ── 1. Announcements ──
s, a, _ = call("GET", API + "/v1/stations/MAS/announcements")
ok(s == 200 and a["languages"] == ["ta", "hi", "en"] and a["station"]["name"] == "Chennai Central",
   f"Chennai Central announces in Tamil, Hindi and English ({len(a['items'])} due now)")
ok(all(len(it["lines"]) == 3 and " ".join(it["train"]) in it["lines"][2]["speech"] for it in a["items"]),
   "every announcement has all three languages, train numbers spoken digit by digit")
s, g, _ = call("GET", API + "/v1/stations/ADI/announcements")
ok(s == 200 and g["languages"] == ["hi", "en"] and g.get("missing_regional") == "gu",
   "Ahmedabad: Hindi and English, and it says the Gujarati template is still to be written")
s, _, _ = call("GET", API + "/v1/stations/NOPE/announcements")
ok(s == 404, "unknown station: 404")

# ── 2. Coach position ──
s, cp, _ = call("GET", API + "/v1/stations/SA/coach-position/12623")
spots = cp.get("spots", [])
ok(s == 200 and spots[0]["coach"] == "ENG" and spots[0]["marker"] > 1 and spots[-1]["marker"] < cp["markers"],
   f"12623 at Salem: engine at marker {spots[0]['marker']}, rake centred on {cp['markers']} markers, engine towards {cp['engine_toward']}")
s, _, _ = call("GET", API + "/v1/stations/NDLS/coach-position/12623")
ok(s == 400, "a train that doesn't call there: 400")

# ── 3. The station master's chart ──
s, p, _ = call("GET", API + "/v1/station-master/MAS/platforms?hours=4", token=SM)
slots = [x for pf in p["platforms"] for x in pf["slots"]]
ok(s == 200 and len(p["platforms"]) == p["station"]["platforms"] and slots and len(p["hourly"]) == 4,
   f"{len(p['platforms'])} platforms, {len(slots)} train slots, {len(p['clashes'])} clashes, crowd {[h['level'] for h in p['hourly']]}")
ok(all(x["end"] > x["start"] for x in slots) and any(x["boarding"] + x["alighting"] > 0 for x in slots),
   "slots have times and reserved boarding/alighting counts")
s, _, _ = call("GET", API + "/v1/station-master/NDLS/platforms", token=SM)
ok(s == 403, "a station master manages only their own station")
s, _, _ = call("GET", API + "/v1/station-master/MAS/platforms", token=passenger())
ok(s == 403, "passengers can't")

# ── 4. Reassign tomorrow's Trivandrum Mail; its passenger hears about it ──
tomorrow = (dt.datetime.now(IST) + dt.timedelta(days=1)).date()
pax = passenger()
_, r, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={tomorrow}")
mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
cls = next((c["code"] for c in (mail or {}).get("classes", []) if c["availability"]["code"] in ("AVL", "RAC")), None)
booked = None
if cls:
    s, b, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": "ERN", "date": str(tomorrow), "class": cls, "quota": "GN",
                                                    "passengers": [{"name": "Platform Watcher", "age": 50, "gender": "M", "berth_pref": ""}],
                                                    "together": True, "only_confirmed": False}, pax, headers={"Idempotency-Key": str(uuid.uuid4())})
    booked = pay(pax, b["id"]) if s == 201 else None
ok(booked and booked["status"] == "CONFIRMED", f"passenger booked tomorrow's 12623 from Chennai ({cls})")
time.sleep(12)  # the live watcher picks up runs with passengers every 10 s
_, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={tomorrow}")
sched = st["stops"][0]["platform"]
new_pf = 1 if sched != 1 else 2
s, after, _ = call("POST", API + "/v1/station-master/MAS/platforms", {"train": "12623", "origin_date": str(tomorrow), "platform": new_pf}, SM)
ok(s == 200 and after["stops"][0]["platform"] == new_pf and after["stops"][0]["platform_changed"],
   f"12623 moved from platform {sched} to {new_pf}: live status follows at once")
deadline, note = time.time() + 30, None
while time.time() < deadline and not note:
    _, n, _ = call("GET", API + "/v1/me/notifications", token=pax)
    note = next((x for x in n["items"] if x["kind"] == "platform"), None)
    time.sleep(1)
ok(note is not None and f"Platform changed to {new_pf}" in note["title"], f"the passenger is told: {note and note['title']}")
s, bad, _ = call("POST", API + "/v1/station-master/MAS/platforms", {"train": "12623", "origin_date": str(tomorrow), "platform": 99}, SM)
ok(s == 400, "a platform the station doesn't have is refused")
s, back, _ = call("POST", API + "/v1/station-master/MAS/platforms", {"train": "12623", "origin_date": str(tomorrow), "platform": 0}, SM)
ok(s == 200 and back["stops"][0]["platform"] == sched and not back["stops"][0]["platform_changed"], "and back to the timetabled platform")

# ── 5. Facilities desk ──
s, item, _ = call("POST", API + "/v1/station-master/MAS/lost-found", {"item": "Blue suitcase", "found_at": "PF 5, near the FOB"}, SM)
s2, _, _ = call("POST", f"{API}/v1/station-master/MAS/lost-found/{item['id']}/return", {"returned_to": "R. Kumar, Aadhaar shown"}, SM)
s3, _, _ = call("POST", f"{API}/v1/station-master/MAS/lost-found/{item['id']}/return", {"returned_to": "again"}, SM)
ok(s == 201 and s2 == 204 and s3 == 404, "lost and found: logged, returned once, not twice")
s, wc, _ = call("POST", API + "/v1/station-master/MAS/wheelchair", {"passenger": "Lakshmi Devi", "pnr": "1234567890", "train": "12623"}, SM)
s2, _, _ = call("POST", f"{API}/v1/station-master/MAS/wheelchair/{wc['id']}", {"status": "assigned", "attendant": "Murugan"}, SM)
s3, _, _ = call("POST", f"{API}/v1/station-master/MAS/wheelchair/{wc['id']}", {"status": "done"}, SM)
ok(s == 201 and s2 == 204 and s3 == 204, "wheelchair request: requested, assigned, done")
s, f, _ = call("GET", API + "/v1/station-master/MAS/facilities", token=SM)
ok(s == 200 and f["retiring_rooms"]["total"] == 24 and any(x["id"] == item["id"] and x["status"] == "returned" for x in f["lost_found"])
   and any(x["id"] == wc["id"] and x["status"] == "done" and x["attendant"] == "Murugan" for x in f["wheelchair"]),
   f"facilities: {f['retiring_rooms']['free']}/{f['retiring_rooms']['total']} retiring rooms, {f['cloak_room']['free']}/{f['cloak_room']['lockers']} lockers free")

sys.exit(1 if FAILED else 0)

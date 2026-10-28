#!/usr/bin/env python3
"""Phase 2 smoke test: holds, the same-berth race, segment sharing, payments through the
mock gateway (success, decline and retry, late payment, duplicate webhooks), signed
e-tickets and PNR status.
Run with the stack up:  python scripts/smoke_phase2.py"""
import datetime as dt
import json
import os
import random
import sys
import threading
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


def call(method, url, body=None, token=None, headers=None, form=None):
    data, hdrs = None, dict(headers or {})
    if body is not None:
        data, hdrs["Content-Type"] = json.dumps(body).encode(), "application/json"
    if form is not None:
        data, hdrs["Content-Type"] = urllib.parse.urlencode(form).encode(), "application/x-www-form-urlencoded"
    if token:
        hdrs["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    opener = urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(req) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw and r.headers.get_content_type() == "application/json" else raw), r.headers
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
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Tester"})
    return s["access_token"]


def hold(token, **kw):
    key = kw.pop("key", None) or str(uuid.uuid4())
    body = {"train": "12623", "from": "MAS", "to": "ERN", "date": str(DAY), "class": "SL", "quota": "GN",
            "passengers": [{"name": "Smoke Passenger", "age": 33, "gender": "F", "berth_pref": ""}], "together": True, "only_confirmed": False}
    body.update(kw)
    return call("POST", API + "/v1/bookings", body, token, {"Idempotency-Key": key})


def pay(token, booking_id, outcome="success"):
    """Start payment, submit the gateway's checkout form, and follow its signed redirect back."""
    s, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=token)
    if s != 200:
        return s, r
    order = r["checkout_url"].rsplit("/", 1)[1]
    s, body, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": outcome})
    if s != 303:
        return s, {"note": "no redirect", "order": order}
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    return call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, token)[:2]


def set_mode(mode):
    call("POST", GW + "/admin/mode", {"mode": mode})


IST = dt.timezone(dt.timedelta(hours=5, minutes=30))
today = dt.datetime.now(IST).date()
# A random weekday 3–6 weeks out keeps repeated runs from colliding on the same berths.
DAY = today + dt.timedelta(days=random.randint(21, 45))
set_mode("normal")
a, b, c = passenger(), passenger(), passenger()

# ── Seat map and the same-berth race ─────────────────────────────────────
s, m, _ = call("GET", f"{API}/v1/seatmap?" + urllib.parse.urlencode(
    {"train": "12623", "from": "MAS", "to": "ERN", "date": str(DAY), "class": "SL", "quota": "GN"}), token=a)
free = [(co["coach"], st["berth"]) for co in m["coaches"] for st in co["seats"] if st["status"] == "free"] if s == 200 else []
booked = sum(st["status"] == "booked" for co in m.get("coaches", []) for st in co["seats"])
ok(s == 200 and free, f"seat map for 12623 SL on {DAY}: {len(free)} General berths free, {booked} already booked by background demand")
coach, berth = random.choice(free)

results = []
barrier = threading.Barrier(2)


def racer(tok, name):
    barrier.wait()
    results.append(hold(tok, passengers=[{"name": name, "age": 30, "gender": "M", "berth_pref": "", "coach": coach, "berth": berth}]))


threads = [threading.Thread(target=racer, args=(a, "Racer A")), threading.Thread(target=racer, args=(b, "Racer B"))]
[t.start() for t in threads]
[t.join() for t in threads]
codes = sorted(r[0] for r in results)
ok(codes == [201, 409], f"two users race for {coach}/{berth}: HTTP {codes} (exactly one wins)")
winner = next(r[1] for r in results if r[0] == 201)
loser_tok = b if winner["passengers"][0]["name"] == "Racer A" else a
winner_tok = a if loser_tok is b else b

# ── Segment sharing: the loser takes the same berth for SA→ERN ────────────
sa_day = DAY + dt.timedelta(days=1)  # the Trivandrum Mail reaches Salem after midnight
s, r, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": "SA", "date": str(DAY), "class": "SL", "quota": "GN",
               "passengers": [{"name": "Overlap", "age": 30, "gender": "M", "berth_pref": "", "coach": coach, "berth": berth}],
               "together": False, "only_confirmed": False}, c, {"Idempotency-Key": str(uuid.uuid4())})
ok(s == 409, "MAS→SA on the same berth overlaps the winner's MAS→ERN: refused")

# ── Payment: success, ticket, PNR ────────────────────────────────────────
s, conf = pay(winner_tok, winner["id"])
ok(s == 200 and conf["status"] == "CONFIRMED" and len(conf.get("pnr", "")) == 10,
   f"paid through the gateway: {conf.get('status')} PNR {conf.get('pnr')} berth {conf['passengers'][0].get('coach')}/{conf['passengers'][0].get('berth')}")
s, t, _ = call("GET", f"{API}/v1/bookings/{winner['id']}/ticket", token=winner_tok)
ok(s == 200 and t["qr"].startswith("YX1."), "e-ticket issued with a signed QR token")
s, v, _ = call("POST", API + "/v1/tickets/verify", {"token": t["qr"]})
ok(v["valid"] and v["ticket"]["p"] == conf["pnr"], "QR signature verifies and carries the PNR")
head, body, sig = t["qr"].split(".")
forged = json.loads(urllib.parse.unquote(__import__("base64").urlsafe_b64decode(body + "==").decode()))
forged["c"] = "1A"
fbody = __import__("base64").urlsafe_b64encode(json.dumps(forged).encode()).decode().rstrip("=")
s, v, _ = call("POST", API + "/v1/tickets/verify", {"token": f"{head}.{fbody}.{sig}"})
ok(v["valid"] is False, "a ticket edited to upgrade the class fails verification")
s, p, _ = call("GET", f"{API}/v1/pnr/{conf['pnr']}")
ok(s == 200 and "." in p["passengers"][0]["name"] and p["passengers"][0]["status"] == "CNF", f"public PNR status hides names: {p['passengers'][0]['name']}")

# Segment sharing on a fresh berth: one user rides MAS→SA, another SA→ERN, same berth.
coach2, berth2 = next(fb for fb in free if fb != (coach, berth))
leg = lambda frm, to, day, name: {"train": "12623", "from": frm, "to": to, "date": str(day), "class": "SL", "quota": "GN",
                                  "passengers": [{"name": name, "age": 30, "gender": "M", "berth_pref": "", "coach": coach2, "berth": berth2}],
                                  "together": False, "only_confirmed": False}
s1, _, _ = call("POST", API + "/v1/bookings", leg("MAS", "SA", DAY, "First Leg"), c, {"Idempotency-Key": str(uuid.uuid4())})
s2, _, _ = call("POST", API + "/v1/bookings", leg("SA", "ERN", sa_day, "Second Leg"), loser_tok, {"Idempotency-Key": str(uuid.uuid4())})
s3, _, _ = call("POST", API + "/v1/bookings", leg("ED", "PGT", sa_day, "Middle"), winner_tok, {"Idempotency-Key": str(uuid.uuid4())})
ok((s1, s2, s3) == (201, 201, 409), f"segment sharing on {coach2}/{berth2}: MAS→SA {s1}, SA→ERN {s2} (both fit), ED→PGT {s3} (overlaps)")

# ── Declined payment, then retry ─────────────────────────────────────────
s, h, _ = hold(c)
s, r = pay(c, h["id"], outcome="failure")
ok(s == 200 and r["status"] == "HELD" and r["payment"]["status"] == "FAILED", "declined payment: booking stays held for a retry")
s, r = pay(c, h["id"])
ok(s == 200 and r["status"] == "CONFIRMED", "retry succeeds and confirms")

# ── Late payment: the bank finishes after the hold expired ───────────────
s, h2, _ = hold(c, passengers=[{"name": "Late Webhook", "age": 41, "gender": "F", "berth_pref": ""}])
s, r, _ = call("POST", f"{API}/v1/bookings/{h2['id']}/pay", token=c)
order = r["checkout_url"].rsplit("/", 1)[1]
call("POST", f"{API}/v1/dev/bookings/{h2['id']}/expire")
s, body, hdr = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "card", "outcome": "success"})
q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(hdr["Location"]).query))
s, r, _ = call("POST", f"{API}/v1/bookings/{h2['id']}/verify", {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q["signature"]}, c)
ok(s == 200 and r["status"] == "CONFIRMED", "payment after the hold expired: berths re-allocated and confirmed (no money kept without a ticket)")

# ── Duplicate webhooks ────────────────────────────────────────────────────
set_mode("duplicate")
s, h, _ = hold(b, passengers=[{"name": "Triple Webhook", "age": 28, "gender": "F", "berth_pref": ""}])
s, r = pay(b, h["id"])
pnr = r.get("pnr")
time.sleep(4)  # let the gateway deliver all three copies
s, r, _ = call("GET", f"{API}/v1/bookings/{h['id']}", token=b)
ok(r["status"] == "CONFIRMED" and r["pnr"] == pnr and r["payment"]["status"] == "CAPTURED",
   "duplicate webhooks: confirmed exactly once, PNR unchanged")
set_mode("normal")

# ── Idempotency, release, limits, validation ─────────────────────────────
key = str(uuid.uuid4())
s1, r1, _ = hold(a, key=key)
s2, r2, _ = hold(a, key=key)
ok(s1 == 201 and s2 == 201 and r1.get("id") == r2.get("id"), f"same Idempotency-Key twice: one booking {'' if s1 == 201 else r1}")
if "id" in r1:
    call("POST", f"{API}/v1/bookings/{r1['id']}/release", token=a)
    s, r, _ = call("GET", f"{API}/v1/bookings/{r1['id']}", token=a)
    ok(r["status"] == "EXPIRED", "released hold goes back on sale")
s, r, _ = hold(a, quota="SS", passengers=[{"name": "Too Young", "age": 30, "gender": "M", "berth_pref": ""}])
ok(s == 400, f"senior quota rejects a 30-year-old: {r.get('message')}")
s, r, _ = hold(a, passengers=[{"name": "Seven", "age": 30, "gender": "M", "berth_pref": ""}] * 7)
ok(s == 400, "seven passengers rejected")
s, r, _ = call("GET", f"{API}/v1/bookings/{winner['id']}", token=c)
ok(s == 404, "another user's booking is not visible")
s, r, _ = call("GET", f"{API}/v1/bookings", token=winner_tok)
ok(s == 200 and any(x["id"] == winner["id"] for x in r), "My trips lists the confirmed booking")

sys.exit(1 if FAILED else 0)

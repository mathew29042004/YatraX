#!/usr/bin/env python3
"""Phase 6 smoke test: the TTE app's server side. Duties, the offline bundle (chart,
coach layouts, fare table, ticket key), and syncing on-board operations: boarding,
the two-station no-show rule, releasing a berth to the next RAC passenger, idempotent
replays, excess-fare tickets with receipts, complaints and chart hand-off.
Run with the stack up:  python scripts/smoke_phase6.py"""
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


def passenger():
    phone = random.choice("6789") + "".join(random.choices("0123456789", k=9))
    _, r, _ = call("POST", API + "/v1/auth/otp/request", {"phone": phone})
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Six"})
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


def op(kind, **payload):
    return {"op_id": str(uuid.uuid4()), "kind": kind, "at": dt.datetime.now(dt.timezone.utc).isoformat(), "payload": payload}


TTE = staff("TTE00001")
today = dt.datetime.now(IST).date()

# ── 1. Duties and access ──
s, d, _ = call("GET", API + "/v1/tte/duties", token=TTE)
ok(s == 200 and d["station"] and isinstance(d["duties"], list), f"duties at {d.get('station_name')}: {len(d.get('duties', []))} trains soon")
s, _, _ = call("GET", API + "/v1/tte/duties", token=passenger())
ok(s == 403, "passengers can't open TTE duties")

# ── 2. A real passenger with a QR ticket on the Trivandrum Mail ──
# A day on which some class has RAC passengers (so a released berth has someone to go to)
# and another still has berths for the passenger we scan.
D, CLS = None, None
for days in random.sample(range(5, 45), 40):
    d0 = today + dt.timedelta(days=days)
    _, r, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={d0}")
    mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
    codes = {c["code"]: c["availability"]["code"] for c in (mail or {}).get("classes", [])}
    cls = next((c for c, st in codes.items() if st == "AVL"), None)
    if cls and "RAC" in codes.values():
        D, CLS = d0, cls
        break
p = passenger()
s, b, _ = call("POST", API + "/v1/bookings", {"train": "12623", "from": "MAS", "to": "ERN", "date": str(D), "class": CLS, "quota": "GN",
                                                "passengers": [{"name": "Scan Me", "age": 33, "gender": "F", "berth_pref": ""}], "together": True,
                                                "only_confirmed": False}, p, headers={"Idempotency-Key": str(uuid.uuid4())})
b = pay(p, b["id"])
_, t, _ = call("GET", f"{API}/v1/bookings/{b['id']}/ticket", token=p)
ok(b["status"] == "CONFIRMED" and t["qr"].startswith("YX1."), f"passenger booked {CLS} PNR {b['pnr']} with a signed QR ticket")

# ── 3. The offline bundle ──
s, B, _ = call("GET", f"{API}/v1/tte/runs/12623/{D}", token=TTE)
n = len(B["stops"])
_, pk, _ = call("GET", API + "/v1/tickets/public-key")
ok(s == 200 and len(B["passengers"]) > 500 and len(B["coaches"]) >= 10,
   f"bundle: {len(B['passengers'])} passengers, {len(B['coaches'])} coaches, {n} stops, chart {B['chart'] and B['chart']['version']}")
ok(all(len(v) == n * (n - 1) // 2 and all(x > 0 for x in v) for v in B["fares"].values()), f"fare table for every stop pair in {sorted(B['fares'])}")
ok(B["public_key"] == (pk.get("public_key") if isinstance(pk, dict) else None) or len(B["public_key"]) == 44, "bundle carries the ticket-verification key")
me = next((x for x in B["passengers"] if x["pnr"] == b["pnr"]), None)
ok(me is not None and me["status"] == "CNF", "the passenger is on the chart")
s, _, _ = call("GET", f"{API}/v1/tte/runs/12623/not-a-date", token=TTE)
ok(s == 400, "bad origin date: 400")

# ── 4. Sync: board, the no-show rule, a berth for the next RAC passenger ──
stops = [x["code"] for x in B["stops"]]
k = 2  # the train has passed Katpadi and reached Salem
P = B["passengers"]


def held_beyond(px):
    return any(q["id"] != px["id"] and q.get("coach") == px["coach"] and q.get("berth") == px["berth"]
               and q["status"] == "CNF" and q["to_idx"] > k for q in P)


# A class with RAC passengers waiting and a not-yet-boarded passenger whose berth is free to the end.
rac, cand = [], None
for c in sorted({x["class"] for x in P}, key=lambda c: c != "SL"):
    rac = sorted([x for x in P if x["status"] == "RAC" and x["class"] == c and x["to_idx"] > k], key=lambda x: x.get("rac_no", 0))
    cand = next((x for x in P if x["class"] == c and x["status"] == "CNF" and x["from_idx"] == 0 and not x.get("boarded_at")
                 and x["pnr"] != b["pnr"] and x["to_idx"] > k and not held_beyond(x)), None)
    if rac and cand:
        break
ok(rac and cand, f"found a no-show candidate in {cand and cand['class']} with {len(rac)} RAC passengers waiting")
first = [op("board", passenger_id=me["id"], station="MAS"),
         op("no_show", passenger_id=cand["id"], station=stops[1])]
noshow = op("no_show", passenger_id=cand["id"], station=stops[k], assign_to=rac[0]["id"] if rac else "")
s, r, _ = call("POST", f"{API}/v1/tte/runs/12623/{D}/sync", {"ops": first + [noshow]}, TTE)
res = {x["op_id"]: x for x in r["results"]}
ok(res[first[0]["op_id"]]["status"] == "applied", "scanned passenger marked boarded")
ok(res[first[1]["op_id"]]["status"] == "rejected", f"no-show one station out refused: {res[first[1]['op_id']]['message'][:70]}")
ns = res[noshow["op_id"]]
moved = (ns.get("data") or {}).get("moved")
ok(ns["status"] == "applied" and moved is not None, f"no-show at {stops[k]}: {ns['message']}")
s, r2, _ = call("POST", f"{API}/v1/tte/runs/12623/{D}/sync", {"ops": [noshow]}, TTE)
ok(r2["results"][0]["status"] == "duplicate", "replaying the same operation is harmless")
after = {x["id"]: x for x in r["bundle"]["passengers"]}
ok(after[cand["id"]]["status"] == "NOSHOW" and after[cand["id"]]["no_show_at"] == stops[k], "the chart shows the no-show")
if moved:
    m = after[moved["passenger_id"]]
    ok(m["status"] == "CNF" and (m["coach"], m["berth"]) == (cand["coach"], cand["berth"]),
       f"RAC passenger now has {m['coach']}/{m['berth']}")
ok(after[me["id"]].get("boarded_at"), "boarding is on the chart")

# ── 5. Excess fare, complaint, hand-off ──
fine = op("fine", name="No Ticket", age=40, gender="M", **{"from": stops[k]}, to="TVC", **{"class": "SL"}, reason="ticketless")
wrong = op("fine", name="Wrong Coach", age=31, gender="F", **{"from": stops[k]}, to=stops[k + 3], **{"class": "3A"}, reason="wrong_class", paid_class="SL")
comp = op("complaint", category="medical", description="Passenger in S4 feeling unwell", coach="S4")
hand = op("handoff", to_employee_id="TTE00002", station="ED", note="1 no-show released")
s, r, _ = call("POST", f"{API}/v1/tte/runs/12623/{D}/sync", {"ops": [fine, wrong, comp, hand]}, TTE)
res = {x["op_id"]: x for x in r["results"]}
f1, f2 = res[fine["op_id"]], res[wrong["op_id"]]
ok(f1["status"] == "applied" and f1["data"]["total"] == f1["data"]["fare"] + 250, f"ticketless: {f1['message']}")
ok(f2["status"] == "applied" and f2["data"]["fare"] > 0, f"wrong class (SL ticket in 3A): {f2['message']}")
ok(res[comp["op_id"]]["status"] == "applied", res[comp["op_id"]]["message"])
ok(res[hand["op_id"]]["status"] == "applied", res[hand["op_id"]]["message"])
bad = op("handoff", to_employee_id="TTE00001", station="ED")
s, r3, _ = call("POST", f"{API}/v1/tte/runs/12623/{D}/sync", {"ops": [bad]}, TTE)
ok(r3["results"][0]["status"] == "rejected", "can't hand the chart to yourself")
ok(len(r["bundle"]["fines"]) >= 2 and len(r["bundle"]["handoffs"]) >= 1, "fines and hand-offs travel with the chart")

# ── 6. Invariants ──
_, integ, _ = call("GET", API + "/v1/admin/integrity", token=staff("ADM00001"))
ok(integ["overlapping_allocations"] == 0 and integ["trial_balance"] == 0,
   f"no berth sold twice after on-board moves; ledger balances (TTE cash in, fares and penalties out): {integ['trial_balance']}")

sys.exit(1 if FAILED else 0)

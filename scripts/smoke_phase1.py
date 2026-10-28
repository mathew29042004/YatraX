#!/usr/bin/env python3
"""Phase 1 smoke test: search, fares, availability calendar and coach layouts.
Run with the stack up:  python scripts/smoke_phase1.py"""
import datetime as dt
import json
import os
import sys
import urllib.error
import urllib.request

B = os.environ.get("API_URL", "http://localhost:8080")
FAILED = []
sys.stdout.reconfigure(encoding="utf-8")  # Windows consoles default to cp1252


def get(path):
    try:
        with urllib.request.urlopen(B + path) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null")


def ok(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        FAILED.append(msg)


IST = dt.timezone(dt.timedelta(hours=5, minutes=30))
today = dt.datetime.now(IST).date()
# A Monday 1–2 weeks out: every MAS→ERN daily train runs, and it is inside the booking window.
monday = today + dt.timedelta(days=7 + (7 - today.weekday()) % 7)

# The plan's Phase 1 demo: search Chennai → Ernakulam and see the real trains, fares and classes.
# (The real Trivandrum Mail calls at Ernakulam Town, ERN.)
s, r = get(f"/v1/search?from=MAS&to=ERN&date={monday}")
trains = r.get("trains", []) if s == 200 else []
ok(s == 200 and len(trains) >= 3, f"MAS→ERN on {monday}: {len(trains)} trains")
for t in trains:
    print(f"     {t['number']} {t['name'][:28]:28} {t['departure'][11:16]}→{t['arrival'][11:16]} "
          + " ".join(f"{c['code']}:₹{c['fare']['total']}/{c['availability']['label']}" for c in t["classes"]))
ok(all(t["departure"] <= u["departure"] for t, u in zip(trains, trains[1:])), "results sorted by departure")
ok(any(t["number"] == "12623" for t in trains), "Trivandrum Mail (12623) is in the results")
ok(all(c.get("fare", {}).get("total", 0) > 0 for t in trains for c in t["classes"]), "every class has a fare")
codes = {c["availability"]["code"] for t in trains for c in t["classes"]}
ok(codes <= {"AVL", "RAC", "WL", "REGRET"}, f"availability statuses look bookable: {sorted(codes)}")

mail = next((t for t in trains if t["number"] == "12623"), None)
if mail:
    sl = next(c for c in mail["classes"] if c["code"] == "SL")["fare"]
    ok(370 <= sl["total"] <= 500, f"Sleeper fare MAS→ERN ₹{sl['total']} (real Indian Railways ≈ ₹415)")
    a3 = next(c for c in mail["classes"] if c["code"] == "3A")["fare"]
    ok(a3["gst"] > 0 and sl["gst"] == 0, "GST on AC classes only")

s, r = get(f"/v1/search?from=MAS&to=TJ&date={monday}")
ok(s == 200 and len(r["trains"]) == 0, "no direct Chennai Central → Thanjavur trains…")
s, r = get(f"/v1/search?from=MAS&to=TJ&date={monday}&nearby=1")
ok(s == 200 and len(r["trains"]) > 0 and {t["from"]["code"] for t in r["trains"]} <= {"MS", "TBM"},
   f"…but nearby stations find {len(r['trains'])} from Chennai Egmore / Tambaram")

s, r = get(f"/v1/search?from=MAS&to=MYS&date={monday + dt.timedelta(days=2)}&flex=3")
wk = next((t for t in r.get("trains", []) if t["number"] == "22682"), None)
ok(s == 200 and len(r.get("dates", [])) == 7 and wk is not None, "flexible dates: 7-day grid includes the weekly Chennai – Mysore SF")
if wk:
    running = sum(d["availability"]["code"] != "NOT_RUNNING" for d in wk["classes"][0]["by_date"])
    ok(running == 1, f"a weekly train runs on {running} of 7 days")

s, r = get(f"/v1/search?from=MAS&to=ERN&date={monday}&quota=TQ")
ok(s == 200 and all(c["availability"]["code"] in ("TATKAL_NOT_OPEN", "NO_QUOTA") for t in r["trains"] for c in t["classes"]),
   "Tatkal not open a week ahead")
far = today + dt.timedelta(days=90)
s, r = get(f"/v1/search?from=MAS&to=ERN&date={far}")
ok(s == 200 and all(c["availability"]["code"] in ("NOT_OPEN", "NOT_RUNNING") for t in r["trains"] for c in t["classes"]),
   "90 days out: booking not open yet")

s, r = get(f"/v1/availability?train=12623&from=MAS&to=ERN&class=SL&start={today}&days=30")
ok(s == 200 and len(r["days"]) == 30 and r["fare"]["total"] > 0, "30-day availability calendar")
s, r = get("/v1/fare?train=12623&from=MAS&to=ERN&class=SL&quota=TQ")
ok(s == 200 and r["tatkal"] >= 100, f"Tatkal fare adds ₹{r.get('tatkal')} charge")

s, r = get("/v1/trains/12623/coaches/S1")
ok(s == 200 and len(r["seats"]) == 72 and [x["type"] for x in r["seats"][:8]] == ["LB", "MB", "UB", "LB", "MB", "UB", "SL", "SU"],
   "S1 berth map: 72 berths, LB MB UB LB MB UB SL SU")

for path, code in [(f"/v1/search?from=MAS&to=ERN&date={today - dt.timedelta(days=1)}", "past_date"),
                   ("/v1/search?from=MAS&to=MAS", "same_station"),
                   ("/v1/search?from=MAS&to=XYZ", "bad_station"),
                   ("/v1/search?from=MAS&to=ERN&quota=ZZ", "bad_quota"),
                   ("/v1/fare?train=12623&from=ERN&to=MAS&class=SL", "bad_journey")]:
    s, r = get(path)
    ok(s == 400 and r["error"] == code, f"rejects {code}")

sys.exit(1 if FAILED else 0)

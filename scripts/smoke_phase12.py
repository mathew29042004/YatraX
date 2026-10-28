#!/usr/bin/env python3
"""Phase 12 smoke test: chaos mode and the digital twin. Every chaos drill must recover on its
own within its deadline — a lost-callback payment gateway, a database failover, the event bus
going down, a train breaking down — and the replay of a past day must tell that day's story.
Takes about four minutes (the gateway drill waits for the reconciler).
Run with the stack up:  python scripts/smoke_phase12.py"""
import datetime as dt
import json
import os
import sys
import time
import urllib.error
import urllib.request

API = os.environ.get("API_URL", "http://localhost:8080")
FAILED = []
sys.stdout.reconfigure(encoding="utf-8")  # Windows consoles default to cp1252
IST = dt.timezone(dt.timedelta(hours=5, minutes=30))


def call(method, url, body=None, token=None):
    data, hdrs = None, {}
    if body is not None:
        data, hdrs["Content-Type"] = json.dumps(body).encode(), "application/json"
    if token:
        hdrs["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, raw


def ok(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        FAILED.append(msg)


def staff(emp):
    _, s = call("POST", API + "/v1/auth/login", {"identifier": emp, "password": "yatrax123"})
    return s["access_token"]


CTL, ADM, SM = staff("CTRL00001"), staff("ADM00001"), staff("SM00001")
call("POST", API + "/v1/admin/sim/clock", {"reset": True}, CTL)

# ── 1. The drills on offer, and who may run them ──
s, drills = call("GET", API + "/v1/chaos/drills", token=CTL)
kinds = [d["kind"] for d in drills] if s == 200 else []
ok(kinds == ["gateway_outage", "db_failover", "event_bus_down", "train_breakdown"], f"{len(kinds)} drills: {', '.join(kinds)}")
s, _ = call("POST", API + "/v1/chaos/runs", {"kind": "db_failover"}, SM)
ok(s == 403, "station masters can't break things")
s, _ = call("POST", API + "/v1/chaos/runs", {"kind": "meteor_strike"}, CTL)
ok(s == 400, "unknown drills are refused")


def drill(kind, token=CTL):
    # Wait out any drill already running (someone may be demoing).
    for _ in range(60):
        s, run = call("POST", API + "/v1/chaos/runs", {"kind": kind}, token)
        if s != 409:
            break
        time.sleep(5)
    if s != 202:
        return s, run
    deadline = time.time() + next(d["deadline_sec"] for d in drills if d["kind"] == kind) + 90
    while time.time() < deadline:
        time.sleep(2)
        st, cur = call("GET", f"{API}/v1/chaos/runs/{run['id']}", token=token)
        if st != 200:
            continue  # the database drill makes the API stumble for a moment: that's the point
        run = cur
        if run["status"] != "running":
            break
    return 202, run


def show(run):
    return " → ".join(s["text"].split(" — ")[0][:70] for s in run["steps"] if s["kind"] in ("fault", "recovered", "failed"))


# ── 2. Every drill recovers on its own ──
s, run = drill("event_bus_down")
ok(s == 202 and run["status"] == "recovered" and run["metrics"].get("backlog", 0) >= 40,
   f"event bus down: {run['metrics'].get('backlog')} events queued, drained {run.get('recovered_in', 0):.1f} s after it came back — {show(run)}")
s, run = drill("db_failover")
ok(s == 202 and run["status"] == "recovered" and run["recovered_in"] < 30 and run["metrics"].get("connections_cut", 0) >= 1,
   f"database failover: {run['metrics'].get('connections_cut')} connections cut, back in {run.get('recovered_in', 0):.1f} s, "
   f"{run['metrics'].get('failed_queries')} queries failed meanwhile")
_, ready = call("GET", API + "/readyz")
ok(ready == {"postgres": "ok", "redis": "ok"}, "the API reports ready after the failover")
s, run = drill("train_breakdown")
ok(s == 202 and run["status"] == "recovered", f"train breakdown on {run['metrics'].get('train')}: {show(run)}")
s, run = drill("gateway_outage")
ok(s == 202 and run["status"] == "recovered" and run["metrics"].get("payments") == 3 and run["metrics"].get("trial_balance") == 0,
   f"gateway lost 3 callbacks; reconciled in {run.get('recovered_in', 0):.0f} s; ledger trial balance ₹{run['metrics'].get('trial_balance')}")
s, runs = call("GET", API + "/v1/chaos/runs", token=CTL)
ok(s == 200 and runs[0]["id"] == run["id"] and all(r["steps"] for r in runs), f"{len(runs)} drills in the log, newest first, each with its timeline")
_, integ = call("GET", API + "/v1/admin/integrity", token=ADM)
ok(integ["trial_balance"] == 0, "the ledger still balances after all four drills")

# ── 3. The digital twin: replay a past day ──
day = (dt.datetime.now(IST) - dt.timedelta(days=4)).date()
s, rep = call("GET", f"{API}/v1/control/replay?date={day}", token=CTL)
ok(s == 200 and rep["runs"] > 500 and 0 < rep["on_time"] < 1,
   f"replay {day}: {rep['runs']} runs, {rep['on_time'] * 100:.1f}% on time, {rep['late_60']} over an hour late, {len(rep['events'])} events")
times = [e["at"] for e in rep["events"]]
ok(times == sorted(times) and all(e["kind"] in ("incident", "breakdown", "disruption", "platform", "late") for e in rep["events"]),
   "the day's events come in time order")
s, rep2 = call("GET", f"{API}/v1/control/replay?date={day}", token=CTL)
ok(rep2["runs"] == rep["runs"] and rep2["on_time"] == rep["on_time"], "replaying the same day gives the same result (the model is deterministic)")
s, _ = call("GET", f"{API}/v1/control/replay?date=yesterday", token=CTL)
ok(s == 400, "a bad date is refused")
s, _ = call("GET", f"{API}/v1/control/replay?date={day}", token=SM)
ok(s == 403, "replay is for the control room")

sys.exit(1 if FAILED else 0)

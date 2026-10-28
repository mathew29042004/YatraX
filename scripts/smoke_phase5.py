#!/usr/bin/env python3
"""Phase 5 smoke test: the simulation clock, live train status, the live map and station
board, controller-injected delays, the WebSocket stream, and live alerts reaching a
passenger whose train is running late.
Run with the stack up:  python scripts/smoke_phase5.py"""
import base64
import datetime as dt
import json
import os
import random
import socket
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
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": "Smoke Five"})
    return s["access_token"]


def staff(emp):
    _, s, _ = call("POST", API + "/v1/auth/login", {"identifier": emp, "password": "yatrax123"})
    return s["access_token"]


def pay(tok, booking_id):
    s, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=tok)
    order = r["checkout_url"].rsplit("/", 1)[1]
    s, _, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": "success"})
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    _, b, _ = call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                   {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, tok)
    return b


def clock(ctl, **change):
    return call("POST", API + "/v1/admin/sim/clock", change, ctl)


def wait_for(tok, pred, timeout=60):
    """Poll the passenger's notifications until one matches."""
    end = time.time() + timeout
    while time.time() < end:
        _, n, _ = call("GET", API + "/v1/me/notifications", token=tok)
        hit = next((x for x in n["items"] if pred(x)), None)
        if hit:
            return hit
        time.sleep(1)
    return None


def ws_first(path):
    u = urllib.parse.urlparse(API)
    s = socket.create_connection((u.hostname, u.port or 80), timeout=8)
    s.sendall((f"GET {path} HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
               f"Sec-WebSocket-Key: {base64.b64encode(os.urandom(16)).decode()}\r\nSec-WebSocket-Version: 13\r\n"
               f"Origin: http://localhost:3000\r\n\r\n").encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        buf += s.recv(65536)
    head, buf = buf.split(b"\r\n\r\n", 1)
    if b" 101 " not in head.split(b"\r\n")[0]:
        return None

    def need(n):
        nonlocal buf
        while len(buf) < n:
            buf += s.recv(65536)
        out, buf = buf[:n], buf[n:]
        return out

    _, b1 = need(2)
    n = b1 & 0x7F
    if n == 126:
        n = int.from_bytes(need(2), "big")
    elif n == 127:
        n = int.from_bytes(need(8), "big")
    msg = json.loads(need(n))
    s.close()
    return msg


CTL = staff("CTRL00001")
today = dt.datetime.now(IST).date()
# A day and class on which the Trivandrum Mail still has berths (festival weeks sell out).
D, CLS = None, None
for days in random.sample(range(5, 45), 40):
    d = today + dt.timedelta(days=days)
    _, r, _ = call("GET", f"{API}/v1/search?from=MAS&to=ERN&date={d}")
    mail = next((t for t in r.get("trains", []) if t["number"] == "12623"), None)
    cls = next((c["code"] for c in (mail or {}).get("classes", []) if c["availability"]["code"] == "AVL"), None)
    if cls:
        D, CLS = d, cls
        break
try:
    # ── 1. Clock ──
    s, c, _ = clock(CTL, reset=True)
    ok(s == 200 and c["speed"] == 1 and not c["paused"], "controller resets the clock to real time")
    s, _, _ = call("POST", API + "/v1/admin/sim/clock", {"speed": 60}, passenger())
    ok(s == 403, "passengers can't touch the clock")
    s, _, _ = clock(CTL, speed=5000)
    ok(s == 400, "speed above a day a minute is refused")

    # ── 2. A passenger on the Trivandrum Mail ──
    p = passenger()
    body = {"train": "12623", "from": "MAS", "to": "ERN", "date": str(D), "class": CLS, "quota": "GN",
            "passengers": [{"name": "Live Traveller", "age": 41, "gender": "F", "berth_pref": ""}], "together": True, "only_confirmed": False}
    s, b, _ = call("POST", API + "/v1/bookings", body, p, headers={"Idempotency-Key": str(uuid.uuid4())})
    b = pay(p, b["id"]) if s == 201 else b
    ok(s == 201 and b.get("status") == "CONFIRMED" and b.get("origin_date") == str(D),
       f"12623 MAS→ERN {CLS} booked for {D}: {b.get('status')} {b.get('passengers', [{}])[0].get('coach')}/{b.get('passengers', [{}])[0].get('berth')}")

    s, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
    ok(s == 200 and st["state"] == "scheduled" and len(st["stops"]) >= 10, f"live status before the day: {st.get('message')}")
    s, _, _ = call("GET", f"{API}/v1/live/trains/99999")
    ok(s == 404, "unknown train: 404")

    # ── 3. Jump to 40 minutes before departure; control holds the train at Chennai ──
    dep = dt.datetime.combine(D, dt.time(19, 5), IST)
    s, c, _ = clock(CTL, jump_to=dep.isoformat(), speed=60)
    ok(s == 200 and abs((dt.datetime.fromisoformat(c["sim_now"].replace("Z", "+00:00")) - dep).total_seconds()) < 120,
       f"clock jumped to {D} 19:05 at 60×")
    time.sleep(3)  # let the watcher see the run before anything changes
    s, held, _ = call("POST", API + "/v1/admin/live/incidents",
                      {"train": "12623", "origin_date": str(D), "station": "MAS", "delay_min": 50, "reason": "Late arrival of the rake"}, CTL)
    ok(s == 201 and held["delay_min"] >= 50, f"control holds 12623 at Chennai: {held.get('message')}")
    n = wait_for(p, lambda x: x["kind"] == "delay", 30)
    ok(n is not None and "late" in n["title"], f"passenger told about the delay: {n and n['title']} — {n and n['body'][:60]}")

    # ── 4. Speed up: the boarding reminder, then the train leaves ──
    clock(CTL, speed=600)
    n = wait_for(p, lambda x: x["kind"] == "live" and "leaves" in x["title"], 30)
    ok(n is not None, f"boarding reminder: {n and n['title']}")
    s, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
    deadline = time.time() + 30
    while st["state"] == "scheduled" and time.time() < deadline:
        time.sleep(1)
        s, st, _ = call("GET", f"{API}/v1/live/trains/12623?date={D}")
    ok(st["state"] in ("running", "at_station") and st["delay_min"] >= 30, f"on its way: {st['message']}")

    msg = ws_first(f"/v1/live/ws?train=12623&date={D}")
    ok(msg is not None and msg["kind"] == "status" and msg["status"]["train"] == "12623" and msg["clock"]["speed"] == 600,
       "WebSocket streams the run's live status with the clock")
    msg = ws_first("/v1/live/ws?map=1&zone=SR")
    ok(msg is not None and msg["kind"] == "map" and all(t["zone"] == "SR" for t in msg["trains"]),
       f"WebSocket map stream filtered to Southern Railway: {len(msg['trains']) if msg else 0} trains")

    # ── 5. Near Ernakulam: the arrival alert ──
    ern = next(v for v in st["stops"] if v["station"] == "ERN")
    eta = dt.datetime.fromisoformat(ern["exp_arr"].replace("Z", "+00:00"))
    clock(CTL, jump_to=(eta - dt.timedelta(minutes=45)).isoformat(), speed=120)
    n = wait_for(p, lambda x: x["kind"] == "live" and "Arriving" in x["title"], 40)
    ok(n is not None, f"arrival alert: {n and n['title']}")

    # ── 6. The network view ──
    s, m, _ = call("GET", API + "/v1/live/map")
    late = sum(1 for t in m["trains"] if t["delay_min"] >= 15)
    ok(s == 200 and len(m["trains"]) > 300, f"live map: {len(m['trains'])} trains running, {late} late by 15+ min")
    s, m2, _ = call("GET", API + "/v1/live/map?late=1")
    ok(all(t["delay_min"] >= 15 for t in m2["trains"]), "late filter keeps only late trains")
    s, board, _ = call("GET", API + "/v1/live/stations/ERS?hours=6")
    ok(s == 200 and board["trains"] and all(r["platform"] >= 1 for r in board["trains"]),
       f"Ernakulam Jn board: {len(board['trains'])} trains, e.g. {board['trains'][0]['train']} {board['trains'][0]['state']}")
    s, incs, _ = call("GET", API + "/v1/admin/live/incidents", token=CTL)
    ok(s == 200 and incs and incs[0]["train"] == "12623" and incs[0]["station"] == "MAS", "incident log lists the hold")
finally:
    clock(CTL, reset=True)
s, c, _ = call("GET", API + "/v1/sim/clock")
ok(c["speed"] == 1 and abs((dt.datetime.fromisoformat(c["sim_now"].replace("Z", "+00:00")) -
                             dt.datetime.now(dt.timezone.utc)).total_seconds()) < 5, "clock back to real time")

sys.exit(1 if FAILED else 0)

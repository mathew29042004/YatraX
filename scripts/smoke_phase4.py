#!/usr/bin/env python3
"""Phase 4 smoke test: the Tatkal waiting room. Joining before opening, drafts, the bot
detector (block, proof-of-work challenge), the live WebSocket, the fair shuffle at
opening, late joiners, booking and paying from the room, the opening-rush lockout of
direct Tatkal bookings, the staff monitor and the integrity check.
Run with the stack up:  python scripts/smoke_phase4.py"""
import base64
import datetime as dt
import hashlib
import json
import os
import random
import socket
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

API = os.environ.get("API_URL", "http://localhost:8080")
GW = os.environ.get("GATEWAY_URL", "http://localhost:8081")
BROWSER = "Mozilla/5.0 (Linux; Android 14) Chrome/130 Mobile"
FAILED = []
sys.stdout.reconfigure(encoding="utf-8")  # Windows consoles default to cp1252


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def call(method, url, body=None, token=None, form=None, headers=None, ua=BROWSER):
    data, hdrs = None, {"User-Agent": ua, **(headers or {})}
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


def passenger(name):
    phone = random.choice("6789") + "".join(random.choices("0123456789", k=9))
    _, r, _ = call("POST", API + "/v1/auth/otp/request", {"phone": phone})
    _, s, _ = call("POST", API + "/v1/auth/otp/verify", {"phone": phone, "otp": r["dev_otp"], "full_name": name})
    return s["access_token"]


def admin():
    _, s, _ = call("POST", API + "/v1/auth/login", {"identifier": "ADM00001", "password": "yatrax123"})
    return s["access_token"]


def human_signals(device):
    return {"device": device, "form_ms": random.randint(9000, 30000),
            "key_intervals": [random.randint(80, 360) for _ in range(12)], "pointer_moves": random.randint(30, 200)}


def draft(date, quota="TQ"):
    return {"train": "12304", "from": "NDLS", "to": "HWH", "date": str(date), "class": "SL", "quota": quota,
            "passengers": [{"name": "Tatkal Traveller", "age": 34, "gender": "F", "berth_pref": ""}],
            "together": False, "only_confirmed": False}


def room_call(method, room, part, tok, body=None, **kw):
    return call(method, f"{API}/v1/waitrooms/{room}/{part}", body, tok, **kw)


def pay(tok, booking_id):
    s, r, _ = call("POST", f"{API}/v1/bookings/{booking_id}/pay", token=tok)
    order = r["checkout_url"].rsplit("/", 1)[1]
    s, _, h = call("POST", f"{GW}/checkout/{order}/pay", form={"method": "upi", "outcome": "success"})
    if s != 303:
        return None
    q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(h["Location"]).query))
    _, b, _ = call("POST", f"{API}/v1/bookings/{booking_id}/verify",
                   {"order_id": q["order_id"], "payment_id": q["payment_id"], "signature": q.get("signature", "")}, tok)
    return b


def solve_pow(prefix, bits):
    i = 0
    while True:
        h = int.from_bytes(hashlib.sha256((prefix + str(i)).encode()).digest(), "big")
        if h >> (256 - bits) == 0:
            return str(i)
        i += 1


def ws_first_message(room, tok):
    """A minimal WebSocket client: handshake, then read one (unmasked, server-sent) text frame."""
    u = urllib.parse.urlparse(API)
    s = socket.create_connection((u.hostname, u.port or 80), timeout=5)
    key = base64.b64encode(os.urandom(16)).decode()
    path = f"/v1/waitrooms/{room}/ws?access_token={urllib.parse.quote(tok)}"
    s.sendall((f"GET {path} HTTP/1.1\r\nHost: {u.netloc}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
               f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nOrigin: http://localhost:3000\r\n\r\n").encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        buf += s.recv(4096)
    head, buf = buf.split(b"\r\n\r\n", 1)
    if b" 101 " not in head.split(b"\r\n")[0]:
        return None

    def need(n):
        nonlocal buf
        while len(buf) < n:
            buf += s.recv(4096)
        out, buf = buf[:n], buf[n:]
        return out

    b0, b1 = need(2)
    n = b1 & 0x7F
    if n == 126:
        n = int.from_bytes(need(2), "big")
    elif n == 127:
        n = int.from_bytes(need(8), "big")
    msg = json.loads(need(n))
    s.close()
    return msg


IST = dt.timezone(dt.timedelta(hours=5, minutes=30))
today = dt.datetime.now(IST).date()
# A fresh day (other drills don't share its berths) on which 12304 actually leaves New Delhi.
date = today + dt.timedelta(days=random.randint(15, 50))
for _ in range(7):
    _, sr, _ = call("GET", f"{API}/v1/search?from=NDLS&to=HWH&date={date}")
    if any(t["number"] == "12304" and t["runs_on_date"] for t in sr["trains"]):
        break
    date += dt.timedelta(days=1)
ADM = admin()
call("POST", GW + "/admin/mode", {"mode": "normal"})

# ── 1. Rooms ──
_, rooms, _ = call("GET", API + "/v1/waitrooms")
real = [r for r in rooms if r["kind"] == "real"]
ok({r["ac"] for r in real} == {True, False}, f"daily rooms exist for AC and non-AC Tatkal ({len(real)} real rooms listed)")
s, room, _ = call("POST", API + "/v1/admin/waitrooms/drill", {"journey_date": str(date), "ac": False, "opens_in_sec": 15, "admit_rate": 50}, ADM)
ok(s == 201 and room["state"] == "joining", f"drill room created for {date}, opening in 15s")
R = room["id"]
s, _, _ = call("POST", API + "/v1/admin/waitrooms/drill", {"journey_date": str(date), "ac": False}, passenger("Not Staff"))
ok(s == 403, "passengers can't create drill rooms")

# ── 2. People join before opening ──
alice, cara, bob = passenger("Alice Tatkal"), passenger("Cara Tatkal"), passenger("Bob Farm")
s, st, _ = room_call("POST", R, "join", alice, human_signals("dev-alice-" + uuid.uuid4().hex[:6]))
ok(s == 200 and st["joined"] and st["state"] == "joining", f"Alice joined; opens in {st.get('opens_in_seconds')}s")
s, err, _ = room_call("PUT", R, "draft", alice, {**draft(date, quota="GN"), "signals": human_signals("dev-alice")})
ok(s == 409, "a general-quota draft is refused in a Tatkal room")
s, st, _ = room_call("PUT", R, "draft", alice, {**draft(date), "signals": human_signals("dev-alice")})
ok(s == 200 and st["has_draft"] and st["action"] == "allow", "Alice's Tatkal draft saved; detector allows her")
s, _, _ = room_call("POST", R, "book", alice)
ok(s == 409, "booking before opening is refused")

# Bob is a script: automation user agent, a form filled in 0.2s, metronome typing.
bot_sig = {"device": "farm-1", "form_ms": 200, "key_intervals": [50] * 10, "pointer_moves": 0}
room_call("POST", R, "join", bob, bot_sig, ua="python-requests/2.32")
s, st, _ = room_call("PUT", R, "draft", bob, {**draft(date), "signals": bot_sig}, ua="python-requests/2.32")
ok(st["action"] == "block", f"the bot is blocked (action {st['action']})")

# Cara has no device fingerprint and fills the form fast: suspicious, not damning.
cara_sig = {"device": "", "form_ms": 1000, "key_intervals": [120, 300, 95, 210, 160, 250], "pointer_moves": 12}
room_call("POST", R, "join", cara, cara_sig)
s, st, _ = room_call("PUT", R, "draft", cara, {**draft(date), "signals": cara_sig})
ok(st["action"] == "challenge" and st.get("challenge", {}).get("bits") == 16, "a borderline account gets a proof-of-work challenge")
s, _, _ = room_call("POST", R, "challenge", cara, {"nonce": "definitely-wrong"})
ok(s == 400, "a wrong verification answer is rejected")
t0 = time.time()
nonce = solve_pow(st["challenge"]["prefix"], st["challenge"]["bits"])
s, st, _ = room_call("POST", R, "challenge", cara, {"nonce": nonce})
ok(s == 200 and st["action"] == "allow", f"solved in {time.time() - t0:.1f}s; Cara is let through")

msg = ws_first_message(R, alice)
ok(msg is not None and msg["joined"] and msg["room"] == R, "the WebSocket streams Alice's place in the room")

# ── 3. Opening: shuffle, admission, booking ──
while True:
    _, st, _ = room_call("GET", R, "status", alice)
    if st["state"] == "open":
        break
    time.sleep(1)
s, err, _ = call("POST", API + "/v1/bookings", draft(date), passenger("Direct Booker"), headers={"Idempotency-Key": str(uuid.uuid4())})
ok(s == 409 and err.get("error") == "use_waiting_room", "during the rush a direct Tatkal booking is sent to the waiting room")

deadline = time.time() + 20
while not st["admitted"] and time.time() < deadline:
    time.sleep(0.5)
    _, st, _ = room_call("GET", R, "status", alice)
ok(st["admitted"], f"Alice admitted (queue of {st['queue_size']})")
s, b, _ = room_call("POST", R, "book", alice)
ok(s == 201 and b["quota"] == "TQ" and b["passengers"][0]["status"] == "CNF", f"Alice booked from the room: "
   f"{b['passengers'][0].get('coach')}/{b['passengers'][0].get('berth')}" if s == 201 else f"Alice booked from the room ({s} {b})")
paid = pay(alice, b["id"]) if s == 201 else None
ok(paid and paid["status"] == "CONFIRMED", "Alice paid; ticket confirmed")
s, again, _ = room_call("POST", R, "book", alice)
ok(s in (200, 201, 409) and (s == 409 or again["id"] == b["id"]), "booking again from the room doesn't make a second booking")
s, _, _ = room_call("POST", R, "book", bob, ua="python-requests/2.32")
ok(s == 403, "the blocked bot can't book")
_, st, _ = room_call("GET", R, "status", cara)
deadline = time.time() + 20
while not st["admitted"] and time.time() < deadline:
    time.sleep(0.5)
    _, st, _ = room_call("GET", R, "status", cara)
s, cb, _ = room_call("POST", R, "book", cara)
ok(s == 201, "the verified account books too")

# ── 4. A late joiner who double-clicks Join still gets one place, and is admitted ──
dan = passenger("Dan Late")
_, before, _ = room_call("GET", R, "status", alice)
threads = [threading.Thread(target=room_call, args=("POST", R, "join", dan, human_signals("dev-dan"))) for _ in range(4)]
[t.start() for t in threads]
[t.join() for t in threads]
_, st, _ = room_call("GET", R, "status", dan)
ok(st["queue_size"] == before["queue_size"] + 1, f"four simultaneous joins put Dan in the queue once ({before['queue_size']} → {st['queue_size']})")
deadline = time.time() + 10
while not st["admitted"] and time.time() < deadline:
    time.sleep(0.5)
    _, st, _ = room_call("GET", R, "status", dan)
ok(st["admitted"], "the late joiner is admitted")

# ── 5. Staff view and invariants ──
_, mon, _ = call("GET", f"{API}/v1/admin/waitrooms/{R}/monitor", token=ADM)
ok(mon["blocked"] >= 1 and mon["verified"] >= 1 and mon["stats"].get("joins", 0) >= 4,
   f"monitor: {mon['blocked']} blocked, {mon['verified']} verified, stats {mon['stats']}")
_, flags, _ = call("GET", f"{API}/v1/admin/waitrooms/{R}/bots", token=ADM)
ok(any(f["name"] == "Bob Farm" and "automation user agent" in f["reasons"] for f in flags), "the bot list names Bob with reasons")
_, integ, _ = call("GET", API + "/v1/admin/integrity", token=ADM)
ok(integ["overlapping_allocations"] == 0 and integ["paid_without_ticket"] == 0 and integ["trial_balance"] == 0,
   f"integrity: {integ['overlapping_allocations']} overlaps, {integ['paid_without_ticket']} paid without ticket, trial balance {integ['trial_balance']}")

sys.exit(1 if FAILED else 0)

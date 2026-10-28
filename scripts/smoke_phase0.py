#!/usr/bin/env python3
"""Phase 0 smoke test: sign in as every role and exercise auth edge cases.
Run with the stack up:  python scripts/smoke_phase0.py"""
import json, urllib.request, urllib.error
import os, random, sys
B = os.environ.get("API_URL", "http://localhost:8080")
def call(method, path, body=None, token=None):
    req = urllib.request.Request(B + path, method=method, data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req) as r:
            t = r.read(); return r.status, (json.loads(t) if t else None)
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null")
FAILED = []
sys.stdout.reconfigure(encoding="utf-8")  # Windows consoles default to cp1252
NEW_PHONE = "7" + "".join(random.choices("0123456789", k=9))
LIMIT_PHONE = "8" + "".join(random.choices("0123456789", k=9))
def ok(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        FAILED.append(msg)

s, st = call("GET", "/v1/network/stats"); print("stats:", st)
s, demos = call("GET", "/v1/auth/demo-accounts")
for d in demos: print("  demo:", d["role"], d["full_name"], d.get("employee_id") or d.get("phone"), d.get("station",""))
tokens = {}
for d in demos:
    if d["role"] == "passenger":
        s, r = call("POST", "/v1/auth/otp/request", {"phone": "+91 " + d["phone"]})
        s, r = call("POST", "/v1/auth/otp/verify", {"phone": d["phone"], "otp": r["dev_otp"]})
    else:
        s, r = call("POST", "/v1/auth/login", {"identifier": d["employee_id"].lower(), "password": d["password"]})
    ok(s == 200 and r["user"]["role"] == d["role"], f"login as {d['role']} -> {r['user']['full_name'] if s==200 else r}")
    tokens[d["role"]] = r
s, me = call("GET", "/v1/me", token=tokens["tte"]["access_token"]); ok(s == 200 and me["division"] == "Chennai", f"/me for TTE: {me.get('designation')}, {me.get('division')} division, {me.get('station')}")
ok(call("GET", "/v1/admin/users?role=controller&limit=2", token=tokens["admin"]["access_token"])[0] == 200, "admin can list users")
ok(call("GET", "/v1/admin/users", token=tokens["passenger"]["access_token"])[0] == 403, "passenger blocked from admin endpoint (403)")
ok(call("GET", "/v1/me")[0] == 401, "no token -> 401")
ok(call("POST", "/v1/auth/login", {"identifier": "TTE00001", "password": "wrong"})[0] == 401, "wrong password -> 401")
s, r = call("POST", "/v1/auth/otp/request", {"phone": NEW_PHONE}); ok(call("POST", "/v1/auth/otp/verify", {"phone": NEW_PHONE, "otp": "000000" if r["dev_otp"] != "000000" else "111111"})[0] == 401, "wrong OTP -> 401")
s, r2 = call("POST", "/v1/auth/otp/verify", {"phone": NEW_PHONE, "otp": r["dev_otp"], "full_name": "New Traveller"}); ok(s == 200 and r2["user"]["role"] == "passenger", "new phone number registers a passenger")
ok(call("POST", "/v1/auth/otp/request", {"phone": "12345"})[0] == 400, "invalid phone -> 400")
old = tokens["station"]["refresh_token"]
s, rot = call("POST", "/v1/auth/refresh", {"refresh_token": old}); ok(s == 200 and rot["refresh_token"] != old, "refresh rotates the token")
ok(call("POST", "/v1/auth/refresh", {"refresh_token": old})[0] == 401, "reusing a rotated refresh token -> 401")
ok(call("POST", "/v1/auth/refresh", {"refresh_token": rot["refresh_token"]})[0] == 401, "...and it revokes the whole session family")
codes = [call("POST", "/v1/auth/otp/request", {"phone": LIMIT_PHONE})[0] for _ in range(4)]; ok(codes[-1] == 429, f"OTP rate limit after 3 requests: {codes}")
s, t = call("GET", "/v1/trains/12623"); ok(s == 200, f"train 12623: {t['name']} {t['origin']}->{t['destination']} {len(t['stops'])} stops, {len(t['coaches'])} coaches, {t['distance_km']} km")
s, st = call("GET", "/v1/stations?q=chennai"); ok(s == 200 and st, "station search 'chennai': " + ", ".join(x["code"] for x in st))
s, l = call("GET", "/v1/coach-classes/SL/layout"); ok([x["type"] for x in l["seats"][:8]] == ["LB","MB","UB","LB","MB","UB","SL","SU"], "SL berth layout LB MB UB LB MB UB SL SU")
s, z = call("GET", "/v1/zones"); ok(len(z) == 17, f"{len(z)} zones, {sum(len(x['divisions']) for x in z)} divisions")
import sys
sys.exit(1 if FAILED else 0)

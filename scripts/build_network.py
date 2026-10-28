#!/usr/bin/env python3
"""Builds YatraX's real railway network from the datameet Indian Railways dataset (CC0,
https://github.com/datameet/railways, data from 2016).

Reads   data/raw/datameet/{stations,trains,schedules}.json   (scripts/fetch_network.sh downloads them)
        services/core/internal/seed/data/stations.csv          (the curated stations: names, zones, divisions)
Writes  services/core/internal/seed/data/real_stations.csv   stations used by real trains, not already curated
        services/core/internal/seed/data/real_trains.json    reserved long-distance trains with every halt

Only bookable long-distance trains are kept (Rajdhani, Duronto, Shatabdi, Jan Shatabdi, Garib Rath,
Sampark Kranti, Superfast, Mail/Express); suburban, MEMU, DEMU, passenger and special (0xxxx) trains
are left out. What the dataset lacks is filled in, deterministically:
  - running days: from the name ("Weekly", "Bi-Weekly", "Tri-Weekly"), otherwise daily;
  - distance at each halt: great-circle distance along the route ×1.22, scaled to the train's total;
  - zone, division and state of stations the dataset doesn't place: from the nearest curated station;
  - station codes renamed since 2016 (CSTM → CSMT and so on) are updated.

Run:  python -I scripts/build_network.py
"""
import csv
import hashlib
import json
import math
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
RAW = ROOT / "data" / "raw" / "datameet"
OUT = ROOT / "services" / "core" / "internal" / "seed" / "data"

RESERVED = {"Raj": "RAJDHANI", "Drnt": "DURONTO", "Shtb": "SHATABDI", "JShtb": "JAN_SHATABDI", "GR": "GARIB_RATH",
            "SKr": "SUPERFAST", "SF": "SUPERFAST", "Exp": "MAIL_EXPRESS", "Mail": "MAIL_EXPRESS"}
RENAMED = {"CSTM": "CSMT", "BCT": "MMCT", "MGS": "DDU", "ALD": "PRYJ", "JHS": "VGLJ", "HBJ": "RKMP", "GR": "KLBG", "FD": "AYC"}
STATE_FIX = {"Orissa": "Odisha", "Delhi NCT": "Delhi", "Jammu and Kashmir": "Jammu & Kashmir", "Pondicherry": "Puducherry",
             "Uttaranchal": "Uttarakhand"}
ZONES = {"CR", "ER", "ECR", "ECoR", "KR", "NR", "NCR", "NER", "NFR", "NWR", "SR", "SCR", "SER", "SECR", "SWR", "WR", "WCR"}
MAX_HALTS = 65  # = MaxSegments + 1 in services/core/internal/inventory
CLASS_FLAGS = {"first_ac": "1A", "second_ac": "2A", "third_ac": "3A", "sleeper": "SL", "chair_car": "CC"}


def km(a, b):
    la1, la2 = math.radians(a[1]), math.radians(b[1])
    h = math.sin((la2 - la1) / 2) ** 2 + math.cos(la1) * math.cos(la2) * math.sin(math.radians(b[0] - a[0]) / 2) ** 2
    return 2 * 6371 * math.asin(math.sqrt(h))


def clock(s):
    if not s or s == "None":
        return None
    h, m = s.split(":")[:2]
    return int(h) * 60 + int(m)


def tidy(name):
    """Datameet names mix ALL CAPS and Title Case; make them read like a timetable."""
    words = []
    for w in re.split(r"(\s+|-)", name.strip()):
        if w.isupper() and len(w) > 3 and w not in {"MEMU", "DEMU"}:
            w = w.capitalize()
        words.append(w)
    out = re.sub(r"\s+", " ", "".join(words)).strip()
    out = re.sub(r"\s+-\s*|\s*-\s+", " - ", out)                       # "Chennai- Alapuzha" -> "Chennai - Alapuzha"
    out = re.sub(r" (Exp|Expr|Expres|Exp\.)$", " Express", out)  # the dataset truncates long names
    return out


def running_days(number, name):
    n = name.lower()
    count = 1 if "weekly" in n and not re.search(r"(bi|tri)[- ]?weekly", n) else 2 if re.search(r"bi[- ]?weekly", n) else 3 if re.search(r"tri[- ]?weekly", n) else 7
    if count == 7:
        return 127
    first = int(hashlib.sha256(number.encode()).hexdigest(), 16) % 7
    step = {1: 7, 2: 3, 3: 2}[count]
    mask = 0
    for k in range(count):
        mask |= 1 << ((first + k * step) % 7)
    return mask


def main():
    curated = {r["code"]: r for r in csv.DictReader(open(OUT / "stations.csv", encoding="utf-8"))}
    cur_list = [(c, float(r["lon"]), float(r["lat"]), r) for c, r in curated.items()]

    def nearest(lon, lat, zone=None):
        best, bd = None, 1e18
        for c, x, y, r in cur_list:
            if zone and r["zone"] != zone:
                continue
            d = (x - lon) ** 2 * math.cos(math.radians(lat)) ** 2 + (y - lat) ** 2
            if d < bd:
                best, bd = r, d
        return best

    raw_st = {}
    for f in json.load(open(RAW / "stations.json", encoding="utf-8"))["features"]:
        p = f["properties"]
        code = RENAMED.get(p["code"], p["code"])
        g = f.get("geometry")
        raw_st[code] = {"name": p.get("name") or code, "state": p.get("state"), "zone": p.get("zone"),
                        "pos": tuple(g["coordinates"]) if g else None}

    trains = {}
    for f in json.load(open(RAW / "trains.json", encoding="utf-8"))["features"]:
        p = f["properties"]
        if p["type"] in RESERVED and not p["number"].startswith("0"):
            trains[p["number"]] = p

    stops = {}
    for s in json.load(open(RAW / "schedules.json", encoding="utf-8")):
        if s["train_number"] in trains:
            stops.setdefault(s["train_number"], []).append(s)

    out_trains, dropped = [], {}
    used = {}
    for number in sorted(trains):
        p = trains[number]
        rows = sorted(stops.get(number, []), key=lambda s: s["id"])
        # Each halt once, in order (a few trains list a station twice). The dataset lists every station
        # the train passes; where arrival equals departure it only runs through, so that row is skipped.
        seen, halts = set(), []
        for i, s in enumerate(rows):
            code = RENAMED.get(s["station_code"], s["station_code"])
            if code in seen:
                continue
            arr, dep = clock(s["arrival"]), clock(s["departure"])
            if 0 < i < len(rows) - 1 and (arr == dep or arr is None):
                continue  # runs through (equal times) or an untimed passing point (no times at all)
            if i == len(rows) - 1 and arr is None:
                if dep is None:
                    continue  # the terminus row is untimed: the last timed halt becomes the end
                s = {**s, "arrival": s["departure"]}
            seen.add(code)
            halts.append((code, s))
        if len(halts) < 2:
            dropped["too few halts"] = dropped.get("too few halts", 0) + 1
            continue
        # Times: minutes after midnight of the day the train leaves its origin, never going backwards.
        timed, last, bad = [], -1, None
        for i, (code, s) in enumerate(halts):
            day = max(1, int(s.get("day") or 1)) - 1
            arr, dep = clock(s["arrival"]), clock(s["departure"])
            if i == 0:
                arr = None
            if i == len(halts) - 1:
                dep = None
            vals = []
            for v in (arr, dep):
                if v is None:
                    vals.append(None)
                    continue
                v += day * 1440
                while v < last:
                    v += 1440
                if v - max(last, 0) > 1440 and last >= 0:
                    bad = "a gap of over a day between halts"
                vals.append(v)
                last = v
            if i > 0 and vals[0] is None:
                bad = "a halt with no arrival time"
            if 0 < i < len(halts) - 1 and vals[1] is None:
                vals[1] = vals[0]
            timed.append((code, vals[0], vals[1]))
        if not bad and timed[0][2] is None:
            bad = "no departure from the origin"
        if not bad and timed[-1][1] - timed[0][2] > 4 * 1440:
            bad = "a journey over four days"
        if bad:
            dropped[bad] = dropped.get(bad, 0) + 1
            continue
        out_trains.append((number, p, timed))
        for code, _, _ in timed:
            used[code] = used.get(code, 0) + 1

    # Stations: curated ones keep their rows; new ones get a zone, division and state from the dataset or the nearest curated station.
    positions = {}
    for code in used:
        if code in curated:
            positions[code] = (float(curated[code]["lon"]), float(curated[code]["lat"]))
        elif code in raw_st and raw_st[code]["pos"]:
            positions[code] = raw_st[code]["pos"]
    # Halts without coordinates: place them between their neighbours on some train's route.
    for number, p, timed in out_trains:
        codes = [c for c, _, _ in timed]
        for i, c in enumerate(codes):
            if c in positions:
                continue
            prev = next((positions[x] for x in reversed(codes[:i]) if x in positions), None)
            nxt = next((positions[x] for x in codes[i + 1:] if x in positions), None)
            if prev and nxt:
                positions[c] = ((prev[0] + nxt[0]) / 2, (prev[1] + nxt[1]) / 2)
            elif prev or nxt:
                positions[c] = prev or nxt
    # Halts we still can't place, or stations the dataset doesn't know, are dropped from routes.
    known = {c for c in used if c in positions and (c in curated or c in raw_st)}

    new_rows = []
    for code in sorted(known - set(curated)):
        r = raw_st[code]
        lon, lat = positions[code]
        zone = r["zone"] if r["zone"] in ZONES else None
        near = nearest(lon, lat, zone) or nearest(lon, lat)
        state = STATE_FIX.get(r["state"], r["state"])
        if state not in {x["state"] for x in curated.values()}:
            state = near["state"]
        importance = 2 if used[code] >= 30 else 3
        new_rows.append([code, tidy(r["name"]), state, zone or near["zone"], near["division"], f"{lat:.5f}", f"{lon:.5f}", importance])
    with open(OUT / "real_stations.csv", "w", encoding="utf-8", newline="\n") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(["code", "name", "state", "zone", "division", "lat", "lon", "importance"])
        w.writerows(new_rows)

    result, kept_numbers, capped = [], set(), 0
    for number, p, timed in out_trains:
        timed = [t for t in timed if t[0] in known]
        if len(timed) < 2:
            dropped["unplaceable halts"] = dropped.get("unplaceable halts", 0) + 1
            continue
        # The booking core tracks up to 64 segments per train (a 64-bit mask). The few trains with
        # more halts keep both ends plus their longest stops at the busiest stations.
        if len(timed) > MAX_HALTS:
            middle = sorted(range(1, len(timed) - 1),
                            key=lambda i: (-((timed[i][2] or 0) - (timed[i][1] or 0)), -used[timed[i][0]], i))
            keep = {0, len(timed) - 1, *middle[:MAX_HALTS - 2]}
            timed = [t for i, t in enumerate(timed) if i in keep]
            capped += 1
        # Re-anchor the ends if a dropped halt was an end.
        first, last = timed[0], timed[-1]
        timed[0] = (first[0], None, first[2] if first[2] is not None else first[1])
        timed[-1] = (last[0], last[1] if last[1] is not None else last[2], None)
        # Distances along the route, scaled to the dataset's total when it is plausible.
        cum, d = [0.0], 0.0
        for a, b in zip(timed, timed[1:]):
            d += max(km(positions[a[0]], positions[b[0]]) * 1.22, 1)
            cum.append(d)
        total = p.get("distance") or 0
        scale = total / d if d > 0 and total and 0.6 < total / d < 1.8 else 1.0
        flags = [cls for k, cls in CLASS_FLAGS.items() if p.get(k)]
        result.append({
            "number": number, "name": tidy(p["name"]), "type": RESERVED[p["type"]],
            "days": running_days(number, p["name"]), "classes": flags, "return": p.get("return_train") or "",
            "stops": [[c, a, dep, round(x * scale)] for (c, a, dep), x in zip(timed, cum)],
        })
        kept_numbers.add(number)
    for t in result:
        if t["return"] not in kept_numbers:
            t["return"] = ""
    with open(OUT / "real_trains.json", "w", encoding="utf-8", newline="\n") as f:
        f.write("[\n" + ",\n".join(json.dumps(t, separators=(",", ":"), ensure_ascii=False) for t in result) + "\n]\n")

    n_stops = sum(len(t["stops"]) for t in result)
    print(f"{len(result)} trains, {n_stops} halts, {len(known)} stations ({len(new_rows)} new beyond the curated "
          f"{len(curated)}); {capped} long trains trimmed to {MAX_HALTS} halts; dropped {dropped or 'none'}", file=sys.stderr)


if __name__ == "__main__":
    main()

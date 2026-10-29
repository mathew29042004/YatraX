# YatraX demo script

A ten-minute walkthrough of the whole system, written so it can be recorded as a video. Each scene lists
what to show, what to say, and what to point the camera at. Times are a guide.

## Before you start (5 minutes, off camera)

1. `docker compose up -d --build`, then `pnpm dev` (or the `web-all` launch configuration) and wait until
   ports 3000–3004 answer. For the TTE app: `pnpm --filter @yatrax/tte dev` (port 3005).
2. In the Control Centre (http://localhost:3002, `CTRL00001`) press **Back to real time** on the railway clock.
3. Open these tabs, each signed in:
   - Passenger app http://localhost:3000 as **Ananya Krishnan** (one click on the sign-in screen)
   - Control Centre http://localhost:3002 as `CTRL00001`
   - Station app http://localhost:3001 as `SM00001`, plus the public board http://localhost:3001/board/MAS
   - Catering app http://localhost:3004 as `VND00001`
   - Staff Portal http://localhost:3003 as `ADM00001`
   - Grafana http://localhost:3100 (`admin` / `yatrax`), dashboard "YatraX — Core service"
4. Browser zoom at 110%, notifications off, a 1920×1080 window. Every staff password is `yatrax123`.

## Scene 1 — The real network (0:00–0:45)

**Show:** Passenger app home. Search **Chennai Central → Ernakulam Town**, a date two weeks out.

**Say:** "YatraX runs on India's real railway network: 2,125 real trains and 2,957 real stations. The
people and bookings are synthetic. Availability is live, and every train shows its fare."

**Point at:** the train list, the class pills (AVL / RAC / WL), the ±3-day strip.

## Scene 2 — Booking without double bookings (0:45–2:00)

**Show:** 12623 Trivandrum Mail → **Book** → seat map → pick two berths together → pay at YatraPay → the
ticket with its QR code.

**Say:** "Every berth's journey is a segment range in Postgres with an exclusion constraint, so two people can
never hold the same berth over the same stretch. Load tests with 5,000 people booking at once never produce a
double booking. Money moves through a double-entry ledger."

**Point at:** the seat map, the PNR, the signed QR ("a TTE can check this offline").

## Scene 3 — Food that follows the train (2:00–3:15)

**Show:** On the booking page, **Order food** → Salem Junction → Salem Biryani House → add a biryani → pay
from the wallet. Switch to the Catering tab: the order is there with **Train arrives** and **Start cooking**.

**Then:** In the Control Centre, find 12623 on that date and add a **40-minute** incident at Chennai.
Back in the Catering app, the times move. The passenger's bell shows "Your food now arrives around …".

**Say:** "The vendor never has to guess. When the train runs late, the cooking time moves with it, and the
passenger is told."

## Scene 4 — The control room (3:15–4:45)

**Show:** Control Centre. Set the clock to **144×** (a day in ten minutes). The live map fills with trains.
Click one on the **section chart** → **What if we hold it 20 minutes?** → the ripple list of trains it
would delay, and how many passengers.

**Then:** **Incident → Divert** around a station on a future run with a booked passenger. Show the
passenger app: refunded in full, seats held on another train, notification.

**Say:** "Controllers see the knock-on effect before they act. When a train is cut short or diverted,
everyone on it is refunded, rebooked and told, automatically."

## Scene 5 — Stations and the TTE (4:45–6:00)

**Show:** The public board at /board/MAS (arrivals, departures, platform changes in red). In the Station
app, move a train to another platform: the board and the announcement follow (turn sound on).

**Then:** TTE app (port 3005), `TTE00001` → **Download chart** for 12623 → turn the network off in DevTools →
mark a no-show, move an RAC passenger onto the freed berth → network back on → **Sync**.

**Say:** "The TTE works with no signal. Each change is an operation with an ID; when the phone reconnects,
the server applies them, and the server's seat allocation wins any conflict."

## Scene 6 — Tatkal at 10:00 (6:00–7:00)

**Show:** Staff Portal → **Tatkal monitor** → start a drill with 500 people; the passenger app's **Tatkal**
page shows the waiting room with a fair queue position. Back in the monitor: bots flagged and blocked.

**Say:** "At 10 a.m. everyone arrives at once. They wait in a queue they can see, bots are scored on how
they type and which device they use, and nobody is sold the same berth twice."

## Scene 7 — Dashboards (7:00–8:00)

**Show:** Staff Portal → **SR · Southern Railway** → **Chennai** division. Revenue tab, then Occupancy
(the heatmap), Punctuality (the zone league table and why trains were late), Fraud (bot farms, possible touts,
TTE cash to reconcile).

**Say:** "Every zone and division gets its own numbers. Station masters only see their own zone."

## Scene 8 — Chaos mode (8:00–9:30)

**Show:** Control Centre → **Chaos mode**. Run **Database failover**: every connection to Postgres is cut,
and the timeline shows it back in about two seconds. Run **Event bus down**: 40 events pile up in the outbox
and drain the moment the bus is back. Run **Train breakdown**: a running train loses 90 minutes, and the
watcher announces it within seconds. Start **Payment gateway loses its callbacks** and let it run.

**Say:** "Nothing here is fixed by hand. The faults are real; the recovery is the system's own design: the
outbox, connection pooling, the live watcher, and the reconciler, which is about to find three payments the
gateway never told us about."

**Point at:** the "Recovered in … s" badges and the drill log. Grafana in a corner, if there's room.

## Scene 9 — Replay yesterday (9:30–10:00)

**Show:** Control Centre → **Replay a day** → yesterday → **Play from 05:00 at 600×**. The map replays the
day. Click a late arrival in the list: the clock jumps there and the train's desk opens. By now the gateway
drill shows **Recovered**.

**Say:** "Because the railway model is deterministic, any day can be replayed exactly: a digital twin for
the control room. That's YatraX."

**End on:** **Back to live**, then the passenger app home.

## Bonus scenes (if there's time)

- **Two tickets, one trip:** search a sold-out date; the results offer **split journeys and connections**
  with berths, and a link to the **confirmation guarantee** (hold two waitlisted trains, keep the first to
  confirm, the other refunded in full).
- **A special train:** Control Centre → *Special trains* → run 02623 on a busy date; it appears in the
  passenger's search at once. Book it in SL with *free upgrade* ticked, then Staff Portal → *Prepare chart*:
  the party moves to 3A.
- **On the ticket:** CO₂ saved against flying, Rewards points, the GST invoice; *Help on board* → *Ask for a
  female TTE* names the TTE coming; the complaint appears in the Station app, which resolves it.
- **At the station:** book a retiring room against the ticket, then check the guest in at the Station app's
  counter; send a group request for a pilgrimage and approve it at the station desk.

- **In your language:** switch the passenger app's language picker to தமிழ் or हिन्दी — search, results,
  booking and the ticket follow.

## Shot list

| # | Shot | Length |
|---|---|---|
| 1 | Passenger search, results | 45 s |
| 2 | Seat map → pay → QR ticket | 75 s |
| 3 | Food order; vendor screen; ETA moves after the incident | 75 s |
| 4 | Clock at 144×, live map, what-if ripple, divert + passenger refund | 90 s |
| 5 | Public board + announcement; TTE offline sync | 75 s |
| 6 | Tatkal waiting room + bot monitor | 60 s |
| 7 | Dashboards: four tabs | 60 s |
| 8 | Four chaos drills, recovery timers | 90 s |
| 9 | Replay a day; back to live | 30 s |

## If something goes wrong on camera

- **A screen looks stale:** the railway clock may be paused or fast. Press **Back to real time**.
- **"Another drill is running":** wait for the current one (the gateway drill takes about two minutes).
- **Search shows no trains:** check the core is healthy: `curl http://localhost:8080/readyz`.
- **Docker isn't running after a reboot:** start Docker Desktop, then `docker compose up -d`.

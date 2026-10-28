# YatraX

India's railway booking platform, properly built. Seven apps, one realistic railway
organisation, synthetic data only. See [YATRAX_PLAN.md](YATRAX_PLAN.md) for the full plan.

**Status: complete — Phases 0–9, 11 and 12 built, on the real Indian Railways network** (2,125 real trains
and 2,957 real stations; see [The railway world](#the-railway-world)). Phase 10 (the AI layer) was left out at
the owner's request. The last phase added **chaos mode** (break the payment gateway, the database, the event bus
or a train on purpose and watch each recover on its own, timed), a **digital twin** that replays any past day
in the control room, an accessibility pass, the [architecture diagram](#architecture) and a
[demo script](docs/DEMO.md).

What's in it: search and fares (Phase 1), a booking core with zero double bookings (2), cancellation,
waitlist and refunds through a double-entry ledger (3), the Tatkal waiting room with bot detection (4), live
trains on a simulation clock (5), an offline-first TTE app (6), the station app with boards and announcements
(7), the control centre (8), catering timed to the train's ETA (9), zone and division dashboards (11), and the
showcase (12) — plus the rest of the plan's feature list: split journeys and connections, the confirmation
guarantee, special trains, free auto-upgrades at chart time, the divyangjan / defence / foreign-tourist quotas,
travel insurance, GST invoices, loyalty points, carbon saved, retiring and cloak rooms, the group booking desk,
RailMadad complaints with resolution times, and an SOS with a female TTE ([details](#more-for-passengers-and-staff)).
People and bookings are synthetic.

**Proved, not claimed** ([how](#proved-not-claimed)): 1,000,000 booking attempts with no berth sold twice; a
5,000-person Tatkal rush; every chaos drill recovering on its own; the ledger balancing to ₹0 throughout.

## Decisions taken (plan §9)

| Question | Decision |
|---|---|
| Booking core language | **Go** (chi + pgx) |
| TTE app | **Expo** (Phase 6); TTEs use the Staff Portal until then |
| Phase order | As written |
| Repo | `yatrax`, local git, not pushed anywhere yet |

## Quick start

Needs Docker Desktop, Node 22+ and pnpm 9. Go 1.26+ is only needed to run Go tests and tools outside Docker.

```bash
docker compose up -d --build   # Postgres, Redis, Redpanda, Prometheus, Grafana, seed, core API
pnpm install
pnpm dev                       # all five web apps
```

The first `up` migrates and seeds the database (about 5 s for 50,000 passengers). Later
`up`s leave the data alone unless the generator changed (`seed.Version`), in which case
the world is regenerated. That also replaces any accounts created since. To force a
reseed: `SEED_FORCE=1 docker compose run --rm seed`; to start from scratch: `docker compose down -v`.
Seed size and randomness are set in `.env` (see `.env.example`).

Check everything works:

```bash
python scripts/smoke_phase0.py   # auth, every role
python scripts/smoke_phase1.py   # search, fares, availability, coach layouts
python scripts/smoke_phase2.py   # holds, the same-berth race, payments, tickets
python scripts/smoke_phase3.py   # cancellation, WL → RAC → CNF, refunds, wallet, charts, TDR
python scripts/smoke_phase4.py   # Tatkal waiting room, bot detector, WebSocket, fair queue
python scripts/smoke_phase5.py   # simulation clock, live status, map, board, holds, live alerts
python scripts/smoke_phase6.py   # TTE bundle, offline ops sync, no-show → RAC berth, fines, hand-off
python scripts/smoke_phase7.py   # announcements, coach position, platform chart, reassignment alerts, facilities
python scripts/smoke_phase8.py   # section chart, what-if ripple, divert/cancel/short-terminate with refunds, extra coaches
python scripts/smoke_phase9.py   # food orders: cut-offs, a 40-min delay retimes the order, delivery, missed train, payouts
python scripts/smoke_phase11.py  # dashboards: revenue adds up and moves per zone, occupancy, punctuality reacts to a hold, access
python scripts/smoke_phase12.py  # chaos: all four drills recover on their own; replay of a past day (~4 min)
python scripts/smoke_extras.py   # specials, auto-upgrade, quotas, insurance, invoice, points, alternatives, SOS, stays, groups
```

Try the demo: open http://localhost:3000, search **Chennai → Ernakulam**, open a class and
click **Book now**. Sign in as the demo passenger, pick a berth on the seat map, hold it, and
pay on the YatraPay checkout page. You get a confirmed ticket with a PNR and a signed QR code.
Switch the gateway to decline, time out or send duplicate webhooks at http://localhost:8081/admin.

Phase 3 demo: book a waitlisted ticket (Delhi → Patna in the week before Chhath is sold out), then
sign in to the Staff Portal as the admin and use **Run tools → Simulate cancellations** on that train:
the ticket moves WL → RAC → CNF and the passenger is notified. Cancel a ticket from its booking page
to see the refund preview, choose wallet or original payment, and watch the Staff Portal's ledger.

Phase 4 demo: in the Staff Portal (admin), **Tatkal waiting rooms → Create drill room** opening in
60 seconds, then run the swarm and watch the monitor and the Grafana Tatkal row:

```bash
cd services/core && go run ./cmd/botswarm -users 5000 -opens 120s -rate 150
```

Or join as a person at http://localhost:3000/tatkal: fill in your journey while you wait, and the
booking runs by itself when your turn comes.

Phase 5 demo: book a ticket, then sign in to the Control Centre (http://localhost:3002, `CTRL00001`).
**Jump** the railway clock to just before your train leaves and press **144×** (a day in ten minutes).
Watch the train move on the live map and on your booking's **Live status**; pick it on the map and
**Hold train** for 40 minutes: the bell on the passenger app shows the delay, then the boarding reminder
with your platform, then "arriving at your destination". **Back to real time** when you're done.

Phase 6 demo: open the TTE app (http://localhost:3005, or on a phone with Expo Go) and sign in as the demo
TTE. **Download chart** for a train, switch on **Work offline**, scan (or paste) a passenger's QR ticket
and mark them boarded, step the train two stations on, open an occupied berth and mark the passenger a
**No-show → RAC n**: the RAC passenger moves onto the berth on the phone at once. Switch offline mode off:
the app syncs by itself and **More → Sync results** shows what the server applied.

Phase 7 demo: open the public board for Chennai Central (http://localhost:3001/board/MAS) and press
**Start auto-announce**: as trains approach in the simulation (speed the clock up in the Control Centre to
see more), each announcement plays after a chime in Tamil, Hindi and English. Sign in to the Station app
as `SM00001`: the platform chart flags clashes (**Move … to platform n** fixes one), clicking a train shows
its coach position and lets you reassign it, and the board, the announcements and passengers' alerts follow.

Phase 8 demo: book a ticket to a station along the Trivandrum Mail's route, then in the Control Centre pick
12623 (on the map or the section chart). **What if we hold 12623?** shows which other trains it would delay
and how many passengers; then under **Incident**, **Divert** around your station: you're refunded in full,
seats are held for you on another train (pay from the notification within two hours), and the train's live
status shows the stations it now skips. **Longest waitlists → Add coach** confirms waitlisted passengers.

Phase 9 demo: on a confirmed Trivandrum Mail booking (Chennai → Ernakulam), **Order food** → Salem Junction →
**Salem Biryani House**, add a biryani and pay from the wallet. Sign in to the vendor app
(http://localhost:3004) as `VND00001`: the order shows the train's ETA and when to start cooking. In the
Control Centre add a 40-minute incident to that run: within a minute the vendor's times move and the
passenger is told "Your food now arrives around …". Accept → start cooking → hand over → delivered, then rate it.

Phase 11 demo: sign in to the Staff Portal (http://localhost:3003) as `ADM00001`, pick **SR · Southern Railway**,
then the **Chennai** division. **Revenue** shows fares, refunds and the busiest trains and journeys; **Occupancy**
the berths sold on each open run; **Punctuality** the zone league table and why trains were late; **Fraud**
the bot farms, possible touts and TTE cash to reconcile. Sign in as `SM00001` (a station master in SR): the
dashboards open on Southern Railway and other zones are locked.

## What runs where

| Service | URL | Notes |
|---|---|---|
| Passenger app | http://localhost:3000 | Public search; sign in at `/login` (mobile + OTP) |
| Station app | http://localhost:3001 | Station masters |
| Control Centre | http://localhost:3002 | Controllers |
| Staff Portal / Dashboards | http://localhost:3003 | Every staff role; admins get the people directory |
| Catering app | http://localhost:3004 | Vendors |
| TTE app (Expo, web build) | http://localhost:3005 | Ticket examiners; `pnpm --filter @yatrax/tte dev`, or `start` for Expo Go |
| Core API | http://localhost:8080 | `/healthz`, `/readyz`, `/metrics`, `/v1/...` |
| YatraPay (mock gateway) | http://localhost:8081 | Checkout pages; failure modes at `/admin` |
| Grafana | http://localhost:3100 | `admin` / `yatrax`, dashboard "YatraX — Core service" |
| Prometheus | http://localhost:9090 | |
| Redpanda Console | http://localhost:8090 | Topics `auth.events`, `booking.events`, `payment.events`, `train.events` |
| Postgres | localhost:5433 | `yatrax` / `yatrax` (host port 5433, so it does not clash with a local Postgres) |

## Demo accounts

Every sign-in screen lists these, and you can sign in with one click. Every staff account
uses the password `yatrax123`. Admins can sign in to every app.

| Role | Sign in with | Who |
|---|---|---|
| Passenger | +91 9000000001, OTP shown on screen (mock SMS) | Ananya Krishnan, Chennai |
| TTE | `TTE00001` | SR · Chennai division |
| Station Master | `SM00001` | Chennai Central (MAS) |
| Catering Vendor | `VND00001` | Salem Biryani House, Salem Jn (SA) |
| Controller | `CTRL00001` | Chief Controller, SR · Chennai |
| Admin | `ADM00001` | YatraX System Administrator |

Any other valid Indian mobile number signs up as a new passenger.

## The railway world

The **network is real**; the people and bookings are synthetic. Built by
[services/core/internal/seed](services/core/internal/seed), deterministic for a given `SEED_RANDOM`:

- **Real stations and trains:** the Indian Railways timetable from the
  [datameet railways dataset](https://github.com/datameet/railways) (CC0, gathered in 2016): 2,125 reserved
  long-distance trains (Rajdhani, Duronto, Shatabdi, Jan Shatabdi, Garib Rath, Sampark Kranti, Superfast,
  Mail/Express) with their real halts and times, calling at 2,957 real stations. Suburban, MEMU, DEMU,
  passenger and special trains are left out.
- **Newer trains:** 124 curated trains the 2016 data lacks (Vande Bharat and others) on curated real corridors,
  with timetables derived from distance and train type: 2,249 trains in all.
- **Stations:** the 510 best-known stations keep curated names, zones, divisions and platform counts;
  the rest take their zone from the dataset, and their division and state from the nearest curated station.
  Codes renamed since 2016 are updated (CSTM → CSMT, BCT → MMCT, MGS → DDU, ALD → PRYJ, JHS → VGLJ, …).
- **What the dataset doesn't have, and how it's filled:** running days (from the name: "Weekly",
  "Bi-Weekly", "Tri-Weekly"; otherwise daily, so many weekly trains show as daily), distance at each halt
  (great-circle distance along the route, scaled to the train's real total), coach composition (each train
  type's usual rake, trimmed to the classes the dataset lists), and platforms. The 57 trains with more
  than 65 halts keep their 65 most significant ones, because the booking core tracks 64 segments per train.
- **Rakes:** about 41,500 coaches in realistic order (SLR, GEN, 1A/2A/3A/3E/SL/CC/EC/2S, pantry car,
  EOG), with Indian Railways berth numbering (SL bay: LB MB UB LB MB UB SL SU)
- **Fares:** per-km rates by class, distance taper, minimum chargeable distance, reservation
  and superfast charges, GST, Tatkal percentage with min/max caps, train-type multipliers
- **People (synthetic):** 50,000 passengers (names, ages and languages matching their region), about
  560 TTEs, 210 controllers, 410 station masters, 18 admins, and 674 catering vendors

To rebuild the real network from the source dataset (about 99 MB, kept out of git in `data/raw/`):

```bash
bash scripts/fetch_network.sh          # download stations.json, trains.json, schedules.json
python -I scripts/build_network.py     # writes seed/data/real_stations.csv and real_trains.json
```

Booking and cancellation history for ML comes with the AI layer in Phase 10.

## Search, fares and availability (Phase 1)

| Endpoint | What it does |
|---|---|
| `GET /v1/search?from=MAS&to=ERS&date=2026-11-02` | Trains between two stations on a date, with fare and availability for every class. Options: `quota` (GN, TQ, PT, LD, SS), `class`, `nearby=1` (stations within 30 km, e.g. all Chennai terminals), `flex=3` (±3-day grid) |
| `GET /v1/availability?train=12623&from=MAS&to=ERN&class=SL&start=…&days=30` | Availability calendar for one train and class |
| `GET /v1/fare?train=12623&from=MAS&to=ERN&class=3A&quota=TQ` | Fare breakdown |
| `GET /v1/trains/12623/coaches/S1` | Numbered berths of one coach (LB/MB/UB/SL/SU, or W/M/A seats) |
| `GET /v1/festivals`, `GET /v1/quotas` | Festival calendar, quotas and the booking window |

- **Fare engine** ([internal/fare](services/core/internal/fare)): telescopic distance taper (each
  distance band at its own rate), rounded up to ₹5 like Indian Railways, plus reservation,
  superfast, Tatkal (percentage of base, capped) and 5% GST on AC classes. Chennai→Ernakulam
  Sleeper comes to ₹435 (real: about ₹415).
- **Booking rules:** a 60-day booking window; Tatkal opens at 10:00 (AC) or 11:00 (non-AC) the day
  before departure from the origin; departed and non-running dates are shown as such. Boarding
  dates account for overnight running (boarding at Salem at 00:10 means the train left Kerala
  the day before).
- **Availability is simulated until Phase 2.** There is no seat inventory yet, so
  [internal/availability](services/core/internal/availability) derives AVL / RAC / WL / Regret from
  a deterministic demand model: days until travel, weekday, evening departures, train type,
  class, a per-train popularity, and festivals (Diwali, Chhath, Christmas, Pongal, Holi…) that
  hit routes into and out of the regions celebrating them. Phase 2 replaces it with real
  inventory behind the same `Provider` interface. The waitlist "confirm chance" is a rough
  heuristic until the trained model in Phase 10.
- The timetable is loaded into memory at startup ([internal/network](services/core/internal/network)),
  so a search touches no database.

## Booking core (Phase 2)

**No double sales.** Each passenger's berth is a row in `seat_allocations` holding a range of
stop indexes. A Postgres `EXCLUDE USING gist` constraint makes it impossible to store two
overlapping journeys on one berth, whatever the application does. On top of that, each booking
takes an advisory lock on its (train run, class), allocates in memory using bitmasks of booked
segments, and inserts. Tested with 200 concurrent attempts on one berth (exactly one wins) and
300 concurrent bookings on one class (zero overlaps).

- **Segment allocation** ([internal/inventory](services/core/internal/inventory)): MAS→SA and SA→ERN
  share one berth. Best fit prefers berths whose free gap the journey fills exactly, so berths
  aren't left with small useless gaps. It honours berth preference (seniors without one get a
  lower berth), keeps parties in one bay, and respects seat-map picks.
- **Quotas** are fixed per berth: Tatkal and Premium Tatkal take the last bays of each coach;
  Ladies takes six berths in the middle coach; Senior/lower-berth takes a few lower berths per coach.
  Side-lower berths in the last two bays of sleeper coaches are RAC (two passengers each). When a
  class is full, passengers get RAC, then a waitlist number (promotion comes in Phase 3).
- **Holds:** berths are allocated at once and held for 10 minutes. A sweeper releases expired holds.
  At most 3 unpaid holds per user. An `Idempotency-Key` makes retries safe.
- **Background demand:** the first time a train run is booked or its seat map opened, bot passengers
  (the synthetic 50,000) book it to the level the demand model predicts, through the same allocator.
  From then on its availability is real inventory; unopened runs still use the model.
- **Payments** ([internal/paygw](services/core/internal/paygw), [internal/booking/payment.go](services/core/internal/booking/payment.go)):
  - YatraPay is a separate service shaped like Razorpay: orders over an authenticated API, a hosted
    checkout page, HMAC-signed redirects and webhooks (retried with backoff).
  - Core confirms from either the verified redirect or the webhook. Both are idempotent, and
    duplicate events are recorded once.
  - A decline keeps the hold so the customer can retry.
  - Money arriving after the hold expired is re-allocated if possible; otherwise the booking fails
    and the payment is marked `REFUND_PENDING` (refunds are processed in Phase 3). No charge
    without a ticket.
- **Events:** booking and payment events go to a transactional outbox and are relayed to Redpanda,
  so an event is never lost or sent for a rolled-back change.
- **E-tickets** ([internal/ticket](services/core/internal/ticket)): the QR code is an Ed25519-signed
  token (verifiable offline with `GET /v1/tickets/public-key`). Editing it, for example to upgrade
  the class, breaks the signature. Public PNR status shows initials only.

| Endpoint | |
|---|---|
| `GET /v1/seatmap?train&from&to&date&class&quota` | Live berth map for a journey |
| `POST /v1/bookings` (+ `Idempotency-Key`) | Allocate and hold |
| `POST /v1/bookings/{id}/pay` · `/verify` · `/release` | Start payment, verify the redirect, give up the hold |
| `GET /v1/bookings`, `/v1/bookings/{id}`, `/v1/bookings/{id}/ticket` | My trips, booking, signed ticket |
| `GET /v1/pnr/{pnr}` | Public PNR status |
| `POST /v1/payments/webhook` | Gateway webhooks (HMAC-signed) |
| `GET/POST/DELETE /v1/me/passengers` | Saved co-travellers |

## Cancellation, waitlist and refunds (Phase 3)

- **Refund rules** ([rules.go](services/core/internal/booking/rules.go), Indian Railways 2015): more than
  48 h before departure, a flat charge (₹240 1A/EC, ₹200 2A, ₹180 3A/CC/3E, ₹120 SL, ₹60 2S);
  48–12 h, 25% of the fare; 12–4 h, 50%; after the chart, nothing (file a TDR). RAC/WL: ₹60 clerkage
  up to 30 minutes before departure. Confirmed Tatkal: no refund. Cancel the whole booking or chosen passengers.
- **Promotion engine** ([promote.go](services/core/internal/booking/promote.go)): whenever berths
  free up, RAC passengers get confirmed berths first (in RAC order), then waitlisted passengers get
  berths or RAC places (in WL order). It works from the current state, so it's idempotent and can be
  replayed. It runs inside the cancellation transaction, and also as a Redpanda consumer on expired
  holds and boarding changes. Tickets keep their booked status ("WL 23") next to the live one.
- **Ledger** ([internal/ledger](services/core/internal/ledger)): every money movement is a balanced
  double-entry transaction in paise, enforced by a database trigger. Wallet balances, refunds owed and
  money at the gateway are sums of entries. The Staff Portal shows the books, and they always reconcile:
  gateway = unearned fares + wallets + cancellation charges.
- **Refunds:** to the YatraX Pay wallet (instant) or the original payment (paid by the refund worker
  through the gateway's idempotent refund API, retried on failure). The wallet can pay for bookings.
- **Reconciliation** ([reconcile.go](services/core/internal/booking/reconcile.go)): every 30 seconds,
  payments still unresolved after 2 minutes are checked with the gateway. A payment whose redirect
  and webhook were both lost is found, and since the customer had given up, refunded automatically.
  A payment arriving within 15 minutes of the hold expiring still gets a berth if one is free.
- **Charts** ([chart.go](services/core/internal/booking/chart.go)): chart 1, 4 hours before departure,
  releases unsold quota berths to everyone, runs a final promotion, cancels fully-waitlisted tickets
  (fare refunded minus ₹60 per passenger) and publishes a snapshot for TTEs. Chart 2 follows 30 minutes
  before departure. After the chart only free berths are sold ("current booking").
- **TDR claims:** passengers file after the chart; admins approve with a refund or reject.
  **Boarding point** can be moved to a later station once, up to 24 hours before departure.
- **Notifications:** a Redpanda consumer turns events into in-app notifications (the header bell),
  deduplicated so replays never notify twice.
- **Consistency between search and booking:** the first time a train run is opened for a journey,
  bot passengers fill it to exactly the status search showed for that journey ("WL 45" stays WL 45),
  and the demand model sizes quotas from the real coach layout.

| Endpoint | |
|---|---|
| `GET /v1/bookings/{id}/cancel-quote?passengers=1,2` · `POST /cancel` | Price and cancel (`refund_to`: source or wallet) |
| `POST /v1/bookings/{id}/pay-wallet` · `/boarding` · `/tdr` | Pay from the wallet, change boarding point, file a TDR |
| `GET /v1/me/wallet` · `/v1/me/notifications` | Wallet with history, notifications |
| `GET /v1/charts?train&origin_date&version` | Reservation chart (TTE, station master, controller) |
| `/v1/admin/finance` · `/tdrs` · `/runs/chart` · `/runs/simulate-cancellations` · `/reconcile` | Operations (admin) |

## Tatkal and the waiting room (Phase 4)

- **Rooms:** real rooms open daily for tomorrow's journeys (AC at 10:00, non-AC at 11:00, joining
  from five minutes before); admins create drill rooms for any date. While a room's 30-minute rush is
  on, direct Tatkal bookings for that day are refused with `use_waiting_room`.
- **Fair queue (Redis):** everyone who joined before opening is shuffled with a cryptographic
  Fisher–Yates shuffle at the opening second; later arrivals join the back in arrival order (one
  atomic script, so a double-clicked Join can't take two places). A leader-elected loop advances an
  admission counter at the room's rate; position and wait estimates stream over a WebSocket.
- **Pre-filled bookings:** people save their journey and passengers while they wait; when admitted,
  one request books it. Saving warms the train run and Tatkal berths so the opening is fast.
  Background (bot) demand is topped up at most every 30 s per run, never on the hot path for every
  buyer, and never buys Tatkal while that day's rush is on.
- **Fast path:** Tatkal berths are rows claimed with `FOR UPDATE SKIP LOCKED`, so thousands of
  buyers never queue on one lock; the seat-allocation exclusion constraint still guarantees no berth
  is sold twice. At most 2 Tatkal bookings per user per day, 4 passengers each.
- **Bot detector:** scores automation user agents, implausibly fast forms, machine-regular typing,
  missing device fingerprints, many accounts per device or network, and queue polling. Score ≥ 75 is
  blocked; 40–74 must solve a proof-of-work (16 bits of SHA-256, about a second in a browser), which
  lets a travel agent with a few accounts through but makes running hundreds expensive.
- **Drill results (5,000 people, 30% bots, 5% agents, local Docker):** every bot blocked, 117 agent
  accounts verified, Tatkal SL on six trains sold out in 39–48 s, booking p50 45 ms / p99 2 s, zero
  double bookings, nothing charged without a ticket. k6 (300 browsers): status p95 6 ms, booking p99 53 ms.

| Endpoint | |
|---|---|
| `GET /v1/waitrooms` · `GET /v1/waitrooms/{id}/ws?access_token=` | Rooms; live position over WebSocket |
| `POST /v1/waitrooms/{id}/join` · `PUT /draft` · `POST /challenge` · `GET /status` · `POST /book` | Join, save booking, verify, poll, book |
| `POST /v1/admin/waitrooms/drill` · `GET /{id}/monitor` · `GET /{id}/bots` · `GET /v1/admin/integrity` | Drills, live monitor, flagged accounts, invariants |

Load tools: `services/core/cmd/botswarm` (humans, bot farms and agents against a drill room, with an
integrity check) and `scripts/k6/waitroom.js` (`k6 run -e USERS=1000 scripts/k6/waitroom.js`).

## Live trains and the simulation clock (Phase 5)

- **Simulation clock** ([internal/simclock](services/core/internal/simclock)): one row in Postgres, changes
  announced on Redis, so every instance agrees without a tick loop. Controllers run it at 1×, 10×, 60×,
  144× (a day in ten minutes) or 600×, pause it, or jump to any moment. Train movement, live status and
  alerts follow it; bookings, holds, payments and Tatkal stay on real time (a hold is still 15 minutes).
- **Movement and delays** ([internal/live](services/core/internal/live)): a run's position is a pure function
  of (train, origin date, simulated time) plus controller holds, so any instance computes the same answer.
  Delays come from a seeded model: a late rake at the origin now and then, small run-time noise,
  occasional incidents (signal failure, a crossing, chain pulling, a weak bridge), winter fog across the
  north at night, the recovery margin timetables carry near the end, and late trains cutting halts to two
  minutes. On a typical afternoon about 1,500 trains are running and roughly one in five is 15+ minutes late.
  Stations occasionally move a train to another platform, announced 20 minutes before it arrives.
- **Alerts:** a leader-elected watcher (Redis lock) follows only runs that carry passengers' confirmed
  bookings and emits `train.departed`, `train.delayed` (when the delay crosses 15/30/60/120/180/300 min),
  `platform.changed`, `train.approaching` (30 minutes out; at the origin, a boarding reminder) and
  `train.arrived` through the outbox to Redpanda. The notifications consumer tells each passenger only
  what concerns their own stations (deduplicated), and delay and platform alerts also go to the mock SMS log.
- **Live screens:** the passenger app's **Live** page (running status with scheduled vs expected times,
  platforms, the train's position, live events) and station boards; a live card on every confirmed
  booking; the Control Centre's live map of every running train coloured by delay, most-delayed list,
  train desk with **Hold train**, and the clock controls. One WebSocket endpoint streams a run every
  second (plus its events from Redis pub/sub) or the whole map every two seconds; a Grafana row shows
  trains running, punctuality, clock speed and alerts.

| Endpoint | |
|---|---|
| `GET /v1/sim/clock` · `POST /v1/admin/sim/clock` | Read the railway clock; controllers set speed, pause, jump or reset |
| `GET /v1/live/trains/{number}?date=` | A run's live status (default: the run that matters now) |
| `GET /v1/live/map?zone=&late=1` · `GET /v1/live/stations/{code}?hours=4` | Every running train; a station's arrivals and departures |
| `GET /v1/live/ws?train=&date=` · `?map=1&zone=` | WebSocket: one run every second (with events), or the map every two seconds |
| `POST /v1/admin/live/incidents` · `GET` | Hold a train at a station (controllers); the log of holds |

## TTE app (Phase 6)

An Expo (React Native) app in [apps/tte](apps/tte), offline-first. It runs on a phone through Expo Go
(`pnpm --filter @yatrax/tte start`, with `EXPO_PUBLIC_API_URL=http://<your computer's LAN IP>:8080`) and
in the browser (port 3005) for development.

- **Offline bundle:** before departure the TTE downloads a run: every passenger on every berth (real and
  simulated), the coach layouts, the stops, a fare table for every stop pair and class, and the Ed25519
  public key. It's kept in AsyncStorage with the session, so the app works with no signal and across restarts.
- **On the phone, with no network:** QR tickets are verified locally (the signature, then the PNR against
  the chart); passengers are marked boarded; a confirmed passenger who hasn't turned up can be marked a
  no-show once the train has passed **two stations** beyond their boarding point, and the berth goes to the
  lowest-numbered RAC passenger whose remaining journey fits; RAC passengers can be given any berth free for
  the rest of their trip; excess-fare tickets (ticketless, wrong class, beyond destination: fare + ₹250) are
  priced from the bundle's fare table, optionally with a vacant berth; complaints and the chart hand-off at a
  crew-change station are recorded.
- **Sync:** each action is an operation with a UUID made on the phone and the time it happened. The server
  applies them in that order, one transaction each, recording every op, so a retried sync changes nothing.
  **The server's allocation wins:** if two phones offered the same RAC passenger a berth, or a berth was
  taken meanwhile, the server picks the next eligible passenger or another free berth and reports the
  operation as `resolved`. The phone then replaces its view with the server's fresh bundle. A RAC passenger
  moved on board gets a `passenger.promoted` notification.
- **Money:** an excess-fare ticket posts to the ledger as cash held by the TTE (`asset:tte_cash:<id>`)
  against fares and `revenue:penalties`, so the trial balance stays zero; a berth allotted with it becomes
  an on-board booking with its own PNR.

| Endpoint | |
|---|---|
| `GET /v1/tte/duties` | Trains calling at the TTE's station in the next day and a half (simulated time) |
| `GET /v1/tte/runs/{train}/{origin_date}` | The offline bundle |
| `POST /v1/tte/runs/{train}/{origin_date}/sync` | `{ops: [...]}` → per-operation results and a fresh bundle |

## Station app (Phase 7)

- **Boards:** arrivals and departures in the classic yellow-on-black look, from the live engine (late
  trains in red, arriving trains flashing, platform changes highlighted). `/board/{code}` is a public,
  full-screen display for a concourse screen; the station master sees the same board on their desk.
- **Announcements** ([internal/station](services/core/internal/station)): generated from the live board for
  trains arriving, standing, about to leave, running 15+ minutes late (again each time they slip another 15),
  and platform changes. Each has a stable id, so screens play it once. Text comes in the station's regional
  language, Hindi and English (templates for English, Hindi, Tamil and Malayalam so far; stations in other
  language regions get Hindi and English and the API says which template is missing — these were written
  without native-speaker review). The browser speaks them after a three-note chime, with train numbers read
  digit by digit; a language plays only if the device has a voice for it, otherwise its text is shown.
- **Platform chart:** the next four hours on every platform (arrival to departure; starting trains placed
  20 minutes early, terminating trains clearing 15 minutes after), **clashes** with a free platform suggested
  and fixed in one click, and the **crowd** hour by hour from reserved passengers boarding and alighting (from
  real bookings, or estimated from capacity for runs nobody has booked yet) plus unreserved travellers.
- **Reassigning a platform** writes `platform_assignments`; the live engine applies it at once, so the board,
  the announcements ("will now arrive on platform 1 instead of platform 4"), the train's live status and the
  passengers' "platform changed" alerts all follow. Station masters manage only their own station.
- **Coach position:** the rake centred on the platform's 26 position markers, engine first and facing the
  direction of travel; passengers see "coach S3 stops at position 12" on their booking's live card.
- **Facilities desk:** retiring rooms and cloak-room lockers (sized by the station, occupancy simulated),
  lost and found (log, return once), wheelchair requests (requested → attendant assigned → done).

| Endpoint | |
|---|---|
| `GET /v1/stations/{code}/announcements` · `GET /v1/stations/{code}/coach-position/{train}?date=` | Public |
| `GET /v1/station-master/{code}/platforms?hours=` · `POST …/platforms` | Platform chart; assign (`platform: 0` = timetable) |
| `GET …/facilities` · `POST …/lost-found` · `POST …/lost-found/{id}/return` · `POST …/wheelchair[/{id}]` | Facilities desk |

## Control centre (Phase 8)

- **Section chart** ([internal/control](services/core/internal/control)): the railway's time-distance
  "master chart" for any two stations — the section is the stops of the train with the most halts between
  them; every train crossing it is drawn from an hour ago to four hours ahead, expected path solid (coloured
  by delay and direction), timetable dashed, its position now marked; it refreshes every five seconds.
- **What-if and delay ripple:** hold a train (hypothetically) at a station and a small discrete-event model
  spreads the delay: a train due on the same platform after it waits until it clears (+2 min), a train
  following it to its next stop keeps a 5-minute headway, and each delayed train can delay others, up to
  three hops. The result lists every knock-on train with the cause and how many booked passengers are on
  board; **Apply this hold** turns it into a real one. (No single-line crossings or platform swaps yet: it
  shows the worst case.)
- **Platform conflicts** across the major stations (or one zone), with the free platform to suggest to the
  station master; **longest waitlists** before the chart.
- **Incidents:** cancel, short-terminate at a station, or divert around stations (with the extra minutes of
  the longer route). Everyone whose journey it breaks is refunded — in full, or for the distance not
  travelled when a train they're on is cut short — through the ledger like any cancellation; each fully
  refunded booking gets seats **held for two hours** on the nearest other train for the same journey (one
  with berths if there is one); and a notification (plus mock SMS) says what happened, the refund and the
  held seats. The live engine shows the run cancelled, terminating early, or skipping stations (with the
  added time); boards show it; new bookings for the broken part of the run are refused.
- **Extra coaches:** add up to four coaches of a class to one run before its chart; the run gets its own
  rake (the booking core, seat map, TTE bundle and charts all use it) and the promotion engine moves
  waitlisted and RAC passengers onto the new berths at once, each told by the usual alert.

| Endpoint | |
|---|---|
| `GET /v1/control/section?from=&to=&hours=` | Section chart |
| `POST /v1/control/what-if` | `{train, origin_date, station, delay_min}` → the ripple; nothing is saved |
| `GET /v1/control/conflicts?zone=&hours=` · `GET /v1/control/waitlists?days=` | Network views |
| `POST /v1/control/runs/disrupt` · `GET /v1/control/disruptions` | Cancel, short-terminate or divert; the log |
| `POST /v1/control/runs/extra-coaches` | `{train, origin_date, class, count}` → coaches and promotions |

## Catering (Phase 9)

- **Where you can eat** ([internal/catering](services/core/internal/catering)): every stop on the booked
  journey with a halt of at least 2 minutes and vendors, with the live ETA. Orders close at the longest
  dish's prep time + 15 minutes before arrival, and once the train is there.
- **Menus:** each vendor gets a starter menu from their cuisines (veg-only outlets get no meat or egg),
  marked veg / Jain / egg / non-veg; vendors add dishes, change prices and prep times, and mark items sold out.
- **Orders:** paid from the YatraX Pay wallet (held in escrow) or cash on delivery; delivered to the coach
  and berth. placed → accepted → preparing → handed over → delivered; vendors can reject with a reason;
  passengers can cancel until cooking starts. Each step notifies the passenger.
- **Timed by the train:** a leader-elected ticker (every 20 s) follows each open order's train. When the ETA
  moves by 10+ minutes the passenger is told and the vendor's "start cooking by" (ETA − prep − 10 min)
  shifts; if the stop is cancelled or skipped, or the train leaves before delivery, the order closes and a
  wallet payment is refunded.
- **Money:** on delivery the escrow goes to the vendor less 12% commission; for cash orders the vendor owes
  the commission. Payouts send the vendor's balance to their bank (simulated UTR). All through the ledger.
- **Ratings:** 1–5 stars and a review after delivery; the vendor's rating becomes the average once there
  are three; a hygiene complaint goes to the railway's complaints queue.

| Endpoint | |
|---|---|
| `GET /v1/bookings/{id}/food` · `GET /v1/vendors/{id}/menu` | Stops with vendors; a menu |
| `POST /v1/food-orders` · `GET /v1/food-orders?booking_id=` | Order; my orders |
| `POST /v1/food-orders/{id}/cancel` · `POST …/rate` | Cancel before cooking; rate (`hygiene_complaint`) |
| `GET /v1/vendor/orders[?closed=1]` · `POST /v1/vendor/orders/{id}/status` | Vendor: orders; advance or reject |
| `GET/POST /v1/vendor/menu` · `PATCH /v1/vendor/menu/{item}` | Vendor: menu |
| `GET/POST /v1/vendor/payouts` | Vendor: ledger; pay out the balance |

## Dashboards & analytics (Phase 11)

- **Scope** ([internal/analytics](services/core/internal/analytics)): all India, a zone, or a division. A
  train belongs to the zone and division of the station it starts from, as on Indian Railways. Admins and
  controllers see every zone; station staff and TTEs see their own zone (any division in it).
- **Revenue:** fares, refunds and net; passengers, average fare, share given a berth at booking,
  cancellations, Tatkal share, TTE excess fares, food sales and commission, complaints; demand by travel
  date; splits by class and quota; top trains and journeys. One grouped query feeds every split.
- **Occupancy:** for runs leaving in the next 7/14/30 days, the share of berth-segments sold (a berth sold
  Chennai–Salem and again Salem–Ernakulam counts as full), from the seat allocations against each run's rake
  including extra coaches; a train × date heatmap, by class, the fullest runs and their waitlists. Runs
  only exist once someone has searched or booked them, so most of the network shows "not open yet".
- **Punctuality:** past runs replayed through the same delay model the live status uses (with injected
  holds, cancellations, short terminations and diversions); on time = at most 15 min late at the
  destination. Zone league table, divisions, day by day, least and most punctual trains, and why late
  trains were late (where each lost the most time). Runs still on their way are left out.
- **Fraud report** (admins): the Tatkal bot detector's verdicts and reasons, networks and devices shared by
  3+ accounts, possible touts (the same traveller booked on Tatkal from 3+ accounts), heavy Tatkal bookers,
  frequent cancellers, ticketless hot spots, cash collected by each TTE, catering vendors to inspect, and
  unresolved payments. Leads, not proof.

| Endpoint | |
|---|---|
| `GET /v1/analytics/zones` | Zones and divisions; the zone the user is held to |
| `GET /v1/analytics/overview?zone=&division=` | Revenue and demand |
| `GET /v1/analytics/occupancy?zone=&division=&days=` | Occupancy by run (1–60 days ahead) |
| `GET /v1/analytics/punctuality?zone=&division=&days=` | Punctuality (1–30 days back) |
| `GET /v1/analytics/fraud` | Fraud report (admins) |

## More for passengers and staff

The rest of the plan's feature list (§3 and §6), apart from the AI layer.

- **Split journeys and connections** (search results, when no direct train has berths in the class): the
  same train on two tickets changing berth on the way, or two trains through a major junction with 40 min
  to 6 h to change; junctions off the route are skipped. `GET /v1/search/alternatives?from=&to=&date=&class=`.
- **Confirmation guarantee** (`/guarantee`): hold the trip on two waitlisted trains; when one is fully
  confirmed (by a cancellation or at the chart), the other is cancelled with a full refund, no clerkage.
  Refused when the first choice already has berths. `POST /v1/bookings/guarantee`.
- **Special trains** (Control Centre → *Special trains*): ten busy trains have a 0-numbered special (e.g.
  02623 Trivandrum Mail Special) on the same route two hours behind; control picks the dates and it opens for
  booking at once, on every instance. `GET/POST /v1/control/specials`.
- **Free auto-upgrade** (opt-in when booking): at chart time whole parties move up a class (SL→3A, 3A→2A,
  2A→1A, CC→EC…) into berths free for their whole journey, and the berths they leave go to the waitlist.
- **More quotas:** HP (divyangjan: UDID card, one escort, 75% off the base fare in sleeper and chair cars,
  50% in AC sleepers), DF (defence warrant) and FT (foreign tourists: passport) — two berths each in the
  last coach. ID numbers are shown masked.
- **Travel insurance** (₹1 per passenger, cover ₹10 lakh — a whole-rupee stand-in for the real 45 paise):
  kept by the insurer once the ticket confirms (`liability:insurer`), not refunded on cancellation.
- **GST invoice** (`/booking/{id}/invoice`, printable): SAC 996421, CGST+SGST or IGST by place of supply,
  with a synthetic GSTIN — a demonstration, not a tax document. `GET /v1/bookings/{id}/invoice`.
- **Loyalty points:** 1 point per ₹100 of fare, taken back if the whole ticket is cancelled; 100 points
  = ₹25 of wallet credit (funded from `expense:loyalty`). `GET /v1/me/loyalty`, `POST /v1/me/loyalty/redeem`.
- **Carbon saved** on every ticket: about 12 g CO₂ per passenger-km by train against 174 g flying.
- **RailMadad complaints** (booking page → *Help on board*; Station app and Control Centre queues): routed
  by rule to housekeeping, catering, RPF, medical, electrical or mechanical at the next halt, with
  priorities (medical and security are urgent); staff acknowledge and resolve, the passenger is told, and the
  dashboards show volumes and average time to acknowledge and resolve.
- **SOS:** a medical emergency, a security alert to the RPF, or a female TTE — the nearest one on duty in
  the zone is asked to come to the passenger's berth and notified.
- **Retiring rooms, dormitory beds and cloak-room lockers** (booking page): booked against a ticket to or
  from the station, around the journey; pay from the wallet or at the counter; the Station app checks guests
  in (collecting counter payments) and out, and the facilities panel counts real occupancy.
- **Group booking desk** (`/groups`): 7–60 passengers for a wedding or pilgrimage; the boarding station's
  desk approves and the berths are held together, in parties of up to six, for 48 hours.
- **Large text:** a text-size control in every header (100 / 112.5 / 125%).

| Endpoint | |
|---|---|
| `POST /v1/bookings/{id}/complaints` · `POST /v1/bookings/{id}/sos` · `GET /v1/me/complaints` | Passenger |
| `GET /v1/complaints` · `POST /v1/complaints/{id}` | Staff queue; `{action: acknowledge or resolve}` |
| `GET /v1/stations/{code}/stays` · `POST /v1/stays` · `GET /v1/me/stays` · `POST /v1/stays/{id}/cancel` | Rooms and lockers |
| `GET /v1/station-master/{code}/stays` · `POST …/stays/{id}` | The counter: check in / out |
| `POST /v1/groups` · `GET /v1/me/groups` · `GET /v1/group-desk` · `POST /v1/group-desk/{id}` | Group desk |

## Proved, not claimed

| Claim (plan §8) | How it's shown | Result |
|---|---|---|
| No double bookings across 1M attempts | `go run ./cmd/stress -attempts 1000000`: a million holds and releases from 48 goroutines straight into the booking core, on 40 busy runs (each passenger cycling through the three-unpaid-holds limit, so nearly every attempt really allocates), then every allocation checked against every other | 0 overlapping allocations, 0 unexpected errors: 233,448 holds got berths, 183,026 joined the RAC/waitlist, 168,081 were refused as full and 415,445 holds were released and resold, in 1 h 25 min (about 200 attempts/s on a laptop) |
| No "paid but not booked" | reconciliation and the gateway chaos drill; `/v1/admin/integrity` | 0 after reconciliation |
| Tatkal: thousands at once | `go run ./cmd/botswarm -users 5000` (CI runs 500) | fair queue, bots blocked, no berth sold twice |
| Each chaos failure recovers on its own | `smoke_phase12.py` | gateway ~2 min, database ~2 s, event bus < 1 s, breakdown ~2 s |
| The books balance | every smoke test ends on the ledger's trial balance | ₹0 |
| No data races | `go test -race ./...` in CI | clean |

PNR-prediction accuracy (also in §8) belonged to the AI layer, which was left out.

## Chaos mode and the digital twin (Phase 12)

- **Chaos mode** ([internal/chaos](services/core/internal/chaos), Control Centre → *Chaos mode*): each drill
  injects a real fault and only watches; nothing is repaired by hand. Each run keeps a timeline and how long
  recovery took.

  | Drill | What breaks | What heals it | Typical recovery |
  |---|---|---|---|
  | Payment gateway loses its callbacks | 3 passengers pay; no redirect, no webhook | the reconciler finds the captured payments and confirms the tickets | ~2 min (payments count as lost after 2 min) |
  | Database failover | every Postgres connection cut at once | pgx pools reconnect; workers carry on | ~2 s |
  | Event bus down | publishing stops for 20 s while events keep coming | the outbox holds them; all drain in order | < 1 s after the bus returns |
  | Train breakdown | a running train loses 90 min before its next stop | live status recalculates; the watcher announces the delay | ~2 s |

  The breakdown is a new kind of incident (`en_route`): time lost on the way in delays the arrival, not just
  the departure. The control room can ask the watcher to follow any run (`live:watch`), not only runs with
  app passengers.
- **Digital twin** (Control Centre → *Replay a day*): pick a past day, play it from 05:00 at 144× or 600×,
  and the map, section chart and boards show it as it ran — the delay model is deterministic for a run and
  its recorded incidents (kept for 35 days). The day's timeline (holds, breakdowns, disruptions, platform
  changes, the worst late arrivals) jumps the clock to each event; *Back to live* returns.
- **Accessibility:** skip links and a single page heading on every screen; a visible focus ring everywhere;
  `prefers-reduced-motion` honoured; every input, select and icon button named; chart marks carry text
  labels as well as hover tooltips; muted text, badges and chart labels meet WCAG AA contrast (4.5:1); the
  display board has labelled tables; TTE app buttons announce disabled and busy states.

| Endpoint | |
|---|---|
| `GET /v1/chaos/drills` · `GET /v1/chaos/runs[/{id}]` | Drills; the log with timelines (controllers, admins) |
| `POST /v1/chaos/runs` | `{kind}` → starts a drill (one at a time) |
| `GET /v1/control/replay?date=` | A past day's punctuality and events |

## Architecture

```mermaid
flowchart LR
  subgraph clients["Apps"]
    P["Passenger :3000"]
    ST["Station :3001"]
    C["Control Centre :3002"]
    D["Staff Portal :3003"]
    V["Catering :3004"]
    T["TTE app (Expo) :3005<br/>offline-first"]
  end

  subgraph core["core (Go, :8080)"]
    API["HTTP API + WebSockets<br/>/v1, /live/ws, /waitrooms/{id}/ws"]
    BK["booking: holds, payments,<br/>cancel, promote, chart, TTE sync"]
    LV["live: delay model, watcher,<br/>simulation clock"]
    OPS["station · control · catering<br/>analytics · chaos"]
    WR["waitroom + bot detector"]
    LG["ledger (double entry)"]
    OB["outbox relay"]
    RC["reconciler"]
  end

  PG[("Postgres 16<br/>bookings, seat ranges (GiST),<br/>ledger, outbox")]
  RD[("Redis 7<br/>OTP, queues, leader locks,<br/>live screens")]
  RP{{"Redpanda<br/>booking / payment / train events"}}
  GW["YatraPay mock gateway :8081"]
  PR["Prometheus → Grafana"]

  clients -->|JSON + WebSocket| API
  API --> BK & LV & OPS & WR
  BK --> LG
  BK & LV & OPS --> PG
  WR & LV --> RD
  OB -->|publish| RP
  RP -->|consume: promotions, notifications| BK
  BK <-->|orders, refunds| GW
  GW -->|webhooks| API
  RC -->|settle lost payments| GW
  core -->|/metrics| PR
```

- **One write, many effects:** a booking change and its event are written in the same Postgres transaction
  (the outbox); the relay publishes to Redpanda, and consumers (the promotion engine, notifications) dedupe,
  so events can repeat but are never lost.
- **No double bookings:** each berth's journey is a segment range with a GiST exclusion constraint; a
  per-run-and-class advisory lock orders allocations.
- **Money:** every rupee moves through a double-entry ledger (fares, refunds, wallets, TTE cash, catering
  escrow); a deferred trigger rejects unbalanced transactions, and the reconciler settles payments whose
  callbacks were lost.
- **Railway time:** the simulation clock drives trains, live status and alerts; bookings, holds and payments
  stay on real time. The live model is deterministic per run, which is what makes the digital twin possible.
- **Leaders:** the live watcher, the catering ticker and the reconciler take Redis leader locks, so several
  core instances can run side by side.

**Auth:** short-lived HS256 access tokens (15 min) and opaque refresh tokens (30 days),
stored only as SHA-256 hashes. Refresh tokens rotate on every use. If a rotated token is
used again, the whole session family is revoked. Passengers sign in with phone + OTP
(5-minute codes, at most 5 attempts, at most 3 sends per 10 minutes). Staff sign in with
employee ID + bcrypt password. Role checks run on the server (`RequireRole`). Sign-ins
are written to `audit_log` and published to Redpanda.

## Layout

```
apps/            passenger, station, control, dashboards, vendor (Next.js 15); tte (Expo)
packages/ui          shared components, theme tokens, session handling, sign-in screen
packages/api-client  typed client for the core API (auto-refreshes tokens)
services/core        Go: cmd/core (API), cmd/payments (mock gateway), cmd/seed, cmd/botswarm (Tatkal load drill)
  internal/            booking, ledger, live, simclock, waitroom, station, control, catering, analytics,
                       chaos, search, fare, inventory, network, seed, events, auth, httpapi, …
infra/               Prometheus scrape config, Grafana provisioning and dashboards
scripts/             smoke tests (one per phase), network import (build_network.py); k6/ load tests
docs/                DEMO.md: the demo script and shot list
```

## Development

```bash
pnpm typecheck                         # all TS packages
pnpm build                             # production build of every app
cd services/core && go test ./...      # Go tests; booking tests use the compose Postgres (port 5433)
cd services/core && go run ./cmd/stress -attempts 1000000   # the million-attempt double-booking check (own database)
docker compose up -d --build core      # rebuild the API after Go changes
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, the Go tests with the race detector, the web typecheck and
build, and the Docker stack with the smoke tests for every phase, the extras, and a 500-person Tatkal drill.
On Windows the race detector needs a C toolchain, so run it in the Go image (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm --network yatrax_default -v "$PWD/services/core:/src" -w /src -e CGO_ENABLED=1 -e TEST_DATABASE_ADMIN_URL=postgres://yatrax:yatrax@postgres:5432/postgres?sslmode=disable golang:1.27-alpine sh -c "apk add --no-cache gcc musl-dev && go test -race ./..."
```

## Demo

[docs/DEMO.md](docs/DEMO.md) is a ten-minute walkthrough with a shot list for recording a video.

# YatraX — Full Feature Plan & Phases

> India's railway booking platform, properly built. 7 apps, one realistic railway organisation, live bots, ~95% real on a laptop, synthetic data only.

---

## 0. Guiding principles

1. **Correctness first.** No double sales, no charge without a ticket, no lost refunds. Every money and seat change is an event with an audit trail.
2. **Feels alive.** Bot passengers, live-running trains and a simulation clock (run 1 day in 10 minutes) keep every screen moving.
3. **Real, not faked.** Real concurrency, real queues, real ML models trained on synthetic data. Where things are simulated (payment gateway, GPS), the simulation is honest and swappable.
4. **Built phase by phase.** You test each phase before the next starts. Each phase ends in a demo you can show.
5. **Not Ride Pulse, not MediMind.** No map-dispatch or SOS core, no form-heavy AI. The core here is **inventory, fairness, and operations**.

---

## 1. Tech stack (final proposal)

| Layer | Choice | Why |
|---|---|---|
| Monorepo | pnpm + Turborepo | 7 apps share types and UI |
| Web apps | Next.js 15 + TypeScript + Tailwind + shadcn/ui | passenger, station, control, dashboards, vendor |
| Mobile | Expo (React Native) | TTE app (offline-first), passenger app later |
| Booking core | **Go** (chi + pgx) | fast and good at concurrency; the Tatkal path needs it |
| Other services | Node (NestJS) or Go | catering, notifications, complaints |
| Database | PostgreSQL 16 | row locks, SERIALIZABLE, exclusion constraints on ranges |
| Cache / queue | Redis 7 | waiting room, seat holds (TTL), rate limits, idempotency keys |
| Event bus | Redpanda (Kafka API) | booking, cancellation and train-movement events |
| Realtime | WebSocket gateway (Go) + Redis pub/sub | live status, queue position, control room |
| ML | Python FastAPI + XGBoost / Prophet | PNR prediction, festival demand |
| LLM | Claude API (tool use) | trip-planner agent, complaint triage, announcements |
| Speech | Browser TTS / Piper TTS | multilingual station announcements |
| Payments | Mock gateway service (Razorpay-shaped) | failure, timeout and late-webhook modes for testing |
| Load / bots | k6 + custom Go bot swarm | 5,000 Tatkal bots |
| Observability | Prometheus + Grafana + Loki + OpenTelemetry | the control room's tech wall |
| Infra | Docker Compose (local), GitHub Actions CI | one command starts everything |
| Auth | Own JWT + refresh tokens, OTP (mock SMS), roles | passenger / TTE / station / vendor / controller / admin |

---

## 2. Data world (synthetic but believable)

- **Network:** about 300 real-named stations (codes like MAS, SA, ERS, NDLS, CSMT), 17 zones and their divisions, and an ordered list of stops for each route.
- **Trains:** about 120 trains (Rajdhani, Vande Bharat, Shatabdi, Mail/Express, Passenger) with running days, timings, distance and halts.
- **Coaches:** realistic layouts: 1A, 2A, 3A, 3E, SL, CC, EC, 2S. Berth types LB/MB/UB/SL/SU, coach numbering (B1, S4, A1), and pantry cars.
- **Fares:** distance slabs, class multipliers, reservation charge, superfast surcharge, GST, Tatkal premium, and dynamic fare (Flexi) on premium trains.
- **People:** 50k synthetic passengers (Indian names, ages, languages), staff rosters (TTEs, station masters, controllers), and vendors.
- **History:** 2 years of synthetic booking and cancellation history, with festival spikes, used to train the ML models.

---

## 3. The 7 apps — full features

### 3.1 Passenger app (web, later mobile)
**Search & discovery**
- Search by from/to/date, including nearby stations ("all Chennai stations") and flexible dates (±3 days grid)
- Filters: class, quota, departure time, train type, duration; sort by fastest, cheapest or chance of confirmation
- Availability calendar with colours: AVL / RAC / WL / REGRET
- **Alternate route suggestions:** split journeys and connecting trains when direct trains are full
- Train schedule, coach layout and amenities per train

**Booking**
- Up to 6 passengers per booking (4 for Tatkal), plus a saved master passenger list
- Berth preference, seated together, auto-upgrade opt-in, and "book only if confirmed"
- Quotas: GN, TQ, PT (Premium Tatkal), LD (ladies), SS (senior), HP (divyang), DF (defence), and a foreign tourist quota
- **Seat map picker** (a feature IRCTC lacks) for classes where it's allowed
- 10-minute seat hold with a countdown, then payment (UPI, card, wallet) on the mock gateway
- Wallet (YatraX Pay) for instant refunds
- Travel insurance opt-in, GST invoice

**After booking**
- E-ticket with a signed QR code (works offline) and PNR status
- **PNR prediction:** "WL 23 → 87% chance to confirm", with the reason
- Cancellation (full or partial), refund calculated by the rules, and a TDR filing flow
- Change boarding point
- **Live train status:** current station, delay, ETA at your station, and platform number
- Coach position at your platform ("B3 stops near pillar 14")
- Food to your seat (orders go to the catering app)
- Push and SMS alerts: chart prepared, berth confirmed, platform changed, train delayed
- Trip timeline: one screen for the whole journey

**Extras**
- Trip planner agent (Claude) in English, Hindi, Tamil, Malayalam, Telugu
- Accessibility: wheelchair request, large text, screen reader support
- Lost & found, retiring rooms, and cloak room booking at stations
- Loyalty points that power fare discounts

### 3.2 Tatkal waiting room
- Opens 09:55; at 10:00:00 everyone in the room is shuffled and assigned a **fair random position** (no advantage from refreshing early)
- Live queue position and estimated wait over WebSocket
- **Pre-filled booking token:** passenger details are entered while waiting, so the booking itself takes seconds
- Bot and agent detection: rate signals, device fingerprint, typing and timing patterns, and too many accounts per payment method or IP. Suspects are flagged, given a challenge, or blocked
- Per-user limits: 2 Tatkal bookings per user ID per day
- Admin view: queue depth, admission rate, bot-block count, and a sell-out timer per train

### 3.3 TTE app (Expo, offline-first)
- Downloads the train chart before departure and works with no network
- Scan QR → verify signature → mark boarded
- Mark no-shows. After 2 stations, release the berth and move the next RAC passenger onto it, then sync when back online
- Current-booking and on-board berth allocation
- Fines and excess-fare tickets (ticketless travel, wrong class), with a receipt
- Report a complaint or incident (feeds RailMadad triage)
- Hand-off of the chart to the next TTE at a crew change station

### 3.4 Station app
- Arrival and departure display boards (the classic yellow-on-black look) with a live platform view
- **Multilingual announcements:** generated text, then TTS in the regional language, Hindi and English ("Kripya dhyan dein…")
- Coach position display per platform
- Station master view: assign platforms, see clashes, crowd estimate
- Facilities: retiring rooms, cloak room, lost & found, wheelchair desk

### 3.5 Catering vendor app
- Vendor onboarding and menu (veg/non-veg, Jain, regional dishes)
- Orders arrive based on the train's **live ETA** at the vendor's station
- Order flow: accept → prepare → handed to delivery staff → delivered to coach and berth
- Cut-off rules: no orders when the train is too close or the halt is too short
- Ratings, hygiene complaints, and payout ledger

### 3.6 Railway control centre (the commander room)
- **Live section chart:** a time-distance graph (the real railway "master chart") with every train moving in real time
- Delay ripple: one train late, and the system shows which trains it will block (single-line sections, crossings, platform occupancy)
- **Platform conflict detector** with suggested fixes
- What-if simulator: "hold 12623 for 10 min. What happens?"
- Incident handling: breakdown, signal failure, weather. Divert, cancel or short-terminate a train, then auto-notify and auto-refund affected passengers
- Add extra coaches or a special train when the WL is long (ties into the demand forecast)
- Tech wall: booking rate, queue depth, payment success, error rates (Grafana)

### 3.7 Zone and division dashboards
- Revenue by zone, division, class and quota
- Occupancy heatmap: trains × dates
- Punctuality league table
- Complaint stats (RailMadad) with resolution time
- Festival forecast vs actual
- Refund backlog and failed-payment recovery
- Fraud and bot report

---

## 4. Hard engineering problems (and the plan for each)

| Problem | Approach |
|---|---|
| **Segment seat allocation** | Each berth stores which stop-segments are booked (a bitmask over the route's stops). Allocation = find a berth whose mask has no overlap with the requested segment, preferring the best fit so berths aren't left with small useless gaps. Postgres `int8range` + exclusion constraint as a safety net. |
| **Quotas** | Seat inventory split per quota per train-date. Unsold quota seats are released to GN at fixed times (e.g., at chart preparation). |
| **WL → RAC → CNF** | Event-driven promotion engine: a cancellation event frees segment(s), then the engine promotes the earliest RAC/WL entries that fit the freed segments. Runs in one transaction, is idempotent, and is replayable. |
| **Chart preparation** | Job runs 4 h before departure: final allocation, unconfirmed WL auto-cancelled and refunded, chart published to the TTE app. A second chart 30 min before departure covers current booking. |
| **Tatkal stampede** | Waiting room admits users at a controlled rate. Seat holds in Redis with TTL, final commit in Postgres with `SELECT … FOR UPDATE SKIP LOCKED`, idempotency key per booking attempt. |
| **No charge without ticket** | Saga: hold seat → create payment → confirm on webhook. Reconciliation job every minute: paid but not booked → auto-refund within minutes; booked but unpaid → release seat. |
| **Delay ripple** | Graph of sections, blocks and platforms. Discrete-event simulation predicts knock-on delays. |
| **Offline TTE sync** | Each change is a separate operation with a timestamp. Server resolves conflicts (server-side allocation always wins). |
| **Scale on a laptop** | Target: 5,000 concurrent bots, >500 bookings/s on the booking core, p99 < 300 ms, measured and shown live. |

---

## 5. AI / ML features

1. **PNR confirmation prediction:** XGBoost on synthetic history (WL number, days to journey, class, quota, route, festival flag, past cancellation rate). Shows the % chance and the top reasons.
2. **Trip-planner agent (Claude):** "Get me to Kochi from Chennai for Onam, cheapest, sleeper, with my mother (68)." It searches, compares, applies the senior quota, suggests alternatives, and books **only after you confirm** on screen. Works in 5 languages, plus voice input.
3. **Complaint triage:** sorts a complaint by category (cleanliness, food, security, medical, staff), urgency and the right department. Medical or security complaints alert the next station and the TTE.
4. **Festival demand forecast:** Prophet/ML per route, with Indian holiday calendars. Recommends special trains and extra coaches in the control room.
5. **Bot detection model:** anomaly scoring on the Tatkal request stream.
6. **Announcement generator:** writes the standard announcement text in each language, then hands it to TTS.
7. **Dynamic pricing advisor** (Flexi fare trains): suggests a price from demand.
8. **Delay prediction:** predicts the train's ETA from the live running data.

---

## 6. Enhancements beyond IRCTC (stand-out features)

- **Seat map booking** and "book together" for families
- **Smart alternatives:** "WL is high. Here's a confirmed split journey: Chennai→Coimbatore→Kochi"
- **Auto-rebook on cancellation:** when a train is cancelled, offer one-tap rebooking on the next train
- **Confirmation-or-refund guarantee mode:** books WL on 2 trains and auto-cancels the loser, with the refund
- **Group booking desk** for weddings and pilgrimages
- **Women-safety features:** ladies' quota, nearest female TTE, alert to RPF (simple, not the Ride Pulse SOS system)
- **Carbon saved vs flying** on each ticket
- **Digital twin replay:** replay any day in the control room
- **Chaos mode:** inject failures (payment gateway down, DB failover, train breakdown) live in the demo and watch the system recover

---

## 7. Phases

Each phase = a working, testable demo. You test before we move on.

### Phase 0 — Foundation (≈1 week)
Monorepo, Docker Compose (Postgres, Redis, Redpanda, Grafana), CI, auth with roles, a shared UI kit, and the **synthetic data generator** (stations, trains, coaches, fares, people).
**Demo:** `docker compose up` → seeded network, log in as each role.

### Phase 1 — Search & schedule (≈1 week)
Train search, schedules, fare engine, availability calendar (read-only), coach layouts.
**Demo:** search MAS→ERS and see real-looking trains, fares and classes.

### Phase 2 — Booking core (≈2 weeks) ⭐ the heart
Segment allocation, quotas, seat hold, mock payment gateway, payment saga, e-ticket with signed QR, PNR page.
**Demo:** two users book the same berth at the same moment → only one wins; MAS→SA and SA→ERS share one berth.

### Phase 3 — Cancellation, waitlist & refunds (≈1.5 weeks)
Cancellation rules, WL/RAC promotion engine, refunds, wallet, reconciliation job, chart preparation.
**Demo:** cancel a ticket → WL passenger goes WL→RAC→CNF live; kill payment mid-flow → auto-refund appears.

### Phase 4 — Tatkal & waiting room (≈2 weeks) ⭐ the demo moment
Waiting room, fair queue, rate admission, bot detection, Go bot swarm, k6 tests, Grafana panel.
**Demo:** 5,000 bots at 10:00:00, sold out in about 40 s, zero double bookings, bots flagged.

### Phase 5 — Live trains & simulation clock (≈1.5 weeks)
Train-movement simulator, live status, delays, WebSocket gateway, notifications.
**Demo:** speed up time and watch trains move, delays appear, passengers get alerts.

### Phase 6 — TTE app (≈1.5 weeks)
Expo app, offline chart, QR scan, no-shows, RAC→berth, fines, sync.
**Demo:** go offline on a phone, mark no-shows, reconnect → RAC passenger gets the berth.

### Phase 7 — Station app & announcements (≈1 week)
Display boards, platform assignment, coach position, multilingual TTS announcements.
**Demo:** a train arrives in simulation → the board updates and the announcement plays in Tamil, Hindi and English.

### Phase 8 — Control centre (≈2 weeks)
Section chart, delay ripple, platform conflicts, what-if, incidents with mass rebook and refund, extra coaches.
**Demo:** break down a train → see the ripple → divert → affected passengers notified and refunded.

### Phase 9 — Catering (≈1 week)
Vendor app, ETA-based ordering, delivery to the berth, payouts.
**Demo:** order a biryani for Salem, the train runs 40 min late, and the vendor's timing adjusts automatically.

### Phase 10 — AI layer (≈2 weeks)
PNR prediction, festival forecast, complaint triage, Claude trip-planner agent, delay prediction.
**Demo:** "Chennai to Kochi for Onam with my amma" in Malayalam → the agent books after you confirm.

### Phase 11 — Dashboards & analytics (≈1 week)
Zone/division dashboards, revenue, occupancy, punctuality, fraud reports.

### Phase 12 — Polish & the showcase (≈1 week)
Chaos mode, digital twin replay, accessibility, README with architecture diagram, demo script and video.

**Total: about 18–20 weeks**, building part-time with you testing each phase.

---

## 8. Success metrics (proved, not claimed)

- 0 double bookings across 1M simulated booking attempts
- 0 "paid but not booked" left after reconciliation
- Tatkal: ≥5,000 concurrent users, p99 booking < 300 ms on a laptop
- PNR prediction: accuracy reported on a held-out test set
- Each failure in chaos mode recovers on its own

---

## 9. Decisions for you before Phase 0

1. Booking core in **Go** (recommended) or all-TypeScript (NestJS)?
2. TTE app in Expo (real phone), or a web app first?
3. Phase order: keep it as written, or pull the Tatkal demo earlier?
4. Repo name and whether it's public on GitHub.

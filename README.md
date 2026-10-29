# YatraX

**A railway booking and operations platform for India, built to the scale and rules of the real thing.**
Seven apps — for passengers, ticket examiners, station masters, controllers, catering vendors and managers —
run on one Go core over the real Indian Railways network (2,957 stations, 2,249 trains). The people, bookings
and money are synthetic; the engineering problems are not.

> YatraX is an engineering project, not a ticketing service. Real tickets can only be sold through IRCTC's
> authorised channels; this is a complete, working model of how such a system can be built and proved.

## The idea

Booking a train in India is one of the hardest consumer-software problems there is: millions of people,
a fixed number of berths shared along a route, a 10 a.m. Tatkal stampede with bots in the queue, money that
must never go missing, staff on moving trains without signal, and trains that run late. YatraX takes each of
those problems seriously and solves it end to end — then **proves** the solutions with tests, load runs and
failure drills rather than claiming them.

## What makes it hard, and how YatraX solves it

| Problem | Solution |
|---|---|
| **One berth, many journeys.** A berth sold Chennai–Salem is free again Salem–Ernakulam; it must never be sold twice for overlapping stretches. | Each allocation is a segment range on a berth; a Postgres GiST **exclusion constraint** makes overlaps impossible, and a per-run lock orders allocations. Waitlisted and RAC passengers are promoted automatically as segments free up. |
| **The Tatkal rush.** Everyone arrives at 10:00 sharp; refreshing early must not help; bots and agents must not win. | A **waiting room** that shuffles everyone present at opening into a fair random queue, streams positions over WebSocket, and scores each session for bot signals (typing rhythm, shared devices and networks) — flag, challenge or block. |
| **Money that never goes missing.** Payments, refunds, wallets, fines, food escrow, vendor payouts, insurance. | A **double-entry ledger** in paise with a database trigger that rejects unbalanced transactions, a mock gateway shaped like Razorpay, and a **reconciler** that finds payments whose callbacks were lost: nobody pays without a ticket. |
| **Staff without signal.** A TTE checks tickets on a moving train. | An **offline-first** Expo app: download the chart, verify signed QR tickets offline, record operations with IDs, sync later — the server's allocation wins any conflict. |
| **Trains run late, and everything depends on it.** Alerts, platforms, boards, food delivery, connections. | A deterministic **live model** on a simulation clock; a leader-elected watcher turns changes into events, and every app reacts: passenger alerts, station boards and announcements, vendors' cooking times. |
| **One change, many effects.** A cancellation must free berths, promote the queue, refund, and notify — reliably. | The **transactional outbox**: the change and its event commit together; a relay publishes to Redpanda; idempotent consumers (promotion engine, notifications) can see an event twice but never miss one. |
| **Things fail.** Gateways lose callbacks, databases fail over, buses go down, engines break. | **Chaos mode** breaks each on purpose and times the system's own recovery; nothing is fixed by hand. |

## Proved, not claimed

| Claim | How it's shown | Result |
|---|---|---|
| No double bookings across 1M attempts | `go run ./cmd/stress -attempts 1000000` — a million holds and releases from 48 goroutines straight into the booking core on 40 busy runs, then every allocation checked against every other | **0 overlaps**, 0 unexpected errors (233,448 berths held, 183,026 RAC/waitlisted, 415,445 released and resold) |
| Nobody pays without a ticket | reconciliation, the gateway chaos drill, `/v1/admin/integrity` | 0 after reconciliation |
| A fair Tatkal rush | `go run ./cmd/botswarm -users 5000` | fair queue, bots blocked, no berth sold twice |
| Every failure recovers on its own | `smoke_phase12.py` | gateway ~2 min, database ~2 s, event bus < 1 s, breakdown ~2 s |
| The books always balance | every smoke test ends on the ledger's trial balance | ₹0 |
| No data races | `go test -race ./...` | clean |

## Seven apps, one system

The front end is split by role, the way railways run it: each person gets only what their job needs, on the
device it runs on. All of them share one backend, one database and one event stream — an action in one app
shows up in the others.

| App | For | Highlights |
|---|---|---|
| **Passenger** (web) | travellers | in English, हिन्दी, தமிழ், മലയാളം or తెలుగు: search with nearby stations and flexible dates, seat maps, quotas, Tatkal waiting room, live status, food to the berth, split journeys and the confirmation guarantee, refunds and TDR, wallet and rewards, GST invoice, help on board and SOS, retiring rooms, group bookings |
| **TTE** (Expo, offline) | ticket examiners | offline chart, QR verification, no-shows → RAC moves up, excess-fare receipts, complaints, crew hand-off |
| **Station** (web) | station masters | platform chart with clash detection, coach positions, announcements in four languages, complaints and SOS queue, retiring-room counter, group desk |
| **Display board** (web, no sign-in) | everyone at the station | the yellow-on-black board with live platforms and spoken announcements |
| **Control Centre** (web) | controllers | live map and section chart, what-if delay ripple, divert / cancel / short-terminate with automatic refunds, extra coaches and special trains, chaos drills, replay any past day |
| **Staff Portal** (web) | managers and admins | zone and division dashboards (revenue, occupancy, punctuality, complaints), fraud report, ledger, TDRs, Tatkal monitor |
| **Catering** (web) | food vendors | orders timed to the train's live ETA, menu, payouts |

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

## Tech stack

- **Core:** Go (chi, pgx, go-redis, franz-go), PostgreSQL 16 (range types, GiST exclusion, advisory locks), Redis 7, Redpanda (Kafka API)
- **Web:** Next.js 15, React, TypeScript, Tailwind; a shared UI library (with its own small i18n layer) and a typed API client (pnpm + Turborepo monorepo)
- **Mobile:** Expo (React Native) for the offline TTE app
- **Ops:** Docker Compose, Prometheus, Grafana, GitHub Actions
- **Data:** the real network from the [datameet railways dataset](https://github.com/datameet/railways) (CC0), plus 124 newer trains; 50,000 synthetic passengers and the staff and vendors to run it

## Quick start

Needs Docker Desktop, Node 22+ and pnpm 9 (Go 1.26+ only to run Go tools outside Docker).

```bash
docker compose up -d --build   # Postgres, Redis, Redpanda, Prometheus, Grafana, seed, core API, payment gateway
pnpm install
pnpm dev                       # the five web apps
pnpm --filter @yatrax/tte dev  # the TTE app (web build), or `start` for Expo Go on a phone
```

The first start migrates and seeds the database. To start from scratch: `docker compose down -v`.

| | Link | Sign in as |
|---|---|---|
| Passenger | http://localhost:3000 | phone 9000000001 (the OTP is shown on screen) |
| Station | http://localhost:3001 | `SM00001` |
| Display board | http://localhost:3001/board/MAS | — |
| Control Centre | http://localhost:3002 | `CTRL00001` |
| Staff Portal | http://localhost:3003 | `ADM00001` |
| Catering | http://localhost:3004 | `VND00001` |
| TTE app | http://localhost:3005 | `TTE00001` |
| Core API | http://localhost:8080 (`/healthz`, `/readyz`, `/metrics`) | |
| YatraPay (mock gateway) | http://localhost:8081/admin | switch it to decline, time out or duplicate webhooks |
| Grafana · Prometheus · Redpanda Console | http://localhost:3100 · :9090 · :8090 | Grafana: `admin` / `yatrax` |

Every staff password is `yatrax123`; every sign-in screen also lists demo accounts for one-click sign-in.

**Take the tour:** [docs/DEMO.md](docs/DEMO.md) is a ten-minute walkthrough (with a shot list for recording it).
**Every feature in depth:** [docs/FEATURES.md](docs/FEATURES.md).

## Testing

```bash
pnpm typecheck && pnpm build                       # every web app
cd services/core && go test ./...                  # unit and database tests (the compose Postgres)
cd services/core && go run ./cmd/stress            # the million-attempt double-booking check (its own database)
python scripts/smoke_phase0.py                     # … through smoke_phase12.py and smoke_extras.py:
                                                   # end-to-end checks of every area against the running stack
```

CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs gofmt, vet, the Go tests with the race
detector, the web typecheck and build, then the whole Docker stack with every smoke test and a 500-person
Tatkal drill. On Windows, run the race detector in the Go image (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm --network yatrax_default -v "$PWD/services/core:/src" -w /src -e CGO_ENABLED=1 -e TEST_DATABASE_ADMIN_URL=postgres://yatrax:yatrax@postgres:5432/postgres?sslmode=disable golang:1.27-alpine sh -c "apk add --no-cache gcc musl-dev && go test -race ./..."
```

## Project layout

```
apps/
  passenger/  station/  control/  dashboards/  vendor/   Next.js web apps
  tte/                                                   Expo app (offline-first)
packages/
  ui/              shared components, theme, sessions, sign-in, live widgets, accessibility
  api-client/      typed client for the core API
services/core/
  cmd/core         the API and its background workers
  cmd/payments     YatraPay, the mock payment gateway
  cmd/seed         migrations and the synthetic world
  cmd/botswarm     the Tatkal rush simulator
  cmd/stress       the million-attempt double-booking check
  internal/        booking, ledger, live, simclock, waitroom, station, control, catering, analytics,
                   complaints, stays, chaos, search, fare, inventory, network, events, auth, httpapi, …
infra/             Prometheus and Grafana configuration
scripts/           smoke tests, the network import (build_network.py)
docs/              DEMO.md (the tour) and FEATURES.md (the reference)
YATRAX_PLAN.md     the original plan
```

## Honest limits

- **Not connected to Indian Railways.** No real seats, tickets or PNRs; that needs IRCTC's authorisation.
- **Synthetic people and bookings; a 2016 timetable** (plus curated newer trains). Running days are inferred,
  so some weekly trains show as daily.
- **Simulated live running.** Delays come from a deterministic model, not GPS or NTES.
- **Mock money and messages.** YatraPay stands in for a real gateway; SMS is shown on screen; invoices use a
  synthetic GSTIN.
- **Translations are a first pass.** The passenger app's main journey is in five languages, written for this
  demo; they should be reviewed by native speakers, and deeper pages still fall back to English.
- **No AI layer.** The plan's PNR prediction, trip-planner agent and demand forecasting were left out by choice.
- **Runs locally** (Docker Compose); it hasn't been deployed or hardened for the public internet.

## Credits

Station and train data: [datameet/railways](https://github.com/datameet/railways), released under CC0.
Everything about passengers, staff, bookings and money is generated. Indian Railways, IRCTC, RailMadad and
the railway names used here belong to their owners; YatraX is not affiliated with them.

## License

The code is released under the [MIT License](LICENSE), © 2026 Kevin Mathew T. The station and train data
come from datameet under CC0 (see Credits).

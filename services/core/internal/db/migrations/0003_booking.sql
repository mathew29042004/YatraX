-- Phase 2: booking core — train runs, bookings, seat allocations, payments, outbox.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- One row per train per departure date, created the first time anyone books or
-- opens its seat map. Untouched runs show simulated availability.
CREATE TABLE train_runs (
    id              bigserial PRIMARY KEY,
    train_number    text NOT NULL REFERENCES trains(number),
    origin_date     date NOT NULL,                 -- day the train leaves its origin
    background_fill jsonb NOT NULL DEFAULT '{}',   -- "class/quota" -> berth units filled by bot passengers
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (train_number, origin_date)
);

CREATE TYPE booking_status AS ENUM ('HELD', 'PAYMENT_PENDING', 'CONFIRMED', 'EXPIRED', 'FAILED', 'CANCELLED');

CREATE TABLE bookings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pnr             char(10) UNIQUE,               -- assigned when confirmed
    user_id         uuid NOT NULL REFERENCES users(id),
    run_id          bigint NOT NULL REFERENCES train_runs(id),
    train_number    text NOT NULL,
    journey_date    date NOT NULL,                 -- boarding date at from_station
    from_station    text NOT NULL REFERENCES stations(code),
    to_station      text NOT NULL REFERENCES stations(code),
    from_idx        smallint NOT NULL,
    to_idx          smallint NOT NULL CHECK (to_idx > from_idx),
    class_code      text NOT NULL REFERENCES coach_classes(code),
    quota           text NOT NULL,
    status          booking_status NOT NULL,
    source          text NOT NULL CHECK (source IN ('user', 'background')),
    total_fare      int NOT NULL,
    fare            jsonb NOT NULL DEFAULT '{}',   -- per-passenger fare quote
    preferences     jsonb NOT NULL DEFAULT '{}',
    hold_expires_at timestamptz,
    idempotency_key text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    confirmed_at    timestamptz,
    UNIQUE (user_id, idempotency_key)
);
CREATE INDEX bookings_run ON bookings (run_id, class_code);
CREATE INDEX bookings_user ON bookings (user_id, created_at DESC);
CREATE INDEX bookings_hold ON bookings (hold_expires_at) WHERE status IN ('HELD', 'PAYMENT_PENDING');

CREATE TYPE berth_status AS ENUM ('CNF', 'RAC', 'WL');

CREATE TABLE booking_passengers (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id uuid NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    seq        smallint NOT NULL,
    name       text NOT NULL,
    age        smallint NOT NULL CHECK (age BETWEEN 5 AND 125),
    gender     char(1) NOT NULL CHECK (gender IN ('M', 'F', 'O')),
    berth_pref text NOT NULL DEFAULT '',
    status     berth_status NOT NULL,
    coach_no   text,
    berth_no   smallint,
    berth_type text,
    rac_no     int,
    wl_no      int,
    fare       int NOT NULL,
    UNIQUE (booking_id, seq),
    CHECK ((status = 'WL') = (coach_no IS NULL))
);

-- The no-double-sale guarantee. A berth (or RAC half-berth "slot") holds a passenger
-- for a range of stop indexes [from, to). Two rows for the same berth and slot can
-- never overlap, whatever the application does.
CREATE TABLE seat_allocations (
    id           bigserial PRIMARY KEY,
    run_id       bigint NOT NULL REFERENCES train_runs(id),
    class_code   text NOT NULL,
    coach_no     text NOT NULL,
    berth_no     smallint NOT NULL,
    slot         smallint NOT NULL DEFAULT 0,
    seg          int4range NOT NULL,
    booking_id   uuid NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    passenger_id uuid NOT NULL UNIQUE REFERENCES booking_passengers(id) ON DELETE CASCADE,
    EXCLUDE USING gist (run_id WITH =, coach_no WITH =, berth_no WITH =, slot WITH =, seg WITH &&)
);
CREATE INDEX seat_allocations_run ON seat_allocations (run_id, class_code);

-- RAC and waitlist numbers are handed out in order and never reused.
CREATE TABLE queue_counters (
    run_id     bigint NOT NULL REFERENCES train_runs(id),
    class_code text NOT NULL,
    quota      text NOT NULL,
    kind       berth_status NOT NULL,
    last_no    int NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, class_code, quota, kind)
);

CREATE TYPE payment_status AS ENUM ('CREATED', 'CAPTURED', 'FAILED', 'REFUND_PENDING', 'REFUNDED');

CREATE TABLE payments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id         uuid NOT NULL REFERENCES bookings(id),
    order_id           text NOT NULL UNIQUE,      -- gateway order
    gateway_payment_id text,
    amount             int NOT NULL,              -- rupees
    method             text,
    status             payment_status NOT NULL,
    failure_reason     text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payments_booking ON payments (booking_id);

-- Every webhook we accept, keyed by the gateway's event id, so retries are no-ops.
CREATE TABLE payment_events (
    event_id    text PRIMARY KEY,
    order_id    text NOT NULL,
    type        text NOT NULL,
    payload     jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- Transactional outbox: events are written with the change that caused them and
-- published to Redpanda afterwards, so an event is never lost or sent for a rolled-back change.
CREATE TABLE outbox (
    id           bigserial PRIMARY KEY,
    topic        text NOT NULL,
    key          text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_unpublished ON outbox (id) WHERE published_at IS NULL;

-- A passenger's saved co-travellers.
CREATE TABLE master_passengers (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL,
    age        smallint NOT NULL CHECK (age BETWEEN 5 AND 125),
    gender     char(1) NOT NULL CHECK (gender IN ('M', 'F', 'O')),
    berth_pref text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX master_passengers_user ON master_passengers (user_id);

-- Service secrets generated on first start in development (the ticket signing key).
CREATE TABLE app_secrets (
    name       text PRIMARY KEY,
    value      bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- PNRs come from a sequence scrambled by a fixed bijection (see booking.pnrFrom),
-- so they are unique without looking sequential.
CREATE SEQUENCE pnr_seq;

-- Phase 5: live trains and the simulation clock.

-- The railway's clock. Train movement, live status and alerts follow it; bookings,
-- holds and payments stay on real time. sim_now = anchor_sim + (now - anchor_real) × speed.
CREATE TABLE sim_clock (
    id          smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    anchor_real timestamptz NOT NULL DEFAULT now(),
    anchor_sim  timestamptz NOT NULL DEFAULT now(),
    speed       double precision NOT NULL DEFAULT 1 CHECK (speed > 0 AND speed <= 1440),
    paused      boolean NOT NULL DEFAULT false,
    updated_by  uuid,                 -- no foreign key: a reseed truncates users and must not reset the clock
    updated_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO sim_clock DEFAULT VALUES;

-- Delays injected by controllers ("hold 12623 at Salem for 25 minutes"). The run's
-- departure from that stop, and everything after it, moves by delay_min.
CREATE TABLE live_incidents (
    id           bigserial PRIMARY KEY,
    train_number text NOT NULL,
    origin_date  date NOT NULL,
    stop_seq     int NOT NULL,          -- 0-based index into the train's stops
    delay_min    int NOT NULL CHECK (delay_min BETWEEN 1 AND 1440),
    reason       text NOT NULL,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX live_incidents_run ON live_incidents (train_number, origin_date);

-- Bookings are looked up by run when a train event needs to reach its passengers.
CREATE INDEX IF NOT EXISTS bookings_run_status ON bookings (run_id, status);

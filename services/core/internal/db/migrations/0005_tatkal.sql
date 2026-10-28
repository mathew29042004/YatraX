-- Phase 4: Tatkal waiting room, fast-path Tatkal inventory, bot detection.

-- Tatkal and Premium Tatkal berths of a run, one row each. Buyers lock rows with
-- FOR UPDATE SKIP LOCKED, so thousands can grab different berths at the same moment
-- without queueing behind one lock; the seat_allocations exclusion constraint still
-- guarantees no berth is sold twice.
CREATE TABLE tatkal_units (
    run_id     bigint NOT NULL REFERENCES train_runs(id),
    class_code text NOT NULL,
    quota      text NOT NULL CHECK (quota IN ('TQ', 'PT')),
    coach_no   text NOT NULL,
    berth_no   smallint NOT NULL,
    berth_type text NOT NULL,
    coach_pos  smallint NOT NULL,
    PRIMARY KEY (run_id, coach_no, berth_no)
);
CREATE INDEX tatkal_units_pick ON tatkal_units (run_id, class_code, quota);

-- A Tatkal opening: every AC (10:00) or non-AC (11:00) window for the next day's
-- journeys gets a room. Drill rooms open whenever an admin says, for demos.
CREATE TABLE waitrooms (
    id           text PRIMARY KEY,           -- e.g. 2026-10-28-AC or drill-…
    kind         text NOT NULL CHECK (kind IN ('real', 'drill')),
    journey_date date NOT NULL,              -- the trains' origin date
    ac           boolean NOT NULL,           -- AC classes (10:00) or non-AC (11:00)
    joins_open   timestamptz NOT NULL,       -- people may join from here (5 minutes early)
    opens_at     timestamptz NOT NULL,       -- the shuffle; admission starts
    closes_at    timestamptz NOT NULL,       -- after this, Tatkal books without a queue
    admit_rate   int NOT NULL DEFAULT 150,   -- people admitted per second
    created_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    stats        jsonb NOT NULL DEFAULT '{}' -- final numbers, written when the room closes
);

-- Everyone the bot detector scored, with why.
CREATE TABLE bot_flags (
    room_id    text NOT NULL REFERENCES waitrooms(id),
    user_id    uuid NOT NULL REFERENCES users(id),
    score      int NOT NULL,
    action     text NOT NULL CHECK (action IN ('allow', 'challenge', 'block')),
    reasons    text[] NOT NULL,
    ip         text NOT NULL DEFAULT '',
    device     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);

-- Tatkal bookings per user per day are capped; this index makes the check cheap.
CREATE INDEX bookings_tatkal_user ON bookings (user_id, created_at) WHERE quota IN ('TQ', 'PT');

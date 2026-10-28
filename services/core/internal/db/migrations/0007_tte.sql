-- Phase 6: the TTE app. On-board work done offline and synced as operations.

-- A confirmed passenger who never turned up; their berth is released to RAC passengers.
ALTER TYPE berth_status ADD VALUE IF NOT EXISTS 'NOSHOW';

ALTER TABLE booking_passengers
    ADD COLUMN boarded_at      timestamptz,
    ADD COLUMN boarded_station text,
    ADD COLUMN no_show_station text;

-- Excess-fare tickets issued on board become bookings of their own.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_source_check;
ALTER TABLE bookings ADD CONSTRAINT bookings_source_check CHECK (source IN ('user', 'background', 'tte'));

-- Every operation a TTE's device sends. op_id is generated on the device, so a retried
-- sync is harmless: the second copy finds the first and returns its result.
CREATE TABLE tte_ops (
    op_id      uuid PRIMARY KEY,
    run_id     bigint NOT NULL REFERENCES train_runs(id),
    user_id    uuid NOT NULL REFERENCES users(id),
    kind       text NOT NULL,
    payload    jsonb NOT NULL,
    device_at  timestamptz NOT NULL,          -- when the TTE did it (possibly offline)
    applied_at timestamptz NOT NULL DEFAULT now(),
    status     text NOT NULL CHECK (status IN ('applied', 'resolved', 'rejected')),
    result     jsonb NOT NULL
);
CREATE INDEX tte_ops_run ON tte_ops (run_id, applied_at);

CREATE TABLE tte_fines (
    receipt_no   text PRIMARY KEY,
    op_id        uuid NOT NULL REFERENCES tte_ops(op_id) DEFERRABLE INITIALLY DEFERRED, -- the op row is written last
    run_id       bigint NOT NULL REFERENCES train_runs(id),
    tte_id       uuid NOT NULL REFERENCES users(id),
    booking_id   uuid REFERENCES bookings(id),      -- when a berth was allotted with the ticket
    name         text NOT NULL,
    age          int NOT NULL,
    gender       text NOT NULL,
    from_station text NOT NULL,
    to_station   text NOT NULL,
    class_code   text NOT NULL,
    reason       text NOT NULL CHECK (reason IN ('ticketless', 'wrong_class', 'beyond_destination')),
    fare         int NOT NULL,                       -- rupees
    penalty      int NOT NULL,
    total        int NOT NULL,
    coach_no     text,
    berth_no     int,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE SEQUENCE tte_receipt_seq;

-- Complaints and incidents reported on board (triaged by the AI layer in Phase 10).
CREATE TABLE complaints (
    id          bigserial PRIMARY KEY,
    run_id      bigint REFERENCES train_runs(id),
    coach_no    text,
    berth_no    int,
    category    text NOT NULL,
    description text NOT NULL,
    reported_by uuid REFERENCES users(id),
    source      text NOT NULL DEFAULT 'tte',
    status      text NOT NULL DEFAULT 'open',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- The chart passes to the next TTE at a crew-change station.
CREATE TABLE tte_handoffs (
    id         bigserial PRIMARY KEY,
    run_id     bigint NOT NULL REFERENCES train_runs(id),
    from_user  uuid NOT NULL REFERENCES users(id),
    to_user    uuid NOT NULL REFERENCES users(id),
    at_station text NOT NULL,
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

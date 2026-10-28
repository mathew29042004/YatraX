-- Phase 7: the station app.

-- A station master's platform for a train run at their station. It overrides the
-- timetable (and the live model's own platform changes) from the moment it's made.
CREATE TABLE platform_assignments (
    train_number text NOT NULL,
    origin_date  date NOT NULL,
    station_code text NOT NULL REFERENCES stations(code),
    platform     int NOT NULL CHECK (platform >= 1),
    assigned_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    assigned_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (train_number, origin_date, station_code)
);

CREATE TABLE lost_found (
    id          bigserial PRIMARY KEY,
    station_code text NOT NULL REFERENCES stations(code),
    item        text NOT NULL,
    description text NOT NULL DEFAULT '',
    found_at    text NOT NULL DEFAULT '',   -- where: platform 3, train 12623 coach S4 …
    status      text NOT NULL DEFAULT 'held' CHECK (status IN ('held', 'returned')),
    returned_to text,
    reported_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    returned_at timestamptz
);
CREATE INDEX lost_found_station ON lost_found (station_code, created_at DESC);

CREATE TABLE wheelchair_requests (
    id           bigserial PRIMARY KEY,
    station_code text NOT NULL REFERENCES stations(code),
    passenger    text NOT NULL,
    pnr          text NOT NULL DEFAULT '',
    train_number text NOT NULL DEFAULT '',
    needed_at    text NOT NULL DEFAULT '',  -- "arrival of 12623, coach B2"
    status       text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'assigned', 'done')),
    attendant    text,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wheelchair_station ON wheelchair_requests (station_code, created_at DESC);

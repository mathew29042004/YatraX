-- Retiring rooms, dormitory beds and cloak-room lockers booked against a ticket; and the
-- group booking desk (weddings, pilgrimages, school trips).

CREATE TABLE station_stays (
    id           bigserial PRIMARY KEY,
    code         text NOT NULL UNIQUE,                 -- RR000123 / CL000123: shown at the counter
    station_code text NOT NULL REFERENCES stations(code),
    user_id      uuid NOT NULL REFERENCES users(id),
    booking_id   uuid NOT NULL REFERENCES bookings(id), -- a ticket to or from the station
    unit_type    text NOT NULL CHECK (unit_type IN ('ac_room', 'dorm_bed', 'locker')),
    units        int NOT NULL CHECK (units BETWEEN 1 AND 6),
    check_in     timestamptz NOT NULL,
    hours        int NOT NULL CHECK (hours IN (12, 24, 48)),
    amount       int NOT NULL,                         -- rupees
    payment      text NOT NULL CHECK (payment IN ('wallet', 'counter')),
    status       text NOT NULL DEFAULT 'booked' CHECK (status IN ('booked', 'checked_in', 'completed', 'cancelled')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX station_stays_station ON station_stays (station_code, unit_type, check_in) WHERE status IN ('booked', 'checked_in');
CREATE SEQUENCE station_stay_seq;

CREATE TABLE group_requests (
    id            bigserial PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id),
    train_number  text NOT NULL REFERENCES trains(number),
    from_station  text NOT NULL REFERENCES stations(code),
    to_station    text NOT NULL REFERENCES stations(code),
    journey_date  date NOT NULL,
    class_code    text NOT NULL,
    purpose       text NOT NULL,
    contact       text NOT NULL,
    passengers    jsonb NOT NULL,                      -- [{name, age, gender}]
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    note          text NOT NULL DEFAULT '',
    bookings      uuid[] NOT NULL DEFAULT '{}',
    decided_by    uuid REFERENCES users(id),
    decided_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX group_requests_station ON group_requests (from_station, status);

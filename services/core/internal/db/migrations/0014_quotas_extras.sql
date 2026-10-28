-- More quotas (divyangjan, defence, foreign tourist) need an ID per passenger; travel insurance;
-- auto-upgrade and the confirmation guarantee; loyalty points.

ALTER TABLE booking_passengers ADD COLUMN id_number text;          -- UDID card, defence warrant or passport
ALTER TABLE booking_passengers ADD COLUMN upgraded_from text;      -- class before an auto-upgrade at chart time
ALTER TABLE bookings ADD COLUMN insurance_premium int NOT NULL DEFAULT 0;  -- rupees, kept by the insurer

-- Confirmation guarantee: two waitlisted bookings for one trip; the first to confirm cancels the other.
CREATE TABLE guarantee_pairs (
    id          bigserial PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id),
    booking_a   uuid NOT NULL UNIQUE REFERENCES bookings(id),
    booking_b   uuid NOT NULL UNIQUE REFERENCES bookings(id),
    settled_at  timestamptz,
    kept        uuid REFERENCES bookings(id),
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Loyalty points: earned on travel, redeemed for wallet credit. Balance = sum(delta).
CREATE TABLE loyalty_points (
    id         bigserial PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id),
    delta      int NOT NULL,
    reason     text NOT NULL,
    ref        text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ref, reason)
);
CREATE INDEX loyalty_points_user ON loyalty_points (user_id, created_at DESC);

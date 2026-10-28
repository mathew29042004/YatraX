-- Phase 9: catering. Food ordered to the berth, timed to the train's live ETA.

CREATE TABLE menu_items (
    id          bigserial PRIMARY KEY,
    vendor_id   uuid NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    category    text NOT NULL CHECK (category IN ('meal', 'snack', 'beverage', 'sweet')),
    diet        text NOT NULL CHECK (diet IN ('veg', 'jain', 'egg', 'nonveg')),
    price       int NOT NULL CHECK (price > 0),       -- rupees, GST included
    prep_min    int NOT NULL CHECK (prep_min BETWEEN 1 AND 120),
    available   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX menu_items_vendor ON menu_items (vendor_id);

CREATE TYPE food_status AS ENUM ('placed', 'accepted', 'preparing', 'handed_over', 'delivered', 'rejected', 'cancelled', 'missed');

CREATE TABLE food_orders (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code          text NOT NULL UNIQUE,              -- FD000123: what the delivery boy shouts
    booking_id    uuid NOT NULL REFERENCES bookings(id),
    user_id       uuid NOT NULL REFERENCES users(id),
    vendor_id     uuid NOT NULL REFERENCES vendors(id),
    run_id        bigint NOT NULL REFERENCES train_runs(id),
    train_number  text NOT NULL,
    origin_date   date NOT NULL,
    station_code  text NOT NULL,
    stop_seq      int NOT NULL,
    coach_no      text NOT NULL,
    berth_no      int NOT NULL,
    passenger     text NOT NULL,
    total         int NOT NULL,                      -- rupees
    commission    int NOT NULL,                      -- YatraX's share
    payment       text NOT NULL CHECK (payment IN ('wallet', 'cod')),
    status        food_status NOT NULL DEFAULT 'placed',
    prep_min      int NOT NULL,                      -- the longest item's
    eta_at        timestamptz NOT NULL,              -- expected arrival when last told (railway time)
    note          text NOT NULL DEFAULT '',          -- why it was rejected, cancelled or missed
    rating        int CHECK (rating BETWEEN 1 AND 5),
    review        text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    delivered_at  timestamptz
);
CREATE INDEX food_orders_vendor ON food_orders (vendor_id, status);
CREATE INDEX food_orders_booking ON food_orders (booking_id);
CREATE INDEX food_orders_active ON food_orders (status) WHERE status IN ('placed', 'accepted', 'preparing', 'handed_over');
CREATE SEQUENCE food_order_seq;

CREATE TABLE food_order_items (
    order_id uuid NOT NULL REFERENCES food_orders(id) ON DELETE CASCADE,
    item_id  bigint NOT NULL REFERENCES menu_items(id),
    name     text NOT NULL,
    diet     text NOT NULL,
    qty      int NOT NULL CHECK (qty BETWEEN 1 AND 20),
    price    int NOT NULL,
    PRIMARY KEY (order_id, item_id)
);

CREATE TABLE vendor_payouts (
    id         bigserial PRIMARY KEY,
    vendor_id  uuid NOT NULL REFERENCES vendors(id),
    amount     int NOT NULL CHECK (amount > 0),   -- rupees
    reference  text NOT NULL,                     -- the (simulated) bank transfer's UTR
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Hygiene complaints about a vendor come from passengers.
ALTER TABLE complaints ADD COLUMN vendor_id uuid REFERENCES vendors(id);

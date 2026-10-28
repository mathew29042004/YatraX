-- Phase 3: cancellation, waitlist promotion, refunds, wallet ledger, charts, TDR, notifications.

-- Passengers can be cancelled individually (partial cancellation).
ALTER TYPE berth_status ADD VALUE IF NOT EXISTS 'CAN';

ALTER TABLE booking_passengers
    DROP CONSTRAINT IF EXISTS booking_passengers_check,
    ADD CONSTRAINT booking_passengers_place CHECK (status::text NOT IN ('CNF', 'RAC') OR coach_no IS NOT NULL),
    -- What the ticket said when it was booked ("WL 23"), kept while the current status moves.
    ADD COLUMN booked_status text,
    ADD COLUMN cancelled_at  timestamptz,
    ADD COLUMN refund        int;

ALTER TABLE bookings
    ADD COLUMN cancelled_at       timestamptz,
    ADD COLUMN boarding_changed   boolean NOT NULL DEFAULT false,
    ADD COLUMN original_from      text;

ALTER TABLE train_runs
    ADD COLUMN chart1_at timestamptz,   -- first chart, 4 hours before departure
    ADD COLUMN chart2_at timestamptz;   -- second chart, 30 minutes before

-- ── Ledger ─────────────────────────────────────────────────────────────────
-- Double-entry: every transaction's lines sum to zero, amounts in paise.
-- Positive = debit, negative = credit.
CREATE TABLE ledger_accounts (
    id         bigserial PRIMARY KEY,
    code       text NOT NULL UNIQUE,      -- asset:gateway, liability:fares, liability:wallet:<user>, …
    user_id    uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_txns (
    id         bigserial PRIMARY KEY,
    kind       text NOT NULL,             -- capture, wallet_payment, cancellation, refund, …
    ref        text NOT NULL,             -- booking or refund id
    memo       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_txns_ref ON ledger_txns (ref);

CREATE TABLE ledger_entries (
    id         bigserial PRIMARY KEY,
    txn_id     bigint NOT NULL REFERENCES ledger_txns(id),
    account_id bigint NOT NULL REFERENCES ledger_accounts(id),
    amount     bigint NOT NULL CHECK (amount <> 0)
);
CREATE INDEX ledger_entries_account ON ledger_entries (account_id);

-- Balances can't drift: a transaction that doesn't sum to zero fails at commit.
CREATE FUNCTION ledger_txn_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT coalesce(sum(amount), 0) FROM ledger_entries WHERE txn_id = NEW.txn_id) <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % does not balance', NEW.txn_id;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER ledger_balanced AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_txn_balanced();

-- ── Refunds ────────────────────────────────────────────────────────────────
CREATE TYPE refund_status AS ENUM ('PENDING', 'PROCESSED', 'FAILED');

CREATE TABLE refunds (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id        uuid NOT NULL REFERENCES bookings(id),
    payment_id        uuid REFERENCES payments(id),
    amount            int NOT NULL CHECK (amount > 0),   -- rupees
    destination       text NOT NULL CHECK (destination IN ('source', 'wallet')),
    reason            text NOT NULL,
    status            refund_status NOT NULL DEFAULT 'PENDING',
    gateway_refund_id text,
    attempts          int NOT NULL DEFAULT 0,
    last_error        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    processed_at      timestamptz
);
CREATE INDEX refunds_pending ON refunds (created_at) WHERE status = 'PENDING';
CREATE INDEX refunds_booking ON refunds (booking_id);

-- ── Charts ─────────────────────────────────────────────────────────────────
-- A snapshot of who sits where, published to the TTE app.
CREATE TABLE charts (
    run_id      bigint NOT NULL REFERENCES train_runs(id),
    version     smallint NOT NULL CHECK (version IN (1, 2)),
    prepared_at timestamptz NOT NULL,
    data        jsonb NOT NULL,
    PRIMARY KEY (run_id, version)
);

-- ── TDR (ticket deposit receipt) claims ────────────────────────────────────
CREATE TYPE tdr_status AS ENUM ('PENDING', 'APPROVED', 'REJECTED');

CREATE TABLE tdrs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id uuid NOT NULL REFERENCES bookings(id),
    user_id    uuid NOT NULL REFERENCES users(id),
    reason     text NOT NULL,
    details    text NOT NULL DEFAULT '',
    status     tdr_status NOT NULL DEFAULT 'PENDING',
    refund     int,
    decided_by uuid REFERENCES users(id),
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (booking_id)
);

-- ── In-app notifications ───────────────────────────────────────────────────
CREATE TABLE notifications (
    id         bigserial PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    title      text NOT NULL,
    body       text NOT NULL,
    link       text NOT NULL DEFAULT '',
    dedupe_key text NOT NULL UNIQUE,      -- replayed events don't notify twice
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user ON notifications (user_id, created_at DESC);

-- Gateway refunds are made idempotent by our refund id.
ALTER TABLE payments ADD COLUMN refunded_amount int NOT NULL DEFAULT 0;

-- Backfill: Phase 2 captured payments before the ledger existed. Book each capture,
-- and turn REFUND_PENDING payments into queued refunds, so balances start right.
UPDATE booking_passengers SET booked_status = CASE status::text
        WHEN 'CNF' THEN 'CNF ' || coach_no || '/' || berth_no
        WHEN 'RAC' THEN 'RAC ' || rac_no || ' ' || coach_no || '/' || berth_no
        ELSE 'WL ' || wl_no END
 WHERE booked_status IS NULL
   AND booking_id IN (SELECT id FROM bookings WHERE status = 'CONFIRMED');

DO $$
DECLARE
    r     record;
    t     bigint;
    a_gw  bigint;
    a_far bigint;
    a_ref bigint;
BEGIN
    INSERT INTO ledger_accounts (code) VALUES ('asset:gateway'), ('liability:fares'), ('liability:refunds') ON CONFLICT DO NOTHING;
    SELECT id INTO a_gw  FROM ledger_accounts WHERE code = 'asset:gateway';
    SELECT id INTO a_far FROM ledger_accounts WHERE code = 'liability:fares';
    SELECT id INTO a_ref FROM ledger_accounts WHERE code = 'liability:refunds';
    FOR r IN SELECT id, booking_id, amount, status FROM payments
              WHERE status IN ('CAPTURED', 'REFUND_PENDING') AND method IS DISTINCT FROM 'wallet' LOOP
        INSERT INTO ledger_txns (kind, ref, memo) VALUES ('capture', r.booking_id::text, 'Backfilled from Phase 2') RETURNING id INTO t;
        INSERT INTO ledger_entries (txn_id, account_id, amount) VALUES (t, a_gw, r.amount * 100), (t, a_far, -r.amount * 100);
        IF r.status = 'REFUND_PENDING' THEN
            INSERT INTO ledger_txns (kind, ref, memo) VALUES ('refund_due', r.booking_id::text, 'Backfilled from Phase 2') RETURNING id INTO t;
            INSERT INTO ledger_entries (txn_id, account_id, amount) VALUES (t, a_far, r.amount * 100), (t, a_ref, -r.amount * 100);
            INSERT INTO refunds (booking_id, payment_id, amount, destination, reason)
            VALUES (r.booking_id, r.id, r.amount, 'source', 'Payment could not be matched to a ticket');
        END IF;
    END LOOP;
END $$;

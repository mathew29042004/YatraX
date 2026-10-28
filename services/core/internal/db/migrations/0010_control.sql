-- Phase 8: the control centre.

-- A run that control has cancelled, cut short, or sent another way.
CREATE TABLE run_disruptions (
    run_id      bigint PRIMARY KEY REFERENCES train_runs(id),
    kind        text NOT NULL CHECK (kind IN ('cancelled', 'short_terminated', 'diverted')),
    at_station  text,                        -- short_terminated: where it now ends
    skip        text[] NOT NULL DEFAULT '{}', -- diverted: stations it no longer calls at
    extra_delay int NOT NULL DEFAULT 0,      -- diverted: minutes the longer route adds
    reason      text NOT NULL,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Coaches added to one run to clear its waitlist.
CREATE TABLE run_extra_coaches (
    run_id     bigint NOT NULL REFERENCES train_runs(id),
    coach_no   text NOT NULL,
    class_code text NOT NULL,
    added_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, coach_no)
);

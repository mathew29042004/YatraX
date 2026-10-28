-- Phase 12: chaos mode. Each drill breaks something on purpose and records how the system recovered.

CREATE TABLE chaos_runs (
    id           bigserial PRIMARY KEY,
    kind         text NOT NULL,
    status       text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'recovered', 'failed')),
    started_by   uuid REFERENCES users(id),
    started_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz,
    recovered_in double precision,                 -- seconds from the fault to recovery
    steps        jsonb NOT NULL DEFAULT '[]',      -- the timeline: [{at, kind, text}]
    metrics      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX chaos_runs_recent ON chaos_runs (started_at DESC);

-- A breakdown between stations delays the arrival at the next stop, not just the departure from it.
ALTER TABLE live_incidents ADD COLUMN en_route boolean NOT NULL DEFAULT false;

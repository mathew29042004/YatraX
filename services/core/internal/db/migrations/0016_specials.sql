-- Special trains: copies of busy trains (numbered 0xxxx, as Indian Railways numbers its
-- specials) that run only on the dates the control room adds, to clear long waitlists.

ALTER TABLE trains DROP CONSTRAINT trains_running_days_check;
ALTER TABLE trains ADD CONSTRAINT trains_running_days_check CHECK (running_days BETWEEN 0 AND 127);
ALTER TABLE trains ADD COLUMN special_of text REFERENCES trains(number) ON DELETE CASCADE;

CREATE TABLE special_runs (
    train_number text NOT NULL REFERENCES trains(number) ON DELETE CASCADE,
    origin_date  date NOT NULL,
    reason       text NOT NULL DEFAULT '',
    created_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (train_number, origin_date)
);

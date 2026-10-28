-- RailMadad-style complaints desk: routing, priorities, acknowledgement and resolution times,
-- and the passenger's SOS (medical, security, a female TTE).

ALTER TABLE complaints
    ADD COLUMN booking_id      uuid REFERENCES bookings(id),
    ADD COLUMN station_code    text REFERENCES stations(code),       -- where it can be acted on (the next halt)
    ADD COLUMN priority        text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal', 'high', 'urgent')),
    ADD COLUMN department      text NOT NULL DEFAULT 'commercial',
    ADD COLUMN assigned_to     uuid REFERENCES users(id),            -- e.g. the female TTE asked to attend
    ADD COLUMN acknowledged_at timestamptz,
    ADD COLUMN acknowledged_by uuid REFERENCES users(id),
    ADD COLUMN resolved_at     timestamptz,
    ADD COLUMN resolved_by     uuid REFERENCES users(id),
    ADD COLUMN resolution      text;
ALTER TABLE complaints ADD CONSTRAINT complaints_status_check CHECK (status IN ('open', 'in_progress', 'resolved'));
CREATE INDEX complaints_open ON complaints (station_code, priority) WHERE status <> 'resolved';

-- Rule-based routing: every complaint, from any source, goes to the department that can act on it.
CREATE FUNCTION complaint_route() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.department := CASE NEW.category
        WHEN 'cleanliness' THEN 'housekeeping'
        WHEN 'food' THEN 'catering'
        WHEN 'food_hygiene' THEN 'catering'
        WHEN 'security' THEN 'rpf'
        WHEN 'women_safety' THEN 'rpf'
        WHEN 'medical' THEN 'medical'
        WHEN 'electrical' THEN 'electrical'
        WHEN 'water' THEN 'mechanical'
        ELSE 'commercial' END;
    IF NEW.priority = 'normal' THEN
        NEW.priority := CASE
            WHEN NEW.category IN ('security', 'women_safety', 'medical') THEN 'urgent'
            WHEN NEW.category IN ('food_hygiene', 'electrical', 'water') THEN 'high'
            ELSE 'normal' END;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER complaints_route BEFORE INSERT ON complaints FOR EACH ROW EXECUTE FUNCTION complaint_route();

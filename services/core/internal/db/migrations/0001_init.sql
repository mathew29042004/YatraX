-- YatraX Phase 0 schema: railway network, rolling stock, fares, people, auth, audit.

-- ── Network ────────────────────────────────────────────────────────────────
CREATE TABLE zones (
    code text PRIMARY KEY,               -- SR, CR, NR …
    name text NOT NULL,
    hq   text NOT NULL
);

CREATE TABLE divisions (
    id        serial PRIMARY KEY,
    zone_code text NOT NULL REFERENCES zones(code),
    name      text NOT NULL,
    UNIQUE (zone_code, name)
);

CREATE TABLE stations (
    code        text PRIMARY KEY,         -- MAS, SA, ERS …
    name        text NOT NULL,
    state       text NOT NULL,
    division_id int  NOT NULL REFERENCES divisions(id),
    lat         double precision NOT NULL,
    lon         double precision NOT NULL,
    importance  smallint NOT NULL CHECK (importance BETWEEN 1 AND 3), -- 1 major junction … 3 wayside
    platforms   smallint NOT NULL CHECK (platforms > 0),
    language    text NOT NULL             -- regional language used for announcements
);
CREATE INDEX stations_name_lower ON stations (lower(name));

-- ── Trains & schedules ─────────────────────────────────────────────────────
CREATE TABLE train_types (
    code            text PRIMARY KEY,     -- RAJDHANI, VANDE_BHARAT …
    name            text NOT NULL,
    avg_speed_kmph  smallint NOT NULL,
    fare_multiplier numeric(4,2) NOT NULL,
    superfast       boolean NOT NULL,     -- superfast surcharge applies
    flexi_fare      boolean NOT NULL      -- dynamic (Flexi) fare applies
);

CREATE TABLE trains (
    number       text PRIMARY KEY,        -- 12623
    name         text NOT NULL,
    type_code    text NOT NULL REFERENCES train_types(code),
    origin       text NOT NULL REFERENCES stations(code),
    destination  text NOT NULL REFERENCES stations(code),
    running_days smallint NOT NULL CHECK (running_days BETWEEN 1 AND 127), -- bit 0 = Monday
    distance_km  int NOT NULL,
    pantry       boolean NOT NULL,
    return_number text                    -- the paired train in the other direction
);

-- Times are minutes after midnight of the day the train leaves its origin,
-- so a stop on day 2 at 06:10 is 1440 + 370.
CREATE TABLE train_stops (
    train_number  text NOT NULL REFERENCES trains(number) ON DELETE CASCADE,
    seq           smallint NOT NULL,
    station_code  text NOT NULL REFERENCES stations(code),
    arrival_min   int,                    -- NULL at origin
    departure_min int,                    -- NULL at destination
    distance_km   int NOT NULL,           -- from origin
    platform      smallint NOT NULL,
    PRIMARY KEY (train_number, seq),
    UNIQUE (train_number, station_code),
    CHECK (arrival_min IS NOT NULL OR departure_min IS NOT NULL)
);
CREATE INDEX train_stops_station ON train_stops (station_code);

-- ── Rolling stock ──────────────────────────────────────────────────────────
CREATE TABLE coach_classes (
    code               text PRIMARY KEY,  -- 1A 2A 3A 3E SL CC EC 2S, plus non-bookable GN SLR PC EOG
    name               text NOT NULL,
    bookable           boolean NOT NULL,
    seats_per_coach    smallint NOT NULL,
    layout             text NOT NULL,     -- see internal/layout
    coach_prefix       text NOT NULL,     -- B for 3A → B1, B2 …
    sort_order         smallint NOT NULL,
    ac                 boolean NOT NULL,
    fare_per_km        numeric(6,3) NOT NULL,
    reservation_charge int NOT NULL,
    superfast_charge   int NOT NULL,
    gst_pct            numeric(4,2) NOT NULL,
    tatkal_pct         numeric(4,2),      -- NULL = no Tatkal in this class
    tatkal_min         int,
    tatkal_max         int
);

-- Long journeys get cheaper per km (Indian Railways tapers its base fare by distance).
CREATE TABLE fare_taper (
    from_km int PRIMARY KEY,
    to_km   int NOT NULL,
    factor  numeric(4,3) NOT NULL
);

CREATE TABLE train_coaches (
    train_number text NOT NULL REFERENCES trains(number) ON DELETE CASCADE,
    coach_no     text NOT NULL,           -- B1, S4, A1, H1, PC, GEN1, SLR1
    class_code   text NOT NULL REFERENCES coach_classes(code),
    position     smallint NOT NULL,       -- 1 = right behind the locomotive
    PRIMARY KEY (train_number, coach_no),
    UNIQUE (train_number, position)
);

-- ── People ─────────────────────────────────────────────────────────────────
CREATE TYPE user_role AS ENUM ('passenger', 'tte', 'station', 'vendor', 'controller', 'admin');

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role          user_role NOT NULL,
    full_name     text NOT NULL,
    phone         text UNIQUE,            -- passengers sign in by phone + OTP
    email         text UNIQUE,
    employee_id   text UNIQUE,            -- staff sign in by employee ID + password
    password_hash text,
    gender        char(1) CHECK (gender IN ('M', 'F', 'O')),
    birth_date    date,
    language      text NOT NULL DEFAULT 'en',
    home_station  text REFERENCES stations(code),
    is_demo       boolean NOT NULL DEFAULT false,
    disabled      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (phone IS NOT NULL OR employee_id IS NOT NULL)
);
CREATE INDEX users_role ON users (role);

CREATE TABLE staff_postings (
    user_id      uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    designation  text NOT NULL,           -- Travelling Ticket Examiner, Station Master …
    zone_code    text NOT NULL REFERENCES zones(code),
    division_id  int  NOT NULL REFERENCES divisions(id),
    station_code text REFERENCES stations(code)
);

CREATE TABLE vendors (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          text NOT NULL,
    station_code  text NOT NULL REFERENCES stations(code),
    cuisines      text[] NOT NULL,
    veg_only      boolean NOT NULL,
    fssai_license text NOT NULL,
    rating        numeric(2,1) NOT NULL
);
CREATE INDEX vendors_station ON vendors (station_code);

-- ── Auth ───────────────────────────────────────────────────────────────────
-- Refresh tokens rotate on every use. Reusing a rotated token revokes its whole family.
CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id   uuid NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    replaced_by uuid,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);

-- ── Audit ──────────────────────────────────────────────────────────────────
CREATE TABLE audit_log (
    id        bigserial PRIMARY KEY,
    at        timestamptz NOT NULL DEFAULT now(),
    actor_id  uuid,
    action    text NOT NULL,
    entity    text NOT NULL,
    entity_id text,
    data      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_entity ON audit_log (entity, entity_id);

CREATE TABLE seed_meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

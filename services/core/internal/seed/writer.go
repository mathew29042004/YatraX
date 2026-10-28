package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Version identifies the generator's output. Bump it whenever generated data
// changes (new stations, trains, fare rules) so existing databases get reseeded.
const Version = "4" // 4: the real Indian Railways network (datameet, 2016) plus newer curated trains

// SeededVersion returns the version of the world in the database, or "" if none.
func SeededVersion(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var v string
	err := pool.QueryRow(ctx, `SELECT coalesce(
		(SELECT value FROM seed_meta WHERE key='version'),
		(SELECT '1' FROM seed_meta WHERE key='seeded_at'), '')`).Scan(&v)
	return v, err
}

// Reset empties every seeded table (used with SEED_FORCE=1).
func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE audit_log, refresh_tokens, vendors, staff_postings, users,
		train_coaches, train_stops, trains, fare_taper, coach_classes, train_types,
		stations, divisions, zones, seed_meta RESTART IDENTITY CASCADE`)
	return err
}

func uuidOf(b [16]byte) pgtype.UUID { return pgtype.UUID{Bytes: b, Valid: true} }

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Write loads the world in a single transaction.
func Write(ctx context.Context, pool *pgxpool.Pool, w *World, randomSeed uint64, staffPassword string) error {
	// Every synthetic staff account shares one password, so hash it once.
	hash, err := bcrypt.GenerateFromPassword([]byte(staffPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Binary COPY needs to know the enum's type to encode role values.
	roleType, err := conn.Conn().LoadType(ctx, "user_role")
	if err != nil {
		return err
	}
	conn.Conn().TypeMap().RegisterType(roleType)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, z := range zones {
		if _, err := tx.Exec(ctx, `INSERT INTO zones (code, name, hq) VALUES ($1,$2,$3)`, z.Code, z.Name, z.HQ); err != nil {
			return fmt.Errorf("zones: %w", err)
		}
	}
	divID := map[string]int{}
	for _, s := range w.Stations {
		k := s.Zone + "/" + s.Division
		if _, ok := divID[k]; ok {
			continue
		}
		var id int
		if err := tx.QueryRow(ctx, `INSERT INTO divisions (zone_code, name) VALUES ($1,$2) RETURNING id`,
			s.Zone, s.Division).Scan(&id); err != nil {
			return fmt.Errorf("divisions: %w", err)
		}
		divID[k] = id
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"stations"},
		[]string{"code", "name", "state", "division_id", "lat", "lon", "importance", "platforms", "language"},
		pgx.CopyFromSlice(len(w.Stations), func(i int) ([]any, error) {
			s := w.Stations[i]
			return []any{s.Code, s.Name, s.State, divID[s.Zone+"/"+s.Division], s.Lat, s.Lon,
				int16(s.Importance), int16(s.Platforms), s.Language}, nil
		})); err != nil {
		return fmt.Errorf("stations: %w", err)
	}

	for _, t := range trainTypes {
		if _, err := tx.Exec(ctx, `INSERT INTO train_types (code, name, avg_speed_kmph, fare_multiplier, superfast, flexi_fare)
			VALUES ($1,$2,$3,$4,$5,$6)`, t.Code, t.Name, t.AvgSpeedKmph, t.FareMultiplier, t.Superfast, t.FlexiFare); err != nil {
			return fmt.Errorf("train_types: %w", err)
		}
	}
	for _, c := range coachClasses {
		if _, err := tx.Exec(ctx, `INSERT INTO coach_classes (code, name, bookable, seats_per_coach, layout, coach_prefix,
			sort_order, ac, fare_per_km, reservation_charge, superfast_charge, gst_pct, tatkal_pct, tatkal_min, tatkal_max,
			min_distance_km)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			c.Code, c.Name, c.Bookable, c.Seats, c.Layout, c.Prefix, c.Sort, c.AC, c.FarePerKm,
			c.ReservationCharge, c.SuperfastCharge, c.GSTPct, c.TatkalPct, c.TatkalMin, c.TatkalMax,
			minDistanceKm[c.Code]); err != nil {
			return fmt.Errorf("coach_classes: %w", err)
		}
	}
	for _, t := range fareTaper {
		if _, err := tx.Exec(ctx, `INSERT INTO fare_taper (from_km, to_km, factor) VALUES ($1,$2,$3)`, t.From, t.To, t.Factor); err != nil {
			return fmt.Errorf("fare_taper: %w", err)
		}
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"trains"},
		[]string{"number", "name", "type_code", "origin", "destination", "running_days", "distance_km", "pantry", "return_number"},
		pgx.CopyFromSlice(len(w.Trains), func(i int) ([]any, error) {
			t := w.Trains[i]
			return []any{t.Number, t.Name, t.Type, t.Origin, t.Destination, t.Days, t.DistKm, t.Pantry, nilIfEmpty(t.Return)}, nil
		})); err != nil {
		return fmt.Errorf("trains: %w", err)
	}
	var stops, coaches [][]any
	for _, t := range w.Trains {
		for i, s := range t.Stops {
			stops = append(stops, []any{t.Number, int16(i + 1), s.Station, s.Arr, s.Dep, s.DistKm, int16(s.Platform)})
		}
		for _, c := range t.Coaches {
			coaches = append(coaches, []any{t.Number, c.No, c.Class, int16(c.Pos)})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"train_stops"},
		[]string{"train_number", "seq", "station_code", "arrival_min", "departure_min", "distance_km", "platform"},
		pgx.CopyFromRows(stops)); err != nil {
		return fmt.Errorf("train_stops: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"train_coaches"},
		[]string{"train_number", "coach_no", "class_code", "position"}, pgx.CopyFromRows(coaches)); err != nil {
		return fmt.Errorf("train_coaches: %w", err)
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"users"},
		[]string{"id", "role", "full_name", "phone", "employee_id", "password_hash", "gender", "birth_date", "language", "home_station", "is_demo"},
		pgx.CopyFromSlice(len(w.People), func(i int) ([]any, error) {
			p := w.People[i]
			var pw any
			if p.EmployeeID != "" {
				pw = string(hash)
			}
			return []any{uuidOf(p.ID), p.Role, p.FullName, nilIfEmpty(p.Phone), nilIfEmpty(p.EmployeeID), pw,
				p.Gender, p.Birth, p.Language, nilIfEmpty(p.Home), p.Demo}, nil
		})); err != nil {
		return fmt.Errorf("users: %w", err)
	}
	var postings [][]any
	for _, p := range w.People {
		if p.Posting != nil {
			postings = append(postings, []any{uuidOf(p.ID), p.Posting.Designation, p.Posting.Zone,
				divID[p.Posting.Zone+"/"+p.Posting.Division], nilIfEmpty(p.Posting.Station)})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"staff_postings"},
		[]string{"user_id", "designation", "zone_code", "division_id", "station_code"}, pgx.CopyFromRows(postings)); err != nil {
		return fmt.Errorf("staff_postings: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"vendors"},
		[]string{"id", "owner_user_id", "name", "station_code", "cuisines", "veg_only", "fssai_license", "rating"},
		pgx.CopyFromSlice(len(w.Vendors), func(i int) ([]any, error) {
			v := w.Vendors[i]
			return []any{uuidOf(v.ID), uuidOf(v.Owner), v.Name, v.Station, v.Cuisines, v.Veg, v.FSSAI, v.Rating}, nil
		})); err != nil {
		return fmt.Errorf("vendors: %w", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO seed_meta (key, value) VALUES
		('seeded_at', $1), ('random_seed', $2), ('passengers', $3), ('version', $4)`,
		time.Now().UTC().Format(time.RFC3339), fmt.Sprint(randomSeed), fmt.Sprint(countRole(w.People, "passenger")), Version); err != nil {
		return fmt.Errorf("seed_meta: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_log (action, entity, data) VALUES ('seed.loaded', 'world', $1)`,
		map[string]any{"random_seed": randomSeed, "stations": len(w.Stations), "trains": len(w.Trains), "people": len(w.People)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func countRole(people []Person, role string) int {
	n := 0
	for _, p := range people {
		if p.Role == role {
			n++
		}
	}
	return n
}

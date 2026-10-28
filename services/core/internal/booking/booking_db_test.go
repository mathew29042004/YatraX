package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/db"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

// These tests need Postgres. They create a fresh database "yatrax_test" on the
// server in TEST_DATABASE_ADMIN_URL (default: the docker compose Postgres) and skip
// if it can't be reached.

var (
	testOnce sync.Once
	testPool *pgxpool.Pool
	testNet  *network.Network
	testErr  error
	clock    = time.Date(2026, 10, 26, 12, 0, 0, 0, network.IST)
)

func setup(t *testing.T) (*Service, []string) {
	t.Helper()
	testOnce.Do(func() {
		ctx := context.Background()
		admin := os.Getenv("TEST_DATABASE_ADMIN_URL")
		if admin == "" {
			admin = "postgres://yatrax:yatrax@localhost:5433/postgres?sslmode=disable"
		}
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			testErr = err
			return
		}
		defer conn.Close(ctx)
		if _, err = conn.Exec(ctx, `DROP DATABASE IF EXISTS yatrax_test WITH (FORCE)`); err == nil {
			_, err = conn.Exec(ctx, `CREATE DATABASE yatrax_test`)
		}
		if err != nil {
			testErr = err
			return
		}
		cfg := conn.Config()
		url := fmt.Sprintf("postgres://%s:%s@%s:%d/yatrax_test?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port)
		testPool, err = pgxpool.New(ctx, url)
		if err != nil {
			testErr = err
			return
		}
		if testErr = db.Migrate(ctx, testPool); testErr != nil {
			return
		}
		w, err := seed.Generate(7, 400)
		if err != nil {
			testErr = err
			return
		}
		if testErr = seed.Write(ctx, testPool, w, 7, "test-password"); testErr != nil {
			return
		}
		testNet, testErr = network.Load(ctx, testPool)
	})
	if testErr != nil {
		t.Skipf("Postgres not available: %v", testErr)
	}
	s := New(testPool, testNet, func() time.Time { return clock })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := testPool.Query(context.Background(), `SELECT id FROM users WHERE role = 'passenger' ORDER BY id LIMIT 300`)
	if err != nil {
		t.Fatal(err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return s, users
}

// gnBerth returns a General-quota berth (not RAC) in a coach, skipping the first n.
func gnBerth(t *testing.T, s *Service, train, class, coach string, skip int) int {
	t.Helper()
	cat, err := s.catalog(s.Net.Trains[train], class)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range cat.Units {
		if u.Coach == coach && u.Quota == "GN" && !u.RAC {
			if skip == 0 {
				return u.Berth
			}
			skip--
		}
	}
	t.Fatalf("no General berth in %s %s", train, coach)
	return 0
}

// quiet turns off bot passengers so a test controls every booking on its run.
func quiet(s *Service) *Service {
	s.people = nil
	return s
}

func pax(name string) []PassengerInput {
	return []PassengerInput{{Name: name, Age: 34, Gender: "F"}}
}

// noOverlaps checks the stored allocations directly: no two journeys share a berth segment.
func noOverlaps(t *testing.T, s *Service) {
	t.Helper()
	var n int
	err := s.DB.QueryRow(context.Background(), `
		SELECT count(*) FROM seat_allocations a JOIN seat_allocations b
		  ON a.run_id = b.run_id AND a.coach_no = b.coach_no AND a.berth_no = b.berth_no AND a.slot = b.slot
		 AND a.id < b.id AND a.seg && b.seg`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d overlapping allocations: a berth was sold twice", n)
	}
}

// The plan's demo: two users book the same berth at the same moment; only one wins.
func TestSameBerthRace(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	const racers = 200
	berth := gnBerth(t, s, "12623", "SL", "S2", 3)
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Hold(context.Background(), users[i], "", HoldRequest{
				Train: "12623", From: "MAS", To: "ERN", Date: "2026-11-09", Class: "SL", Quota: "GN",
				Passengers: []PassengerInput{{Name: fmt.Sprint("Racer ", i), Age: 30, Gender: "M", Coach: "S2", Berth: berth}},
			})
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("racer %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != racers-1 {
		t.Fatalf("wins=%d conflicts=%d, want exactly one winner", wins.Load(), conflicts.Load())
	}
	noOverlaps(t, s)
}

// The plan's demo: MAS→SA and SA→ERS share one berth.
func TestSegmentsShareABerth(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	no := gnBerth(t, s, "12623", "SL", "S5", 10)
	berth := []PassengerInput{{Name: "First Leg", Age: 40, Gender: "M", Coach: "S5", Berth: no}}
	a, err := s.Hold(ctx, users[0], "", HoldRequest{Train: "12623", From: "MAS", To: "SA", Date: "2026-11-10", Class: "SL", Quota: "GN", Passengers: berth})
	if err != nil {
		t.Fatal(err)
	}
	berth[0].Name = "Second Leg"
	b, err := s.Hold(ctx, users[1], "", HoldRequest{Train: "12623", From: "SA", To: "ERN", Date: "2026-11-11", Class: "SL", Quota: "GN", Passengers: berth})
	if err != nil {
		t.Fatalf("SA→ERS on the same berth (boarding SA after midnight, so the next calendar day): %v", err)
	}
	if a.Passengers[0].Coach != "S5" || b.Passengers[0].Berth != no || a.RunID != b.RunID {
		t.Fatalf("expected both on S5/%d of one run: %+v %+v", no, a.Passengers[0], b.Passengers[0])
	}
	berth[0].Name = "Overlap"
	if _, err := s.Hold(ctx, users[2], "", HoldRequest{Train: "12623", From: "ED", To: "PGT", Date: "2026-11-11", Class: "SL", Quota: "GN", Passengers: berth}); !errors.Is(err, ErrConflict) {
		t.Errorf("ED→CBE overlaps SA→ERS; got %v", err)
	}
}

func TestManyConcurrentBookingsNeverOverlap(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	tr := s.Net.Trains["12695"]
	stops := len(tr.Stops)
	var wg sync.WaitGroup
	var ok, waitlisted atomic.Int32
	for i := range 300 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			from := i % (stops - 2)
			to := from + 1 + (i*7)%(stops-from-1)
			date := time.Date(2026, 11, 12, 0, 0, 0, 0, network.IST).AddDate(0, 0, tr.Stops[from].Time()/1440)
			b, err := s.Hold(context.Background(), users[i%len(users)], "", HoldRequest{
				Train: "12695", From: tr.Stops[from].Station, To: tr.Stops[to].Station, Date: date.Format(time.DateOnly),
				Class: "3A", Quota: "GN", Passengers: []PassengerInput{{Name: "Load", Age: 30, Gender: "M"}, {Name: "Test", Age: 31, Gender: "F"}},
				Together: i%2 == 0,
			})
			switch {
			case errors.Is(err, ErrRegret), errors.Is(err, ErrTooManyHolds):
			case err != nil:
				t.Errorf("booking %d: %v", i, err)
			default:
				ok.Add(1)
				for _, p := range b.Passengers {
					if p.Status != "CNF" {
						waitlisted.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()
	noOverlaps(t, s)
	t.Logf("%d bookings held, %d passengers on RAC/WL", ok.Load(), waitlisted.Load())
	if ok.Load() == 0 {
		t.Fatal("nothing was booked")
	}
}

func TestIdempotencyKey(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	req := HoldRequest{Train: "12007", From: "MAS", To: "MYS", Date: "2026-11-13", Class: "CC", Quota: "GN", Passengers: pax("Same Request")}
	var wg sync.WaitGroup
	ids := make([]string, 10)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.Hold(context.Background(), users[5], "key-123", req)
			if err != nil {
				t.Errorf("attempt %d: %v", i, err)
				return
			}
			ids[i] = b.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("one key produced several bookings: %v", ids)
		}
	}
}

func TestExpiredHoldFreesTheBerth(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	req := HoldRequest{Train: "12007", From: "MAS", To: "SBC", Date: "2026-11-16", Class: "EC", Quota: "GN",
		Passengers: []PassengerInput{{Name: "Hold Then Leave", Age: 50, Gender: "M", Coach: "E1", Berth: gnBerth(t, s, "12007", "EC", "E1", 0)}}}
	b, err := s.Hold(ctx, users[10], "", req)
	if err != nil {
		t.Fatal(err)
	}
	req.Passengers[0].Name = "Waiting"
	if _, err := s.Hold(ctx, users[11], "", req); !errors.Is(err, ErrConflict) {
		t.Fatalf("berth should be held: %v", err)
	}
	if err := s.ExpireNow(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hold(ctx, users[11], "", req); err != nil {
		t.Fatalf("after the hold expired the berth should be free: %v", err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "EXPIRED" {
		t.Errorf("first booking status %s, want EXPIRED", got.Status)
	}
}

func TestQuotaAndValidationRules(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	base := HoldRequest{Train: "12623", From: "MAS", To: "ERN", Date: "2026-11-17", Class: "SL"}
	cases := map[string]HoldRequest{
		"tatkal before opening": {Quota: "TQ", Passengers: pax("Too Early")},
		"senior quota, young":   {Quota: "SS", Passengers: []PassengerInput{{Name: "Young One", Age: 30, Gender: "M"}}},
		"ladies quota, man":     {Quota: "LD", Passengers: []PassengerInput{{Name: "Mister", Age: 30, Gender: "M"}}},
		"seven passengers":      {Quota: "GN", Passengers: make([]PassengerInput, 7)},
		"bad berth preference":  {Quota: "GN", Passengers: []PassengerInput{{Name: "Pref", Age: 30, Gender: "M", Pref: "W"}}},
		"past date":             {Quota: "GN", Passengers: pax("Late"), Date: "2026-10-01"},
	}
	for name, c := range cases {
		r := base
		r.Quota, r.Passengers = c.Quota, c.Passengers
		if c.Date != "" {
			r.Date = c.Date
		}
		_, err := s.Hold(ctx, users[20], "", r)
		var v ValidationError
		if !errors.As(err, &v) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
	// A senior on the senior quota is fine and gets a lower berth.
	b, err := s.Hold(ctx, users[21], "", HoldRequest{Train: "12623", From: "MAS", To: "ERN", Date: "2026-11-17", Class: "SL", Quota: "SS",
		Passengers: []PassengerInput{{Name: "Senior Citizen", Age: 67, Gender: "M"}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Passengers[0].BerthType != "LB" {
		t.Errorf("senior quota berth %s, want LB", b.Passengers[0].BerthType)
	}
}

func TestBackgroundDemandFillsARun(t *testing.T) {
	s, users := setup(t)
	ctx := context.Background()
	m, err := s.SeatMap(ctx, HoldRequest{Train: "12309", From: "PNBE", To: "NDLS", Date: "2026-11-18", Class: "3A", Quota: "GN"})
	if err != nil {
		t.Fatal(err)
	}
	booked, total := 0, 0
	for _, c := range m.Coaches {
		for _, st := range c.Seats {
			total++
			if st.Status == "booked" {
				booked++
			}
		}
	}
	if booked == 0 {
		t.Fatal("opening a popular run should show bot passengers' bookings")
	}
	t.Logf("Patna Rajdhani 3A, 3 weeks out: %d of %d berths booked by background demand", booked, total)
	// Opening it again adds nothing new at the same moment.
	var before, after int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE source = 'background'`).Scan(&before)
	if _, err := s.SeatMap(ctx, HoldRequest{Train: "12309", From: "PNBE", To: "NDLS", Date: "2026-11-18", Class: "3A", Quota: "GN"}); err != nil {
		t.Fatal(err)
	}
	s.DB.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE source = 'background'`).Scan(&after)
	if after != before {
		t.Errorf("re-opening at the same time added %d bot bookings", after-before)
	}
	// A real passenger can still book on top.
	if _, err := s.Hold(ctx, users[30], "", HoldRequest{Train: "12309", From: "PNBE", To: "NDLS", Date: "2026-11-18", Class: "3A", Quota: "GN", Passengers: pax("Real Person")}); err != nil && !errors.Is(err, ErrRegret) {
		t.Fatal(err)
	}
	noOverlaps(t, s)
}

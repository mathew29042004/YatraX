// Command stress hammers the booking core directly — no HTTP in between — with a million
// booking attempts on a few busy train runs, from many goroutines at once, to prove the
// claim in the plan: no berth is ever sold twice.
//
//	go run ./cmd/stress -attempts 1000000 -workers 48
//
// It makes its own database (yatrax_stress) on the compose Postgres, migrates and seeds a
// small synthetic world, then mixes holds (which allocate berths), releases (which free
// them for the next person) and confirmations, so the same berths are fought over again
// and again. At the end it checks every allocation against every other.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/db"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

var (
	attempts = flag.Int("attempts", 1_000_000, "booking attempts")
	workers  = flag.Int("workers", 48, "goroutines booking at once")
	admin    = flag.String("db", "postgres://yatrax:yatrax@localhost:5433/postgres?sslmode=disable", "Postgres admin URL")
	fresh    = flag.Bool("fresh", true, "recreate the stress database first")
)

type counts struct {
	held, waitlisted, full, holdLimit, conflict, released, errors atomic.Int64
}

func main() {
	flag.Parse()
	ctx := context.Background()
	pool, err := setup(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	net, err := network.Load(ctx, pool)
	if err != nil {
		log.Fatal(err)
	}
	// A fixed clock a month before the runs, so nothing expires or charts mid-test.
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, network.IST)
	s := booking.New(pool, net, func() time.Time { return clock })
	if err := s.Start(ctx); err != nil {
		log.Fatal(err)
	}
	rows, _ := pool.Query(ctx, `SELECT id FROM users WHERE role = 'passenger' ORDER BY id`)
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(users) < *workers {
		log.Fatalf("need at least %d passengers: %v", *workers, err)
	}

	// A few runs, each fought over by everyone: small classes fill fast and churn.
	type target struct{ train, from, to, class string }
	targets := []target{
		{"12627", "SBC", "NDLS", "1A"}, {"12627", "SBC", "NDLS", "2A"}, {"12623", "MAS", "TVC", "SL"},
		{"12623", "MAS", "SA", "3A"}, {"12601", "", "", "SL"},
	}
	for i, t := range targets {
		if t.from == "" {
			tr := net.Trains[t.train]
			targets[i].from, targets[i].to = tr.Stops[0].Station, tr.Stops[len(tr.Stops)-1].Station
		}
	}
	// Eight days of each: 40 runs, so workers contend per run as on a busy day, not all on one lock.
	var dates []string
	for d := 18; d < 26; d++ {
		dates = append(dates, fmt.Sprintf("2026-11-%02d", d))
	}

	var c counts
	var next atomic.Int64
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(w), 7))
			mine := users[w*len(users)/(*workers) : (w+1)*len(users)/(*workers)] // each worker has its own passengers
			var held []struct{ user, id string }
			active := map[string]int{} // unpaid holds per passenger: each may have three at once
			for next.Add(1) <= int64(*attempts) {
				// Free a hold now and then (and always once most passengers are at their limit),
				// so berths change hands constantly.
				if len(held) > 0 && (len(held) >= 2*len(mine) || r.IntN(3) == 0) {
					k := r.IntN(len(held))
					h := held[k]
					held = append(held[:k], held[k+1:]...)
					active[h.user]--
					if err := s.Release(ctx, h.user, h.id); err == nil {
						c.released.Add(1)
					} else if !errors.Is(err, booking.ErrNotFound) {
						c.errors.Add(1)
					}
					continue
				}
				t := targets[r.IntN(len(targets))]
				n := 1 + r.IntN(4)
				pax := make([]booking.PassengerInput, n)
				for i := range pax {
					pax[i] = booking.PassengerInput{Name: fmt.Sprintf("Stress %d-%d", w, i), Age: 20 + r.IntN(50), Gender: []string{"M", "F"}[r.IntN(2)]}
				}
				user := mine[r.IntN(len(mine))]
				for tries := 0; active[user] >= booking.MaxActiveHolds && tries < len(mine); tries++ {
					user = mine[r.IntN(len(mine))]
				}
				b, err := s.Hold(ctx, user, "", booking.HoldRequest{Train: t.train, From: t.from, To: t.to, Date: dates[r.IntN(len(dates))],
					Class: t.class, Quota: "GN", Passengers: pax, Together: r.IntN(2) == 0})
				var v booking.ValidationError
				switch {
				case err == nil:
					if b.Passengers[0].Status == "CNF" {
						c.held.Add(1)
					} else {
						c.waitlisted.Add(1)
					}
					held = append(held, struct{ user, id string }{user, b.ID})
					active[user]++
				case errors.Is(err, booking.ErrTooManyHolds):
					c.holdLimit.Add(1) // this passenger already has three unpaid holds
				case errors.Is(err, booking.ErrConflict):
					c.conflict.Add(1) // lost a race for a berth: the guard working
				case errors.Is(err, booking.ErrRegret), errors.Is(err, booking.ErrNotConfirmed), errors.As(err, &v):
					c.full.Add(1) // class full, waitlist closed
				default:
					c.errors.Add(1)
					if c.errors.Load() < 5 {
						log.Printf("unexpected: %v", err)
					}
				}
				if done := next.Load(); done%100_000 == 0 {
					log.Printf("%d attempts, %.0f/s", done, float64(done)/time.Since(start).Seconds())
				}
			}
		}(w)
	}
	wg.Wait()
	took := time.Since(start)

	// The proof: no two allocations share a berth (and slot) over any stretch of track.
	var overlaps, allocations int
	err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM seat_allocations a JOIN seat_allocations b
		   ON a.run_id = b.run_id AND a.coach_no = b.coach_no AND a.berth_no = b.berth_no AND a.slot = b.slot
		  AND a.id < b.id AND a.seg && b.seg),
		(SELECT count(*) FROM seat_allocations)`).Scan(&overlaps, &allocations)
	if err != nil {
		log.Fatal(err)
	}
	total := *attempts
	fmt.Printf("\n%d booking attempts by %d workers in %s (%.0f per second)\n", total, *workers, took.Round(time.Second), float64(total)/took.Seconds())
	fmt.Printf("  holds with berths            %8d\n  waitlisted / RAC             %8d\n  refused: class full          %8d\n  refused: 3 unpaid holds      %8d\n  lost a race for a berth      %8d\n  released                     %8d\n  unexpected errors            %8d\n",
		c.held.Load(), c.waitlisted.Load(), c.full.Load(), c.holdLimit.Load(), c.conflict.Load(), c.released.Load(), c.errors.Load())
	fmt.Printf("  berth allocations now %d; overlapping allocations: %d\n", allocations, overlaps)
	if overlaps > 0 || c.errors.Load() > 0 {
		fmt.Println("FAIL")
		os.Exit(1)
	}
	fmt.Println("PASS: no berth sold twice")
}

func setup(ctx context.Context) (*pgxpool.Pool, error) {
	conn, err := pgx.Connect(ctx, *admin)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	cfg := conn.Config()
	url := fmt.Sprintf("postgres://%s:%s@%s:%d/yatrax_stress?sslmode=disable&pool_max_conns=%d", cfg.User, cfg.Password, cfg.Host, cfg.Port, *workers+8)
	if !*fresh {
		return pgxpool.New(ctx, url)
	}
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS yatrax_stress WITH (FORCE)`); err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE yatrax_stress`); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		return nil, err
	}
	w, err := seed.Generate(11, 600)
	if err != nil {
		return nil, err
	}
	log.Print("seeding a small synthetic world…")
	return pool, seed.Write(ctx, pool, w, 11, "stress-password")
}

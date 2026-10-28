package booking

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTatkalRushNeverDoubleSells(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	tr := s.Net.Trains["12623"]
	cat, _ := s.catalog(tr, "SL")
	capacity := cat.Capacity("TQ")
	const buyers = 600
	var held, soldOut, other atomic.Int32
	var paxHeld atomic.Int32
	latencies := make([]time.Duration, buyers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n := 1 + i%2
			pax := make([]PassengerInput, n)
			for j := range pax {
				pax[j] = PassengerInput{Name: fmt.Sprintf("Rush %d-%d", i, j), Age: 30, Gender: "M"}
			}
			t0 := time.Now()
			b, err := s.TatkalHold(ctx, users[i%len(users)], fmt.Sprint("rush-", i), HoldRequest{
				Train: "12623", From: "MAS", To: "TVC", Date: "2026-11-21", Class: "SL", Quota: "TQ", Passengers: pax,
			}, TatkalOptions{RoomOpened: true})
			latencies[i] = time.Since(t0)
			switch {
			case err == nil:
				held.Add(1)
				paxHeld.Add(int32(len(b.Passengers)))
			case errors.Is(err, ErrTatkalSoldOut):
				soldOut.Add(1)
			case errors.Is(err, ErrTatkalLimit):
				// users repeat across 600 buyers; the daily cap kicks in
			default:
				other.Add(1)
				t.Errorf("buyer %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	noOverlaps(t, s)
	sort.Slice(latencies, func(a, b int) bool { return latencies[a] < latencies[b] })
	t.Logf("Tatkal SL capacity %d: %d bookings (%d passengers) held, %d told sold out; p50 %v p99 %v",
		capacity, held.Load(), paxHeld.Load(), soldOut.Load(), latencies[buyers/2], latencies[buyers*99/100])
	if int(paxHeld.Load()) > capacity {
		t.Fatalf("sold %d Tatkal berths, only %d exist", paxHeld.Load(), capacity)
	}
	if int(paxHeld.Load()) < capacity-1 { // a last party of 2 may not fit a single remaining berth
		t.Errorf("only %d of %d Tatkal berths sold despite %d buyers", paxHeld.Load(), capacity, buyers)
	}
	if soldOut.Load() == 0 {
		t.Error("with more buyers than berths, some must be told sold out")
	}
}

func TestTatkalDailyLimitAndGate(t *testing.T) {
	s, _ := setup(t)
	s = quiet(s)
	ctx := context.Background()
	// Passengers with no Tatkal bookings yet: the stampede test above uses up the usual ones' daily limit.
	rows, err := testPool.Query(ctx, `SELECT id FROM users u WHERE role = 'passenger'
		AND NOT EXISTS (SELECT 1 FROM bookings b WHERE b.user_id = u.id AND b.quota IN ('TQ', 'PT')) ORDER BY id DESC LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(users) < 3 {
		t.Fatalf("fresh passengers: %v (%d)", err, len(users))
	}
	u := users[0]
	req := func(date string) HoldRequest {
		return HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: date, Class: "3A", Quota: "TQ", Passengers: pax("Tatkal Limit")}
	}
	for i, d := range []string{"2026-11-22", "2026-11-23"} {
		if _, err := s.TatkalHold(ctx, u, "", req(d), TatkalOptions{RoomOpened: true}); err != nil {
			t.Fatalf("booking %d: %v", i+1, err)
		}
	}
	if _, err := s.TatkalHold(ctx, u, "", req("2026-11-24"), TatkalOptions{RoomOpened: true}); !errors.Is(err, ErrTatkalLimit) {
		t.Errorf("third Tatkal booking today: %v, want ErrTatkalLimit", err)
	}
	// Without a waiting room vouching for it, Tatkal far ahead is simply not open.
	var v ValidationError
	if _, err := s.TatkalHold(ctx, users[1], "", req("2026-11-25"), TatkalOptions{}); !errors.As(err, &v) {
		t.Errorf("Tatkal weeks ahead without a room: %v, want not open", err)
	}
	if _, err := s.TatkalHold(ctx, users[2], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-25", Class: "3A", Quota: "GN",
		Passengers: pax("Wrong Quota")}, TatkalOptions{RoomOpened: true}); !errors.Is(err, ErrNotTatkalQuota) {
		t.Errorf("General quota on the Tatkal path: %v", err)
	}
}

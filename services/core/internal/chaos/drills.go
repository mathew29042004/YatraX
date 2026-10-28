package chaos

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/events"
	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/live"
)

// ── Payment gateway loses its callbacks ────────────────────────────────────

// drillUser is the passenger the gateway drill books as: a test account that never logs in.
func (s *Service) drillUser(ctx context.Context) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `INSERT INTO users (role, full_name, phone, language) VALUES ('passenger', 'Chaos Drill Passenger', '5000000001', 'en')
		ON CONFLICT (phone) DO UPDATE SET full_name = excluded.full_name RETURNING id`).Scan(&id)
	return id, err
}

func (s *Service) gatewayMode(ctx context.Context, mode string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.GatewayURL+"/admin/mode", strings.NewReader(`{"mode":"`+mode+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway mode %s: HTTP %d", mode, res.StatusCode)
	}
	return nil
}

// customerPays does what a passenger's browser does on the checkout page.
func (s *Service) customerPays(ctx context.Context, checkoutURL string) error {
	u, err := url.Parse(checkoutURL)
	if err != nil {
		return err
	}
	order := u.Path[strings.LastIndex(u.Path, "/")+1:]
	form := url.Values{"method": {"upi"}, "outcome": {"success"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.GatewayURL+"/checkout/"+order+"/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (s *Service) gatewayOutage(ctx context.Context, r *recorder) error {
	user, err := s.drillUser(ctx)
	if err != nil {
		return err
	}
	// Hold three single-passenger tickets on the Trivandrum Mail, Chennai → Salem, a few weeks out.
	var held []*booking.Booking
	today := live.Midnight(time.Now())
	for _, days := range rand.Perm(30)[:12] {
		if len(held) == 3 {
			break
		}
		d := today.AddDate(0, 0, 10+days).Format(time.DateOnly)
		for len(held) < 3 {
			b, err := s.Booking.Hold(ctx, user, fmt.Sprintf("chaos-%d-%d", r.id, time.Now().UnixNano()), booking.HoldRequest{Train: "12623", From: "MAS", To: "SA", Date: d, Class: "SL", Quota: "GN",
				Passengers: []booking.PassengerInput{{Name: fmt.Sprintf("Drill Traveller %d", len(held)+1), Age: 30 + len(held), Gender: "F"}}, OnlyConfirmed: true})
			if err != nil {
				if errors.Is(err, booking.ErrTooManyHolds) {
					return fmt.Errorf("the drill passenger already has unpaid holds; try again in a few minutes")
				}
				break // full that day: try another
			}
			held = append(held, b)
		}
	}
	if len(held) < 3 {
		return fmt.Errorf("couldn't find berths for the test bookings")
	}
	r.step("info", "Held 3 test tickets on 12623 Trivandrum Mail, Chennai → Salem (₹%d each)", held[0].TotalFare)

	if err := s.gatewayMode(ctx, "timeout"); err != nil {
		return err
	}
	r.fault("Gateway switched to lose every callback: money is taken, but no redirect and no webhook reach YatraX")
	for _, b := range held {
		checkout, err := s.Booking.StartPayment(ctx, s.Payments, user, b.ID)
		if err == nil {
			err = s.customerPays(ctx, checkout)
		}
		if err != nil {
			s.gatewayMode(context.Background(), "normal")
			return err
		}
	}
	r.step("info", "3 passengers paid at the gateway; YatraX heard nothing — the bookings sit at PAYMENT_PENDING")
	if err := s.gatewayMode(ctx, "normal"); err != nil {
		return err
	}
	r.step("info", "Gateway back to normal. The three lost callbacks never come; only reconciliation can find that money")
	r.metric("payments", len(held))

	done := map[string]bool{}
	err = wait(ctx, 3*time.Second, func() (bool, error) {
		for _, b := range held {
			if done[b.ID] {
				continue
			}
			cur, err := s.Booking.Get(ctx, b.ID)
			if err != nil {
				return false, nil // keep polling through a blip
			}
			switch cur.Status {
			case "CONFIRMED":
				done[b.ID] = true
				r.step("ok", "Reconciler found the payment and confirmed PNR %s", cur.PNR)
			case "FAILED", "EXPIRED":
				done[b.ID] = true
				r.step("ok", "Reconciler found the payment after the hold expired and refunded it in full")
			}
		}
		return len(done) == len(held), nil
	})
	if err != nil || len(done) < len(held) {
		return err
	}
	r.recovered("Every captured payment is settled: no passenger paid without a ticket")

	// Put the berths back and check the books.
	for _, b := range held {
		if cur, err := s.Booking.Get(ctx, b.ID); err == nil && cur.Status == "CONFIRMED" {
			s.Booking.Cancel(ctx, user, b.ID, booking.CancelRequest{RefundTo: "wallet"})
		}
	}
	tb, err := ledger.TrialBalance(ctx, s.DB)
	if err != nil {
		return err
	}
	r.metric("trial_balance", tb)
	r.step("ok", "Test tickets cancelled (refunds to the drill wallet); ledger trial balance ₹%d", tb)
	return nil
}

// ── Database failover ──────────────────────────────────────────────────────

func (s *Service) dbFailover(ctx context.Context, r *recorder) error {
	var ok, failed atomic.Int64
	var lastFail atomic.Int64
	probeCtx, stop := context.WithCancel(ctx)
	defer stop()
	streak := atomic.Int64{}
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-t.C:
			}
			c, cancel := context.WithTimeout(probeCtx, time.Second)
			var n int
			err := s.DB.QueryRow(c, `SELECT count(*) FROM train_runs WHERE origin_date >= current_date`).Scan(&n)
			cancel()
			if err != nil {
				failed.Add(1)
				lastFail.Store(time.Now().UnixNano())
				streak.Store(0)
			} else {
				ok.Add(1)
				streak.Add(1)
			}
		}
	}()
	time.Sleep(2 * time.Second)
	r.step("info", "Probing the database every 150 ms: %d queries answered, %d failed", ok.Load(), failed.Load())

	var conns int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM (SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend') x`).Scan(&conns); err != nil {
		return err
	}
	r.fault("Primary lost: %d open connections to Postgres cut at once (API, workers and the payment gateway)", conns)
	r.metric("connections_cut", conns)
	before := ok.Load()

	err := wait(ctx, 100*time.Millisecond, func() (bool, error) { return streak.Load() >= 10 && ok.Load() > before+10, nil })
	if err != nil || streak.Load() < 10 {
		return err
	}
	r.metric("failed_queries", failed.Load())
	r.recovered("Pools reconnected; queries answering again (%d failed during the switch)", failed.Load())

	// The background workers (outbox relay, reconciler, watchers) must carry on too.
	var backlog int
	err = wait(ctx, time.Second, func() (bool, error) {
		if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL AND created_at < now() - interval '3 seconds'`).Scan(&backlog); e != nil {
			return false, nil
		}
		return backlog == 0, nil
	})
	if err == nil && backlog == 0 {
		r.step("ok", "Background workers survived: the event outbox is draining normally")
	}
	return err
}

// ── Event bus down ─────────────────────────────────────────────────────────

func (s *Service) eventBusDown(ctx context.Context, r *recorder) error {
	const pause, n = 20 * time.Second, 40
	events.PauseRelay(pause)
	defer events.ResumeRelay()
	r.fault("Event bus unreachable: publishing to Redpanda stopped for %d s", int(pause.Seconds()))

	// Life goes on: changes keep writing their events to the outbox in the same transaction.
	var ids []int64
	for i := 0; i < n; i++ {
		var id int64
		payload, _ := json.Marshal(map[string]any{"type": "chaos.ping", "run": r.id, "seq": i})
		if err := s.DB.QueryRow(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ('booking.events', $1, $2) RETURNING id`,
			fmt.Sprintf("chaos-%d", r.id), payload).Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		time.Sleep(200 * time.Millisecond)
	}
	var waiting int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&waiting)
	r.step("info", "%d events written while the bus was down; %d waiting in the outbox", n, waiting)
	r.metric("backlog", waiting)

	for events.RelayPaused() {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
	r.step("info", "Event bus back")
	resumed := time.Now()
	var left int
	err := wait(ctx, 200*time.Millisecond, func() (bool, error) {
		if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE id = ANY($1) AND published_at IS NULL`, ids).Scan(&left); e != nil {
			return false, nil
		}
		return left == 0, nil
	})
	if err != nil || left > 0 {
		return err
	}
	r.metric("drain_sec", time.Since(resumed).Seconds())
	r.recoveredSince(resumed, "All %d events published in order once the bus came back; none lost", n)
	return nil
}

// ── Train breakdown ────────────────────────────────────────────────────────

func (s *Service) trainBreakdown(ctx context.Context, r *recorder) error {
	now := s.Now()
	// The running train carrying the most booked passengers, with a stop still ahead.
	type cand struct {
		b      live.Brief
		booked int
	}
	var cands []cand
	for _, b := range s.Live.Running(now) {
		if b.State != "running" {
			continue
		}
		var n int
		var held bool
		s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM booking_passengers p JOIN bookings k ON k.id = p.booking_id JOIN train_runs t ON t.id = k.run_id
			WHERE t.train_number = $1 AND t.origin_date = $2 AND p.status IN ('CNF', 'RAC')),
			EXISTS (SELECT 1 FROM live_incidents WHERE train_number = $1 AND origin_date = $2)`, b.Train, b.OriginDate).Scan(&n, &held)
		if !held { // leave trains that are already in trouble alone
			cands = append(cands, cand{b, n})
		}
	}
	if len(cands) == 0 {
		return fmt.Errorf("no train is running at %s railway time", now.In(time.FixedZone("IST", 19800)).Format("15:04"))
	}
	// Most passengers first; among empty runs, the longest-distance train.
	slices.SortFunc(cands, func(a, b cand) int {
		return cmp.Or(b.booked-a.booked, s.Net.Trains[b.b.Train].DistKm-s.Net.Trains[a.b.Train].DistKm, strings.Compare(a.b.Train, b.b.Train))
	})
	c := cands[0]
	t := s.Net.Trains[c.b.Train]
	origin, _ := time.ParseInLocation(time.DateOnly, c.b.OriginDate, live.Midnight(now).Location())
	st := s.Live.Status(t, origin, now)
	if st.NextIdx < 0 {
		return fmt.Errorf("%s has no stop ahead", t.Number)
	}
	next := st.Stops[st.NextIdx]
	r.step("info", "Picked %s %s, running %d min late towards %s with %d booked passengers", t.Number, t.Name, st.DelayMin, next.Name, c.booked)

	// Make sure the watcher is following this run before anything goes wrong, so it has a
	// "before" to compare with.
	if err := live.Watch(ctx, s.Redis, t.Number, origin, 15*time.Minute); err != nil {
		return err
	}
	r.step("info", "Control room watching %s", t.Number)
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(13 * time.Second):
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO live_incidents (train_number, origin_date, stop_seq, delay_min, reason, en_route) VALUES ($1, $2, $3, 90, 'Engine failure (chaos drill)', true)`,
		t.Number, origin, st.NextIdx); err != nil {
		return err
	}
	if err := s.Live.LoadIncidents(ctx); err != nil {
		return err
	}
	r.fault("Engine failure before %s: 90 minutes' delay injected", next.Name)
	after := s.Live.Status(t, origin, now)
	r.step("ok", "Live status recalculated at once: now %d min late, expected at %s %s", after.DelayMin, next.Name, after.Stops[st.NextIdx].ExpArr.In(origin.Location()).Format("15:04"))
	var food int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM food_orders WHERE train_number = $1 AND origin_date = $2 AND status IN ('placed', 'accepted', 'preparing', 'handed_over')`,
		t.Number, origin).Scan(&food)
	r.metric("train", t.Number)
	r.metric("passengers", c.booked)
	r.metric("food_orders", food)

	// The watcher compares each running train with what it last saw and announces changes.
	var published bool
	err := wait(ctx, 2*time.Second, func() (bool, error) {
		e := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox WHERE topic = 'train.events' AND payload->>'type' = 'train.delayed'
			AND payload->>'train' = $1 AND payload->>'origin_date' = $2 AND created_at >= $3 AND published_at IS NOT NULL)`,
			t.Number, c.b.OriginDate, r.faultAt.Add(-time.Second)).Scan(&published)
		return e == nil && published, nil
	})
	if err != nil || !published {
		return err
	}
	msg := "The watcher announced the delay on the event bus: passengers' alerts, station boards and announcements follow"
	if food > 0 {
		msg += fmt.Sprintf("; %d food orders retime", food)
	}
	r.recovered("%s", msg)
	return nil
}

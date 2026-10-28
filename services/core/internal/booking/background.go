package booking

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// botPerson is a synthetic passenger account that makes background bookings.
type botPerson struct {
	id     pgtype.UUID
	name   string
	gender string
	age    int
}

func (s *Service) loadPeople(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `
		SELECT id, full_name, coalesce(gender, 'M'), coalesce(date_part('year', age(birth_date))::int, 30)
		  FROM users WHERE role = 'passenger' AND NOT is_demo ORDER BY id`)
	if err != nil {
		return err
	}
	s.people, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (botPerson, error) {
		var p botPerson
		err := r.Scan(&p.id, &p.name, &p.gender, &p.age)
		p.age = max(p.age, 5)
		return p, err
	})
	return err
}

// fillBackground tops a run up with bot bookings until each class and quota is as
// full as the demand model says it should be by now. Bots book real berths through
// the same allocator as people, so from this point availability is real inventory.
// It runs again on later visits and adds only the demand that has grown since.
func (s *Service) fillBackground(ctx context.Context, runID int64, t *network.Train, originDate time.Time, focus *Segment) error {
	if len(s.people) == 0 {
		return nil
	}
	now := s.Now()
	if !originDate.Add(24 * time.Hour).After(now) {
		return nil // the train has left; nothing more to book
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey(lockNSBackground, runID, "")); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT background_fill FROM train_runs WHERE id = $1`, runID).Scan(&raw); err != nil {
			return err
		}
		filled := map[string]float64{}
		if err := json.Unmarshal(raw, &filled); err != nil {
			return err
		}
		changed := false
		for _, class := range t.Classes(s.Net) {
			for _, quota := range availability.Quotas {
				if !availability.QuotaAllowed(s.Net, class, quota) {
					continue
				}
				if (quota == "TQ" || quota == "PT") && s.TatkalRush != nil && s.TatkalRush(originDate, s.Net.Classes[class].AC) {
					continue // during the opening rush Tatkal is sold only through the waiting room
				}
				gate := availability.Request{Train: t, OriginDate: originDate, Class: class, Quota: quota, FromIdx: 0, ToIdx: len(t.Stops) - 1}
				if _, open := availability.Gate(s.Net, gate, now); !open {
					continue // e.g. Tatkal not open yet: nobody has booked it
				}
				target, _ := s.Sim.Booked(gate, now)
				key := class + "/" + quota
				if target-filled[key] < 1 {
					continue
				}
				if err := lock(ctx, tx, runID, class); err != nil {
					return err
				}
				if _, err := s.addBots(ctx, tx, runID, t, originDate, class, quota, target-filled[key]); err != nil {
					return err
				}
				// Demand that found no room (waitlist full) was turned away; don't retry it on the next visit.
				filled[key] = target
				changed = true
				s.invalidate(runID, class)
			}
		}
		if focus != nil {
			did, err := s.fillFocus(ctx, tx, runID, t, originDate, *focus, filled)
			if err != nil {
				return err
			}
			changed = changed || did
		}
		if !changed {
			return nil
		}
		_, err := tx.Exec(ctx, `UPDATE train_runs SET background_fill = $2 WHERE id = $1`, runID, filled)
		return err
	})
}

// Segment is the part of a run a person is booking or looking at.
type Segment struct {
	Class, Quota string
	From, To     int
}

// fillFocus makes real availability for one journey match exactly what search showed
// for it (the demand model's status). The whole-route fill alone can disagree for a
// segment with its own rush, like Delhi→Patna before Chhath on a Delhi→Howrah train.
// If search said "AVL 40", bots covering the segment book until 40 berths are left;
// "RAC 5" means berths full and 4 people ahead on RAC; "WL 12" means RAC full and 11
// people ahead on the waitlist; "Regret" fills the waitlist to its limit.
func (s *Service) fillFocus(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, originDate time.Time,
	seg Segment, filled map[string]float64) (bool, error) {
	now := s.Now()
	req := availability.Request{Train: t, OriginDate: originDate, Class: seg.Class, Quota: seg.Quota, FromIdx: seg.From, ToIdx: seg.To}
	if _, open := availability.Gate(s.Net, req, now); !open {
		return false, nil
	}
	booked, simCap := s.Sim.Booked(req, now)
	key := fmt.Sprintf("focus/%s/%s/%d-%d", seg.Class, seg.Quota, seg.From, seg.To)
	if simCap < 1 || booked-filled[key] < 1 {
		return false, nil
	}
	filled[key] = booked
	shown := s.Sim.Status(ctx, req, now)
	if err := lock(ctx, tx, runID, seg.Class); err != nil {
		return false, err
	}
	cat, err := s.catalog(s.runTrain(runID, t), seg.Class)
	if err != nil {
		return false, err
	}
	occ, queue, err := loadOccupancy(ctx, tx, runID, seg.Class)
	if err != nil {
		return false, err
	}
	realCap := cat.Capacity(seg.Quota)
	targetFree, racAhead, wlAhead := 0, 0, 0
	wlLimit := availability.WaitlistLimit(seg.Quota, float64(realCap))
	switch shown.Code {
	case availability.AVL:
		targetFree = min(shown.Count, realCap)
	case availability.RAC:
		racAhead = shown.Count - 1
	case availability.WL:
		racAhead = 1 << 30 // every RAC place taken
		wlAhead = min(shown.Count-1, wlLimit-1)
	case availability.Regret:
		racAhead, wlAhead = 1<<30, wlLimit
	default:
		return true, nil
	}
	h := fnv.New64a()
	fmt.Fprint(h, runID, key, booked)
	r := rand.New(rand.NewPCG(h.Sum64(), uint64(runID)))

	var bookings []botBooking
	var pax []botPax
	add := func(from, to int, people []botPerson, placements []inventory.Placement) error {
		quote, err := fare.Compute(s.Net, t.Type, seg.Class, t.Stops[to].DistKm-t.Stops[from].DistKm, seg.Quota)
		if err != nil {
			return err
		}
		bookings = append(bookings, botBooking{people[0].id, from, to, quote, len(people)})
		for i, pl := range placements {
			pax = append(pax, botPax{bookingIdx: len(bookings) - 1, p: people[i], pl: pl})
		}
		return nil
	}
	pick := func(n int) []botPerson {
		people := make([]botPerson, n)
		for i := range people {
			people[i] = s.people[r.IntN(len(s.people))]
			if seg.Quota == "LD" {
				people[i].gender = "F"
			}
			if seg.Quota == "SS" && people[i].age < 60 {
				people[i].age = 60 + r.IntN(20)
			}
		}
		return people
	}

	// Confirmed travellers covering the segment, boarding at or before it and leaving at or after it.
	free := cat.FreeCount(occ, seg.Quota, seg.From, seg.To)
	for tries := 0; free > targetFree && tries < realCap*3; tries++ {
		from := r.IntN(seg.From + 1)
		if r.IntN(3) > 0 {
			from = seg.From // most board where the rush starts
		}
		to := seg.To + r.IntN(len(t.Stops)-seg.To)
		if r.IntN(3) > 0 {
			to = seg.To
		}
		size := min([]int{1, 1, 2, 2, 3, 4}[r.IntN(6)], free-targetFree)
		if seg.Quota == "SS" {
			size = 1
		}
		people := pick(size)
		ireq := inventory.Request{Quota: seg.Quota, From: from, To: to, Together: size > 1}
		for _, p := range people {
			ireq.Pax = append(ireq.Pax, inventory.Passenger{Age: p.age, Gender: p.gender})
		}
		trial := occ.Clone()
		placements, err := cat.Allocate(trial, ireq)
		if err != nil {
			return false, err
		}
		allCNF := true
		for _, pl := range placements {
			allCNF = allCNF && pl.Status == inventory.CNF
		}
		if !allCNF {
			continue // the wider journey doesn't fit; try another
		}
		occ = trial
		if err := add(from, to, people, placements); err != nil {
			return false, err
		}
		free = cat.FreeCount(occ, seg.Quota, seg.From, seg.To)
	}

	// Oversold: people queue on RAC, then the waitlist, for exactly this journey,
	// until the positions ahead match what search showed.
	if racAhead > 0 || wlAhead > 0 {
		rac, wl := queue[seg.Quota].rac, queue[seg.Quota].wl
		for tries := 0; tries < 2*(realCap+wlLimit); tries++ {
			people := pick(1)
			placements, err := cat.Allocate(occ.Clone(), inventory.Request{Quota: seg.Quota, From: seg.From, To: seg.To,
				Pax: []inventory.Passenger{{Age: people[0].age, Gender: people[0].gender}}})
			if err != nil {
				return false, err
			}
			pl := placements[0]
			switch {
			case pl.Status == inventory.CNF:
				// A berth still fits this journey: someone takes it first.
			case pl.Status == inventory.RAC && rac < racAhead:
				rac++
			case pl.Status == inventory.WL && wl < wlAhead:
				wl++
			default:
				tries = 1 << 30 // queue matches what was shown
				continue
			}
			placed, _ := cat.Allocate(occ, inventory.Request{Quota: seg.Quota, From: seg.From, To: seg.To,
				Pax: []inventory.Passenger{{Age: people[0].age, Gender: people[0].gender}}})
			if err := add(seg.From, seg.To, people, placed); err != nil {
				return false, err
			}
		}
	}
	if len(bookings) == 0 {
		return true, nil
	}
	s.invalidate(runID, seg.Class)
	return true, s.insertBots(ctx, tx, runID, t, originDate, seg.Class, seg.Quota, bookings, pax, r)
}

// stopWeight makes big stations likelier places to board and alight.
func stopWeight(n *network.Network, t *network.Train, i int) float64 {
	if i == 0 || i == len(t.Stops)-1 {
		return 6
	}
	switch n.Stations[t.Stops[i].Station].Importance {
	case 1:
		return 3
	case 2:
		return 1.5
	}
	return 0.5
}

func pickWeighted(r *rand.Rand, w []float64) int {
	total := 0.0
	for _, x := range w {
		total += x
	}
	v := r.Float64() * total
	for i, x := range w {
		if v -= x; v < 0 {
			return i
		}
	}
	return len(w) - 1
}

// addBots books parties of bot passengers until they add `units` of full-route
// demand (a passenger riding half the route counts as 0.5).
func (s *Service) addBots(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, originDate time.Time,
	class, quota string, units float64) (float64, error) {
	cat, err := s.catalog(s.runTrain(runID, t), class)
	if err != nil {
		return 0, err
	}
	occ, _, err := loadOccupancy(ctx, tx, runID, class)
	if err != nil {
		return 0, err
	}
	h := fnv.New64a()
	fmt.Fprint(h, runID, class, quota, units)
	r := rand.New(rand.NewPCG(h.Sum64(), uint64(runID)))
	segs := float64(cat.Segments)
	misses := 0

	board := make([]float64, len(t.Stops)-1)
	for i := range board {
		board[i] = stopWeight(s.Net, t, i)
	}

	var bookings []botBooking
	var pax []botPax
	added := 0.0
	for tries := 0; added < units && tries < cat.Capacity("")*3; tries++ {
		size := []int{1, 1, 1, 1, 2, 2, 3, 4}[r.IntN(8)]
		if quota == "SS" {
			size = 1
		}
		from := pickWeighted(r, board)
		alight := make([]float64, len(t.Stops))
		for i := from + 1; i < len(t.Stops); i++ {
			share := float64(t.Stops[i].DistKm-t.Stops[from].DistKm) / float64(max(t.DistKm, 1))
			alight[i] = stopWeight(s.Net, t, i) * (0.4 + 2*share)
		}
		to := pickWeighted(r, alight)
		if to <= from {
			continue
		}
		people := make([]botPerson, size)
		req := inventory.Request{Quota: quota, From: from, To: to, Together: size > 1}
		for i := range people {
			people[i] = s.people[r.IntN(len(s.people))]
			if quota == "LD" {
				people[i].gender = "F"
			}
			if quota == "SS" && people[i].age < 60 {
				people[i].age = 60 + r.IntN(20)
			}
			req.Pax = append(req.Pax, inventory.Passenger{Age: people[i].age, Gender: people[i].gender})
		}
		trial := occ.Clone()
		placements, err := cat.Allocate(trial, req)
		if err != nil {
			return 0, err
		}
		nWL := 0
		for _, p := range placements {
			if p.Status == inventory.WL {
				nWL++
			}
		}
		if nWL > 0 {
			// The whole-route fill only takes berths and RAC places. Waitlists are built
			// for a specific journey by fillFocus, so they match what search showed.
			misses++
			if misses > 30 {
				break
			}
			continue
		}
		occ = trial
		quote, err := fare.Compute(s.Net, t.Type, class, t.Stops[to].DistKm-t.Stops[from].DistKm, quota)
		if err != nil {
			return 0, err
		}
		bookings = append(bookings, botBooking{people[0].id, from, to, quote, size})
		for i, pl := range placements {
			pax = append(pax, botPax{bookingIdx: len(bookings) - 1, p: people[i], pl: pl})
		}
		added += float64(size) * float64(to-from) / segs
	}
	if len(bookings) == 0 {
		return added, nil
	}
	return added, s.insertBots(ctx, tx, runID, t, originDate, class, quota, bookings, pax, r)
}

// botBooking and botPax are generated bot bookings waiting to be stored.
type botBooking struct {
	user     pgtype.UUID
	from, to int
	fare     fare.Quote
	pax      int
}

type botPax struct {
	bookingIdx int
	p          botPerson
	pl         inventory.Placement
	rac, wl    *int
}

// insertBots numbers RAC/WL passengers and bulk-loads generated bookings.
func (s *Service) insertBots(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, originDate time.Time,
	class, quota string, bookings []botBooking, pax []botPax, r *rand.Rand) error {
	var err error

	// Number RAC and WL passengers in booking order.
	nRAC, nWL := 0, 0
	for _, p := range pax {
		if p.pl.Status == inventory.RAC {
			nRAC++
		} else if p.pl.Status == inventory.WL {
			nWL++
		}
	}
	racNo, wlNo := 0, 0
	if nRAC > 0 {
		if racNo, err = nextNumbers(ctx, tx, runID, class, quota, "RAC", nRAC); err != nil {
			return err
		}
	}
	if nWL > 0 {
		if wlNo, err = nextNumbers(ctx, tx, runID, class, quota, "WL", nWL); err != nil {
			return err
		}
	}
	for i := range pax {
		switch pax[i].pl.Status {
		case inventory.RAC:
			n := racNo
			racNo++
			pax[i].rac = &n
		case inventory.WL:
			n := wlNo
			wlNo++
			pax[i].wl = &n
		}
	}

	pnrs, err := nextPNRs(ctx, tx, len(bookings))
	if err != nil {
		return err
	}
	ids := make([]pgtype.UUID, len(bookings))
	confirmed := s.Now().Add(-time.Duration(r.IntN(3600)) * time.Second)
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"bookings"},
		[]string{"id", "pnr", "user_id", "run_id", "train_number", "journey_date", "from_station", "to_station", "from_idx", "to_idx",
			"class_code", "quota", "status", "source", "total_fare", "fare", "confirmed_at"},
		pgx.CopyFromSlice(len(bookings), func(i int) ([]any, error) {
			b := bookings[i]
			ids[i] = newUUID()
			journey := originDate.AddDate(0, 0, t.Stops[b.from].Time()/1440)
			return []any{ids[i], pnrs[i], b.user, runID, t.Number, journey, t.Stops[b.from].Station, t.Stops[b.to].Station,
				int16(b.from), int16(b.to), class, quota, "CONFIRMED", "background", b.fare.Total * b.pax, b.fare, confirmed}, nil
		})); err != nil {
		return fmt.Errorf("copy bookings: %w", err)
	}
	pids := make([]pgtype.UUID, len(pax))
	seq := map[int]int{}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"booking_passengers"},
		[]string{"id", "booking_id", "seq", "name", "age", "gender", "status", "coach_no", "berth_no", "berth_type", "rac_no", "wl_no", "fare"},
		pgx.CopyFromSlice(len(pax), func(i int) ([]any, error) {
			p := pax[i]
			pids[i] = newUUID()
			seq[p.bookingIdx]++
			var coach, btype any
			var berth any
			if p.pl.Unit != nil {
				coach, berth, btype = p.pl.Unit.Coach, int16(p.pl.Unit.Berth), p.pl.Unit.Type
			}
			return []any{pids[i], ids[p.bookingIdx], int16(seq[p.bookingIdx]), p.p.name, int16(p.p.age), p.p.gender,
				p.pl.Status, coach, berth, btype, p.rac, p.wl, bookings[p.bookingIdx].fare.Total}, nil
		})); err != nil {
		return fmt.Errorf("copy passengers: %w", err)
	}
	var allocs [][]any
	for i, p := range pax {
		if p.pl.Unit == nil {
			continue
		}
		b := bookings[p.bookingIdx]
		allocs = append(allocs, []any{runID, class, p.pl.Unit.Coach, int16(p.pl.Unit.Berth), int16(p.pl.Unit.Slot),
			pgtype.Range[pgtype.Int4]{Lower: pgtype.Int4{Int32: int32(b.from), Valid: true}, Upper: pgtype.Int4{Int32: int32(b.to), Valid: true},
				LowerType: pgtype.Inclusive, UpperType: pgtype.Exclusive, Valid: true},
			ids[p.bookingIdx], pids[i]})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"seat_allocations"},
		[]string{"run_id", "class_code", "coach_no", "berth_no", "slot", "seg", "booking_id", "passenger_id"},
		pgx.CopyFromRows(allocs)); err != nil {
		return fmt.Errorf("copy allocations: %w", err)
	}
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = id.String()
	}
	if err := setBookedStatus(ctx, tx, idStrs...); err != nil {
		return err
	}
	return nil
}

// newUUID returns a random (v4) UUID from crypto/rand. IDs must never repeat, so
// they don't come from the seeded generator that picks journeys.
func newUUID() pgtype.UUID {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return pgtype.UUID{Bytes: b, Valid: true}
}

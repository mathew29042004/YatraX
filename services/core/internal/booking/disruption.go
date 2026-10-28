package booking

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// ── Disruptions: control cancels, cuts short or diverts a run ──────────────

type Disruption struct {
	Kind       string   `json:"kind"`    // cancelled, short_terminated, diverted
	Station    string   `json:"station"` // short_terminated: where it now ends
	Skip       []string `json:"skip"`    // diverted: stations it no longer calls at
	ExtraDelay int      `json:"extra_delay"`
	Reason     string   `json:"reason"`
}

type Alternative struct {
	BookingID string    `json:"booking_id"`
	Train     string    `json:"train"`
	Name      string    `json:"name"`
	Class     string    `json:"class"`
	Departure time.Time `json:"departure"`
	HoldUntil time.Time `json:"hold_until"`
	Status    string    `json:"status"` // what the passengers got: "CNF S4/23, RAC 3"
}

type Affected struct {
	BookingID   string       `json:"booking_id"`
	PNR         string       `json:"pnr"`
	Passengers  int          `json:"passengers"`
	Refund      int          `json:"refund"`
	Partial     bool         `json:"partial"` // short-terminated after boarding: refund for the distance not travelled
	Alternative *Alternative `json:"alternative,omitempty"`
}

type DisruptionResult struct {
	Train         string     `json:"train"`
	OriginDate    string     `json:"origin_date"`
	Kind          string     `json:"kind"`
	Bookings      int        `json:"bookings"`       // passengers' bookings affected
	Simulated     int        `json:"simulated"`      // background bookings affected (no money involved)
	Refunded      int        `json:"refunded"`       // rupees, all refunds together
	Rebooked      int        `json:"rebooked"`       // bookings with seats held on another train
	NoAlternative int        `json:"no_alternative"` // fully refunded, but nothing to offer
	Affected      []Affected `json:"affected"`
}

var (
	ErrDisrupted     = errors.New("this train has been cancelled or changed by control for that journey")
	ErrAlreadyDone   = errors.New("this run already has a disruption")
	ErrBadDisruption = errors.New("bad disruption")
)

// RebookHold is how long seats on an alternative train are held for a passenger whose
// train was cancelled (much longer than a normal hold: they didn't ask to be here).
const RebookHold = 2 * time.Hour

type runDisruption struct {
	Disruption
	at   int          // short_terminated: stop index it now ends at
	skip map[int]bool // diverted: stop indices it skips
}

// affects reports whether a journey on a run is hit, and what the passenger is owed:
// the share of the fare to refund (1 = all of it).
func (d *runDisruption) affects(from, to int, t *network.Train) (bool, float64) {
	switch d.Kind {
	case "cancelled":
		return true, 1
	case "short_terminated":
		if to <= d.at {
			return false, 0
		}
		if from >= d.at {
			return true, 1
		}
		total := float64(t.Stops[to].DistKm - t.Stops[from].DistKm)
		if total <= 0 {
			return true, 1
		}
		return true, float64(t.Stops[to].DistKm-t.Stops[d.at].DistKm) / total
	case "diverted":
		return d.skip[from] || d.skip[to], 1
	}
	return false, 0
}

func (s *Service) disruptionFor(runID int64) *runDisruption {
	if v, ok := s.disrupted.Load(runID); ok {
		return v.(*runDisruption)
	}
	return nil
}

// loadDisruptions reads every run's disruption (and extra coaches) into memory.
func (s *Service) loadDisruptions(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT d.run_id, r.train_number, d.kind, coalesce(d.at_station, ''), d.skip, d.extra_delay, d.reason
		FROM run_disruptions d JOIN train_runs r ON r.id = d.run_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var train string
		var d Disruption
		if err := rows.Scan(&id, &train, &d.Kind, &d.Station, &d.Skip, &d.ExtraDelay, &d.Reason); err != nil {
			return err
		}
		if t := s.Net.Trains[train]; t != nil {
			s.disrupted.Store(id, compile(t, d))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.loadExtraCoaches(ctx)
}

func compile(t *network.Train, d Disruption) *runDisruption {
	rd := &runDisruption{Disruption: d, skip: map[int]bool{}}
	rd.at = t.StopIndex(d.Station)
	for _, code := range d.Skip {
		if i := t.StopIndex(code); i >= 0 {
			rd.skip[i] = true
		}
	}
	return rd
}

// checkDisruption refuses a new booking on a part of a run control has taken away.
func (s *Service) checkDisruption(runID int64, t *network.Train, from, to int) error {
	if d := s.disruptionFor(runID); d != nil {
		if hit, _ := d.affects(from, to, t); hit {
			return fmt.Errorf("%w (%s)", ErrDisrupted, d.Reason)
		}
	}
	return nil
}

// Disrupt applies a disruption to a run: records it, refunds everyone it hits (in full,
// or for the distance not travelled), holds seats for them on another train, and tells them.
func (s *Service) Disrupt(ctx context.Context, train string, originDate time.Time, d Disruption, by string) (*DisruptionResult, error) {
	t := s.Net.Trains[train]
	if t == nil {
		return nil, invalid("No such train %s", train)
	}
	if !t.RunsOn(originDate) {
		return nil, invalid("%s does not run on %s", train, originDate.Format("2 Jan"))
	}
	switch d.Kind {
	case "cancelled":
		d.Station, d.Skip = "", nil
	case "short_terminated":
		if i := t.StopIndex(d.Station); i <= 0 || i >= len(t.Stops)-1 {
			return nil, fmt.Errorf("%w: short-terminate at an intermediate stop of %s", ErrBadDisruption, train)
		}
		d.Skip = nil
	case "diverted":
		if len(d.Skip) == 0 {
			return nil, fmt.Errorf("%w: name the stations the diversion misses", ErrBadDisruption)
		}
		for _, code := range d.Skip {
			if i := t.StopIndex(code); i <= 0 || i >= len(t.Stops)-1 {
				return nil, fmt.Errorf("%w: %s isn't an intermediate stop of %s", ErrBadDisruption, code, train)
			}
		}
		d.Station = ""
	default:
		return nil, fmt.Errorf("%w: kind is cancelled, short_terminated or diverted", ErrBadDisruption)
	}
	if d.Reason == "" {
		d.Reason = map[string]string{"cancelled": "Cancelled by control", "short_terminated": "Short-terminated by control", "diverted": "Diverted by control"}[d.Kind]
	}
	if d.Skip == nil {
		d.Skip = []string{}
	}
	runID, err := s.ensureRun(ctx, t, originDate)
	if err != nil {
		return nil, err
	}
	rd := compile(t, d)
	tag, err := s.DB.Exec(ctx, `INSERT INTO run_disruptions (run_id, kind, at_station, skip, extra_delay, reason, created_by)
		VALUES ($1,$2,nullif($3,''),$4,$5,$6,nullif($7,'')::uuid) ON CONFLICT (run_id) DO NOTHING`,
		runID, d.Kind, d.Station, d.Skip, d.ExtraDelay, d.Reason, by)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAlreadyDone
	}
	s.disrupted.Store(runID, rd)

	res := &DisruptionResult{Train: train, OriginDate: originDate.Format(time.DateOnly), Kind: d.Kind, Affected: []Affected{}}
	rows, err := s.DB.Query(ctx, `SELECT id, source, from_idx, to_idx FROM bookings WHERE run_id = $1 AND status = 'CONFIRMED' ORDER BY confirmed_at`, runID)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id, source string
		from, to   int
	}
	var list []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.source, &h.from, &h.to); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, h)
	}
	rows.Close()

	reason := fmt.Sprintf("%s %s: %s", train, t.Name, d.Reason)
	for _, h := range list {
		ok, share := rd.affects(h.from, h.to, t)
		if !ok {
			continue
		}
		if h.source != "user" {
			// Simulated passengers: free their berths, no money involved.
			if share == 1 {
				err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
					b, err := s.getTx(ctx, tx, h.id)
					if err != nil {
						return err
					}
					_, _, err = s.cancelInTx(ctx, tx, b, fullQuote(b), "source", reason)
					return err
				})
				if err != nil {
					return nil, err
				}
			}
			res.Simulated++
			continue
		}
		a := Affected{BookingID: h.id, Partial: share < 1}
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			b, err := s.getTx(ctx, tx, h.id)
			if err != nil {
				return err
			}
			a.PNR = b.PNR
			if share == 1 {
				q := fullQuote(b)
				a.Passengers, a.Refund = len(q.Passengers), q.Refund
				_, _, err = s.cancelInTx(ctx, tx, b, q, "source", reason)
				return err
			}
			// Cut short after they boarded: they keep the trip they made; refund the rest.
			for _, p := range b.Passengers {
				if p.Status == "CAN" {
					continue
				}
				part := int(float64(p.Fare)*share + 0.5)
				a.Passengers++
				a.Refund += part
				if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET refund = coalesce(refund, 0) + $3 WHERE booking_id = $1 AND seq = $2`,
					b.ID, p.Seq, part); err != nil {
					return err
				}
			}
			if a.Refund == 0 {
				return nil
			}
			if err := ledger.Post(ctx, tx, "partial_refund", b.ID, reason,
				ledger.Line{Account: ledger.Fares, Amount: ledger.Paise(a.Refund)},
				ledger.Line{Account: ledger.Refunds, Amount: -ledger.Paise(a.Refund)}); err != nil {
				return err
			}
			var paymentID *string
			tx.QueryRow(ctx, `SELECT id FROM payments WHERE booking_id = $1 AND status IN ('CAPTURED', 'REFUNDED') ORDER BY updated_at DESC LIMIT 1`, b.ID).Scan(&paymentID)
			dest := "source"
			if paymentID == nil {
				dest = "wallet"
			}
			_, err = s.createRefund(ctx, tx, b.ID, b.UserID, paymentID, a.Refund, dest, reason)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("booking %s: %w", h.id, err)
		}
		res.Bookings++
		res.Refunded += a.Refund
		if !a.Partial {
			if alt, err := s.rebook(ctx, h.id, train); err == nil && alt != nil {
				a.Alternative = alt
				res.Rebooked++
			} else {
				res.NoAlternative++
			}
		}
		ev := map[string]any{"type": "disruption.notice", "booking_id": h.id, "train": train, "train_name": t.Name, "kind": d.Kind,
			"reason": d.Reason, "station": d.Station, "refund": a.Refund, "partial": a.Partial}
		if a.Alternative != nil {
			ev["alt_booking_id"], ev["alt_train"], ev["alt_name"] = a.Alternative.BookingID, a.Alternative.Train, a.Alternative.Name
			ev["alt_departure"], ev["hold_until"], ev["alt_status"] = a.Alternative.Departure.Format(time.RFC3339), a.Alternative.HoldUntil.Format(time.RFC3339), a.Alternative.Status
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ('booking.events', $1, $2)`, h.id, ev); err != nil {
			return nil, err
		}
		res.Affected = append(res.Affected, a)
	}
	for _, class := range t.Classes(s.Net) {
		s.invalidate(runID, class)
	}
	return res, nil
}

// fullQuote refunds every remaining passenger in full: the railway cancelled, not them.
func fullQuote(b *Booking) *CancelQuote {
	q := &CancelQuote{BookingID: b.ID}
	for _, p := range b.Passengers {
		if p.Status == "CAN" {
			continue
		}
		q.Passengers = append(q.Passengers, Refund{Seq: p.Seq, Status: p.Status, Fare: p.Fare, Refund: p.Fare, Rule: "Train cancelled or changed by the railway: full refund"})
		q.Refund += p.Fare
	}
	return q
}

// rebook holds seats for a booking's passengers on the best other train for the same
// journey: same stations and class, leaving no more than 3 hours earlier and within a day.
func (s *Service) rebook(ctx context.Context, bookingID, avoid string) (*Alternative, error) {
	b, err := s.Get(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	dep := b.Departure
	type cand struct {
		t      *network.Train
		board  time.Time
		leaves time.Time
	}
	var cands []cand
	for _, t := range s.Net.Calls[b.From] {
		i, j := t.StopIndex(b.From), t.StopIndex(b.To)
		if t.Number == avoid || i < 0 || j <= i || t.Capacity(s.Net, b.Class) == 0 {
			continue
		}
		for d := -2; d <= 1; d++ {
			day := time.Date(dep.In(network.IST).Year(), dep.In(network.IST).Month(), dep.In(network.IST).Day(), 0, 0, 0, 0, network.IST).AddDate(0, 0, d)
			origin := day.AddDate(0, 0, -t.Stops[i].Time()/1440)
			if !t.RunsOn(origin) {
				continue
			}
			leaves := origin.Add(time.Duration(t.Stops[i].Time()) * time.Minute)
			if leaves.Before(dep.Add(-3*time.Hour)) || leaves.After(dep.Add(24*time.Hour)) || leaves.Before(s.Now()) {
				continue
			}
			cands = append(cands, cand{t, time.Date(leaves.In(network.IST).Year(), leaves.In(network.IST).Month(), leaves.In(network.IST).Day(), 0, 0, 0, 0, network.IST), leaves})
		}
	}
	sort.Slice(cands, func(a, c int) bool {
		da, dc := cands[a].leaves.Sub(dep), cands[c].leaves.Sub(dep)
		return da.Abs() < dc.Abs()
	})
	var pax []PassengerInput
	for _, p := range b.Passengers {
		pax = append(pax, PassengerInput{Name: p.Name, Age: p.Age, Gender: p.Gender, Pref: p.Pref})
	}
	for _, c := range cands[:min(len(cands), 6)] {
		nb, err := s.Hold(ctx, b.UserID, "rebook:"+bookingID+":"+c.t.Number, HoldRequest{Train: c.t.Number, From: b.From, To: b.To,
			Date: c.board.Format(time.DateOnly), Class: b.Class, Quota: "GN", Passengers: pax})
		if err != nil {
			continue
		}
		if slices.ContainsFunc(nb.Passengers, func(p Passenger) bool { return p.Status == "WL" }) && len(cands) > 1 {
			// Prefer a train with berths; release a waitlisted hold and keep looking.
			s.Release(ctx, b.UserID, nb.ID)
			continue
		}
		until := s.Now().Add(RebookHold)
		if _, err := s.DB.Exec(ctx, `UPDATE bookings SET hold_expires_at = $2 WHERE id = $1`, nb.ID, until); err != nil {
			return nil, err
		}
		alt := &Alternative{BookingID: nb.ID, Train: c.t.Number, Name: c.t.Name, Class: b.Class, Departure: c.leaves, HoldUntil: until}
		for i, p := range nb.Passengers {
			if i > 0 {
				alt.Status += ", "
			}
			alt.Status += statusLabel(p.Status, p.Coach, p.Berth, p.RACNo, p.WLNo)
		}
		return alt, nil
	}
	return nil, nil
}

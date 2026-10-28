package booking

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/network"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

// Chart timing: the first chart is prepared 4 hours before the train leaves its
// origin, the second 30 minutes before.
const (
	Chart1Before = 4 * time.Hour
	Chart2Before = 30 * time.Minute
)

var ErrChartPrepared = errors.New("the chart is prepared; only berths still free can be booked")

type ChartBerth struct {
	Coach     string `json:"coach"`
	Berth     int    `json:"berth"`
	BerthType string `json:"berth_type"`
	Status    string `json:"status"` // CNF, RAC, WL (WL travels only with a confirmed party)
	PNR       string `json:"pnr"`
	Name      string `json:"name"`
	Age       int    `json:"age"`
	Gender    string `json:"gender"`
	From      string `json:"from"`
	To        string `json:"to"`
	Quota     string `json:"quota"`
}

type ChartClass struct {
	Class      string       `json:"class"`
	Passengers []ChartBerth `json:"passengers"`
	Vacant     int          `json:"vacant_berths"` // free for the whole journey
}

type Chart struct {
	RunID       int64        `json:"run_id"`
	Train       string       `json:"train"`
	OriginDate  string       `json:"origin_date"`
	Version     int          `json:"version"`
	PreparedAt  time.Time    `json:"prepared_at"`
	Classes     []ChartClass `json:"classes"`
	Promotions  int          `json:"promotions"`
	Upgrades    int          `json:"upgrades"` // passengers moved up a class for free
	AutoCancels int          `json:"auto_cancelled_bookings"`
}

func (s *Service) chartPrepared(runID int64) bool {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	return s.charted[runID]
}

// originDeparture is when a run leaves its origin.
func (s *Service) originDeparture(train string, originDate time.Time) time.Time {
	t := s.Net.Trains[train]
	return originDate.Add(time.Duration(t.Stops[0].Time()) * time.Minute)
}

// PrepareChart finalises a run. Chart 1 releases unsold quota berths to everyone,
// runs a last promotion, and cancels fully-waitlisted tickets with a refund (the
// fare minus ₹60 clerkage per passenger). Both charts publish a snapshot for the TTE app.
func (s *Service) PrepareChart(ctx context.Context, train string, originDate time.Time, version int) (*Chart, error) {
	t := s.Net.Trains[train]
	if t == nil {
		return nil, invalid("No such train %s", train)
	}
	runID, err := s.ensureRun(ctx, t, originDate)
	if err != nil {
		return nil, err
	}
	ch := &Chart{RunID: runID, Train: train, OriginDate: originDate.Format(time.DateOnly), Version: version, PreparedAt: s.Now()}
	classes := t.Classes(s.Net)
	sort.Strings(classes) // lock classes in a fixed order
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if version == 1 {
			// Free upgrades first, across classes, so the berths they leave go to the waitlist below.
			for _, class := range classes {
				if err := lock(ctx, tx, runID, class); err != nil {
					return err
				}
			}
			n, err := s.autoUpgrade(ctx, tx, runID, classes)
			if err != nil {
				return err
			}
			ch.Upgrades = n
		}
		for _, class := range classes {
			if err := lock(ctx, tx, runID, class); err != nil {
				return err
			}
			if _, err := s.expireStale(ctx, tx, runID, class); err != nil {
				return err
			}
			promos, err := s.promote(ctx, tx, runID, class, true)
			if err != nil {
				return err
			}
			ch.Promotions += len(promos)
			if version == 1 {
				n, err := s.cancelWaitlisted(ctx, tx, runID, class)
				if err != nil {
					return err
				}
				ch.AutoCancels += n
			}
			cc, err := s.snapshot(ctx, tx, runID, t, class)
			if err != nil {
				return err
			}
			ch.Classes = append(ch.Classes, *cc)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO charts (run_id, version, prepared_at, data) VALUES ($1, $2, $3, $4)
			ON CONFLICT (run_id, version) DO UPDATE SET prepared_at = EXCLUDED.prepared_at, data = EXCLUDED.data`,
			runID, version, ch.PreparedAt, ch); err != nil {
			return err
		}
		col := "chart1_at"
		if version == 2 {
			col = "chart2_at"
		}
		if _, err := tx.Exec(ctx, `UPDATE train_runs SET `+col+` = coalesce(`+col+`, $2) WHERE id = $1`, runID, ch.PreparedAt); err != nil {
			return err
		}
		return outbox(ctx, tx, "booking.events", train, map[string]any{
			"type": "chart.prepared", "run_id": runID, "train": train, "origin_date": ch.OriginDate, "version": version,
		})
	})
	for _, class := range classes {
		s.invalidate(runID, class)
	}
	if err != nil {
		return nil, err
	}
	s.runsMu.Lock()
	s.charted[runID] = true
	s.runsMu.Unlock()
	metrics.Promotions.Add(float64(ch.Promotions))
	slog.Info("chart prepared", "train", train, "origin_date", ch.OriginDate, "version", version,
		"promotions", ch.Promotions, "upgrades", ch.Upgrades, "auto_cancelled", ch.AutoCancels)
	return ch, nil
}

// cancelWaitlisted cancels bookings in which every remaining passenger is still
// waitlisted after the final promotion. Partly confirmed parties keep their
// waitlisted members, who may travel with them.
func (s *Service) cancelWaitlisted(ctx context.Context, tx pgx.Tx, runID int64, class string) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT b.id FROM bookings b
		 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED'
		   AND NOT EXISTS (SELECT 1 FROM booking_passengers p WHERE p.booking_id = b.id AND p.status IN ('CNF', 'RAC'))
		   AND EXISTS (SELECT 1 FROM booking_passengers p WHERE p.booking_id = b.id AND p.status = 'WL')`, runID, class)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		b, err := s.getTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		q := &CancelQuote{BookingID: id, ChartPrepared: true}
		for _, p := range b.Passengers {
			if p.Status != "WL" {
				continue
			}
			r := Refund{Seq: p.Seq, Status: "WL", Fare: p.Fare, Deduction: min(RACWLClerkage, p.Fare), Rule: "Waitlisted at chart preparation: cancelled automatically, fare refunded minus ₹60 clerkage"}
			r.Refund = p.Fare - r.Deduction
			q.Passengers = append(q.Passengers, r)
			q.Refund += r.Refund
			q.Deduction += r.Deduction
		}
		if _, _, err := s.cancelInTx(ctx, tx, b, q, "source", "Waitlisted at chart preparation"); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// snapshot lists who holds each berth in a class, plus how many berths are vacant
// for the whole run (the TTE can allot those on board).
func (s *Service) snapshot(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, class string) (*ChartClass, error) {
	rows, err := tx.Query(ctx, `
		SELECT coalesce(p.coach_no, ''), coalesce(p.berth_no, 0), coalesce(p.berth_type, ''), p.status::text, coalesce(b.pnr, ''),
		       p.name, p.age, p.gender, b.from_station, b.to_station, b.quota
		  FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND p.status IN ('CNF', 'RAC', 'WL')
		 ORDER BY p.coach_no NULLS LAST, p.berth_no, b.from_idx`, runID, class)
	if err != nil {
		return nil, err
	}
	pax, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChartBerth, error) {
		var c ChartBerth
		return c, r.Scan(&c.Coach, &c.Berth, &c.BerthType, &c.Status, &c.PNR, &c.Name, &c.Age, &c.Gender, &c.From, &c.To, &c.Quota)
	})
	if err != nil {
		return nil, err
	}
	cat, err := s.catalog(s.runTrain(runID, t), class)
	if err != nil {
		return nil, err
	}
	occ, _, err := loadOccupancy(ctx, tx, runID, class)
	if err != nil {
		return nil, err
	}
	vacant := 0
	for _, u := range cat.Units {
		if !u.RAC && occ[u.Key()] == 0 {
			vacant++
		}
	}
	if pax == nil {
		pax = []ChartBerth{}
	}
	return &ChartClass{Class: class, Passengers: pax, Vacant: vacant}, nil
}

// GetChart returns a published chart.
func (s *Service) GetChart(ctx context.Context, train string, originDate time.Time, version int) (*Chart, error) {
	runID, ok := s.runID(train, originDate)
	if !ok {
		return nil, ErrNotFound
	}
	var ch Chart
	err := s.DB.QueryRow(ctx, `SELECT data FROM charts WHERE run_id = $1 AND version = $2`, runID, version).Scan(&ch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &ch, err
}

// RunCharts prepares charts on schedule for runs that exist.
func (s *Service) RunCharts(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		rows, err := s.DB.Query(ctx, `SELECT train_number, origin_date, chart1_at IS NOT NULL, chart2_at IS NOT NULL
			FROM train_runs WHERE origin_date BETWEEN $1::date - 1 AND $1::date + 1 AND chart2_at IS NULL`, s.Now())
		if err != nil {
			slog.Warn("chart scan", "err", err)
			continue
		}
		type due struct {
			train  string
			date   time.Time
			c1, c2 bool
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			return d, r.Scan(&d.train, &d.date, &d.c1, &d.c2)
		})
		if err != nil {
			continue
		}
		now := s.Now()
		for _, d := range list {
			origin := time.Date(d.date.Year(), d.date.Month(), d.date.Day(), 0, 0, 0, 0, network.IST)
			dep := s.originDeparture(d.train, origin)
			if now.After(dep) {
				continue
			}
			version := 0
			switch {
			case !d.c1 && dep.Sub(now) <= Chart1Before:
				version = 1
			case d.c1 && !d.c2 && dep.Sub(now) <= Chart2Before:
				version = 2
			}
			if version > 0 {
				if _, err := s.PrepareChart(ctx, d.train, origin, version); err != nil {
					slog.Warn("chart", "train", d.train, "err", err)
				}
			}
		}
	}
}

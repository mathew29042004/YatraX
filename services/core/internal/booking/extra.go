package booking

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// ── Extra coaches ──────────────────────────────────────────────────────────
//
// When a run's waitlist is long, control can attach coaches to that one run. The run
// then has its own copy of the train with the extra coaches at the end of the rake, and
// the promotion engine moves waitlisted passengers onto the new berths at once.

// runTrain is the train as it runs on a given date: the timetabled rake plus any coaches
// added to that run.
func (s *Service) runTrain(runID int64, t *network.Train) *network.Train {
	v, ok := s.extras.Load(runID)
	if !ok {
		return t
	}
	extra := v.([]network.Coach)
	if c, ok := s.runTrains.Load(runID); ok && len(c.(*network.Train).Coaches) == len(t.Coaches)+len(extra) {
		return c.(*network.Train)
	}
	clone := *t
	clone.Coaches = append(append([]network.Coach(nil), t.Coaches...), extra...)
	s.runTrains.Store(runID, &clone)
	return &clone
}

func (s *Service) loadExtraCoaches(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT e.run_id, r.train_number, e.coach_no, e.class_code FROM run_extra_coaches e
		JOIN train_runs r ON r.id = e.run_id ORDER BY e.run_id, e.created_at, e.coach_no`)
	if err != nil {
		return err
	}
	defer rows.Close()
	byRun := map[int64][]network.Coach{}
	trains := map[int64]string{}
	for rows.Next() {
		var id int64
		var train, no, class string
		if err := rows.Scan(&id, &train, &no, &class); err != nil {
			return err
		}
		trains[id] = train
		byRun[id] = append(byRun[id], network.Coach{No: no, Class: class})
	}
	for id, list := range byRun {
		t := s.Net.Trains[trains[id]]
		if t == nil {
			continue
		}
		for i := range list {
			list[i].Position = len(t.Coaches) + i + 1
		}
		s.extras.Store(id, list)
	}
	return rows.Err()
}

type ExtraCoaches struct {
	Train      string      `json:"train"`
	OriginDate string      `json:"origin_date"`
	Class      string      `json:"class"`
	Coaches    []string    `json:"coaches"`
	Promotions []Promotion `json:"promotions"` // waitlisted and RAC passengers moved onto the new berths
	Waitlisted int         `json:"still_waitlisted"`
}

var coachNo = regexp.MustCompile(`^([A-Z]+)(\d+)$`)

// AddCoaches attaches count coaches of a class to one run and runs the promotion engine.
func (s *Service) AddCoaches(ctx context.Context, train string, originDate time.Time, class string, count int, by string) (*ExtraCoaches, error) {
	t := s.Net.Trains[train]
	if t == nil {
		return nil, invalid("No such train %s", train)
	}
	cc := s.Net.Classes[class]
	if cc == nil || !cc.Bookable {
		return nil, invalid("%s isn't a reserved class", class)
	}
	if count < 1 || count > 4 {
		return nil, invalid("add 1 to 4 coaches at a time")
	}
	if t.Capacity(s.Net, class) == 0 {
		return nil, invalid("%s has no %s coaches to add to", train, class)
	}
	runID, err := s.ensureRun(ctx, t, originDate)
	if err != nil {
		return nil, err
	}
	if s.chartPrepared(runID) {
		return nil, invalid("the chart is prepared; coaches can't be added now")
	}
	if len(s.runTrain(runID, t).Coaches)+count > 26 {
		return nil, invalid("a rake can't be longer than 26 coaches")
	}
	// Number them after the highest coach of the same series (S11 → S12, S13 …).
	prefix, top := "", 0
	for _, c := range s.runTrain(runID, t).Coaches {
		if c.Class != class {
			continue
		}
		if m := coachNo.FindStringSubmatch(c.No); m != nil {
			prefix = m[1]
			if n, _ := strconv.Atoi(m[2]); n > top {
				top = n
			}
		}
	}
	out := &ExtraCoaches{Train: train, OriginDate: originDate.Format(time.DateOnly), Class: class}
	for i := 1; i <= count; i++ {
		no := fmt.Sprintf("%s%d", prefix, top+i)
		if _, err := s.DB.Exec(ctx, `INSERT INTO run_extra_coaches (run_id, coach_no, class_code, added_by) VALUES ($1,$2,$3,nullif($4,'')::uuid)`,
			runID, no, class, by); err != nil {
			return nil, err
		}
		out.Coaches = append(out.Coaches, no)
	}
	if err := s.loadExtraCoaches(ctx); err != nil {
		return nil, err
	}
	s.invalidate(runID, class)
	if out.Promotions, err = s.Promote(ctx, runID, class); err != nil {
		return nil, err
	}
	if out.Promotions == nil {
		out.Promotions = []Promotion{}
	}
	s.DB.QueryRow(ctx, `SELECT count(*) FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND p.status = 'WL'`, runID, class).Scan(&out.Waitlisted)
	return out, nil
}

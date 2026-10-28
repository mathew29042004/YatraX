package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// ── The TTE's offline bundle ──────────────────────────────────────────────
//
// Before departure the TTE app downloads everything it needs to work with no network:
// who is on every berth, the coach layouts, the stops, a fare table for excess-fare
// tickets, and the key that verifies QR tickets. On-board work is then recorded as
// operations and synced; the server applies them in order and its allocation wins.

// Penalty is the minimum charge added to an excess-fare ticket (ticketless travel,
// wrong class, travelling beyond the destination).
const Penalty = 250

type TTEStop struct {
	Seq    int        `json:"seq"`
	Code   string     `json:"code"`
	Name   string     `json:"name"`
	Arr    *time.Time `json:"arr,omitempty"`
	Dep    *time.Time `json:"dep,omitempty"`
	DistKm int        `json:"distance_km"`
}

type TTEBerth struct {
	Berth int    `json:"berth"`
	Type  string `json:"type"`
	Quota string `json:"quota"`
	RAC   bool   `json:"rac"` // a side-lower shared by two RAC passengers
}

type TTECoach struct {
	Coach    string     `json:"coach"`
	Class    string     `json:"class"`
	Position int        `json:"position"`
	Berths   []TTEBerth `json:"berths"`
}

type TTEPassenger struct {
	ID        string     `json:"id"`
	BookingID string     `json:"booking_id"`
	PNR       string     `json:"pnr"`
	Seq       int        `json:"seq"`
	Name      string     `json:"name"`
	Age       int        `json:"age"`
	Gender    string     `json:"gender"`
	Status    string     `json:"status"` // CNF, RAC, WL, NOSHOW
	Coach     string     `json:"coach,omitempty"`
	Berth     int        `json:"berth,omitempty"`
	BerthType string     `json:"berth_type,omitempty"`
	RACNo     int        `json:"rac_no,omitempty"`
	WLNo      int        `json:"wl_no,omitempty"`
	Class     string     `json:"class"`
	Quota     string     `json:"quota"`
	FromIdx   int        `json:"from_idx"`
	ToIdx     int        `json:"to_idx"`
	Source    string     `json:"source"`
	BoardedAt *time.Time `json:"boarded_at,omitempty"`
	NoShowAt  string     `json:"no_show_at,omitempty"` // station where the berth was released
}

type TTEFine struct {
	Receipt   string    `json:"receipt_no"`
	Name      string    `json:"name"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Class     string    `json:"class"`
	Reason    string    `json:"reason"`
	Fare      int       `json:"fare"`
	Penalty   int       `json:"penalty"`
	Total     int       `json:"total"`
	Coach     string    `json:"coach,omitempty"`
	Berth     int       `json:"berth,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TTEHandoff struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	ToID      string    `json:"to_employee_id"`
	Station   string    `json:"station"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type TTEBundle struct {
	RunID      int64             `json:"run_id"`
	Train      string            `json:"train"`
	Name       string            `json:"name"`
	OriginDate string            `json:"origin_date"`
	Chart      *ChartInfo        `json:"chart"` // nil: no chart yet, this is the live allocation
	Stops      []TTEStop         `json:"stops"`
	Coaches    []TTECoach        `json:"coaches"`
	Passengers []TTEPassenger    `json:"passengers"`
	Fares      map[string][]int  `json:"fares"` // class -> total fare (rupees) for stop pair (i, j), i < j, row-major upper triangle
	Penalty    int               `json:"penalty"`
	Fines      []TTEFine         `json:"fines"`
	Handoffs   []TTEHandoff      `json:"handoffs"`
	OpsApplied int               `json:"ops_applied"`
	Generated  time.Time         `json:"generated_at"`
	Extra      map[string]string `json:"extra,omitempty"`
}

type ChartInfo struct {
	Version    int       `json:"version"`
	PreparedAt time.Time `json:"prepared_at"`
}

// FareIndex is where the fare for stops (i, j) sits in a bundle's fare table.
func FareIndex(n, i, j int) int { return i*(2*n-i-1)/2 + (j - i - 1) }

// TTEBundle builds a run's offline bundle.
func (s *Service) TTEBundle(ctx context.Context, train string, originDate time.Time) (*TTEBundle, error) {
	t := s.Net.Trains[train]
	if t == nil {
		return nil, invalid("No such train %s", train)
	}
	if !t.RunsOn(originDate) {
		return nil, invalid("%s does not run from %s on %s", train, t.Origin, originDate.Format("2 Jan"))
	}
	runID, err := s.ensureRun(ctx, t, originDate)
	if err != nil {
		return nil, err
	}
	b := &TTEBundle{RunID: runID, Train: t.Number, Name: t.Name, OriginDate: originDate.Format(time.DateOnly),
		Penalty: Penalty, Generated: s.Now(), Fares: map[string][]int{}, Fines: []TTEFine{}, Handoffs: []TTEHandoff{}}
	var ci ChartInfo
	err = s.DB.QueryRow(ctx, `SELECT version, prepared_at FROM charts WHERE run_id = $1 ORDER BY version DESC LIMIT 1`, runID).
		Scan(&ci.Version, &ci.PreparedAt)
	if err == nil {
		b.Chart = &ci
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	for i, st := range t.Stops {
		v := TTEStop{Seq: i, Code: st.Station, DistKm: st.DistKm}
		if stn := s.Net.Stations[st.Station]; stn != nil {
			v.Name = stn.Name
		}
		if st.Arr != nil {
			a := originDate.Add(time.Duration(*st.Arr) * time.Minute)
			v.Arr = &a
		}
		if st.Dep != nil {
			d := originDate.Add(time.Duration(*st.Dep) * time.Minute)
			v.Dep = &d
		}
		b.Stops = append(b.Stops, v)
	}

	n := len(t.Stops)
	for _, class := range t.Classes(s.Net) {
		cat, err := s.catalog(s.runTrain(runID, t), class)
		if err != nil {
			return nil, err
		}
		coaches := map[string]*TTECoach{}
		var order []string
		for _, u := range cat.Units {
			if u.Slot != 0 {
				continue // the second RAC place on a side-lower is the same physical berth
			}
			c := coaches[u.Coach]
			if c == nil {
				c = &TTECoach{Coach: u.Coach, Class: class, Position: u.CoachPos}
				coaches[u.Coach] = c
				order = append(order, u.Coach)
			}
			c.Berths = append(c.Berths, TTEBerth{Berth: u.Berth, Type: u.Type, Quota: u.Quota, RAC: u.RAC})
		}
		for _, k := range order {
			b.Coaches = append(b.Coaches, *coaches[k])
		}
		table := make([]int, n*(n-1)/2)
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				q, err := fare.Compute(s.Net, t.Type, class, t.Stops[j].DistKm-t.Stops[i].DistKm, "GN")
				if err == nil {
					table[FareIndex(n, i, j)] = q.Total
				}
			}
		}
		b.Fares[class] = table
	}
	sort.Slice(b.Coaches, func(i, j int) bool { return b.Coaches[i].Position < b.Coaches[j].Position })

	rows, err := s.DB.Query(ctx, `
		SELECT p.id, p.booking_id, coalesce(bk.pnr, ''), p.seq, p.name, p.age, p.gender, p.status::text,
		       coalesce(p.coach_no, ''), coalesce(p.berth_no, 0), coalesce(p.berth_type, ''), coalesce(p.rac_no, 0), coalesce(p.wl_no, 0),
		       bk.class_code, bk.quota, bk.from_idx, bk.to_idx, bk.source, p.boarded_at, coalesce(p.no_show_station, '')
		  FROM booking_passengers p JOIN bookings bk ON bk.id = p.booking_id
		 WHERE bk.run_id = $1 AND bk.status = 'CONFIRMED' AND p.status IN ('CNF', 'RAC', 'WL', 'NOSHOW')
		 ORDER BY bk.class_code, p.coach_no NULLS LAST, p.berth_no, bk.from_idx`, runID)
	if err != nil {
		return nil, err
	}
	b.Passengers, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TTEPassenger, error) {
		var p TTEPassenger
		return p, r.Scan(&p.ID, &p.BookingID, &p.PNR, &p.Seq, &p.Name, &p.Age, &p.Gender, &p.Status, &p.Coach, &p.Berth, &p.BerthType,
			&p.RACNo, &p.WLNo, &p.Class, &p.Quota, &p.FromIdx, &p.ToIdx, &p.Source, &p.BoardedAt, &p.NoShowAt)
	})
	if err != nil {
		return nil, err
	}

	rows, err = s.DB.Query(ctx, `SELECT receipt_no, name, from_station, to_station, class_code, reason, fare, penalty, total,
		coalesce(coach_no, ''), coalesce(berth_no, 0), created_at FROM tte_fines WHERE run_id = $1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	if b.Fines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TTEFine, error) {
		var f TTEFine
		return f, r.Scan(&f.Receipt, &f.Name, &f.From, &f.To, &f.Class, &f.Reason, &f.Fare, &f.Penalty, &f.Total, &f.Coach, &f.Berth, &f.CreatedAt)
	}); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT f.full_name, t.full_name, coalesce(t.employee_id, ''), h.at_station, h.note, h.created_at
		FROM tte_handoffs h JOIN users f ON f.id = h.from_user JOIN users t ON t.id = h.to_user WHERE h.run_id = $1 ORDER BY h.id`, runID)
	if err != nil {
		return nil, err
	}
	if b.Handoffs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TTEHandoff, error) {
		var h TTEHandoff
		return h, r.Scan(&h.From, &h.To, &h.ToID, &h.Station, &h.Note, &h.CreatedAt)
	}); err != nil {
		return nil, err
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM tte_ops WHERE run_id = $1`, runID).Scan(&b.OpsApplied); err != nil {
		return nil, err
	}
	return b, nil
}

// ── Operations ─────────────────────────────────────────────────────────────

type TTEOp struct {
	ID      string          `json:"op_id"`
	Kind    string          `json:"kind"` // board, no_show, assign_berth, fine, complaint, handoff
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

type TTEResult struct {
	OpID    string         `json:"op_id"`
	Kind    string         `json:"kind"`
	Status  string         `json:"status"` // applied, resolved (done differently: the server's allocation won), rejected, duplicate
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// NoShowAfter is how many stations past their boarding point a passenger may still
// turn up before the TTE can release the berth.
const NoShowAfter = 2

type opError struct{ msg string }

func (e opError) Error() string { return e.msg }

func reject(format string, a ...any) error { return opError{fmt.Sprintf(format, a...)} }

// ApplyTTEOps applies a device's operations in the order they were done. Each runs in
// its own transaction and is recorded by op_id, so a retried sync changes nothing.
func (s *Service) ApplyTTEOps(ctx context.Context, userID, train string, originDate time.Time, ops []TTEOp) ([]TTEResult, error) {
	t := s.Net.Trains[train]
	if t == nil {
		return nil, invalid("No such train %s", train)
	}
	runID, ok := s.runID(train, originDate)
	if !ok {
		return nil, invalid("Download the chart for %s on %s first", train, originDate.Format("2 Jan"))
	}
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].At.Before(ops[j].At) })
	out := make([]TTEResult, 0, len(ops))
	for _, op := range ops {
		res := TTEResult{OpID: op.ID, Kind: op.Kind}
		var prior []byte
		var priorStatus string
		err := s.DB.QueryRow(ctx, `SELECT status, result FROM tte_ops WHERE op_id = $1`, op.ID).Scan(&priorStatus, &prior)
		if err == nil {
			json.Unmarshal(prior, &res)
			res.Status = "duplicate"
			out = append(out, res)
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			var e error
			switch op.Kind {
			case "board":
				e = s.opBoard(ctx, tx, runID, t, op, &res)
			case "no_show":
				e = s.opNoShow(ctx, tx, runID, t, op, &res)
			case "assign_berth":
				e = s.opAssign(ctx, tx, runID, t, op, &res)
			case "fine":
				e = s.opFine(ctx, tx, userID, runID, t, originDate, op, &res)
			case "complaint":
				e = s.opComplaint(ctx, tx, userID, runID, op, &res)
			case "handoff":
				e = s.opHandoff(ctx, tx, userID, runID, t, op, &res)
			default:
				e = reject("unknown operation %q", op.Kind)
			}
			var oe opError
			if errors.As(e, &oe) {
				res.Status, res.Message = "rejected", oe.msg
				e = nil
			}
			if e != nil {
				return e
			}
			if res.Status == "" {
				res.Status = "applied"
			}
			at := op.At
			if at.IsZero() {
				at = s.Now()
			}
			_, e = tx.Exec(ctx, `INSERT INTO tte_ops (op_id, run_id, user_id, kind, payload, device_at, status, result)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, op.ID, runID, userID, op.Kind, op.Payload, at, res.Status, res)
			return e
		})
		if err != nil {
			return out, fmt.Errorf("op %s (%s): %w", op.ID, op.Kind, err)
		}
		out = append(out, res)
	}
	for _, class := range t.Classes(s.Net) {
		s.invalidate(runID, class)
	}
	return out, nil
}

type paxRow struct {
	id, booking, user, source, status, coach, berthType, class, quota, pnr, name string
	seq, berth, rac, from, to                                                    int
	boarded                                                                      bool
}

func (s *Service) passengerForUpdate(ctx context.Context, tx pgx.Tx, runID int64, id string) (*paxRow, error) {
	var p paxRow
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.booking_id, b.user_id, b.source, p.status::text, coalesce(p.coach_no, ''), coalesce(p.berth_type, ''),
		       b.class_code, b.quota, coalesce(b.pnr, ''), p.name, p.seq, coalesce(p.berth_no, 0), coalesce(p.rac_no, 0),
		       b.from_idx, b.to_idx, p.boarded_at IS NOT NULL
		  FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		 WHERE p.id = $1 AND b.run_id = $2 AND b.status = 'CONFIRMED' FOR UPDATE OF p`, id, runID).
		Scan(&p.id, &p.booking, &p.user, &p.source, &p.status, &p.coach, &p.berthType, &p.class, &p.quota, &p.pnr, &p.name,
			&p.seq, &p.berth, &p.rac, &p.from, &p.to, &p.boarded)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, reject("that passenger isn't on this train's chart")
	}
	return &p, err
}

func stopIdx(t *network.Train, code string) (int, error) {
	if i := t.StopIndex(code); i >= 0 {
		return i, nil
	}
	return 0, reject("%s isn't a stop of %s", code, t.Number)
}

func (s *Service) opBoard(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, op TTEOp, res *TTEResult) error {
	var in struct {
		Passenger string `json:"passenger_id"`
		Station   string `json:"station"`
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil {
		return reject("bad payload")
	}
	p, err := s.passengerForUpdate(ctx, tx, runID, in.Passenger)
	if err != nil {
		return err
	}
	if p.status == "NOSHOW" {
		return reject("%s was marked a no-show and the berth released; issue an excess-fare ticket instead", p.name)
	}
	if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET boarded_at = coalesce(boarded_at, $2), boarded_station = coalesce(boarded_station, nullif($3, ''))
		WHERE id = $1`, p.id, op.At, in.Station); err != nil {
		return err
	}
	res.Message = fmt.Sprintf("%s boarded", p.name)
	return nil
}

// moveRAC puts a RAC passenger on a full berth from stop `from` onward. A passenger
// holds one allocation, so the shared side-lower goes; the stretch already travelled
// on it is behind the train and can't be sold again anyway.
func (s *Service) moveRAC(ctx context.Context, tx pgx.Tx, runID int64, q *paxRow, coach string, berth int, berthType string, from int) error {
	start := max(from, q.from)
	if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE passenger_id = $1`, q.id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
		VALUES ($1,$2,$3,$4,0,int4range($5,$6),$7,$8)`, runID, q.class, coach, berth, start, q.to, q.booking, q.id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET status = 'CNF', coach_no = $2, berth_no = $3, berth_type = $4, rac_no = NULL WHERE id = $1`,
		q.id, coach, berth, berthType); err != nil {
		return err
	}
	if q.source == "user" {
		return outbox(ctx, tx, "booking.events", q.booking, map[string]any{
			"type": "passenger.promoted", "booking_id": q.booking, "seq": q.seq,
			"from": statusLabel("RAC", q.coach, q.berth, q.rac, 0), "to": statusLabel("CNF", coach, berth, 0, 0), "on_board": true,
		})
	}
	return nil
}

// berthFree reports whether a berth (slot 0) is free over stops [from, to).
func berthFree(occ inventory.Occupancy, coach string, berth, from, to int) bool {
	return occ[inventory.Key{Coach: coach, Berth: berth}]&inventory.SegMask(from, to) == 0
}

// racQueue lists RAC passengers of a class still travelling beyond stop k, best first.
func (s *Service) racQueue(ctx context.Context, tx pgx.Tx, runID int64, class string, k int) ([]*paxRow, error) {
	rows, err := tx.Query(ctx, `SELECT p.id FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND p.status = 'RAC' AND b.to_idx > $3
		ORDER BY p.rac_no, b.confirmed_at, p.seq`, runID, class, k)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var out []*paxRow
	for _, id := range ids {
		p, err := s.passengerForUpdate(ctx, tx, runID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Service) opNoShow(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, op TTEOp, res *TTEResult) error {
	var in struct {
		Passenger string `json:"passenger_id"`
		Station   string `json:"station"`   // where the train is now
		AssignTo  string `json:"assign_to"` // the RAC passenger the TTE moved onto the berth offline, if any
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil {
		return reject("bad payload")
	}
	p, err := s.passengerForUpdate(ctx, tx, runID, in.Passenger)
	if err != nil {
		return err
	}
	k, err := stopIdx(t, in.Station)
	if err != nil {
		return err
	}
	switch {
	case p.status == "NOSHOW":
		return reject("%s is already marked a no-show", p.name)
	case p.status != "CNF":
		return reject("only a passenger with a confirmed berth can be marked a no-show (%s is %s)", p.name, p.status)
	case p.boarded:
		return reject("%s has boarded", p.name)
	case k < p.from+NoShowAfter:
		return reject("%s boards at %s; the berth can be released once the train has passed %d stations beyond it",
			p.name, t.Stops[p.from].Station, NoShowAfter)
	case k >= p.to:
		return reject("%s's journey has already ended", p.name)
	}
	if err := lock(ctx, tx, runID, p.class); err != nil {
		return err
	}
	// Release the berth from here on (the part already run stays recorded).
	// (If their berth only starts here — they were themselves moved onto it here — there's nothing left to keep.)
	end := max(k, p.from+1)
	if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE passenger_id = $1 AND lower(seg) >= $2`, p.id, end); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE seat_allocations SET seg = int4range(lower(seg), $2) WHERE passenger_id = $1`, p.id, end); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET status = 'NOSHOW', no_show_station = $2 WHERE id = $1`, p.id, in.Station); err != nil {
		return err
	}
	place := fmt.Sprintf("%s/%d", p.coach, p.berth)
	res.Message = fmt.Sprintf("%s marked a no-show; %s released from %s", p.name, place, in.Station)
	res.Data = map[string]any{"released": place}

	// Move the next RAC passenger onto it — the one the TTE chose offline if the server agrees.
	occ, _, err := loadOccupancy(ctx, tx, runID, p.class)
	if err != nil {
		return err
	}
	queue, err := s.racQueue(ctx, tx, runID, p.class, k)
	if err != nil {
		return err
	}
	if in.AssignTo != "" {
		sort.SliceStable(queue, func(i, j int) bool { return queue[i].id == in.AssignTo && queue[j].id != in.AssignTo })
	}
	for _, q := range queue {
		if !berthFree(occ, p.coach, p.berth, max(k, q.from), q.to) {
			continue
		}
		if err := s.moveRAC(ctx, tx, runID, q, p.coach, p.berth, p.berthType, k); err != nil {
			return err
		}
		res.Data["moved"] = map[string]any{"passenger_id": q.id, "pnr": q.pnr, "name": q.name,
			"from": statusLabel("RAC", q.coach, q.berth, q.rac, 0), "to": place}
		res.Message += fmt.Sprintf("; RAC %d %s moved to %s", q.rac, q.name, place)
		if in.AssignTo != "" && q.id != in.AssignTo {
			res.Status = "resolved"
			res.Message += " (the passenger you chose had already been given a berth)"
		}
		return nil
	}
	if in.AssignTo != "" {
		res.Status = "resolved"
		res.Message += "; no RAC passenger could take it, so it stays vacant"
	}
	return nil
}

func (s *Service) opAssign(ctx context.Context, tx pgx.Tx, runID int64, t *network.Train, op TTEOp, res *TTEResult) error {
	var in struct {
		Passenger string `json:"passenger_id"`
		Coach     string `json:"coach"`
		Berth     int    `json:"berth"`
		Station   string `json:"station"`
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil {
		return reject("bad payload")
	}
	q, err := s.passengerForUpdate(ctx, tx, runID, in.Passenger)
	if err != nil {
		return err
	}
	if q.status != "RAC" {
		return reject("%s is %s, not RAC", q.name, q.status)
	}
	k, err := stopIdx(t, in.Station)
	if err != nil {
		return err
	}
	if err := lock(ctx, tx, runID, q.class); err != nil {
		return err
	}
	cat, err := s.catalog(s.runTrain(runID, t), q.class)
	if err != nil {
		return err
	}
	occ, _, err := loadOccupancy(ctx, tx, runID, q.class)
	if err != nil {
		return err
	}
	from := max(k, q.from)
	want := cat.Index(inventory.Key{Coach: in.Coach, Berth: in.Berth})
	if want >= 0 && !cat.Units[want].RAC && berthFree(occ, in.Coach, in.Berth, from, q.to) {
		u := cat.Units[want]
		if err := s.moveRAC(ctx, tx, runID, q, u.Coach, u.Berth, u.Type, k); err != nil {
			return err
		}
		res.Message = fmt.Sprintf("%s moved from RAC %d to %s/%d", q.name, q.rac, u.Coach, u.Berth)
		res.Data = map[string]any{"coach": u.Coach, "berth": u.Berth}
		return nil
	}
	// Someone else has that berth now: the server picks another free one if there is any.
	for _, u := range cat.Units {
		if u.Slot != 0 || u.RAC || !berthFree(occ, u.Coach, u.Berth, from, q.to) {
			continue
		}
		if err := s.moveRAC(ctx, tx, runID, q, u.Coach, u.Berth, u.Type, k); err != nil {
			return err
		}
		res.Status = "resolved"
		res.Message = fmt.Sprintf("%s/%d was taken, so %s was given %s/%d instead", in.Coach, in.Berth, q.name, u.Coach, u.Berth)
		res.Data = map[string]any{"coach": u.Coach, "berth": u.Berth}
		return nil
	}
	return reject("%s/%d is taken and no other berth is free for %s's journey", in.Coach, in.Berth, q.name)
}

func (s *Service) opFine(ctx context.Context, tx pgx.Tx, userID string, runID int64, t *network.Train, originDate time.Time, op TTEOp, res *TTEResult) error {
	var in struct {
		Name      string `json:"name"`
		Age       int    `json:"age"`
		Gender    string `json:"gender"`
		From      string `json:"from"`
		To        string `json:"to"`
		Class     string `json:"class"`
		Reason    string `json:"reason"`     // ticketless, wrong_class, beyond_destination
		PaidClass string `json:"paid_class"` // wrong_class: the class the ticket is for
		Coach     string `json:"coach"`      // optional: allot this vacant berth with the ticket
		Berth     int    `json:"berth"`
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil {
		return reject("bad payload")
	}
	from, err := stopIdx(t, in.From)
	if err != nil {
		return err
	}
	to, err := stopIdx(t, in.To)
	if err != nil {
		return err
	}
	if to <= from {
		return reject("the journey must run forward along the route")
	}
	if in.Name == "" || in.Age <= 0 || (in.Gender != "M" && in.Gender != "F" && in.Gender != "O") {
		return reject("name, age and gender are needed for the receipt")
	}
	cc := s.Net.Classes[in.Class]
	if cc == nil || !cc.Bookable || t.Capacity(s.Net, in.Class) == 0 {
		return reject("%s has no %s class", t.Number, in.Class)
	}
	dist := t.Stops[to].DistKm - t.Stops[from].DistKm
	quote, err := fare.Compute(s.Net, t.Type, in.Class, dist, "GN")
	if err != nil {
		return reject("%v", err)
	}
	charge := quote.Total
	switch in.Reason {
	case "ticketless", "beyond_destination":
	case "wrong_class":
		paid, err := fare.Compute(s.Net, t.Type, in.PaidClass, dist, "GN")
		if err != nil {
			return reject("the class on the ticket: %v", err)
		}
		charge = max(0, quote.Total-paid.Total)
	default:
		return reject("reason is ticketless, wrong_class or beyond_destination")
	}
	total := charge + Penalty
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('tte_receipt_seq')`).Scan(&seq); err != nil {
		return err
	}
	receipt := fmt.Sprintf("EFT%s%06d", originDate.Format("060102"), seq)

	// Allot a berth with the ticket if the TTE picked a free one.
	var bookingID *string
	if in.Coach != "" {
		if err := lock(ctx, tx, runID, in.Class); err != nil {
			return err
		}
		cat, err := s.catalog(s.runTrain(runID, t), in.Class)
		if err != nil {
			return err
		}
		occ, _, err := loadOccupancy(ctx, tx, runID, in.Class)
		if err != nil {
			return err
		}
		i := cat.Index(inventory.Key{Coach: in.Coach, Berth: in.Berth})
		if i < 0 || !berthFree(occ, in.Coach, in.Berth, from, to) {
			res.Status = "resolved"
			res.Message = fmt.Sprintf("%s/%d is no longer free, so the ticket was issued without a berth; ", in.Coach, in.Berth)
		} else {
			u := cat.Units[i]
			pnrs, err := nextPNRs(ctx, tx, 1)
			if err != nil {
				return err
			}
			board := originDate.Add(time.Duration(t.Stops[from].Time()) * time.Minute)
			board = time.Date(board.Year(), board.Month(), board.Day(), 0, 0, 0, 0, board.Location())
			var id, pid string
			if err := tx.QueryRow(ctx, `INSERT INTO bookings (user_id, run_id, train_number, journey_date, from_station, to_station,
				from_idx, to_idx, class_code, quota, status, source, total_fare, fare, preferences, pnr, confirmed_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'GN','CONFIRMED','tte',$10,$11,'{}',$12,$13) RETURNING id`,
				userID, runID, t.Number, board, in.From, in.To, from, to, in.Class, quote.Total, quote, pnrs[0], s.Now()).Scan(&id); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `INSERT INTO booking_passengers (booking_id, seq, name, age, gender, berth_pref, status, coach_no, berth_no, berth_type, fare, booked_status, boarded_at, boarded_station)
				VALUES ($1,1,$2,$3,$4,'','CNF',$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
				id, in.Name, in.Age, in.Gender, u.Coach, u.Berth, u.Type, quote.Total, fmt.Sprintf("CNF %s/%d", u.Coach, u.Berth), op.At, in.From).Scan(&pid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
				VALUES ($1,$2,$3,$4,0,int4range($5,$6),$7,$8)`, runID, in.Class, u.Coach, u.Berth, from, to, id, pid); err != nil {
				return err
			}
			bookingID = &id
			res.Message = fmt.Sprintf("Berth %s/%d allotted, PNR %s; ", u.Coach, u.Berth, pnrs[0])
		}
	}
	coach, berth := in.Coach, in.Berth
	if bookingID == nil {
		coach, berth = "", 0
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tte_fines (receipt_no, op_id, run_id, tte_id, booking_id, name, age, gender, from_station, to_station,
		class_code, reason, fare, penalty, total, coach_no, berth_no, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,nullif($16,''),nullif($17,0),$18)`,
		receipt, op.ID, runID, userID, bookingID, in.Name, in.Age, in.Gender, in.From, in.To, in.Class, in.Reason, charge, Penalty, total,
		coach, berth, op.At); err != nil {
		return err
	}
	// The TTE now holds the cash: the fare is a fare like any other, the penalty is revenue.
	if err := ledger.Post(ctx, tx, "excess_fare", receipt, "Excess-fare ticket on "+t.Number,
		ledger.Line{Account: ledger.TTECash(userID), Amount: ledger.Paise(total)},
		ledger.Line{Account: ledger.Fares, Amount: -ledger.Paise(charge)},
		ledger.Line{Account: ledger.Penalties, Amount: -ledger.Paise(Penalty)}); err != nil {
		return err
	}
	res.Message += fmt.Sprintf("receipt %s: ₹%d (₹%d fare + ₹%d penalty)", receipt, total, charge, Penalty)
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	res.Data["receipt_no"], res.Data["total"], res.Data["fare"] = receipt, total, charge
	if bookingID != nil {
		res.Data["booking_id"] = *bookingID
	}
	return nil
}

func (s *Service) opComplaint(ctx context.Context, tx pgx.Tx, userID string, runID int64, op TTEOp, res *TTEResult) error {
	var in struct {
		Category    string `json:"category"`
		Description string `json:"description"`
		Coach       string `json:"coach"`
		Berth       int    `json:"berth"`
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil || in.Description == "" {
		return reject("describe the complaint")
	}
	if in.Category == "" {
		in.Category = "other"
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO complaints (run_id, coach_no, berth_no, category, description, reported_by, created_at)
		VALUES ($1, nullif($2,''), nullif($3,0), $4, $5, $6, $7) RETURNING id`, runID, in.Coach, in.Berth, in.Category, in.Description, userID, op.At).Scan(&id); err != nil {
		return err
	}
	res.Message = fmt.Sprintf("Complaint #%d recorded (%s)", id, in.Category)
	res.Data = map[string]any{"complaint_id": id}
	return nil
}

func (s *Service) opHandoff(ctx context.Context, tx pgx.Tx, userID string, runID int64, t *network.Train, op TTEOp, res *TTEResult) error {
	var in struct {
		To      string `json:"to_employee_id"`
		Station string `json:"station"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal(op.Payload, &in); err != nil {
		return reject("bad payload")
	}
	if _, err := stopIdx(t, in.Station); err != nil {
		return err
	}
	var to, name string
	err := tx.QueryRow(ctx, `SELECT id, full_name FROM users WHERE employee_id = upper($1) AND role = 'tte'`, in.To).Scan(&to, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return reject("no TTE with employee ID %s", in.To)
	} else if err != nil {
		return err
	}
	if to == userID {
		return reject("you can't hand the chart to yourself")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tte_handoffs (run_id, from_user, to_user, at_station, note, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		runID, userID, to, in.Station, in.Note, op.At); err != nil {
		return err
	}
	res.Message = fmt.Sprintf("Chart handed to %s at %s", name, in.Station)
	return nil
}

// ── Duties ─────────────────────────────────────────────────────────────────

type Duty struct {
	Train      string    `json:"train"`
	Name       string    `json:"name"`
	OriginDate string    `json:"origin_date"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	At         time.Time `json:"at"` // when the train is at the TTE's station
	Charted    bool      `json:"charted"`
}

// Duties lists trains calling at a station within the next day and a half of
// (simulated) time: the runs a TTE posted there might work.
func (s *Service) Duties(ctx context.Context, station string, now time.Time) ([]Duty, error) {
	today := time.Date(now.In(network.IST).Year(), now.In(network.IST).Month(), now.In(network.IST).Day(), 0, 0, 0, 0, network.IST)
	var out []Duty
	for _, t := range s.Net.Calls[station] {
		i := t.StopIndex(station)
		for d := -3; d <= 2; d++ {
			origin := today.AddDate(0, 0, d)
			if !t.RunsOn(origin) {
				continue
			}
			at := origin.Add(time.Duration(t.Stops[i].Time()) * time.Minute)
			if at.Before(now.Add(-2*time.Hour)) || at.After(now.Add(36*time.Hour)) {
				continue
			}
			out = append(out, Duty{Train: t.Number, Name: t.Name, OriginDate: origin.Format(time.DateOnly), From: t.Origin, To: t.Destination, At: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > 40 {
		out = out[:40]
	}
	for i := range out {
		var charted bool
		s.DB.QueryRow(ctx, `SELECT r.chart1_at IS NOT NULL FROM train_runs r WHERE r.train_number = $1 AND r.origin_date = $2`,
			out[i].Train, out[i].OriginDate).Scan(&charted)
		out[i].Charted = charted
	}
	return out, nil
}

package station

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

type Service struct {
	DB   *pgxpool.Pool
	Net  *network.Network
	Live *live.Engine
	Now  func() time.Time // simulated: the station runs on railway time
}

var ErrNoStation = errors.New("no such station")

func (s *Service) station(code string) (*network.Station, error) {
	st := s.Net.Stations[code]
	if st == nil {
		return nil, ErrNoStation
	}
	return st, nil
}

func clock(t time.Time) string { return t.In(network.IST).Format("15:04") }

// ── Announcements ──────────────────────────────────────────────────────────

type Announcement struct {
	ID    string    `json:"id"` // stable: the same situation keeps the same id, so screens play it once
	Kind  string    `json:"kind"`
	Train string    `json:"train"`
	Name  string    `json:"name"`
	At    time.Time `json:"at"` // when the train is due here
	Lines []Line    `json:"lines"`
}

type Announcements struct {
	Station         *network.Station `json:"station"`
	SimNow          time.Time        `json:"sim_now"`
	Languages       []string         `json:"languages"`
	MissingRegional string           `json:"missing_regional,omitempty"` // a regional language without a template yet
	Items           []Announcement   `json:"items"`
}

// Announcements lists what the station should be saying now, from the live board.
func (s *Service) Announcements(code string) (*Announcements, error) {
	st, err := s.station(code)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	langs, missing := Languages(st.Language)
	out := &Announcements{Station: st, SimNow: now, Languages: langs, MissingRegional: missing, Items: []Announcement{}}
	for _, r := range s.Live.Board(code, now, 3*time.Hour) {
		t := s.Net.Trains[r.Train]
		if t == nil || r.State == "departed" {
			continue
		}
		f := facts{Number: r.Train, Name: r.Name, From: r.FromName, To: r.ToName, Platform: r.Platform, Delay: r.DelayMin}
		if i := t.StopIndex(code); i >= 0 {
			f.OldPlatform = t.Stops[i].Platform
		}
		due := r.ExpArr
		if due == nil {
			due = r.ExpDep
		}
		f.Time = clock(*due)
		add := func(kind, key string) {
			f.Kind = kind
			out.Items = append(out.Items, Announcement{ID: fmt.Sprintf("%s|%s|%s|%s", r.Train, r.OriginDate, kind, key),
				Kind: kind, Train: r.Train, Name: r.Name, At: *due, Lines: Compose(st.Language, f)})
		}
		switch {
		case r.State == "on_platform" && r.ExpDep != nil && r.ExpDep.Sub(now) <= 3*time.Minute:
			add(KindLeaving, "")
		case r.State == "on_platform":
			add(KindStanding, "")
		case r.State == "arriving":
			add(KindArriving, fmt.Sprint(r.Platform))
		case r.State == "expected" && r.DelayMin >= 15 && due.Sub(now) <= 3*time.Hour:
			add(KindDelayed, fmt.Sprint(r.DelayMin/15*15)) // a new announcement each time it slips another 15 minutes
		}
		if r.PlatformChanged && (r.State == "expected" || r.State == "arriving") && due.Sub(now) <= 90*time.Minute {
			add(KindPlatform, fmt.Sprint(r.Platform))
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].At.Before(out.Items[j].At) })
	return out, nil
}

// ── Coach position ─────────────────────────────────────────────────────────

type CoachSpot struct {
	Marker int    `json:"marker"` // the numbered board on the platform where the coach stops
	Coach  string `json:"coach"`
	Class  string `json:"class"`
}

type CoachPosition struct {
	Train        string      `json:"train"`
	Name         string      `json:"name"`
	OriginDate   string      `json:"origin_date"`
	Station      string      `json:"station"`
	Platform     int         `json:"platform"`
	Markers      int         `json:"markers"`
	EngineToward string      `json:"engine_toward"` // the engine end faces the way the train is going
	Spots        []CoachSpot `json:"spots"`
}

// PlatformMarkers is how many coach-position boards a platform has.
const PlatformMarkers = 26

// CoachPosition says where each coach of a run stops on its platform at a station:
// the rake centred on the platform, engine first, facing the direction of travel.
func (s *Service) CoachPosition(code, train string, origin time.Time) (*CoachPosition, error) {
	if _, err := s.station(code); err != nil {
		return nil, err
	}
	t := s.Net.Trains[train]
	if t == nil {
		return nil, fmt.Errorf("no train %s", train)
	}
	i := t.StopIndex(code)
	if i < 0 {
		return nil, fmt.Errorf("%s does not call at %s", train, code)
	}
	coaches := append([]network.Coach(nil), t.Coaches...)
	sort.Slice(coaches, func(a, b int) bool { return coaches[a].Position < coaches[b].Position })
	n := len(coaches) + 1 // + the engine
	markers := max(PlatformMarkers, n)
	start := (markers-n)/2 + 1
	cp := &CoachPosition{Train: t.Number, Name: t.Name, OriginDate: origin.Format(time.DateOnly), Station: code, Markers: markers}
	st := s.Live.Status(t, origin, s.Now())
	if i < len(st.Stops) {
		cp.Platform = st.Stops[i].Platform
	}
	toward := t.Destination
	if i+1 < len(t.Stops) {
		toward = t.Stops[i+1].Station
	}
	if stn := s.Net.Stations[toward]; stn != nil {
		cp.EngineToward = stn.Name
	}
	cp.Spots = append(cp.Spots, CoachSpot{Marker: start, Coach: "ENG", Class: "ENG"})
	for k, c := range coaches {
		cp.Spots = append(cp.Spots, CoachSpot{Marker: start + 1 + k, Coach: c.No, Class: c.Class})
	}
	return cp, nil
}

// ── Platforms, clashes and crowds ──────────────────────────────────────────

type Slot struct {
	Train      string    `json:"train"`
	Name       string    `json:"name"`
	OriginDate string    `json:"origin_date"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Start      time.Time `json:"start"` // platform occupied from …
	End        time.Time `json:"end"`   // … to
	State      string    `json:"state"`
	DelayMin   int       `json:"delay_min"`
	Scheduled  int       `json:"scheduled_platform"`
	Assigned   bool      `json:"assigned"`  // set by the station master
	Boarding   int       `json:"boarding"`  // reserved passengers getting on here
	Alighting  int       `json:"alighting"` // reserved passengers getting off here
	General    int       `json:"general"`   // estimated unreserved travellers
	Estimated  bool      `json:"estimated"` // no bookings opened for this run yet: reserved counts are estimates
}

type Platform struct {
	No    int    `json:"no"`
	Slots []Slot `json:"slots"`
}

type Clash struct {
	Platform   int    `json:"platform"`
	A          string `json:"a"`
	B          string `json:"b"`
	OverlapMin int    `json:"overlap_min"`
	Suggest    int    `json:"suggest"` // a platform free for B's whole stay, 0 if none
}

type Platforms struct {
	Station   *network.Station `json:"station"`
	SimNow    time.Time        `json:"sim_now"`
	Platforms []Platform       `json:"platforms"`
	Clashes   []Clash          `json:"clashes"`
	Hourly    []Crowd          `json:"hourly"`
}

type Crowd struct {
	Hour  time.Time `json:"hour"`
	Count int       `json:"count"` // people expected on the platforms: boarding, alighting and unreserved
	Level string    `json:"level"` // low, busy, crowded
}

const (
	dwellOrigin   = 20 * time.Minute // a starting train is placed on its platform this long before leaving
	dwellTerminus = 15 * time.Minute // a terminating train clears its platform this long after arriving
)

func overlap(a, b Slot) time.Duration {
	lo, hi := a.Start, a.End
	if b.Start.After(lo) {
		lo = b.Start
	}
	if b.End.Before(hi) {
		hi = b.End
	}
	return hi.Sub(lo)
}

func hashOf(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return h.Sum64()
}

// Platforms lays the next few hours of trains onto the station's platforms, finds
// clashes (two trains on one platform at once) and estimates the crowd.
func (s *Service) Platforms(ctx context.Context, code string, hours int) (*Platforms, error) {
	st, err := s.station(code)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	rows := s.Live.Board(code, now, time.Duration(hours)*time.Hour)
	out := &Platforms{Station: st, SimNow: now, Platforms: []Platform{}, Clashes: []Clash{}, Hourly: []Crowd{}}

	var assigned = map[string]bool{}
	if len(rows) > 0 {
		trains, dates := make([]string, len(rows)), make([]string, len(rows))
		for i, r := range rows {
			trains[i], dates[i] = r.Train, r.OriginDate
		}
		ar, err := s.DB.Query(ctx, `SELECT train_number || '|' || origin_date::text FROM platform_assignments
			WHERE station_code = $1 AND (train_number, origin_date) IN (SELECT * FROM unnest($2::text[], $3::date[]))`, code, trains, dates)
		if err != nil {
			return nil, err
		}
		keys, err := pgx.CollectRows(ar, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			assigned[k] = true
		}
	}
	counts, err := s.passengerCounts(ctx, code, rows)
	if err != nil {
		return nil, err
	}

	byPF := map[int][]Slot{}
	for _, r := range rows {
		if r.State == "cancelled" {
			continue // control took this call away: it holds no platform
		}
		t := s.Net.Trains[r.Train]
		sl := Slot{Train: r.Train, Name: r.Name, OriginDate: r.OriginDate, From: r.From, To: r.To, State: r.State, DelayMin: r.DelayMin,
			Assigned: assigned[r.Train+"|"+r.OriginDate]}
		if t != nil {
			if i := t.StopIndex(code); i >= 0 {
				sl.Scheduled = t.Stops[i].Platform
			}
		}
		switch {
		case r.ExpArr != nil && r.ExpDep != nil:
			sl.Start, sl.End = *r.ExpArr, *r.ExpDep
		case r.ExpDep != nil:
			sl.Start, sl.End = r.ExpDep.Add(-dwellOrigin), *r.ExpDep
		default:
			sl.Start, sl.End = *r.ExpArr, r.ExpArr.Add(dwellTerminus)
		}
		if sl.End.Sub(sl.Start) < 2*time.Minute {
			sl.End = sl.Start.Add(2 * time.Minute)
		}
		c, known := counts[r.Train+"|"+r.OriginDate]
		sl.Boarding, sl.Alighting = c[0], c[1]
		if !known && t != nil {
			// Nobody has opened this run's inventory yet: estimate from its reserved seats and where
			// this station sits on the route (most board at the origin and leave at the terminus).
			seats := 0
			for _, cl := range t.Classes(s.Net) {
				seats += t.Capacity(s.Net, cl)
			}
			mid := int(float64(seats) * 0.05 * float64(4-st.Importance))
			switch {
			case t.Origin == code:
				sl.Boarding = seats * 55 / 100
			case t.Destination == code:
				sl.Alighting = seats * 55 / 100
			default:
				sl.Boarding, sl.Alighting = mid, mid
			}
			sl.Estimated = true
		}
		if t != nil {
			gen := 0
			for _, co := range t.Coaches {
				if co.Class == "GN" || co.Class == "2S" {
					gen++
				}
			}
			// Unreserved coaches run full or more; busier stations put more people on.
			factor := map[int]float64{1: 0.9, 2: 0.5, 3: 0.25}[st.Importance]
			sl.General = int(float64(gen*90) * factor * (0.8 + float64(hashOf(r.Train, r.OriginDate)%40)/100))
		}
		byPF[r.Platform] = append(byPF[r.Platform], sl)
	}
	for pf := 1; pf <= st.Platforms; pf++ {
		slots := byPF[pf]
		if slots == nil {
			slots = []Slot{} // an idle platform: an empty list, not null
		}
		sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })
		out.Platforms = append(out.Platforms, Platform{No: pf, Slots: slots})
	}
	free := func(pf int, x Slot) bool {
		for _, o := range byPF[pf] {
			if o.Train != x.Train && overlap(o, x) > 0 {
				return false
			}
		}
		return true
	}
	for _, p := range out.Platforms {
		for i := 0; i < len(p.Slots); i++ {
			for j := i + 1; j < len(p.Slots); j++ {
				a, b := p.Slots[i], p.Slots[j]
				if ov := overlap(a, b); ov > 0 && b.End.After(now) {
					c := Clash{Platform: p.No, A: a.Train, B: b.Train, OverlapMin: int(ov.Minutes() + 0.5)}
					for pf := 1; pf <= st.Platforms; pf++ {
						if pf != p.No && free(pf, b) {
							c.Suggest = pf
							break
						}
					}
					out.Clashes = append(out.Clashes, c)
				}
			}
		}
	}
	// People on the platforms, hour by hour: boarders arrive ~20 min early, alighters leave quickly.
	start := now.Truncate(time.Hour)
	for h := 0; h < hours; h++ {
		lo, hi := start.Add(time.Duration(h)*time.Hour), start.Add(time.Duration(h+1)*time.Hour)
		n := 0
		for _, slots := range byPF {
			for _, sl := range slots {
				if sl.End.After(lo) && sl.Start.Before(hi) {
					n += sl.Boarding + sl.Alighting + sl.General
				}
			}
		}
		level := "low"
		switch {
		case n >= 4000:
			level = "crowded"
		case n >= 1500:
			level = "busy"
		}
		out.Hourly = append(out.Hourly, Crowd{Hour: lo, Count: n, Level: level})
	}
	return out, nil
}

// passengerCounts: reserved passengers boarding and alighting at a station, per run.
func (s *Service) passengerCounts(ctx context.Context, code string, rows []live.BoardRow) (map[string][2]int, error) {
	out := map[string][2]int{}
	if len(rows) == 0 {
		return out, nil
	}
	trains, dates := make([]string, len(rows)), make([]string, len(rows))
	for i, r := range rows {
		trains[i], dates[i] = r.Train, r.OriginDate
	}
	q, err := s.DB.Query(ctx, `
		SELECT r.train_number || '|' || r.origin_date::text,
		       count(*) FILTER (WHERE b.from_station = $1), count(*) FILTER (WHERE b.to_station = $1)
		  FROM bookings b JOIN train_runs r ON r.id = b.run_id JOIN booking_passengers p ON p.booking_id = b.id
		 WHERE b.status = 'CONFIRMED' AND p.status IN ('CNF', 'RAC', 'WL') AND (b.from_station = $1 OR b.to_station = $1)
		   AND (r.train_number, r.origin_date) IN (SELECT * FROM unnest($2::text[], $3::date[]))
		 GROUP BY 1`, code, trains, dates)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	for q.Next() {
		var k string
		var on, off int
		if err := q.Scan(&k, &on, &off); err != nil {
			return nil, err
		}
		out[k] = [2]int{on, off}
	}
	return out, q.Err()
}

var ErrPlatform = errors.New("no such platform at this station")

// Assign puts a train run on a platform at a station (or back on its timetabled one when
// platform is 0). The live engine, boards, announcements and passenger alerts follow.
func (s *Service) Assign(ctx context.Context, code, train string, origin time.Time, platform int, by string) error {
	st, err := s.station(code)
	if err != nil {
		return err
	}
	t := s.Net.Trains[train]
	if t == nil || t.StopIndex(code) < 0 {
		return fmt.Errorf("%s does not call at %s", train, code)
	}
	if platform < 0 || platform > st.Platforms {
		return ErrPlatform
	}
	if platform == 0 {
		_, err = s.DB.Exec(ctx, `DELETE FROM platform_assignments WHERE train_number = $1 AND origin_date = $2 AND station_code = $3`, train, origin, code)
	} else {
		_, err = s.DB.Exec(ctx, `INSERT INTO platform_assignments (train_number, origin_date, station_code, platform, assigned_by)
			VALUES ($1,$2,$3,$4,nullif($5,'')::uuid)
			ON CONFLICT (train_number, origin_date, station_code) DO UPDATE SET platform = EXCLUDED.platform,
			  assigned_by = EXCLUDED.assigned_by, assigned_at = now()`, train, origin, code, platform, by)
	}
	if err != nil {
		return err
	}
	return s.Live.LoadIncidents(ctx)
}

// ── Facilities desk ────────────────────────────────────────────────────────

type Facilities struct {
	RetiringRooms struct {
		Total int `json:"total"`
		Free  int `json:"free"`
	} `json:"retiring_rooms"`
	CloakRoom struct {
		Lockers int `json:"lockers"`
		Free    int `json:"free"`
	} `json:"cloak_room"`
	LostFound  []LostItem   `json:"lost_found"`
	Wheelchair []Wheelchair `json:"wheelchair"`
}

type LostItem struct {
	ID          int64      `json:"id"`
	Item        string     `json:"item"`
	Description string     `json:"description"`
	FoundAt     string     `json:"found_at"`
	Status      string     `json:"status"`
	ReturnedTo  string     `json:"returned_to,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ReturnedAt  *time.Time `json:"returned_at,omitempty"`
}

type Wheelchair struct {
	ID        int64     `json:"id"`
	Passenger string    `json:"passenger"`
	PNR       string    `json:"pnr"`
	Train     string    `json:"train"`
	NeededAt  string    `json:"needed_at"`
	Status    string    `json:"status"`
	Attendant string    `json:"attendant,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) Facilities(ctx context.Context, code string) (*Facilities, error) {
	st, err := s.station(code)
	if err != nil {
		return nil, err
	}
	f := &Facilities{LostFound: []LostItem{}, Wheelchair: []Wheelchair{}}
	// Rooms and lockers: sized by the station; in use now = online stays plus some walk-ins.
	day := s.Now().In(network.IST).Format(time.DateOnly)
	f.RetiringRooms.Total = map[int]int{1: 24, 2: 8, 3: 2}[st.Importance]
	f.CloakRoom.Lockers = map[int]int{1: 120, 2: 40, 3: 10}[st.Importance]
	var rooms, lockers int
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(sum(units) FILTER (WHERE unit_type = 'ac_room'), 0), coalesce(sum(units) FILTER (WHERE unit_type = 'locker'), 0)
		FROM station_stays WHERE station_code = $1 AND status IN ('booked', 'checked_in') AND check_in <= $2 AND check_in + make_interval(hours => hours) > $2`,
		code, s.Now()).Scan(&rooms, &lockers); err != nil {
		return nil, err
	}
	walkRooms := int(hashOf(code, day, "rooms") % uint64(f.RetiringRooms.Total/3+1))
	walkLockers := int(hashOf(code, day, "lockers") % uint64(f.CloakRoom.Lockers/3+1))
	f.RetiringRooms.Free = max(0, f.RetiringRooms.Total-rooms-walkRooms)
	f.CloakRoom.Free = max(0, f.CloakRoom.Lockers-lockers-walkLockers)
	rows, err := s.DB.Query(ctx, `SELECT id, item, description, found_at, status, coalesce(returned_to, ''), created_at, returned_at
		FROM lost_found WHERE station_code = $1 ORDER BY id DESC LIMIT 50`, code)
	if err != nil {
		return nil, err
	}
	if f.LostFound, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (LostItem, error) {
		var x LostItem
		return x, r.Scan(&x.ID, &x.Item, &x.Description, &x.FoundAt, &x.Status, &x.ReturnedTo, &x.CreatedAt, &x.ReturnedAt)
	}); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT id, passenger, pnr, train_number, needed_at, status, coalesce(attendant, ''), created_at
		FROM wheelchair_requests WHERE station_code = $1 ORDER BY (status = 'done'), id DESC LIMIT 50`, code)
	if err != nil {
		return nil, err
	}
	if f.Wheelchair, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Wheelchair, error) {
		var x Wheelchair
		return x, r.Scan(&x.ID, &x.Passenger, &x.PNR, &x.Train, &x.NeededAt, &x.Status, &x.Attendant, &x.CreatedAt)
	}); err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Service) AddLost(ctx context.Context, code, item, desc, where, by string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO lost_found (station_code, item, description, found_at, reported_by) VALUES ($1,$2,$3,$4,nullif($5,'')::uuid) RETURNING id`,
		code, item, desc, where, by).Scan(&id)
	return id, err
}

var ErrNotFound = errors.New("not found")

func (s *Service) ReturnLost(ctx context.Context, code string, id int64, to string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE lost_found SET status = 'returned', returned_to = $3, returned_at = now()
		WHERE id = $1 AND station_code = $2 AND status = 'held'`, id, code, to)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Service) AddWheelchair(ctx context.Context, code, passenger, pnr, train, at, by string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO wheelchair_requests (station_code, passenger, pnr, train_number, needed_at, created_by)
		VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid) RETURNING id`, code, passenger, pnr, train, at, by).Scan(&id)
	return id, err
}

func (s *Service) UpdateWheelchair(ctx context.Context, code string, id int64, status, attendant string) error {
	if status != "assigned" && status != "done" {
		return errors.New("status is assigned or done")
	}
	tag, err := s.DB.Exec(ctx, `UPDATE wheelchair_requests SET status = $3, attendant = coalesce(nullif($4, ''), attendant), updated_at = now()
		WHERE id = $1 AND station_code = $2`, id, code, status, attendant)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

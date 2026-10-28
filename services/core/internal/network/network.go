// Package network holds an in-memory copy of the static railway network —
// stations, trains, stops, rakes and fare rules — loaded once at startup.
// Search, fares and availability read from it instead of querying Postgres per request.
package network

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// IST is Indian Standard Time; every timetable time is IST.
var IST = time.FixedZone("IST", 5*3600+1800)

type Station struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Zone       string  `json:"zone"`
	Division   string  `json:"division"`
	Language   string  `json:"language"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Importance int     `json:"importance"`
	Platforms  int     `json:"platforms"`
}

type TrainType struct {
	Code, Name     string
	FareMultiplier float64
	Superfast      bool
	FlexiFare      bool
}

type CoachClass struct {
	Code, Name        string
	Bookable, AC      bool
	Seats             int
	Layout            string
	Sort              int
	FarePerKm         float64
	ReservationCharge int
	SuperfastCharge   int
	GSTPct            float64
	TatkalPct         *float64
	TatkalMin         *int
	TatkalMax         *int
	MinDistanceKm     int
}

type TaperBand struct {
	FromKm, ToKm int
	Factor       float64
}

type Stop struct {
	Seq      int
	Station  string
	Arr, Dep *int // minutes after midnight of the origin day
	DistKm   int
	Platform int
}

// Time is when the train is at the stop: departure if it departs, else arrival.
func (s Stop) Time() int {
	if s.Dep != nil {
		return *s.Dep
	}
	return *s.Arr
}

type Coach struct {
	No, Class string
	Position  int
}

type Train struct {
	Number, Name, Type  string
	Origin, Destination string
	RunningDays         int16 // bit 0 = Monday
	DistKm              int
	Pantry              bool
	Return              string
	Stops               []Stop
	Coaches             []Coach
	stopIndex           map[string]int
	// SpecialOf marks a special train (a copy of that train): it runs only on dates added
	// to Specials, never by its weekly pattern.
	SpecialOf string
	Specials  *sync.Map // origin date "YYYY-MM-DD" -> true; shared by copies of the train
}

// AddSpecialRun lets a special train run on a date.
func (t *Train) AddSpecialRun(originDate time.Time) {
	if t.Specials != nil {
		t.Specials.Store(originDate.Format(time.DateOnly), true)
	}
}

// StopIndex returns the position of a station in the train's stops, or -1.
func (t *Train) StopIndex(code string) int {
	if i, ok := t.stopIndex[code]; ok {
		return i
	}
	return -1
}

// RunsOn reports whether the train leaves its origin on the given date.
func (t *Train) RunsOn(originDate time.Time) bool {
	if t.SpecialOf != "" {
		if t.Specials == nil {
			return false
		}
		_, ok := t.Specials.Load(originDate.Format(time.DateOnly))
		return ok
	}
	wd := (int(originDate.Weekday()) + 6) % 7 // Monday = 0
	return t.RunningDays&(1<<wd) != 0
}

// Classes lists the train's bookable classes, best first.
func (t *Train) Classes(n *Network) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range t.Coaches {
		if cc := n.Classes[c.Class]; cc != nil && cc.Bookable && !seen[c.Class] {
			seen[c.Class] = true
			out = append(out, c.Class)
		}
	}
	sort.Slice(out, func(i, j int) bool { return n.Classes[out[i]].Sort < n.Classes[out[j]].Sort })
	return out
}

// Capacity is the number of berths or seats of a class on the train.
func (t *Train) Capacity(n *Network, class string) int {
	total := 0
	for _, c := range t.Coaches {
		if c.Class == class {
			total += n.Classes[class].Seats
		}
	}
	return total
}

type Network struct {
	Stations   map[string]*Station
	Trains     map[string]*Train
	TrainTypes map[string]*TrainType
	Classes    map[string]*CoachClass
	Taper      []TaperBand
	// Calls lists, per station, every train stopping there.
	Calls    map[string][]*Train
	LoadedAt time.Time
}

// Load reads the whole static network from Postgres.
func Load(ctx context.Context, pool *pgxpool.Pool) (*Network, error) {
	n := New()

	rows, err := pool.Query(ctx, `
		SELECT st.code, st.name, st.state, d.zone_code, d.name, st.language, st.lat, st.lon, st.importance, st.platforms
		  FROM stations st JOIN divisions d ON d.id = st.division_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s Station
		if err := rows.Scan(&s.Code, &s.Name, &s.State, &s.Zone, &s.Division, &s.Language, &s.Lat, &s.Lon, &s.Importance, &s.Platforms); err != nil {
			return nil, err
		}
		n.Stations[s.Code] = &s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `SELECT code, name, fare_multiplier::float8, superfast, flexi_fare FROM train_types`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t TrainType
		if err := rows.Scan(&t.Code, &t.Name, &t.FareMultiplier, &t.Superfast, &t.FlexiFare); err != nil {
			return nil, err
		}
		n.TrainTypes[t.Code] = &t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT code, name, bookable, ac, seats_per_coach, layout, sort_order, fare_per_km::float8, reservation_charge,
		       superfast_charge, gst_pct::float8, tatkal_pct::float8, tatkal_min, tatkal_max, min_distance_km
		  FROM coach_classes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c CoachClass
		if err := rows.Scan(&c.Code, &c.Name, &c.Bookable, &c.AC, &c.Seats, &c.Layout, &c.Sort, &c.FarePerKm,
			&c.ReservationCharge, &c.SuperfastCharge, &c.GSTPct, &c.TatkalPct, &c.TatkalMin, &c.TatkalMax, &c.MinDistanceKm); err != nil {
			return nil, err
		}
		n.Classes[c.Code] = &c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `SELECT from_km, to_km, factor::float8 FROM fare_taper ORDER BY from_km`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b TaperBand
		if err := rows.Scan(&b.FromKm, &b.ToKm, &b.Factor); err != nil {
			return nil, err
		}
		n.Taper = append(n.Taper, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT number, name, type_code, origin, destination, running_days, distance_km, pantry, coalesce(return_number, ''), coalesce(special_of, '')
		  FROM trains`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t := &Train{Specials: &sync.Map{}}
		if err := rows.Scan(&t.Number, &t.Name, &t.Type, &t.Origin, &t.Destination, &t.RunningDays, &t.DistKm, &t.Pantry, &t.Return, &t.SpecialOf); err != nil {
			return nil, err
		}
		n.Trains[t.Number] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `
		SELECT train_number, seq, station_code, arrival_min, departure_min, distance_km, platform
		  FROM train_stops ORDER BY train_number, seq`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var num string
		var s Stop
		if err := rows.Scan(&num, &s.Seq, &s.Station, &s.Arr, &s.Dep, &s.DistKm, &s.Platform); err != nil {
			return nil, err
		}
		t := n.Trains[num]
		t.Stops = append(t.Stops, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `SELECT train_number, coach_no, class_code, position FROM train_coaches ORDER BY train_number, position`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var num string
		var c Coach
		if err := rows.Scan(&num, &c.No, &c.Class, &c.Position); err != nil {
			return nil, err
		}
		n.Trains[num].Coaches = append(n.Trains[num].Coaches, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for num, t := range n.Trains {
		if len(t.Stops) < 2 {
			return nil, fmt.Errorf("train %s has %d stops", num, len(t.Stops))
		}
		n.Add(t)
	}
	return n, nil
}

// New returns an empty network for callers that build one in memory (tests, the seed).
func New() *Network {
	return &Network{
		Stations: map[string]*Station{}, Trains: map[string]*Train{}, TrainTypes: map[string]*TrainType{},
		Classes: map[string]*CoachClass{}, Calls: map[string][]*Train{}, LoadedAt: time.Now(),
	}
}

// Add registers a train whose Stops are filled in, indexing it by station.
func (n *Network) Add(t *Train) {
	t.stopIndex = make(map[string]int, len(t.Stops))
	for i, s := range t.Stops {
		t.stopIndex[s.Station] = i
		n.Calls[s.Station] = append(n.Calls[s.Station], t)
	}
	n.Trains[t.Number] = t
}

// DistanceKm is the great-circle distance between two stations.
func DistanceKm(a, b *Station) float64 {
	const r = 6371.0
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat, dLon := la2-la1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// NearbyKm is how far "nearby stations" reach, enough to group a city's terminals
// (Chennai Central, Egmore and Tambaram) without merging neighbouring cities.
const NearbyKm = 30

// Nearby returns the station plus every station within NearbyKm, closest first.
func (n *Network) Nearby(code string) []string {
	base := n.Stations[code]
	if base == nil {
		return nil
	}
	type cand struct {
		code string
		km   float64
	}
	var cs []cand
	for c, s := range n.Stations {
		if km := DistanceKm(base, s); km <= NearbyKm {
			cs = append(cs, cand{c, km})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].km != cs[j].km {
			return cs[i].km < cs[j].km
		}
		return cs[i].code < cs[j].code
	})
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.code
	}
	return out
}

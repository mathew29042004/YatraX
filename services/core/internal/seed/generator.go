// Package seed generates YatraX's synthetic railway world: stations, trains,
// schedules, rakes, passengers, staff and vendors. Generation is pure and
// deterministic for a given random seed; Write loads the result into Postgres.
package seed

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed data/stations.csv
var stationsCSV []byte

type Station struct {
	Code, Name, State, Zone, Division string
	Lat, Lon                          float64
	Importance, Platforms             int
	Language                          string
}

type Stop struct {
	Station  string
	Arr, Dep *int // minutes after midnight of the origin departure day
	DistKm   int
	Platform int
}

type Coach struct {
	No, Class string
	Pos       int
}

type Train struct {
	Number, Name, Type  string
	Origin, Destination string
	Days                int16
	DistKm              int
	Pantry              bool
	Return              string
	Stops               []Stop
	Coaches             []Coach
}

type Posting struct {
	Designation, Zone, Division, Station string
}

type Person struct {
	ID         [16]byte
	Role       string
	FullName   string
	Phone      string
	EmployeeID string
	Gender     string
	Birth      time.Time
	Language   string
	Home       string
	Demo       bool
	Posting    *Posting
}

type Vendor struct {
	ID, Owner [16]byte
	Name      string
	Station   string
	Cuisines  []string
	Veg       bool
	FSSAI     string
	Rating    float64
}

type World struct {
	Stations []Station
	Trains   []Train
	People   []Person
	Vendors  []Vendor
}

// Demo account identifiers, stable across seeds.
const (
	DemoPassengerPhone = "9000000001"
	DemoStationCode    = "MAS"
	DemoVendorStation  = "SA"
)

type gen struct {
	rng      *rand.Rand
	stations map[string]*Station
	phones   map[string]bool
	empSeq   map[string]int
	today    time.Time
}

// Generate builds the world. nPassengers controls the passenger population.
func Generate(seed uint64, nPassengers int) (*World, error) {
	g := &gen{
		rng:      rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		stations: map[string]*Station{},
		phones:   map[string]bool{DemoPassengerPhone: true},
		empSeq:   map[string]int{},
		today:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	w := &World{}
	var err error
	if w.Stations, err = g.loadStations(); err != nil {
		return nil, err
	}
	if w.Trains, err = g.trains(); err != nil {
		return nil, err
	}
	w.People = append(w.People, g.passengers(nPassengers)...)
	staff, vendors := g.staff(w.Stations)
	w.People = append(w.People, staff...)
	w.Vendors = vendors
	return w, nil
}

var platformOverride = map[string]int{
	"HWH": 23, "CSMT": 18, "MAS": 17, "NDLS": 16, "SDAH": 21, "DLI": 18, "SBC": 10, "ADI": 12,
	"MMCT": 9, "LTT": 5, "PNBE": 10, "LKO": 9, "CNB": 10, "NGP": 8, "BPL": 6, "ERS": 6, "TVC": 5,
}

// loadStations reads the curated stations, then the further stations real trains call at.
func (g *gen) loadStations() ([]Station, error) {
	rows, err := csv.NewReader(bytes.NewReader(stationsCSV)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("stations.csv: %w", err)
	}
	real, err := csv.NewReader(bytes.NewReader(realStationsCSV)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("real_stations.csv: %w", err)
	}
	rows = append(rows, real[1:]...)
	zoneOK := map[string]bool{}
	for _, z := range zones {
		zoneOK[z.Code] = true
	}
	var out []Station
	for n, row := range rows[1:] {
		lat, err1 := strconv.ParseFloat(row[5], 64)
		lon, err2 := strconv.ParseFloat(row[6], 64)
		imp, err3 := strconv.Atoi(row[7])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("station row %d (%s): bad number", n+2, row[0])
		}
		lang, ok := stateLanguage[row[2]]
		if !ok {
			return nil, fmt.Errorf("station %s: no language for state %q", row[0], row[2])
		}
		if !zoneOK[row[3]] {
			return nil, fmt.Errorf("station %s: unknown zone %q", row[0], row[3])
		}
		s := Station{Code: row[0], Name: row[1], State: row[2], Zone: row[3], Division: row[4],
			Lat: lat, Lon: lon, Importance: imp, Language: lang}
		switch imp {
		case 1:
			s.Platforms = 6 + g.rng.IntN(5)
		case 2:
			s.Platforms = 3 + g.rng.IntN(4)
		default:
			s.Platforms = 2 + g.rng.IntN(2)
		}
		if p, ok := platformOverride[s.Code]; ok {
			s.Platforms = p
		}
		if g.stations[s.Code] != nil {
			return nil, fmt.Errorf("duplicate station %s", s.Code)
		}
		out = append(out, s)
		g.stations[s.Code] = &out[len(out)-1]
	}
	// Re-point the map at the final slice (append may have moved it).
	for i := range out {
		g.stations[out[i].Code] = &out[i]
	}
	return out, nil
}

// railKm approximates track distance from straight-line distance.
func railKm(a, b *Station) float64 {
	const r = 6371.0
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat := la2 - la1
	dLon := (b.Lon - a.Lon) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	km := 2 * r * math.Asin(math.Sqrt(h))
	return math.Max(km*1.22, 3)
}

var dayIndex = map[string]int{"Mon": 0, "Tue": 1, "Wed": 2, "Thu": 3, "Fri": 4, "Sat": 5, "Sun": 6}

func parseDays(s string) (int16, error) {
	if s == "daily" {
		return 127, nil
	}
	var m int16
	for _, d := range strings.Split(s, ",") {
		i, ok := dayIndex[strings.TrimSpace(d)]
		if !ok {
			return 0, fmt.Errorf("bad day %q", d)
		}
		m |= 1 << i
	}
	return m, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// trains is the real timetable plus the curated trains it lacks (Vande Bharat and other trains
// introduced after the dataset was gathered in 2016).
func (g *gen) trains() ([]Train, error) {
	out, err := g.realTrains()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, t := range out {
		seen[t.Number] = true
	}
	for _, d := range trainDefs {
		if seen[strconv.Itoa(d.Number)] || seen[strconv.Itoa(d.Number+1)] {
			continue // the real train with this number wins
		}
		route, err := g.routeSlice(d)
		if err != nil {
			return nil, fmt.Errorf("train %d: %w", d.Number, err)
		}
		tt := trainType(d.Type)
		// Pick which stations on the route the train calls at; the return working uses the same set.
		calls := make([]bool, len(route))
		for i, code := range route {
			st := g.stations[code]
			switch {
			case i == 0 || i == len(route)-1:
				calls[i] = true
			case d.Type == "PASSENGER":
				calls[i] = true
			case st.Importance <= tt.StopImportance:
				calls[i] = d.Type != "MAIL_EXPRESS" || st.Importance < 3 || g.rng.Float64() < 0.65
			}
		}
		days, err := parseDays(d.Days)
		if err != nil {
			return nil, fmt.Errorf("train %d: %w", d.Number, err)
		}
		coaches := g.rake(d.Type)
		up, down := strconv.Itoa(d.Number), strconv.Itoa(d.Number+1)
		for _, n := range []string{up, down} {
			if seen[n] {
				return nil, fmt.Errorf("duplicate train number %s", n)
			}
			seen[n] = true
		}

		// Each train pair gets its own pace (±8%) so trains of one type don't all take the same time.
		tt.AvgSpeedKmph = int(float64(tt.AvgSpeedKmph) * (0.92 + 0.16*g.rng.Float64()))
		upTrain, err := g.buildTrain(up, d.Name, tt, route, calls, d.Dep, days, coaches)
		if err != nil {
			return nil, fmt.Errorf("train %s: %w", up, err)
		}
		rRoute, rCalls := slices.Clone(route), slices.Clone(calls)
		slices.Reverse(rRoute)
		slices.Reverse(rCalls)
		downTrain, err := g.buildTrain(down, d.Name, tt, rRoute, rCalls, d.RetDep, days, coaches)
		if err != nil {
			return nil, fmt.Errorf("train %s: %w", down, err)
		}
		upTrain.Return, downTrain.Return = down, up
		out = append(out, upTrain, downTrain)
	}
	return out, nil
}

func (g *gen) routeSlice(d TrainDef) ([]string, error) {
	c, ok := corridors[d.Corridor]
	if !ok {
		return nil, fmt.Errorf("unknown corridor %s", d.Corridor)
	}
	for _, code := range c {
		if g.stations[code] == nil {
			return nil, fmt.Errorf("corridor %s: unknown station %s", d.Corridor, code)
		}
	}
	a, b := slices.Index(c, d.From), slices.Index(c, d.To)
	if a < 0 || b < 0 || a == b {
		return nil, fmt.Errorf("%s→%s not on corridor %s", d.From, d.To, d.Corridor)
	}
	if a < b {
		return slices.Clone(c[a : b+1]), nil
	}
	r := slices.Clone(c[b : a+1])
	slices.Reverse(r)
	return r, nil
}

func (g *gen) buildTrain(number, name string, tt TrainType, route []string, calls []bool,
	dep string, days int16, coaches []Coach) (Train, error) {
	start, err := parseClock(dep)
	if err != nil {
		return Train{}, err
	}
	t := Train{
		Number: number, Name: name, Type: tt.Code,
		Origin: route[0], Destination: route[len(route)-1],
		Days: days, Coaches: coaches,
	}
	for _, c := range coaches {
		if c.Class == "PC" {
			t.Pantry = true
		}
	}
	dist := 0.0
	clock := float64(start)
	for i, code := range route {
		st := g.stations[code]
		if i > 0 {
			leg := railKm(g.stations[route[i-1]], st)
			dist += leg
			clock += leg / float64(tt.AvgSpeedKmph) * 60
		}
		if !calls[i] {
			continue
		}
		stop := Stop{Station: code, DistKm: int(math.Round(dist)), Platform: 1 + g.rng.IntN(st.Platforms)}
		if i == 0 {
			d := start
			stop.Dep = &d
		} else {
			arr := int(math.Ceil(clock/5) * 5) // timetables round to 5 minutes
			stop.Arr = &arr
			clock = float64(arr)
			if i < len(route)-1 {
				halt := haltMinutes(tt, st.Importance)
				d := arr + halt
				stop.Dep = &d
				clock = float64(d)
			}
		}
		t.Stops = append(t.Stops, stop)
	}
	t.DistKm = t.Stops[len(t.Stops)-1].DistKm
	return t, nil
}

func haltMinutes(tt TrainType, importance int) int {
	switch {
	case tt.Code == "PASSENGER":
		return 1
	case importance == 1:
		return tt.MajorHaltMin
	case importance == 2:
		return max(2, tt.MajorHaltMin/2)
	default:
		return 1
	}
}

func (g *gen) rake(trainType string) []Coach { return g.rakeOf(compositions[trainType]) }

func (g *gen) rakeOf(groups []coachGroup) []Coach {
	var out []Coach
	count := map[string]int{}
	for _, grp := range groups {
		n := grp.Min
		if grp.Max > grp.Min {
			n += g.rng.IntN(grp.Max - grp.Min + 1)
		}
		cls := classByCode(grp.Class)
		for range n {
			count[grp.Class]++
			no := cls.Prefix + strconv.Itoa(count[grp.Class])
			if grp.Class == "PC" {
				no = "PC"
			}
			out = append(out, Coach{No: no, Class: grp.Class, Pos: len(out) + 1})
		}
	}
	return out
}

func classByCode(code string) CoachClass {
	for _, c := range coachClasses {
		if c.Code == code {
			return c
		}
	}
	panic("unknown coach class " + code)
}

// ── People ──────────────────────────────────────────────────────────────────

func (g *gen) uuid() [16]byte {
	var b [16]byte
	for i := 0; i < 16; i += 8 {
		v := g.rng.Uint64()
		for j := range 8 {
			b[i+j] = byte(v >> (8 * j))
		}
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return b
}

func (g *gen) phone() string {
	for {
		p := strconv.Itoa(6+g.rng.IntN(4)) + fmt.Sprintf("%09d", g.rng.IntN(1_000_000_000))
		if !g.phones[p] {
			g.phones[p] = true
			return p
		}
	}
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func (g *gen) name(lang, gender string) string {
	pool, ok := namesByLang[lang]
	if !ok {
		pool = hindiNames
	}
	var first string
	if gender == "F" {
		first = pick(g.rng, pool.Female)
	} else {
		first = pick(g.rng, pool.Male)
	}
	last := pick(g.rng, pool.Surname)
	if lang == "pa" && gender == "F" && g.rng.Float64() < 0.6 {
		last = "Kaur"
	}
	return first + " " + last
}

func (g *gen) gender() string {
	switch r := g.rng.Float64(); {
	case r < 0.002:
		return "O"
	case r < 0.51:
		return "M"
	default:
		return "F"
	}
}

func (g *gen) birth(minAge, maxAge int) time.Time {
	age := minAge + g.rng.IntN(maxAge-minAge+1)
	return g.today.AddDate(-age, 0, -g.rng.IntN(365))
}

func (g *gen) passengerAge() (int, int) {
	switch r := g.rng.Float64(); {
	case r < 0.08:
		return 5, 17
	case r < 0.78:
		return 18, 59
	default:
		return 60, 85
	}
}

func (g *gen) passengers(n int) []Person {
	// Bigger stations have bigger catchments.
	var weighted []*Station
	for _, s := range g.stations {
		for range 4 - s.Importance {
			weighted = append(weighted, s)
		}
	}
	slices.SortFunc(weighted, func(a, b *Station) int { return strings.Compare(a.Code, b.Code) })

	out := make([]Person, 0, n)
	out = append(out, Person{
		ID: g.uuid(), Role: "passenger", FullName: "Ananya Krishnan", Phone: DemoPassengerPhone,
		Gender: "F", Birth: time.Date(1994, 8, 14, 0, 0, 0, 0, time.UTC), Language: "ta", Home: "MAS", Demo: true,
	})
	for len(out) < n {
		home := pick(g.rng, weighted)
		lang := home.Language
		if g.rng.Float64() < 0.12 {
			lang = "hi" // people move for work; Hindi speakers live everywhere
		}
		gender := g.gender()
		lo, hi := g.passengerAge()
		out = append(out, Person{
			ID: g.uuid(), Role: "passenger", FullName: g.name(lang, gender), Phone: g.phone(),
			Gender: gender, Birth: g.birth(lo, hi), Language: lang, Home: home.Code,
		})
	}
	return out
}

func (g *gen) employeeID(prefix string) string {
	g.empSeq[prefix]++
	return fmt.Sprintf("%s%05d", prefix, g.empSeq[prefix])
}

func (g *gen) staffMember(role, prefix, designation string, at *Station, demo bool) Person {
	gender := "M"
	if g.rng.Float64() < 0.28 {
		gender = "F"
	}
	return Person{
		ID: g.uuid(), Role: role, FullName: g.name(at.Language, gender), EmployeeID: g.employeeID(prefix),
		Gender: gender, Birth: g.birth(24, 58), Language: at.Language, Home: at.Code, Demo: demo,
		Posting: &Posting{Designation: designation, Zone: at.Zone, Division: at.Division, Station: at.Code},
	}
}

func (g *gen) staff(stations []Station) ([]Person, []Vendor) {
	byDivision := map[string][]*Station{}
	var divKeys []string
	for i := range stations {
		s := &stations[i]
		k := s.Zone + "/" + s.Division
		if _, ok := byDivision[k]; !ok {
			divKeys = append(divKeys, k)
		}
		byDivision[k] = append(byDivision[k], s)
	}
	// Division HQ = its most important station (first in file order on ties).
	hq := func(k string) *Station {
		best := byDivision[k][0]
		for _, s := range byDivision[k] {
			if s.Importance < best.Importance {
				best = s
			}
		}
		return best
	}
	// Demo staff first so they get the 00001 employee IDs.
	chennai := hq("SR/Chennai")
	var people []Person
	admin := g.staffMember("admin", "ADM", "YatraX System Administrator", g.stations["NDLS"], true)
	admin.FullName = "Rakesh Bhatia"
	people = append(people,
		admin,
		g.staffMember("station", "SM", "Station Master", g.stations[DemoStationCode], true),
		g.staffMember("tte", "TTE", "Travelling Ticket Examiner", chennai, true),
		g.staffMember("controller", "CTRL", "Chief Controller", chennai, true),
	)

	for _, z := range zones {
		var zhq *Station
		for _, k := range divKeys {
			if strings.HasPrefix(k, z.Code+"/") && (zhq == nil || hq(k).Importance < zhq.Importance) {
				zhq = hq(k)
			}
		}
		if zhq != nil {
			people = append(people, g.staffMember("admin", "ADM", "Zonal Operations Admin, "+z.Name, zhq, false))
		}
	}
	for _, k := range divKeys {
		h := hq(k)
		for n := range 8 {
			des := "Travelling Ticket Examiner"
			if n == 0 {
				des = "Chief Ticket Inspector"
			}
			people = append(people, g.staffMember("tte", "TTE", des, h, false))
		}
		for n := range 3 {
			des := "Section Controller"
			if n == 0 {
				des = "Chief Controller"
			}
			people = append(people, g.staffMember("controller", "CTRL", des, h, false))
		}
	}
	for i := range stations {
		s := &stations[i]
		if s.Importance <= 2 && s.Code != DemoStationCode {
			people = append(people, g.staffMember("station", "SM", "Station Master", s, false))
		}
	}

	// Vendors: the demo vendor at Salem first, then 3 per major and 1 per important station.
	var vendors []Vendor
	addVendor := func(s *Station, name string, demo bool) {
		owner := g.staffMember("vendor", "VND", "Catering Licensee", s, demo)
		cuisines := slices.Clone(cuisinesByLang[s.Language])
		g.rng.Shuffle(len(cuisines), func(i, j int) { cuisines[i], cuisines[j] = cuisines[j], cuisines[i] })
		cuisines = cuisines[:2]
		veg := slices.Contains(cuisines, "Jain") || slices.Contains(cuisines, "Udupi") || g.rng.Float64() < 0.2
		if slices.ContainsFunc(cuisines, func(c string) bool { return c == "Fish" || c == "Seafood" || c == "Biryani" || c == "Wazwan" }) {
			veg = false
		}
		people = append(people, owner)
		vendors = append(vendors, Vendor{
			ID: g.uuid(), Owner: owner.ID, Name: name, Station: s.Code, Cuisines: cuisines, Veg: veg,
			FSSAI: fmt.Sprintf("1%013d", g.rng.Int64N(1e13)), Rating: float64(30+g.rng.IntN(21)) / 10,
		})
	}
	addVendor(g.stations[DemoVendorStation], "Salem Biryani House", true)
	vendors[0].Cuisines, vendors[0].Veg = []string{"Biryani", "South Indian"}, false
	for i := range stations {
		s := &stations[i]
		n := map[int]int{1: 3, 2: 1}[s.Importance]
		if s.Code == DemoVendorStation {
			n--
		}
		for range n {
			addVendor(s, strings.Split(s.Name, " ")[0]+" "+pick(g.rng, vendorNames), false)
		}
	}
	return people, vendors
}

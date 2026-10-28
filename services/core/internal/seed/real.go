package seed

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

// The real network: Indian Railways' reserved long-distance trains with every halt, and the
// stations they call at, from the datameet dataset (CC0, 2016). scripts/build_network.py
// turns the raw dataset into these two files; the curated stations.csv still supplies the
// names, zones and divisions of the 510 best-known stations.
//
//go:embed data/real_stations.csv
var realStationsCSV []byte

//go:embed data/real_trains.json
var realTrainsJSON []byte

type realTrain struct {
	Number  string   `json:"number"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Days    int16    `json:"days"`
	Classes []string `json:"classes"` // classes the dataset says the train carries; empty = unknown
	Return  string   `json:"return"`
	Stops   [][4]any `json:"stops"` // [station, arrival|null, departure|null, km]
}

// realTrains builds every train in the real timetable. Names come from the curated train list
// where it has the same number (those read better than the dataset's), otherwise the dataset's.
func (g *gen) realTrains() ([]Train, error) {
	var defs []realTrain
	if err := json.Unmarshal(realTrainsJSON, &defs); err != nil {
		return nil, fmt.Errorf("real_trains.json: %w", err)
	}
	names := map[string]string{}
	for _, d := range trainDefs {
		names[strconv.Itoa(d.Number)], names[strconv.Itoa(d.Number+1)] = d.Name, d.Name
	}
	out := make([]Train, 0, len(defs))
	for _, d := range defs {
		t := Train{Number: d.Number, Name: d.Name, Type: d.Type, Days: d.Days, Return: d.Return}
		if n, ok := names[d.Number]; ok {
			t.Name = n
		}
		for _, raw := range d.Stops {
			code, _ := raw[0].(string)
			st := g.stations[code]
			if st == nil {
				return nil, fmt.Errorf("train %s: unknown station %s", d.Number, code)
			}
			s := Stop{Station: code, DistKm: int(raw[3].(float64)), Platform: 1 + g.rng.IntN(st.Platforms)}
			if v, ok := raw[1].(float64); ok {
				m := int(v)
				s.Arr = &m
			}
			if v, ok := raw[2].(float64); ok {
				m := int(v)
				s.Dep = &m
			}
			t.Stops = append(t.Stops, s)
		}
		t.Origin, t.Destination = t.Stops[0].Station, t.Stops[len(t.Stops)-1].Station
		t.DistKm = t.Stops[len(t.Stops)-1].DistKm
		t.Coaches = g.rakeFor(d.Type, d.Classes, t.DistKm)
		t.Pantry = slices.ContainsFunc(t.Coaches, func(c Coach) bool { return c.Class == "PC" })
		out = append(out, t)
	}
	return out, nil
}

// extraGroups are added when the dataset says a train carries a class its type's usual rake lacks.
var extraGroups = map[string]coachGroup{
	"1A": {"1A", 1, 1}, "2A": {"2A", 1, 2}, "3A": {"3A", 3, 5}, "SL": {"SL", 6, 9}, "CC": {"CC", 2, 4}, "EC": {"EC", 1, 1},
}

// rakeFor gives a train its type's usual rake, trimmed to the classes the dataset lists for it.
func (g *gen) rakeFor(trainType string, classes []string, distKm int) []Coach {
	if len(classes) == 0 {
		return g.rake(trainType)
	}
	want := map[string]bool{}
	for _, c := range classes {
		if c == "1A" && (trainType == "SHATABDI" || trainType == "JAN_SHATABDI") {
			c = "EC" // on chair-car trains the dataset's "first AC" is the Executive Chair Car
		}
		want[c] = true
	}
	var groups []coachGroup
	have := map[string]bool{}
	for _, grp := range compositions[trainType] {
		if classByCode(grp.Class).Bookable && !want[grp.Class] {
			continue
		}
		groups = append(groups, grp)
		have[grp.Class] = true
	}
	// Classes the type doesn't usually carry go just before the brake van at the end.
	tail := groups[len(groups)-1:]
	groups = slices.Clone(groups[:len(groups)-1])
	for _, c := range []string{"1A", "EC", "2A", "3A", "CC", "SL"} {
		if want[c] && !have[c] {
			groups = append(groups, extraGroups[c])
		}
	}
	if want["CC"] && len(want) == 1 && (trainType == "SUPERFAST" || trainType == "MAIL_EXPRESS") {
		groups = append(groups, coachGroup{"2S", 6, 10}) // a day intercity: chair car plus second sitting
	}
	if trainType == "MAIL_EXPRESS" && distKm >= 1000 && want["SL"] && !have["PC"] {
		groups = append(groups, coachGroup{"PC", 1, 1})
	}
	groups = append(groups, tail...)
	return g.rakeOf(groups)
}

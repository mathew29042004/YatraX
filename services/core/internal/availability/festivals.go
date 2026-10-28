package availability

import (
	"slices"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Festival is a travel-demand peak. People travel home in the week before and
// return in the days after; an empty States list means the whole country.
type Festival struct {
	Name   string    `json:"name"`
	Date   time.Time `json:"date"`
	States []string  `json:"states,omitempty"`
	Peak   float64   `json:"-"` // extra demand on the busiest day, e.g. 0.6 = +60%
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, network.IST) }

// Festivals covers the booking window from late 2026 into 2027.
var Festivals = []Festival{
	{"Diwali", day(2026, 11, 8), nil, 0.7},
	{"Chhath Puja", day(2026, 11, 15), []string{"Bihar", "Jharkhand", "Uttar Pradesh"}, 0.9},
	{"Christmas", day(2026, 12, 25), []string{"Kerala", "Goa", "Tamil Nadu", "Karnataka", "Nagaland"}, 0.45},
	{"New Year", day(2027, 1, 1), []string{"Goa"}, 0.4},
	{"Pongal", day(2027, 1, 14), []string{"Tamil Nadu", "Puducherry"}, 0.7},
	{"Makar Sankranti", day(2027, 1, 14), []string{"Andhra Pradesh", "Telangana", "Karnataka", "Gujarat"}, 0.5},
	{"Holi", day(2027, 3, 22), []string{"Uttar Pradesh", "Bihar", "Jharkhand", "Madhya Pradesh", "Rajasthan", "Delhi", "Haryana"}, 0.6},
}

func (f Festival) covers(state string) bool {
	return len(f.States) == 0 || slices.Contains(f.States, state)
}

// FestivalFactor raises demand on journeys heading into a festival region in the
// week before it, and journeys leaving that region in the five days after.
func FestivalFactor(n *network.Network, t *network.Train, from, to int, originDate time.Time) float64 {
	fromState := n.Stations[t.Stops[from].Station].State
	toState := n.Stations[t.Stops[to].Station].State
	travel := originDate.AddDate(0, 0, t.Stops[from].Time()/1440)
	factor := 1.0
	for _, f := range Festivals {
		d := int(f.Date.Sub(travel).Hours() / 24) // days until the festival
		switch {
		case d >= 0 && d <= 7 && f.covers(toState):
			// Going home: busiest 1–3 days before.
			w := 1.0
			if d == 0 || d > 4 {
				w = 0.55
			}
			factor += f.Peak * w
		case d < 0 && d >= -5 && f.covers(fromState):
			// Coming back.
			factor += f.Peak * 0.7 * (1 + float64(d)/6)
		}
	}
	return factor
}

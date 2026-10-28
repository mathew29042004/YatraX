// Package control is the railway's control room: the live section chart (the
// time-distance "master chart"), the delay-ripple and what-if simulator, the network's
// platform conflicts, and incidents — cancelling, short-terminating or diverting a
// train — with refunds and rebooking for its passengers.
package control

import (
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/station"
)

type Service struct {
	DB      *pgxpool.Pool
	Net     *network.Network
	Live    *live.Engine
	Station *station.Service
	Booking *booking.Service
	Now     func() time.Time // simulated
}

var ErrSection = errors.New("no train runs between those two stations")

type SectionStation struct {
	Code string  `json:"code"`
	Name string  `json:"name"`
	Km   float64 `json:"km"`
}

type Point struct {
	Km       float64    `json:"km"`
	Station  string     `json:"station"`
	SchedArr *time.Time `json:"sched_arr,omitempty"`
	SchedDep *time.Time `json:"sched_dep,omitempty"`
	ExpArr   *time.Time `json:"exp_arr,omitempty"`
	ExpDep   *time.Time `json:"exp_dep,omitempty"`
}

type Path struct {
	Train      string   `json:"train"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	OriginDate string   `json:"origin_date"`
	Direction  string   `json:"direction"` // down: away from the section's first station; up: towards it
	DelayMin   int      `json:"delay_min"`
	State      string   `json:"state"`
	NowKm      *float64 `json:"now_km,omitempty"` // where it is on the section right now
	Points     []Point  `json:"points"`
}

type Section struct {
	From     string           `json:"from"`
	To       string           `json:"to"`
	Stations []SectionStation `json:"stations"`
	Start    time.Time        `json:"start"`
	End      time.Time        `json:"end"`
	SimNow   time.Time        `json:"sim_now"`
	Trains   []Path           `json:"trains"`
}

// spine picks the train with the most halts between two stations: its stops define the section.
func (c *Service) spine(from, to string) (*network.Train, int, int) {
	var best *network.Train
	var bi, bj int
	for _, t := range c.Net.Calls[from] {
		i, j := t.StopIndex(from), t.StopIndex(to)
		if i < 0 || j < 0 || i == j {
			continue
		}
		if best == nil || abs(j-i) > abs(bj-bi) {
			best, bi, bj = t, i, j
		}
	}
	return best, bi, bj
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func pick(a, b *time.Time) *time.Time {
	if a != nil {
		return a
	}
	return b
}

// Section draws every train crossing the section between from and to, from an hour ago
// to `hours` ahead of simulated now: scheduled and expected paths, and where each is now.
func (c *Service) Section(from, to string, hours int) (*Section, error) {
	sp, i, j := c.spine(from, to)
	if sp == nil {
		return nil, ErrSection
	}
	now := c.Now()
	out := &Section{From: from, To: to, SimNow: now, Start: now.Add(-time.Hour), End: now.Add(time.Duration(hours) * time.Hour), Trains: []Path{}}
	step := 1
	if j < i {
		step = -1
	}
	km := map[string]float64{}
	base := float64(sp.Stops[i].DistKm)
	for k := i; ; k += step {
		st := sp.Stops[k]
		d := float64(st.DistKm) - base
		if d < 0 {
			d = -d
		}
		name := st.Station
		if s := c.Net.Stations[st.Station]; s != nil {
			name = s.Name
		}
		out.Stations = append(out.Stations, SectionStation{Code: st.Station, Name: name, Km: d})
		km[st.Station] = d
		if k == j {
			break
		}
	}

	seen := map[string]bool{}
	today := live.Midnight(now)
	for _, s := range out.Stations {
		for _, t := range c.Net.Calls[s.Code] {
			if seen[t.Number] {
				continue
			}
			seen[t.Number] = true
			var idx []int // the train's stops that are on the section
			for k, st := range t.Stops {
				if _, ok := km[st.Station]; ok {
					idx = append(idx, k)
				}
			}
			if len(idx) < 2 {
				continue
			}
			for d := -3; d <= 1; d++ {
				origin := today.AddDate(0, 0, d)
				if !t.RunsOn(origin) {
					continue
				}
				first := origin.Add(time.Duration(t.Stops[idx[0]].Time()) * time.Minute)
				last := origin.Add(time.Duration(t.Stops[idx[len(idx)-1]].Time()) * time.Minute)
				if last.Before(out.Start.Add(-6*time.Hour)) || first.After(out.End) {
					continue
				}
				st := c.Live.Status(t, origin, now)
				p := Path{Train: t.Number, Name: t.Name, Type: t.Type, OriginDate: st.OriginDate, DelayMin: st.DelayMin, State: st.State, Direction: "down"}
				for _, k := range idx {
					v := st.Stops[k]
					p.Points = append(p.Points, Point{Km: km[v.Station], Station: v.Station, SchedArr: v.SchedArr, SchedDep: v.SchedDep, ExpArr: v.ExpArr, ExpDep: v.ExpDep})
				}
				if p.Points[len(p.Points)-1].Km < p.Points[0].Km {
					p.Direction = "up"
				}
				t0 := pick(p.Points[0].ExpDep, p.Points[0].ExpArr)
				pl := p.Points[len(p.Points)-1]
				t1 := pick(pl.ExpArr, pl.ExpDep)
				if t1.Before(out.Start) || t0.After(out.End) {
					continue
				}
				// Position now: between the last section stop left and the next one reached.
				for a := 0; a+1 < len(p.Points); a++ {
					pa, pb := p.Points[a], p.Points[a+1]
					leave, reach := pick(pa.ExpDep, pa.ExpArr), pick(pb.ExpArr, pb.ExpDep)
					arrA := pick(pa.ExpArr, pa.ExpDep)
					if !now.Before(*arrA) && now.Before(*leave) {
						k := pa.Km
						p.NowKm = &k
						break
					}
					if !now.Before(*leave) && now.Before(*reach) {
						f := float64(now.Sub(*leave)) / float64(reach.Sub(*leave))
						k := pa.Km + (pb.Km-pa.Km)*f
						p.NowKm = &k
						break
					}
				}
				out.Trains = append(out.Trains, p)
			}
		}
	}
	sort.Slice(out.Trains, func(a, b int) bool {
		return pick(out.Trains[a].Points[0].ExpDep, out.Trains[a].Points[0].ExpArr).Before(*pick(out.Trains[b].Points[0].ExpDep, out.Trains[b].Points[0].ExpArr))
	})
	if len(out.Trains) > 200 {
		out.Trains = out.Trains[:200]
	}
	return out, nil
}

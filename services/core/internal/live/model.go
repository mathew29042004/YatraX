// Package live runs the railway in simulated time: where every train is, how late it
// is, which platform it will use, and the events passengers are told about.
//
// Movement is a pure function of (train, origin date, simulated time) plus any delays
// controllers inject, so every core instance computes the same answer without sharing
// state. Delays come from a seeded model: late rakes at the origin, small run-time
// noise, occasional incidents (signals, crossings, a chain pull), winter fog across
// the north, and the recovery margin timetables carry near the destination.
package live

import (
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Incident is a controller-injected delay: the run leaves StopSeq DelayMin later.
type Incident struct {
	StopSeq  int    `json:"stop_seq"`
	DelayMin int    `json:"delay_min"`
	Reason   string `json:"reason"`
	EnRoute  bool   `json:"en_route,omitempty"` // lost on the way in (a breakdown): delays the arrival at StopSeq
}

// stopPlan is what the model expects at one stop of a run.
type stopPlan struct {
	ArrDelay, DepDelay int    // minutes late
	Reason             string // why the train is late here, if it lost time on the way in
	Platform           int    // platform it will use (may differ from the timetable)
	PlatformChanged    bool
}

var incidentReasons = []string{
	"Signal failure", "Waiting for a crossing", "Track maintenance block", "Chain pulling",
	"Late running of a connecting train", "Engine change took longer", "Speed restriction on a weak bridge",
	"Cattle on the track", "Overhead wire repair",
}

// fogZones are where winter fog slows night running.
var fogZones = map[string]bool{"NR": true, "NCR": true, "NER": true, "ECR": true, "NWR": true}

func seeded(parts ...string) *rand.Rand {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	v := h.Sum64()
	return rand.New(rand.NewPCG(v, v^0x9e3779b97f4a7c15))
}

// Plan computes a run's delays and platforms. It is deterministic for a given run and
// set of incidents.
func Plan(n *network.Network, t *network.Train, origin time.Time, incidents []Incident) []stopPlan {
	r := seeded("live", t.Number, origin.Format(time.DateOnly))
	out := make([]stopPlan, len(t.Stops))
	held, enRoute := map[int][]Incident{}, map[int][]Incident{}
	for _, in := range incidents {
		if in.EnRoute && in.StopSeq > 0 {
			enRoute[in.StopSeq] = append(enRoute[in.StopSeq], in)
		} else {
			held[in.StopSeq] = append(held[in.StopSeq], in)
		}
	}
	hold := func(i int, d *int, reason *string) {
		for _, in := range held[i] {
			*d += in.DelayMin
			*reason = in.Reason
		}
	}

	delay, reason := 0, ""
	if r.Float64() < 0.15 {
		delay, reason = 5+r.IntN(36), "Late arrival of the rake"
	}
	hold(0, &delay, &reason)
	out[0] = stopPlan{DepDelay: delay, Reason: reason, Platform: t.Stops[0].Platform}

	winter := origin.Month() == time.December && origin.Day() >= 10 || origin.Month() == time.January ||
		origin.Month() == time.February && origin.Day() <= 10
	for i := 1; i < len(t.Stops); i++ {
		prev, cur := t.Stops[i-1], t.Stops[i]
		run := float64(max(*cur.Arr-prev.Time(), 1))
		delta := run * (0.012 + 0.045*r.NormFloat64())
		km := float64(cur.DistKm - prev.DistKm)
		if r.Float64() < math.Min(0.07, 0.008+km*0.00018) {
			delta += float64(8 + r.IntN(48))
			reason = incidentReasons[r.IntN(len(incidentReasons))]
		}
		if st := n.Stations[cur.Station]; winter && st != nil && fogZones[st.Zone] {
			if m := *cur.Arr % 1440; m >= 21*60 || m <= 9*60 {
				delta += run * (0.1 + 0.45*r.Float64())
				reason = "Fog"
			}
		}
		if i >= len(t.Stops)*4/5 {
			delta -= run * 0.08 // the recovery margin timetables carry near the end
		}
		arr := max(0, delay+int(math.Round(delta)))
		if arr == 0 {
			reason = ""
		}
		for _, in := range enRoute[i] {
			arr += in.DelayMin
			reason = in.Reason
		}
		p := stopPlan{ArrDelay: arr, Reason: reason, Platform: cur.Platform}
		if cur.Dep != nil {
			halt := *cur.Dep - *cur.Arr
			dep := max(0, arr-(halt-min(halt, 2))) // a late train cuts its halt to two minutes
			hold(i, &dep, &reason)
			p.DepDelay, p.Reason = dep, reason
			delay = dep
		} else {
			delay = arr
		}
		// Now and then the station moves the train to another platform.
		if st := n.Stations[cur.Station]; st != nil && st.Platforms >= 3 {
			pr := seeded("platform", t.Number, origin.Format(time.DateOnly), cur.Station)
			if pr.Float64() < 0.05 {
				p.Platform = (cur.Platform-1+1+pr.IntN(st.Platforms-1))%st.Platforms + 1
				p.PlatformChanged = p.Platform != cur.Platform
			}
		}
		out[i] = p
	}
	return out
}

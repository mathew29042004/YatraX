package live

import (
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Outcome is how a finished run went, for punctuality statistics.
type Outcome struct {
	ArrDelay  int    // minutes late at the destination
	Reason    string // where it lost the most time, if it was late
	Cancelled bool
	Arrival   time.Time // expected arrival at the destination
}

// Outcome works out a run's arrival delay at its destination from the same model the live
// status uses (including injected incidents and diversions), without needing a clock.
func (e *Engine) Outcome(t *network.Train, origin time.Time) (Outcome, bool) {
	origin = Midnight(origin)
	if !t.RunsOn(origin) || len(t.Stops) < 2 {
		return Outcome{}, false
	}
	e.mu.RLock()
	dis, disrupted := e.disrupted[runKey(t.Number, origin)]
	e.mu.RUnlock()
	if disrupted && dis.kind == "cancelled" {
		return Outcome{Cancelled: true}, true
	}
	p := e.plan(t, origin, nil)
	last := len(p) - 1
	if disrupted && dis.kind == "short_terminated" {
		if k := t.StopIndex(dis.at); k > 0 {
			last = k
		}
	}
	o := Outcome{ArrDelay: max(0, p[last].ArrDelay)}
	if disrupted && dis.kind == "diverted" {
		o.ArrDelay += dis.extraDelay
		o.Reason = "Diverted: " + dis.reason
	}
	if o.Reason == "" && o.ArrDelay > 0 {
		worst, prev := p[0].DepDelay, p[0].DepDelay
		o.Reason = p[0].Reason
		for i := 1; i <= last; i++ {
			if gain := p[i].ArrDelay - prev; gain > worst && p[i].Reason != "" {
				worst, o.Reason = gain, p[i].Reason
			}
			prev = p[i].DepDelay
		}
		if o.Reason == "" {
			o.Reason = "Slow running"
		}
	}
	o.Arrival = at(origin, *t.Stops[last].Arr+o.ArrDelay)
	return o, true
}

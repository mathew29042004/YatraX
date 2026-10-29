// Package availability answers "how many berths are left?" for a train, date,
// class and quota.
//
// Runs nobody has opened yet have no seat inventory, so Simulated derives a believable
// answer from a demand model (days to travel, weekday, festivals, train type, class). It
// is deterministic, so the same query always gives the same answer. Once a run exists,
// the booking core answers from real inventory behind the same Provider interface.
package availability

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Booking rules shared by every provider.
const (
	AdvanceDays  = 60 // reservations open 60 days before the journey (excluding the journey day)
	tatkalACHour = 10 // Tatkal opens the day before at 10:00 for AC classes…
	tatkalSLHour = 11 // …and 11:00 for non-AC classes
)

// Status codes.
const (
	AVL           = "AVL"
	RAC           = "RAC"
	WL            = "WL"
	Regret        = "REGRET"
	NotOpen       = "NOT_OPEN"        // beyond the 60-day booking window
	TatkalNotOpen = "TATKAL_NOT_OPEN" // Tatkal opens the day before
	Departed      = "DEPARTED"
	NotRunning    = "NOT_RUNNING"
	NoQuota       = "NO_QUOTA" // this class has no seats in this quota
)

type Request struct {
	Train      *network.Train
	OriginDate time.Time // the day the train leaves its origin (midnight IST)
	Class      string
	Quota      string
	FromIdx    int
	ToIdx      int
}

type Status struct {
	Code  string `json:"code"`
	Count int    `json:"count,omitempty"`
	Label string `json:"label"`
	// OpensAt is set for NOT_OPEN and TATKAL_NOT_OPEN.
	OpensAt *time.Time `json:"opens_at,omitempty"`
	// ConfirmChance is a rough 0–100 estimate from the waitlist position, days left and demand.
	ConfirmChance int `json:"confirm_chance"`
}

type Provider interface {
	Status(ctx context.Context, r Request, now time.Time) Status
}

// Midnight returns the IST calendar day containing t.
func Midnight(t time.Time) time.Time {
	t = t.In(network.IST)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, network.IST)
}

// Gate applies the rules every provider shares: running days, departure time,
// the booking window and Tatkal opening. It returns ok=false with the status to show.
func Gate(n *network.Network, r Request, now time.Time) (Status, bool) {
	if !r.Train.RunsOn(r.OriginDate) {
		return Status{Code: NotRunning, Label: "Not running"}, false
	}
	board := r.OriginDate.Add(time.Duration(r.Train.Stops[r.FromIdx].Time()) * time.Minute)
	if !board.After(now) {
		return Status{Code: Departed, Label: "Departed"}, false
	}
	if !QuotaAllowed(n, r.Class, r.Quota) {
		return Status{Code: NoQuota, Label: "No " + r.Quota + " quota"}, false
	}
	today := Midnight(now)
	if r.OriginDate.After(today.AddDate(0, 0, AdvanceDays)) {
		opens := r.OriginDate.AddDate(0, 0, -AdvanceDays).Add(8 * time.Hour)
		return Status{Code: NotOpen, Label: "Opens " + opens.Format("2 Jan"), OpensAt: &opens}, false
	}
	if r.Quota == "TQ" || r.Quota == "PT" {
		hour := tatkalSLHour
		if n.Classes[r.Class].AC {
			hour = tatkalACHour
		}
		opens := r.OriginDate.AddDate(0, 0, -1).Add(time.Duration(hour) * time.Hour)
		if now.Before(opens) {
			return Status{Code: TatkalNotOpen, Label: "Opens " + opens.Format("2 Jan, 15:04"), OpensAt: &opens}, false
		}
	}
	return Status{}, true
}

// Quotas lists every quota in display order.
var Quotas = []string{"GN", "TQ", "PT", "LD", "SS", "HP", "DF", "FT"}

// QuotaAllowed reports whether a class has seats in a quota.
func QuotaAllowed(n *network.Network, class, quota string) bool {
	cc := n.Classes[class]
	switch quota {
	case "GN":
		return true
	case "TQ", "PT":
		return cc.TatkalPct != nil
	case "LD":
		return class == "SL" || class == "3A" || class == "CC" || class == "2S"
	case "SS":
		return class == "SL" || class == "3A" || class == "2A"
	case "HP": // divyangjan (persons with disabilities) and an escort
		return class == "SL" || class == "3A" || class == "3E" || class == "CC" || class == "2S"
	case "DF": // defence personnel on warrant
		return class == "SL" || class == "3A" || class == "2A"
	case "FT": // foreign tourists, on a passport
		return class == "1A" || class == "2A" || class == "3A" || class == "EC" || class == "CC"
	}
	return false
}

// quotaShare is the fraction of a class's berths set aside for each quota.
func quotaShare(n *network.Network, class, quota string) float64 {
	others := 0.0
	shares := map[string]float64{"TQ": 0.22, "PT": 0.06, "LD": 0.03, "SS": 0.06, "HP": 0.01, "DF": 0.01, "FT": 0.01}
	for q, s := range shares {
		if QuotaAllowed(n, class, q) {
			others += s
		}
	}
	if quota == "GN" {
		return 1 - others
	}
	return shares[quota]
}

// Simulated produces availability from a demand model.
type Simulated struct {
	N *network.Network
	// Capacity, if set, gives a quota's real confirmable berths and RAC places (from
	// the coach layout), so the model's statuses match what booking will find. Without
	// it the model uses average quota shares.
	Capacity func(t *network.Train, class, quota string) (berths, rac float64)
}

func (s Simulated) capacity(t *network.Train, class, quota string) (float64, float64) {
	if s.Capacity != nil {
		return s.Capacity(t, class, quota)
	}
	c := float64(t.Capacity(s.N, class)) * quotaShare(s.N, class, quota)
	return c, RACSlots(s.N, class, quota, c)
}

var typeDemand = map[string]float64{
	"RAJDHANI": 1.02, "DURONTO": 0.95, "VANDE_BHARAT": 0.92, "SHATABDI": 0.85,
	"JAN_SHATABDI": 0.95, "GARIB_RATH": 1.04,
	"SUPERFAST": 0.98, "MAIL_EXPRESS": 0.92, "PASSENGER": 0.55,
}

var classDemand = map[string]float64{
	"SL": 1.12, "3A": 1.08, "3E": 1.0, "2A": 0.9, "1A": 0.72, "CC": 0.98, "EC": 0.78, "2S": 1.05,
}

// Booked estimates how many passengers (in berth units) have booked this quota for
// the requested segment by now, and the quota's capacity. Booked can exceed
// capacity: the excess is RAC and waitlist demand.
func (s Simulated) Booked(r Request, now time.Time) (booked, capacity float64) {
	t := r.Train
	capacity, _ = s.capacity(t, r.Class, r.Quota)
	daysOut := r.OriginDate.Sub(Midnight(now)).Hours() / 24
	festival := FestivalFactor(s.N, t, r.FromIdx, r.ToIdx, r.OriginDate)
	demand := typeDemand[t.Type] * classDemand[r.Class] * weekdayFactor(r.OriginDate) *
		departureFactor(t.Stops[r.FromIdx].Time()) * festival *
		segmentFactor(t, r.FromIdx, r.ToIdx) *
		// A train's popularity is stable; day-to-day swings are smaller.
		noise(fmt.Sprint(t.Number, r.Class), 0.15) *
		noise(fmt.Sprint(t.Number, r.OriginDate.Format("2006-01-02"), r.Class, r.Quota), 0.07)

	switch r.Quota {
	case "TQ", "PT":
		// Tatkal sells out within minutes of opening on popular trains.
		booked = capacity * demand * 1.9
	case "HP", "DF", "FT":
		// Few people qualify: these berths are usually still free near departure.
		booked = capacity * demand * 0.45 * (1 - 0.8*math.Pow(math.Max(daysOut, 0)/AdvanceDays, 0.6))
	default:
		// Bookings build up as the journey approaches; festival trips are booked
		// much earlier, often the day the 60-day window opens.
		early := 0.82 / (festival * festival)
		booked = capacity * demand * (1 - early*math.Pow(math.Max(daysOut, 0)/AdvanceDays, 0.6))
	}
	return booked, capacity
}

// RACSlots is how many RAC passengers a quota can hold: two per side-lower RAC berth.
func RACSlots(n *network.Network, class, quota string, capacity float64) float64 {
	cc := n.Classes[class]
	if (cc.Layout != "sleeper_8" && cc.Layout != "sleeper_6") || (quota != "GN" && quota != "TQ") {
		return 0
	}
	bay := 8.0
	if cc.Layout == "sleeper_6" {
		bay = 6
	}
	return capacity / bay * 2
}

// WaitlistLimit is the longest waitlist a quota accepts before showing Regret.
func WaitlistLimit(quota string, capacity float64) int {
	limit := capacity * 0.5
	if quota == "TQ" || quota == "PT" {
		limit = capacity * 0.25
	}
	return int(math.Max(limit, 10))
}

func (s Simulated) Status(_ context.Context, r Request, now time.Time) Status {
	if st, ok := Gate(s.N, r, now); !ok {
		return st
	}
	booked, capacity := s.Booked(r, now)
	if capacity < 1 {
		return Status{Code: NoQuota, Label: "No " + r.Quota + " quota"}
	}
	if booked < capacity {
		if left := int(capacity - booked); left > 0 {
			return AvailableStatus(left)
		}
	}
	_, racCap := s.capacity(r.Train, r.Class, r.Quota)
	over := booked - capacity
	if over < racCap {
		return RACStatus(max(1, int(math.Ceil(over))))
	}
	wl := max(1, int(math.Ceil(over-racCap)))
	if wl > WaitlistLimit(r.Quota, capacity) {
		return Status{Code: Regret, Label: "Regret"}
	}
	daysOut := r.OriginDate.Sub(Midnight(now)).Hours() / 24
	return WaitlistStatus(wl, capacity, racCap, daysOut)
}

// AvailableStatus, RACStatus and WaitlistStatus build the statuses every provider shows.
func AvailableStatus(left int) Status {
	return Status{Code: AVL, Count: left, Label: fmt.Sprintf("AVL %d", left), ConfirmChance: 100}
}

func RACStatus(n int) Status {
	return Status{Code: RAC, Count: n, Label: fmt.Sprintf("RAC %d", n), ConfirmChance: max(80, 97-n/4)}
}

func WaitlistStatus(wl int, capacity, racCap, daysOut float64) Status {
	return Status{Code: WL, Count: wl, Label: fmt.Sprintf("WL %d", wl), ConfirmChance: confirmChance(wl, capacity, racCap, daysOut)}
}

// confirmChance compares the waitlist position with the cancellations expected
// before chart preparation (more days out = more time for cancellations).
func confirmChance(wl int, capacity, racCap, daysOut float64) int {
	expected := capacity*0.16*(0.35+0.65*math.Min(daysOut, 30)/30) + racCap*0.5
	scale := expected*0.3 + 3
	p := 100 / (1 + math.Exp((float64(wl)-expected)/scale))
	return int(math.Max(3, math.Min(95, math.Round(p))))
}

func weekdayFactor(d time.Time) float64 {
	switch d.Weekday() {
	case time.Friday, time.Sunday:
		return 1.12
	case time.Saturday:
		return 1.04
	case time.Tuesday, time.Wednesday:
		return 0.9
	}
	return 0.97
}

// departureFactor favours evening departures, which give an overnight journey.
func departureFactor(min int) float64 {
	h := min % 1440 / 60
	switch {
	case h >= 18 && h <= 23:
		return 1.08
	case h >= 0 && h < 5:
		return 0.9
	}
	return 1
}

// segmentFactor: short hops draw from a larger shared pool than end-to-end journeys.
func segmentFactor(t *network.Train, from, to int) float64 {
	frac := float64(t.Stops[to].DistKm-t.Stops[from].DistKm) / float64(max(t.DistKm, 1))
	return 0.82 + 0.22*frac
}

// noise is a deterministic log-normal multiplier around 1 for a key.
func noise(key string, sigma float64) float64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	v := h.Sum64()
	u1 := (float64(v&0xffffffff) + 0.5) / 4294967296
	u2 := (float64(v>>32) + 0.5) / 4294967296
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return math.Exp(sigma * z)
}

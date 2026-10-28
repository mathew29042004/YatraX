package analytics

import (
	"context"
	"slices"
	"time"
)

// Money is in rupees throughout.

type KPIs struct {
	Bookings       int     `json:"bookings"`        // confirmed at some point
	Passengers     int     `json:"passengers"`      // on those bookings
	Gross          int     `json:"gross"`           // fares collected
	Refunds        int     `json:"refunds"`         // refunded or owed
	Net            int     `json:"net"`             // gross − refunds
	AvgFare        int     `json:"avg_fare"`        // per passenger
	Cancelled      int     `json:"cancelled"`       // passengers who cancelled
	Waitlisted     int     `json:"waitlisted"`      // passengers still on the waitlist
	BookedConfirm  float64 `json:"booked_confirm"`  // share of passengers given a berth at booking
	TatkalShare    float64 `json:"tatkal_share"`    // share of fares from Tatkal and Premium Tatkal
	Penalties      int     `json:"penalties"`       // TTE excess fares and penalties
	FoodSales      int     `json:"food_sales"`      // delivered food orders
	FoodCommission int     `json:"food_commission"` // YatraX's share of those
}

type Slice struct {
	Key        string `json:"key"`
	Passengers int    `json:"passengers"`
	Revenue    int    `json:"revenue"`
}

type TrainRevenue struct {
	Number     string `json:"number"`
	Name       string `json:"name"`
	Passengers int    `json:"passengers"`
	Revenue    int    `json:"revenue"`
}

type Route struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Passengers int    `json:"passengers"`
	Revenue    int    `json:"revenue"`
}

type DayPoint struct {
	Date       time.Time `json:"date"`
	Passengers int       `json:"passengers"`
	Revenue    int       `json:"revenue"`
}

type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type Overview struct {
	Scope      Scope          `json:"scope"`
	KPIs       KPIs           `json:"kpis"`
	ByClass    []Slice        `json:"by_class"`
	ByQuota    []Slice        `json:"by_quota"`
	TopTrains  []TrainRevenue `json:"top_trains"`
	TopRoutes  []Route        `json:"top_routes"`
	ByTravel   []DayPoint     `json:"by_travel_date"` // demand by journey date
	Complaints ComplaintStats `json:"complaints"`
}

// ComplaintStats is the RailMadad picture: how many, how many still open, how fast they close.
type ComplaintStats struct {
	Total         int              `json:"total"`
	Open          int              `json:"open"`
	Urgent        int              `json:"urgent_open"`
	AvgAckMin     float64          `json:"avg_ack_min"`     // reported → acknowledged
	AvgResolveMin float64          `json:"avg_resolve_min"` // reported → resolved
	ByCategory    []ComplaintSlice `json:"by_category"`
}

type ComplaintSlice struct {
	Key        string  `json:"key"`
	Department string  `json:"department"`
	Count      int     `json:"count"`
	Open       int     `json:"open"`
	AvgMin     float64 `json:"avg_resolve_min"`
}

// Overview is the revenue and demand picture for a scope.
func (s *Service) Overview(ctx context.Context, sc Scope) (*Overview, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	o := &Overview{Scope: sc}
	args := []any{sc.Zone, sc.Division}

	// One pass over the bookings, grouped finely enough to build every split in Go.
	rows, err := s.DB.Query(ctx, `WITH tr AS (`+scoped+`),
		px AS (SELECT p.booking_id, count(*) n, count(*) FILTER (WHERE p.status = 'CAN') can, count(*) FILTER (WHERE p.status = 'WL') wl,
		              count(*) FILTER (WHERE p.booked_status LIKE 'CNF%') cnf
		         FROM booking_passengers p GROUP BY 1)
		SELECT b.class_code, b.quota, b.train_number, b.from_station, b.to_station, b.journey_date::text,
		       count(*)::int, sum(px.n)::int, sum(b.total_fare)::int, sum(px.can)::int, sum(px.wl)::int, sum(px.cnf)::int
		  FROM bookings b JOIN tr ON tr.number = b.train_number JOIN px ON px.booking_id = b.id
		 WHERE b.confirmed_at IS NOT NULL
		 GROUP BY 1, 2, 3, 4, 5, 6`, args...)
	if err != nil {
		return nil, err
	}
	k := &o.KPIs
	class, quota, routes, days := map[string]*Slice{}, map[string]*Slice{}, map[[2]string]*Route{}, map[string]*DayPoint{}
	trains := map[string]*TrainRevenue{}
	tatkal, cnf := 0, 0
	for rows.Next() {
		var c, q, t, from, to, day string
		var nb, np, fare, can, wl, ok int
		if err := rows.Scan(&c, &q, &t, &from, &to, &day, &nb, &np, &fare, &can, &wl, &ok); err != nil {
			return nil, err
		}
		k.Bookings += nb
		k.Passengers += np
		k.Gross += fare
		k.Cancelled += can
		k.Waitlisted += wl
		cnf += ok
		if q == "TQ" || q == "PT" {
			tatkal += fare
		}
		add := func(m map[string]*Slice, key string) {
			if m[key] == nil {
				m[key] = &Slice{Key: key}
			}
			m[key].Passengers += np
			m[key].Revenue += fare
		}
		add(class, c)
		add(quota, q)
		if trains[t] == nil {
			trains[t] = &TrainRevenue{Number: t, Name: s.Net.Trains[t].Name}
		}
		trains[t].Passengers += np
		trains[t].Revenue += fare
		rk := [2]string{from, to}
		if routes[rk] == nil {
			routes[rk] = &Route{From: from, To: to}
		}
		routes[rk].Passengers += np
		routes[rk].Revenue += fare
		if days[day] == nil {
			d, _ := time.ParseInLocation(time.DateOnly, day, s.today().Location())
			days[day] = &DayPoint{Date: d}
		}
		days[day].Passengers += np
		days[day].Revenue += fare
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if k.Passengers > 0 {
		k.AvgFare = k.Gross / k.Passengers
		k.BookedConfirm = float64(cnf) / float64(k.Passengers)
	}
	if k.Gross > 0 {
		k.TatkalShare = float64(tatkal) / float64(k.Gross)
	}
	o.ByClass = top(class, 0, func(a, b *Slice) int { return b.Revenue - a.Revenue })
	o.ByQuota = top(quota, 0, func(a, b *Slice) int { return b.Revenue - a.Revenue })
	o.TopTrains = top(trains, 10, func(a, b *TrainRevenue) int { return b.Revenue - a.Revenue })
	o.TopRoutes = top(routes, 10, func(a, b *Route) int { return b.Passengers - a.Passengers })
	o.ByTravel = top(days, 0, func(a, b *DayPoint) int { return a.Date.Compare(b.Date) })

	err = s.DB.QueryRow(ctx, `WITH tr AS (`+scoped+`) SELECT
		(SELECT coalesce(sum(r.amount), 0) FROM refunds r JOIN bookings b ON b.id = r.booking_id JOIN tr ON tr.number = b.train_number WHERE r.status <> 'FAILED'),
		(SELECT coalesce(sum(f.fare + f.penalty), 0) FROM tte_fines f JOIN train_runs r ON r.id = f.run_id JOIN tr ON tr.number = r.train_number),
		(SELECT coalesce(sum(total), 0) FROM food_orders fo JOIN tr ON tr.number = fo.train_number WHERE fo.status = 'delivered'),
		(SELECT coalesce(sum(commission), 0) FROM food_orders fo JOIN tr ON tr.number = fo.train_number WHERE fo.status = 'delivered')`,
		args...).Scan(&k.Refunds, &k.Penalties, &k.FoodSales, &k.FoodCommission)
	if err != nil {
		return nil, err
	}
	k.Net = k.Gross - k.Refunds

	cs := &o.Complaints
	inScope := `WITH tr AS (` + scoped + `), c AS (SELECT c.* FROM complaints c JOIN train_runs r ON r.id = c.run_id JOIN tr ON tr.number = r.train_number)`
	err = s.DB.QueryRow(ctx, inScope+` SELECT count(*), count(*) FILTER (WHERE status <> 'resolved'),
			count(*) FILTER (WHERE status <> 'resolved' AND priority = 'urgent'),
			coalesce(avg(extract(epoch FROM acknowledged_at - created_at) / 60), 0),
			coalesce(avg(extract(epoch FROM resolved_at - created_at) / 60), 0) FROM c`, args...).
		Scan(&cs.Total, &cs.Open, &cs.Urgent, &cs.AvgAckMin, &cs.AvgResolveMin)
	if err != nil {
		return nil, err
	}
	cs.ByCategory, err = collect[ComplaintSlice](ctx, s.DB, inScope+` SELECT category, min(department), count(*)::int,
			count(*) FILTER (WHERE status <> 'resolved')::int, coalesce(avg(extract(epoch FROM resolved_at - created_at) / 60), 0)::float8
		FROM c GROUP BY 1 ORDER BY 3 DESC`, args...)
	return o, err
}

// top sorts a map's values and keeps the first n (all if n is 0); never nil.
func top[K comparable, V any](m map[K]*V, n int, cmp func(a, b *V) int) []V {
	list := make([]*V, 0, len(m))
	for _, v := range m {
		list = append(list, v)
	}
	slices.SortFunc(list, cmp)
	if n > 0 && len(list) > n {
		list = list[:n]
	}
	out := make([]V, len(list))
	for i, v := range list {
		out[i] = *v
	}
	return out
}

package analytics

import (
	"context"
	"slices"
	"time"
)

// Occupancy is berth use: of all the berth-segments a run offers (every berth, between every
// pair of consecutive stops), the share sold. A berth taken Chennai–Salem and again
// Salem–Ernakulam counts as full for the whole run.

type Cell struct {
	Date    string   `json:"date"`
	Runs    bool     `json:"runs"`           // the train runs that day
	Load    *float64 `json:"load,omitempty"` // nil: not open for booking yet
	WL      int      `json:"wl"`
	Booking int      `json:"bookings"`
}

type HeatRow struct {
	Number string  `json:"number"`
	Name   string  `json:"name"`
	Avg    float64 `json:"avg"`
	Cells  []Cell  `json:"cells"`
}

type ClassLoad struct {
	Class  string  `json:"class"`
	Name   string  `json:"name"`
	Load   float64 `json:"load"`
	Berths int     `json:"berths"` // summed over the runs counted
}

type RunLoad struct {
	Number string  `json:"number"`
	Name   string  `json:"name"`
	Date   string  `json:"date"`
	Load   float64 `json:"load"`
	WL     int     `json:"wl"`
}

type Occupancy struct {
	Scope    Scope       `json:"scope"`
	Dates    []string    `json:"dates"`
	Runs     int         `json:"runs"` // open for booking in the window
	AvgLoad  float64     `json:"avg_load"`
	Over90   int         `json:"over_90"`
	WL       int         `json:"waitlisted"`
	Heatmap  []HeatRow   `json:"heatmap"`
	ByClass  []ClassLoad `json:"by_class"`
	Busiest  []RunLoad   `json:"busiest"`
	Trains   int         `json:"trains"` // with a run open in the window
	MaxTrain int         `json:"max_rows"`
}

type runKey struct {
	train string
	date  string
}

// Occupancy covers runs leaving in the next `days` days.
func (s *Service) Occupancy(ctx context.Context, sc Scope, days int) (*Occupancy, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	from := s.today()
	to := from.AddDate(0, 0, days)
	o := &Occupancy{Scope: sc, MaxTrain: 20}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		o.Dates = append(o.Dates, d.Format(time.DateOnly))
	}

	type use struct{ used, cap, berths int }
	runs := map[runKey]map[string]*use{} // run -> class -> berth-segments
	wl := map[runKey]int{}
	bookings := map[runKey]int{}
	extra := map[runKey]map[string]int{}

	rows, err := s.DB.Query(ctx, `WITH tr AS (`+scoped+`)
		SELECT r.train_number, r.origin_date::text, coalesce(sa.class_code, ''), coalesce(sum(upper(sa.seg) - lower(sa.seg)), 0)::int
		  FROM train_runs r JOIN tr ON tr.number = r.train_number
		  LEFT JOIN seat_allocations sa ON sa.run_id = r.id AND sa.slot = 0
		 WHERE r.origin_date >= $3 AND r.origin_date < $4
		 GROUP BY 1, 2, 3`, sc.Zone, sc.Division, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k runKey
		var class string
		var used int
		if err := rows.Scan(&k.train, &k.date, &class, &used); err != nil {
			return nil, err
		}
		if runs[k] == nil {
			runs[k] = map[string]*use{}
		}
		if class != "" {
			runs[k][class] = &use{used: used}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `WITH tr AS (`+scoped+`)
		SELECT b.train_number, b.journey_date::text, count(DISTINCT b.id)::int, count(*) FILTER (WHERE p.status = 'WL')::int
		  FROM bookings b JOIN tr ON tr.number = b.train_number JOIN booking_passengers p ON p.booking_id = b.id
		 WHERE b.journey_date >= $3 AND b.journey_date < $4 AND b.status = 'CONFIRMED'
		 GROUP BY 1, 2`, sc.Zone, sc.Division, from.AddDate(0, 0, -3), to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k runKey
		var n, w int
		if err := rows.Scan(&k.train, &k.date, &n, &w); err != nil {
			return nil, err
		}
		bookings[k], wl[k] = n, w
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT r.train_number, r.origin_date::text, x.class_code, count(*)::int
		FROM run_extra_coaches x JOIN train_runs r ON r.id = x.run_id WHERE r.origin_date >= $1 AND r.origin_date < $2 GROUP BY 1, 2, 3`, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k runKey
		var class string
		var n int
		if err := rows.Scan(&k.train, &k.date, &class, &n); err != nil {
			return nil, err
		}
		if extra[k] == nil {
			extra[k] = map[string]int{}
		}
		extra[k][class] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Capacity from each train's rake (plus any extra coaches on that run).
	classTotals := map[string]*use{}
	loads := map[runKey]float64{}
	for k, cls := range runs {
		t := s.Net.Trains[k.train]
		if t == nil || len(t.Stops) < 2 {
			continue
		}
		segs := len(t.Stops) - 1
		coaches := map[string]int{}
		for _, c := range t.Coaches {
			coaches[c.Class]++
		}
		for c, n := range extra[k] {
			coaches[c] += n
		}
		used, capacity := 0, 0
		for c, n := range coaches {
			cc := s.Net.Classes[c]
			if cc == nil || !cc.Bookable || cc.Seats == 0 {
				continue
			}
			u := cls[c]
			if u == nil {
				u = &use{}
			}
			u.cap = n * cc.Seats * segs
			used += min(u.used, u.cap)
			capacity += u.cap
			if classTotals[c] == nil {
				classTotals[c] = &use{}
			}
			classTotals[c].used += min(u.used, u.cap)
			classTotals[c].cap += u.cap
			classTotals[c].berths += n * cc.Seats
		}
		if capacity == 0 {
			continue
		}
		loads[k] = float64(used) / float64(capacity)
	}

	o.Runs = len(loads)
	perTrain := map[string][]float64{}
	sum := 0.0
	for k, l := range loads {
		sum += l
		if l >= 0.9 {
			o.Over90++
		}
		o.WL += wl[k]
		perTrain[k.train] = append(perTrain[k.train], l)
		t := s.Net.Trains[k.train]
		o.Busiest = append(o.Busiest, RunLoad{Number: k.train, Name: t.Name, Date: k.date, Load: l, WL: wl[k]})
	}
	if o.Runs > 0 {
		o.AvgLoad = sum / float64(o.Runs)
	}
	slices.SortFunc(o.Busiest, func(a, b RunLoad) int {
		if a.Load != b.Load {
			if a.Load > b.Load {
				return -1
			}
			return 1
		}
		return b.WL - a.WL
	})
	o.Busiest = o.Busiest[:min(10, len(o.Busiest))]

	o.Trains = len(perTrain)
	for num, ls := range perTrain {
		t := s.Net.Trains[num]
		row := HeatRow{Number: num, Name: t.Name}
		for _, l := range ls {
			row.Avg += l
		}
		row.Avg /= float64(len(ls))
		for _, d := range o.Dates {
			day, _ := time.ParseInLocation(time.DateOnly, d, from.Location())
			k := runKey{num, d}
			c := Cell{Date: d, Runs: t.RunsOn(day), WL: wl[k], Booking: bookings[k]}
			if l, ok := loads[k]; ok {
				c.Load = &l
			}
			row.Cells = append(row.Cells, c)
		}
		o.Heatmap = append(o.Heatmap, row)
	}
	slices.SortFunc(o.Heatmap, func(a, b HeatRow) int {
		if a.Avg > b.Avg {
			return -1
		}
		if a.Avg < b.Avg {
			return 1
		}
		return 0
	})
	o.Heatmap = o.Heatmap[:min(o.MaxTrain, len(o.Heatmap))]

	for c, u := range classTotals {
		if u.cap == 0 {
			continue
		}
		cc := s.Net.Classes[c]
		o.ByClass = append(o.ByClass, ClassLoad{Class: c, Name: cc.Name, Load: float64(u.used) / float64(u.cap), Berths: u.berths})
	}
	slices.SortFunc(o.ByClass, func(a, b ClassLoad) int { return s.Net.Classes[a.Class].Sort - s.Net.Classes[b.Class].Sort })
	if o.Heatmap == nil {
		o.Heatmap = []HeatRow{}
	}
	if o.Busiest == nil {
		o.Busiest = []RunLoad{}
	}
	if o.ByClass == nil {
		o.ByClass = []ClassLoad{}
	}
	return o, nil
}

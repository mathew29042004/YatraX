package control

import (
	"context"
	"sort"
	"sync"
	"time"
)

type Conflict struct {
	Station     string `json:"station"`
	StationName string `json:"station_name"`
	Platform    int    `json:"platform"`
	A           string `json:"a"`
	B           string `json:"b"`
	BOrigin     string `json:"b_origin_date"`
	OverlapMin  int    `json:"overlap_min"`
	Suggest     int    `json:"suggest"`
}

var (
	conflictsMu    sync.Mutex
	conflictsCache = map[string]struct {
		at   time.Time
		list []Conflict
	}{}
)

// Conflicts finds platform clashes at the network's major stations (optionally one
// zone's) over the next hours. Cached for half a minute: it walks a lot of boards.
func (c *Service) Conflicts(ctx context.Context, zone string, hours int) ([]Conflict, error) {
	key := zone + "|" + time.Duration(hours).String()
	conflictsMu.Lock()
	if v, ok := conflictsCache[key]; ok && time.Since(v.at) < 30*time.Second {
		conflictsMu.Unlock()
		return v.list, nil
	}
	conflictsMu.Unlock()
	out := []Conflict{}
	for _, st := range c.Net.Stations {
		if st.Importance != 1 || (zone != "" && st.Zone != zone) {
			continue
		}
		p, err := c.Station.Platforms(ctx, st.Code, hours)
		if err != nil {
			return nil, err
		}
		origins := map[string]string{}
		for _, pf := range p.Platforms {
			for _, s := range pf.Slots {
				origins[s.Train] = s.OriginDate
			}
		}
		for _, cl := range p.Clashes {
			out = append(out, Conflict{Station: st.Code, StationName: st.Name, Platform: cl.Platform, A: cl.A, B: cl.B,
				BOrigin: origins[cl.B], OverlapMin: cl.OverlapMin, Suggest: cl.Suggest})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OverlapMin != out[j].OverlapMin {
			return out[i].OverlapMin > out[j].OverlapMin
		}
		return out[i].Station < out[j].Station
	})
	conflictsMu.Lock()
	conflictsCache[key] = struct {
		at   time.Time
		list []Conflict
	}{time.Now(), out}
	conflictsMu.Unlock()
	return out, nil
}

type Waitlist struct {
	Train      string `json:"train"`
	Name       string `json:"name"`
	OriginDate string `json:"origin_date"`
	Class      string `json:"class"`
	Waitlisted int    `json:"waitlisted"`
	RAC        int    `json:"rac"`
	Extra      int    `json:"extra_coaches"` // already added to this run
}

// Waitlists are the runs with the longest waitlists in the coming days: where an extra
// coach would confirm the most people.
func (c *Service) Waitlists(ctx context.Context, days int) ([]Waitlist, error) {
	from := c.Now()
	rows, err := c.DB.Query(ctx, `
		SELECT r.train_number, r.origin_date, b.class_code,
		       count(*) FILTER (WHERE p.status = 'WL'), count(*) FILTER (WHERE p.status = 'RAC'),
		       (SELECT count(*) FROM run_extra_coaches e WHERE e.run_id = r.id AND e.class_code = b.class_code)
		  FROM train_runs r JOIN bookings b ON b.run_id = r.id JOIN booking_passengers p ON p.booking_id = b.id
		 WHERE b.status = 'CONFIRMED' AND p.status IN ('WL', 'RAC') AND r.chart1_at IS NULL
		   AND r.origin_date BETWEEN $1::date AND $1::date + $2::int
		 GROUP BY r.id, r.train_number, r.origin_date, b.class_code
		HAVING count(*) FILTER (WHERE p.status = 'WL') > 0
		 ORDER BY 4 DESC LIMIT 25`, from.Format(time.DateOnly), days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Waitlist{}
	for rows.Next() {
		var w Waitlist
		var d time.Time
		if err := rows.Scan(&w.Train, &d, &w.Class, &w.Waitlisted, &w.RAC, &w.Extra); err != nil {
			return nil, err
		}
		w.OriginDate = d.Format(time.DateOnly)
		if t := c.Net.Trains[w.Train]; t != nil {
			w.Name = t.Name
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

type DisruptionRow struct {
	Train      string    `json:"train"`
	Name       string    `json:"name"`
	OriginDate string    `json:"origin_date"`
	Kind       string    `json:"kind"`
	Station    string    `json:"station,omitempty"`
	Skip       []string  `json:"skip"`
	Reason     string    `json:"reason"`
	By         string    `json:"by"`
	At         time.Time `json:"at"`
}

func (c *Service) Disruptions(ctx context.Context) ([]DisruptionRow, error) {
	rows, err := c.DB.Query(ctx, `SELECT r.train_number, r.origin_date, d.kind, coalesce(d.at_station, ''), d.skip, d.reason,
		coalesce(u.full_name, ''), d.created_at
		FROM run_disruptions d JOIN train_runs r ON r.id = d.run_id LEFT JOIN users u ON u.id = d.created_by
		ORDER BY d.created_at DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DisruptionRow{}
	for rows.Next() {
		var x DisruptionRow
		var d time.Time
		if err := rows.Scan(&x.Train, &d, &x.Kind, &x.Station, &x.Skip, &x.Reason, &x.By, &x.At); err != nil {
			return nil, err
		}
		x.OriginDate = d.Format(time.DateOnly)
		if t := c.Net.Trains[x.Train]; t != nil {
			x.Name = t.Name
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

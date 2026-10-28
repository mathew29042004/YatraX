// Package analytics is the zone and division dashboards: revenue, occupancy, punctuality,
// complaints and the fraud report. A train belongs to the zone and division of the station
// it starts from, as on Indian Railways, and everything about it is counted there.
package analytics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// OnTimeMin is how late a train may reach its destination and still count as on time.
const OnTimeMin = 15

var ErrScope = errors.New("outside your zone")

type Service struct {
	DB   *pgxpool.Pool
	Net  *network.Network
	Live *live.Engine
	Now  func() time.Time // railway time

	once      sync.Once
	loadErr   error
	zones     []Zone
	owner     map[string]owner // train number -> its zone and division
	mu        sync.Mutex
	outcomes  map[string]map[string]live.Outcome // date -> train -> how the run went
	outcomeAt map[string]time.Time
}

type owner struct {
	zone     string
	division int
}

type Division struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Zone struct {
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	HQ        string     `json:"hq"`
	Divisions []Division `json:"divisions"`
}

// Scope narrows a dashboard to a zone, or a division within it; empty is all India.
type Scope struct {
	Zone     string `json:"zone"`
	Division int    `json:"division"`
}

func (s *Service) load(ctx context.Context) error {
	s.once.Do(func() {
		rows, err := s.DB.Query(ctx, `SELECT z.code, z.name, z.hq, d.id, d.name FROM zones z JOIN divisions d ON d.zone_code = z.code ORDER BY z.code, d.name`)
		if err != nil {
			s.loadErr = err
			return
		}
		for rows.Next() {
			var code, name, hq, dn string
			var id int
			if err := rows.Scan(&code, &name, &hq, &id, &dn); err != nil {
				s.loadErr = err
				return
			}
			if n := len(s.zones); n == 0 || s.zones[n-1].Code != code {
				s.zones = append(s.zones, Zone{Code: code, Name: name, HQ: hq})
			}
			z := &s.zones[len(s.zones)-1]
			z.Divisions = append(z.Divisions, Division{ID: id, Name: dn})
		}
		if s.loadErr = rows.Err(); s.loadErr != nil {
			return
		}
		rows, err = s.DB.Query(ctx, `SELECT t.number, d.zone_code, d.id FROM trains t JOIN stations st ON st.code = t.origin JOIN divisions d ON d.id = st.division_id`)
		if err != nil {
			s.loadErr = err
			return
		}
		s.owner = map[string]owner{}
		var num, zone string
		var div int
		_, s.loadErr = pgx.ForEachRow(rows, []any{&num, &zone, &div}, func() error {
			s.owner[num] = owner{zone, div}
			return nil
		})
	})
	return s.loadErr
}

// Zones lists the zones and their divisions.
func (s *Service) Zones(ctx context.Context) ([]Zone, error) {
	err := s.load(ctx)
	return s.zones, err
}

// Valid checks a scope names a real zone and a division inside it.
func (s *Service) Valid(ctx context.Context, sc Scope) bool {
	if s.load(ctx) != nil {
		return false
	}
	if sc.Zone == "" {
		return sc.Division == 0
	}
	for _, z := range s.zones {
		if z.Code == sc.Zone {
			if sc.Division == 0 {
				return true
			}
			for _, d := range z.Divisions {
				if d.ID == sc.Division {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) in(sc Scope, train string) bool {
	o, ok := s.owner[train]
	return ok && (sc.Zone == "" || o.zone == sc.Zone) && (sc.Division == 0 || o.division == sc.Division)
}

// scoped is the SQL for the train numbers in a scope ($1 zone, $2 division).
const scoped = `SELECT t.number FROM trains t JOIN stations st ON st.code = t.origin JOIN divisions d ON d.id = st.division_id
	WHERE ($1 = '' OR d.zone_code = $1) AND ($2 = 0 OR d.id = $2)`

func (s *Service) today() time.Time { return live.Midnight(s.Now()) }

func collect[T any](ctx context.Context, db *pgxpool.Pool, sql string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[T])
	if out == nil {
		out = []T{}
	}
	return out, err
}

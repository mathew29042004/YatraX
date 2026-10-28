package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

type stationCall struct {
	Number      string `json:"number"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	Arrival     string `json:"arrival,omitempty"`
	Departure   string `json:"departure,omitempty"`
	Platform    int16  `json:"platform"`
	RunningDays []bool `json:"running_days"`
}

// stationTrains lists every train calling at a station, ordered by time of day.
func (s *Server) stationTrains(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT t.number, t.name, t.type_code, t.origin, t.destination,
		       ts.arrival_min, ts.departure_min, ts.platform, t.running_days
		  FROM train_stops ts JOIN trains t ON t.number = ts.train_number
		 WHERE ts.station_code = upper($1)
		 ORDER BY coalesce(ts.departure_min, ts.arrival_min) % 1440`, chi.URLParam(r, "code"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (stationCall, error) {
		var c stationCall
		var arr, dep *int
		var mask int16
		err := row.Scan(&c.Number, &c.Name, &c.Type, &c.Origin, &c.Destination, &arr, &dep, &c.Platform, &mask)
		if arr != nil {
			c.Arrival = clock(*arr)
		}
		if dep != nil {
			c.Departure = clock(*dep)
		}
		c.RunningDays = days(mask)
		return c, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) vendorMe(w http.ResponseWriter, r *http.Request) {
	var v struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Station     string   `json:"station"`
		StationName string   `json:"station_name"`
		Cuisines    []string `json:"cuisines"`
		VegOnly     bool     `json:"veg_only"`
		FSSAI       string   `json:"fssai_license"`
		Rating      float64  `json:"rating"`
	}
	err := s.DB.QueryRow(r.Context(), `
		SELECT v.id, v.name, v.station_code, st.name, v.cuisines, v.veg_only, v.fssai_license, v.rating::float8
		  FROM vendors v JOIN stations st ON st.code = v.station_code
		 WHERE v.owner_user_id = $1`, auth.ClaimsFrom(r.Context()).Subject).
		Scan(&v.ID, &v.Name, &v.Station, &v.StationName, &v.Cuisines, &v.VegOnly, &v.FSSAI, &v.Rating)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "not_found", "No vendor profile for this account")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

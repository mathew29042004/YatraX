package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/yatrax/yatrax/services/core/internal/analytics"
	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

// analyticsScope reads ?zone=&division= and applies who may see what: admins and
// controllers see any zone; station staff and TTEs see their own zone (any division in it).
// The second value is the zone the user is held to, if any.
func (s *Server) analyticsScope(w http.ResponseWriter, r *http.Request) (analytics.Scope, bool) {
	q := r.URL.Query()
	sc := analytics.Scope{Zone: strings.ToUpper(q.Get("zone"))}
	sc.Division, _ = strconv.Atoi(q.Get("division"))
	home, err := s.homeZone(r)
	if err != nil {
		httpx.Internal(w, r, err)
		return sc, false
	}
	if home != "" {
		if sc.Zone == "" {
			sc.Zone = home
		}
		if sc.Zone != home {
			httpx.Error(w, http.StatusForbidden, "forbidden", "You can see your own zone's dashboards ("+home+")")
			return sc, false
		}
	}
	if !s.Analytics.Valid(r.Context(), sc) {
		httpx.Error(w, http.StatusBadRequest, "bad_scope", "Unknown zone or division")
		return sc, false
	}
	return sc, true
}

// homeZone is the zone a user's dashboards are held to: "" for admins and controllers.
func (s *Server) homeZone(r *http.Request) (string, error) {
	c := auth.ClaimsFrom(r.Context())
	if c.Role == auth.RoleAdmin || c.Role == auth.RoleController {
		return "", nil
	}
	var zone string
	err := s.DB.QueryRow(r.Context(), `SELECT zone_code FROM staff_postings WHERE user_id = $1`, c.Subject).Scan(&zone)
	return zone, err
}

func (s *Server) analyticsZones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.Analytics.Zones(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	home, err := s.homeZone(r)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"zones": zones, "home_zone": home, "on_time_min": analytics.OnTimeMin})
}

func (s *Server) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.analyticsScope(w, r)
	if !ok {
		return
	}
	o, err := s.Analytics.Overview(r.Context(), sc)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func (s *Server) analyticsOccupancy(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.analyticsScope(w, r)
	if !ok {
		return
	}
	o, err := s.Analytics.Occupancy(r.Context(), sc, intParam(r, "days", 14, 1, 60))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func (s *Server) analyticsPunctuality(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.analyticsScope(w, r)
	if !ok {
		return
	}
	p, err := s.Analytics.Punctuality(r.Context(), sc, intParam(r, "days", 7, 1, 30))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (s *Server) analyticsFraud(w http.ResponseWriter, r *http.Request) {
	f, err := s.Analytics.Fraud(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, f)
}

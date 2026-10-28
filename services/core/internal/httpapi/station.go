package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/network"
	stationsvc "github.com/yatrax/yatrax/services/core/internal/station"
)

func stationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, stationsvc.ErrNoStation), errors.Is(err, stationsvc.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, stationsvc.ErrPlatform):
		httpx.Error(w, http.StatusBadRequest, "bad_platform", err.Error())
	default:
		if strings.Contains(err.Error(), "does not call") || strings.Contains(err.Error(), "no train") || strings.Contains(err.Error(), "status is") {
			httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		httpx.Internal(w, r, err)
	}
}

func stationCode(r *http.Request) string { return strings.ToUpper(chi.URLParam(r, "code")) }

// ── Public: announcements and coach position ───────────────────────────────

func (s *Server) stationAnnouncements(w http.ResponseWriter, r *http.Request) {
	a, err := s.Station.Announcements(stationCode(r))
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, a)
}

func (s *Server) coachPosition(w http.ResponseWriter, r *http.Request) {
	t, origin, ok := s.runFor(w, chi.URLParam(r, "train"), r.URL.Query().Get("date"))
	if !ok {
		return
	}
	cp, err := s.Station.CoachPosition(stationCode(r), t.Number, origin)
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, cp)
}

// ── Station master: only at their own station (admins anywhere) ────────────

func (s *Server) ownStation(w http.ResponseWriter, r *http.Request) (string, bool) {
	code := stationCode(r)
	if c := auth.ClaimsFrom(r.Context()); c != nil && c.Role == auth.RoleAdmin {
		return code, true
	}
	var posted string
	s.DB.QueryRow(r.Context(), `SELECT coalesce(station_code, '') FROM staff_postings WHERE user_id = $1`, userID(r)).Scan(&posted)
	if posted != code {
		httpx.Error(w, http.StatusForbidden, "not_your_station", "You're posted at "+posted+"; you can only manage that station")
		return "", false
	}
	return code, true
}

func (s *Server) stationPlatforms(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 12 {
		hours = 4
	}
	p, err := s.Station.Platforms(r.Context(), code, hours)
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (s *Server) assignPlatform(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Platform   int    `json:"platform"` // 0 = back to the timetable
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, err := time.ParseInLocation(time.DateOnly, in.OriginDate, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "origin_date looks like 2026-11-02")
		return
	}
	if err := s.Station.Assign(r.Context(), code, in.Train, d, in.Platform, userID(r)); err != nil {
		stationError(w, r, err)
		return
	}
	t := s.Net.Trains[in.Train]
	httpx.JSON(w, http.StatusOK, s.Live.Status(t, d, s.Clock.Now()))
}

func (s *Server) stationFacilities(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	f, err := s.Station.Facilities(r.Context(), code)
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, f)
}

func (s *Server) addLost(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	var in struct {
		Item        string `json:"item"`
		Description string `json:"description"`
		FoundAt     string `json:"found_at"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Item) == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid", "what was found?")
		return
	}
	id, err := s.Station.AddLost(r.Context(), code, in.Item, in.Description, in.FoundAt, userID(r))
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) returnLost(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		To string `json:"returned_to"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.To) == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid", "who collected it?")
		return
	}
	if err := s.Station.ReturnLost(r.Context(), code, id, in.To); err != nil {
		stationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addWheelchair(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	var in struct {
		Passenger string `json:"passenger"`
		PNR       string `json:"pnr"`
		Train     string `json:"train"`
		NeededAt  string `json:"needed_at"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Passenger) == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid", "who needs the wheelchair?")
		return
	}
	id, err := s.Station.AddWheelchair(r.Context(), code, in.Passenger, in.PNR, in.Train, in.NeededAt, userID(r))
	if err != nil {
		stationError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) updateWheelchair(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Status    string `json:"status"`
		Attendant string `json:"attendant"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := s.Station.UpdateWheelchair(r.Context(), code, id, in.Status, in.Attendant); err != nil {
		stationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

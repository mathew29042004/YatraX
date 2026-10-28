package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/complaints"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

func complaintError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, complaints.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, complaints.ErrNotAllowed):
		httpx.Error(w, http.StatusConflict, "not_allowed", strings.TrimPrefix(err.Error(), complaints.ErrNotAllowed.Error()+": "))
	default:
		httpx.Internal(w, r, err)
	}
}

func (s *Server) fileComplaint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Category    string `json:"category"`
		Description string `json:"description"`
		Coach       string `json:"coach"`
		Berth       int    `json:"berth"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	c, err := s.Complaints.File(r.Context(), userID(r), chi.URLParam(r, "id"), in.Category, in.Description, strings.ToUpper(in.Coach), in.Berth)
	if err != nil {
		complaintError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

func (s *Server) raiseSOS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := s.Complaints.SOS(r.Context(), userID(r), chi.URLParam(r, "id"), in.Kind)
	if err != nil {
		complaintError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, res)
}

func (s *Server) myComplaints(w http.ResponseWriter, r *http.Request) {
	list, err := s.Complaints.Mine(r.Context(), userID(r), r.URL.Query().Get("booking_id"))
	if err != nil {
		complaintError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

// complaintStation is the station a staff member's queue is held to: a station master sees
// their own station; controllers and admins see everything (or pick one).
func (s *Server) complaintStation(r *http.Request) (string, error) {
	c := auth.ClaimsFrom(r.Context())
	if c.Role == auth.RoleStation {
		var code string
		err := s.DB.QueryRow(r.Context(), `SELECT coalesce(station_code, '') FROM staff_postings WHERE user_id = $1`, c.Subject).Scan(&code)
		return code, err
	}
	return strings.ToUpper(r.URL.Query().Get("station")), nil
}

func (s *Server) complaintQueue(w http.ResponseWriter, r *http.Request) {
	station, err := s.complaintStation(r)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	list, err := s.Complaints.Queue(r.Context(), station, r.URL.Query().Get("resolved") == "1")
	if err != nil {
		complaintError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) actOnComplaint(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Action     string `json:"action"` // acknowledge or resolve
		Resolution string `json:"resolution"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	station := ""
	if auth.ClaimsFrom(r.Context()).Role == auth.RoleStation {
		var err error
		if station, err = s.complaintStation(r); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	var c *complaints.Complaint
	var err error
	switch in.Action {
	case "acknowledge":
		c, err = s.Complaints.Acknowledge(r.Context(), userID(r), id, station)
	case "resolve":
		c, err = s.Complaints.Resolve(r.Context(), userID(r), id, station, in.Resolution)
	default:
		httpx.Error(w, http.StatusBadRequest, "invalid", "action must be acknowledge or resolve")
		return
	}
	if err != nil {
		complaintError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

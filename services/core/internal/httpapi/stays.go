package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/stays"
)

func stayError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, stays.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, stays.ErrNotAllowed):
		httpx.Error(w, http.StatusConflict, "not_allowed", strings.TrimPrefix(err.Error(), stays.ErrNotAllowed.Error()+": "))
	default:
		httpx.Internal(w, r, err)
	}
}

// stayOptions: GET /v1/stations/{code}/stays?from=RFC3339&hours=24 (public)
func (s *Server) stayOptions(w http.ResponseWriter, r *http.Request) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		from = s.Now()
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	out, err := s.Stays.Options(r.Context(), strings.ToUpper(chi.URLParam(r, "code")), from, hours)
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) bookStay(w http.ResponseWriter, r *http.Request) {
	var in stays.Request
	if !httpx.Decode(w, r, &in) {
		return
	}
	st, err := s.Stays.Book(r.Context(), userID(r), in)
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, st)
}

func (s *Server) myStays(w http.ResponseWriter, r *http.Request) {
	out, err := s.Stays.Mine(r.Context(), userID(r))
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) cancelStay(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.Stays.Cancel(r.Context(), userID(r), id)
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (s *Server) stayDesk(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	out, err := s.Stays.Desk(r.Context(), code)
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) advanceStay(w http.ResponseWriter, r *http.Request) {
	code, ok := s.ownStation(w, r)
	if !ok {
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	st, err := s.Stays.Advance(r.Context(), code, id, in.Action)
	if err != nil {
		stayError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// ── Group booking desk ─────────────────────────────────────────────────────

func (s *Server) requestGroup(w http.ResponseWriter, r *http.Request) {
	var in booking.GroupRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	g, err := s.Booking.RequestGroup(r.Context(), userID(r), in)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, g)
}

func (s *Server) myGroups(w http.ResponseWriter, r *http.Request) {
	out, err := s.Booking.MyGroups(r.Context(), userID(r))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) cancelGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	g, err := s.Booking.CancelGroup(r.Context(), userID(r), id)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, g)
}

// groupStation: station masters handle their own station's departures; others see all.
func (s *Server) groupStation(r *http.Request) (string, error) {
	if auth.ClaimsFrom(r.Context()).Role != auth.RoleStation {
		return "", nil
	}
	return s.complaintStation(r)
}

func (s *Server) groupQueue(w http.ResponseWriter, r *http.Request) {
	station, err := s.groupStation(r)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := s.Booking.GroupQueue(r.Context(), station)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) decideGroup(w http.ResponseWriter, r *http.Request) {
	station, err := s.groupStation(r)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	g, err := s.Booking.DecideGroup(r.Context(), userID(r), id, station, in.Approve, in.Note)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, g)
}

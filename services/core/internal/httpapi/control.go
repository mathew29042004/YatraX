package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/control"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || v < lo || v > hi {
		return def
	}
	return v
}

func (s *Server) sectionChart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sec, err := s.Control.Section(strings.ToUpper(q.Get("from")), strings.ToUpper(q.Get("to")), intParam(r, "hours", 4, 1, 12))
	if errors.Is(err, control.ErrSection) {
		httpx.Error(w, http.StatusBadRequest, "no_section", err.Error())
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, sec)
}

func (s *Server) whatIf(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Station    string `json:"station"`
		DelayMin   int    `json:"delay_min"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, origin, ok := s.runFor(w, in.Train, in.OriginDate)
	if !ok {
		return
	}
	rip, err := s.Control.WhatIf(r.Context(), control.Hold{Train: t.Number, Origin: origin, Station: strings.ToUpper(in.Station), DelayMin: in.DelayMin})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, rip)
}

func (s *Server) networkConflicts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Control.Conflicts(r.Context(), r.URL.Query().Get("zone"), intParam(r, "hours", 2, 1, 6))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) longWaitlists(w http.ResponseWriter, r *http.Request) {
	list, err := s.Control.Waitlists(r.Context(), intParam(r, "days", 14, 1, 60))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) listDisruptions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Control.Disruptions(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) disruptRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		booking.Disruption
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, err := time.ParseInLocation(time.DateOnly, in.OriginDate, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "origin_date looks like 2026-11-02")
		return
	}
	in.Station = strings.ToUpper(in.Station)
	for i := range in.Skip {
		in.Skip[i] = strings.ToUpper(in.Skip[i])
	}
	res, err := s.Booking.Disrupt(r.Context(), strings.TrimSpace(in.Train), d, in.Disruption, userID(r))
	switch {
	case errors.Is(err, booking.ErrAlreadyDone):
		httpx.Error(w, http.StatusConflict, "already_disrupted", err.Error())
		return
	case errors.Is(err, booking.ErrBadDisruption):
		httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
		return
	case err != nil:
		bookingError(w, r, err)
		return
	}
	if err := s.Live.LoadIncidents(r.Context()); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (s *Server) addExtraCoaches(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Class      string `json:"class"`
		Count      int    `json:"count"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, err := time.ParseInLocation(time.DateOnly, in.OriginDate, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "origin_date looks like 2026-11-02")
		return
	}
	if in.Count == 0 {
		in.Count = 1
	}
	res, err := s.Booking.AddCoaches(r.Context(), in.Train, d, strings.ToUpper(in.Class), in.Count, userID(r))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

func (s *Server) replayDay(w http.ResponseWriter, r *http.Request) {
	day, err := time.ParseInLocation(time.DateOnly, r.URL.Query().Get("date"), network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "date must be YYYY-MM-DD")
		return
	}
	out, err := s.Control.Replay(r.Context(), day)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) listSpecials(w http.ResponseWriter, r *http.Request) {
	out, err := s.Control.Specials(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) runSpecial(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train  string `json:"train"`
		Date   string `json:"date"`
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	day, err := time.ParseInLocation(time.DateOnly, in.Date, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "date must be YYYY-MM-DD")
		return
	}
	sp, err := s.Control.RunSpecial(r.Context(), in.Train, day, in.Reason, userID(r))
	if errors.Is(err, control.ErrSection) {
		httpx.Error(w, http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), control.ErrSection.Error()+": "))
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sp)
}

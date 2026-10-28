package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/chaos"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

func (s *Server) chaosDrills(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, s.Chaos.Drills())
}

func (s *Server) chaosRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Chaos.Recent(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, runs)
}

func (s *Server) chaosRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	run, err := s.Chaos.Get(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such drill")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, run)
}

func (s *Server) startChaos(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	run, err := s.Chaos.Start(r.Context(), in.Kind, userID(r))
	switch {
	case errors.Is(err, chaos.ErrUnknown):
		httpx.Error(w, http.StatusBadRequest, "unknown_drill", err.Error())
	case errors.Is(err, chaos.ErrBusy):
		httpx.Error(w, http.StatusConflict, "busy", "Another drill is running; wait for it to finish")
	case err != nil:
		httpx.Internal(w, r, err)
	default:
		httpx.JSON(w, http.StatusAccepted, run)
	}
}

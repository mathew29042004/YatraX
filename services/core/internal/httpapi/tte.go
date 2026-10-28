package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// bundleView is the offline bundle plus what the device needs to check tickets and keep time.
type bundleView struct {
	*booking.TTEBundle
	PublicKey string    `json:"public_key"` // Ed25519, base64: verifies QR tickets offline
	SimNow    time.Time `json:"sim_now"`
}

func (s *Server) tteRun(w http.ResponseWriter, r *http.Request) (string, time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, chi.URLParam(r, "date"), network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "date looks like 2026-11-02 (the day the train leaves its first station)")
		return "", time.Time{}, false
	}
	return chi.URLParam(r, "train"), d, true
}

func (s *Server) tteDuties(w http.ResponseWriter, r *http.Request) {
	var station string
	err := s.DB.QueryRow(r.Context(), `SELECT coalesce(station_code, '') FROM staff_postings WHERE user_id = $1`, userID(r)).Scan(&station)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	if q := r.URL.Query().Get("station"); q != "" {
		station = q
	}
	if s.Net.Stations[station] == nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"station": station, "duties": []booking.Duty{}})
		return
	}
	duties, err := s.Booking.Duties(r.Context(), station, s.Clock.Now())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"station": station, "station_name": s.Net.Stations[station].Name, "duties": duties})
}

func (s *Server) tteBundle(w http.ResponseWriter, r *http.Request) {
	train, date, ok := s.tteRun(w, r)
	if !ok {
		return
	}
	b, err := s.Booking.TTEBundle(r.Context(), train, date)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, bundleView{TTEBundle: b, PublicKey: s.Payments.Signer.PublicKeyBase64(), SimNow: s.Clock.Now()})
}

func (s *Server) tteSync(w http.ResponseWriter, r *http.Request) {
	train, date, ok := s.tteRun(w, r)
	if !ok {
		return
	}
	var in struct {
		Ops []booking.TTEOp `json:"ops"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if len(in.Ops) > 500 {
		httpx.Error(w, http.StatusBadRequest, "too_many", "sync at most 500 operations at a time")
		return
	}
	for _, op := range in.Ops {
		if len(op.ID) != 36 {
			httpx.Error(w, http.StatusBadRequest, "bad_op", "every operation needs a UUID op_id made on the device")
			return
		}
	}
	results, err := s.Booking.ApplyTTEOps(r.Context(), userID(r), train, date, in.Ops)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	b, err := s.Booking.TTEBundle(r.Context(), train, date)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"results": results,
		"bundle":  bundleView{TTEBundle: b, PublicKey: s.Payments.Signer.PublicKeyBase64(), SimNow: s.Clock.Now()},
	})
}

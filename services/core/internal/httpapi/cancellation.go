package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// phase3Error maps cancellation, wallet, chart and TDR failures; anything else falls through.
func phase3Error(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, booking.ErrNotCancellable), errors.Is(err, booking.ErrBoardingChange), errors.Is(err, booking.ErrChartPrepared),
		errors.Is(err, booking.ErrTDRNotEligible), errors.Is(err, booking.ErrTDRExists), errors.Is(err, booking.ErrTDRDecided):
		httpx.Error(w, http.StatusConflict, "not_allowed", err.Error())
	case errors.Is(err, booking.ErrNoSuchSeq):
		httpx.Error(w, http.StatusBadRequest, "bad_passenger", err.Error())
	case errors.Is(err, booking.ErrInsufficientFunds):
		httpx.Error(w, http.StatusPaymentRequired, "insufficient_funds", err.Error())
	default:
		bookingError(w, r, err)
	}
}

func seqsParam(raw string) ([]int, bool) {
	var out []int
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func (s *Server) cancelQuote(w http.ResponseWriter, r *http.Request) {
	seqs, ok := seqsParam(r.URL.Query().Get("passengers"))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_passenger", "passengers is a comma-separated list of passenger numbers")
		return
	}
	q, err := s.Booking.CancelQuote(r.Context(), userID(r), chi.URLParam(r, "id"), seqs)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, q)
}

func (s *Server) cancelBooking(w http.ResponseWriter, r *http.Request) {
	var in booking.CancelRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := s.Booking.Cancel(r.Context(), userID(r), chi.URLParam(r, "id"), in)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	metrics.Cancellations.Add(float64(len(res.Quote.Passengers)))
	metrics.Promotions.Add(float64(len(res.Promotions)))
	httpx.JSON(w, http.StatusOK, res)
}

func (s *Server) payWithWallet(w http.ResponseWriter, r *http.Request) {
	b, err := s.Booking.PayWithWallet(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, b)
}

func (s *Server) changeBoarding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Station string `json:"station"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	b, promos, err := s.Booking.ChangeBoarding(r.Context(), userID(r), chi.URLParam(r, "id"), in.Station)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	metrics.Promotions.Add(float64(len(promos)))
	httpx.JSON(w, http.StatusOK, b)
}

func (s *Server) fileTDR(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, err := s.Booking.FileTDR(r.Context(), userID(r), chi.URLParam(r, "id"), in.Reason, strings.TrimSpace(in.Details))
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (s *Server) tdrReasons(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, booking.TDRReasons)
}

func (s *Server) wallet(w http.ResponseWriter, r *http.Request) {
	wl, err := s.Booking.Wallet(r.Context(), userID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, wl)
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	list, unread, err := s.Booking.Notifications(r.Context(), userID(r), limit(r, 30, 100))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"unread": unread, "items": list})
}

func (s *Server) readNotifications(w http.ResponseWriter, r *http.Request) {
	if err := s.Booking.MarkNotificationsRead(r.Context(), userID(r)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Operations (admin / staff) ─────────────────────────────────────────────

func originDateParam(w http.ResponseWriter, raw string) (time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, raw, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "origin_date looks like 2026-11-02 (the day the train leaves its origin)")
		return time.Time{}, false
	}
	return d, true
}

func (s *Server) prepareChart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Version    int    `json:"version"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, ok := originDateParam(w, in.OriginDate)
	if !ok {
		return
	}
	if in.Version != 2 {
		in.Version = 1
	}
	ch, err := s.Booking.PrepareChart(r.Context(), in.Train, d, in.Version)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ch)
}

func (s *Server) getChart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, ok := originDateParam(w, q.Get("origin_date"))
	if !ok {
		return
	}
	v, _ := strconv.Atoi(q.Get("version"))
	if v != 2 {
		v = 1
	}
	ch, err := s.Booking.GetChart(r.Context(), q.Get("train"), d, v)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ch)
}

func (s *Server) simulateCancellations(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Class      string `json:"class"`
		Count      int    `json:"count"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d, ok := originDateParam(w, in.OriginDate)
	if !ok {
		return
	}
	n, promos, err := s.Booking.SimulateCancellations(r.Context(), in.Train, d, strings.ToUpper(in.Class), in.Count)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	metrics.Promotions.Add(float64(len(promos)))
	if promos == nil {
		promos = []booking.Promotion{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cancelled": n, "promotions": promos})
}

func (s *Server) reconcileNow(w http.ResponseWriter, r *http.Request) {
	stale := booking.StalePaymentAge
	// In development, ?stale_after_sec=0 checks payments immediately (for demos and tests).
	if v := r.URL.Query().Get("stale_after_sec"); v != "" && s.Dev {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			stale = time.Duration(n) * time.Second
		}
	}
	rep, err := s.Booking.ReconcileWith(r.Context(), s.Payments, stale)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rep)
}

func (s *Server) finance(w http.ResponseWriter, r *http.Request) {
	f, err := s.Booking.FinanceSummary(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, f)
}

func (s *Server) listTDRs(w http.ResponseWriter, r *http.Request) {
	list, err := s.Booking.ListTDRs(r.Context(), strings.ToUpper(r.URL.Query().Get("status")))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) decideTDR(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approve bool `json:"approve"`
		Refund  int  `json:"refund"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, err := s.Booking.DecideTDR(r.Context(), userID(r), chi.URLParam(r, "id"), in.Approve, in.Refund)
	if err != nil {
		phase3Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (s *Server) integrity(w http.ResponseWriter, r *http.Request) {
	in, err := s.Booking.Integrity(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, in)
}

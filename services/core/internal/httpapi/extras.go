package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

func (s *Server) bookingInvoice(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Booking.Invoice(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}

func (s *Server) myLoyalty(w http.ResponseWriter, r *http.Request) {
	l, err := s.Booking.Loyalty(r.Context(), userID(r))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

func (s *Server) redeemPoints(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Points int `json:"points"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	l, err := s.Booking.RedeemPoints(r.Context(), userID(r), in.Points)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

func (s *Server) createGuarantee(w http.ResponseWriter, r *http.Request) {
	var in booking.GuaranteeRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	pair, err := s.Booking.HoldGuarantee(r.Context(), userID(r), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, pair)
}

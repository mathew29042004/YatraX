package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/ticket"
)

// bookingError maps booking failures to HTTP responses.
func bookingError(w http.ResponseWriter, r *http.Request, err error) {
	var v booking.ValidationError
	switch {
	case errors.As(err, &v):
		httpx.Error(w, http.StatusBadRequest, "invalid", v.Msg)
	case errors.Is(err, booking.ErrNotFound), errors.Is(err, booking.ErrNoPayment):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, booking.ErrConflict):
		httpx.Error(w, http.StatusConflict, "berth_taken", err.Error())
	case errors.Is(err, booking.ErrNotConfirmed):
		httpx.Error(w, http.StatusConflict, "not_confirmed", "Not every passenger would get a confirmed berth, and you asked to book only if confirmed")
	case errors.Is(err, booking.ErrRegret):
		httpx.Error(w, http.StatusConflict, "regret", err.Error())
	case errors.Is(err, booking.ErrNotHeld):
		httpx.Error(w, http.StatusConflict, "not_held", err.Error())
	case errors.Is(err, booking.ErrTooManyHolds):
		httpx.Error(w, http.StatusTooManyRequests, "too_many_holds", err.Error())
	case errors.Is(err, booking.ErrDisrupted):
		httpx.Error(w, http.StatusConflict, "train_disrupted", err.Error())
	case errors.Is(err, booking.ErrUseWaitingRoom):
		httpx.Error(w, http.StatusConflict, "use_waiting_room", err.Error())
	case errors.Is(err, booking.ErrTatkalSoldOut), errors.Is(err, booking.ErrTatkalLimit):
		httpx.Error(w, http.StatusConflict, "tatkal", err.Error())
	case errors.Is(err, booking.ErrBadSignature):
		httpx.Error(w, http.StatusUnauthorized, "bad_signature", err.Error())
	case strings.Contains(err.Error(), "payment gateway"):
		httpx.Error(w, http.StatusBadGateway, "gateway_unavailable", "The payment gateway is not responding. Your berths stay held; try again shortly.")
	default:
		httpx.Internal(w, r, err)
	}
}

func userID(r *http.Request) string { return auth.ClaimsFrom(r.Context()).Subject }

// createBooking: POST /v1/bookings (header Idempotency-Key recommended)
func (s *Server) createBooking(w http.ResponseWriter, r *http.Request) {
	var in booking.HoldRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 100 {
		httpx.Error(w, http.StatusBadRequest, "bad_key", "Idempotency-Key is too long")
		return
	}
	start := time.Now()
	b, err := s.Booking.Hold(r.Context(), userID(r), key, in)
	metrics.HoldLatency.Observe(time.Since(start).Seconds())
	if err != nil {
		outcome := "error"
		switch {
		case errors.Is(err, booking.ErrConflict):
			outcome = "conflict"
		case errors.Is(err, booking.ErrRegret):
			outcome = "regret"
		case errors.As(err, new(booking.ValidationError)):
			outcome = "invalid"
		}
		metrics.Bookings.WithLabelValues(outcome).Inc()
		bookingError(w, r, err)
		return
	}
	metrics.Bookings.WithLabelValues("held").Inc()
	httpx.JSON(w, http.StatusCreated, b)
}

func (s *Server) listBookings(w http.ResponseWriter, r *http.Request) {
	list, err := s.Booking.List(r.Context(), userID(r), 50)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if list == nil {
		list = []*booking.Booking{}
	}
	httpx.JSON(w, http.StatusOK, list)
}

func (s *Server) getBooking(w http.ResponseWriter, r *http.Request) {
	b, err := s.Booking.GetFor(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, b)
}

func (s *Server) payBooking(w http.ResponseWriter, r *http.Request) {
	url, err := s.Booking.StartPayment(r.Context(), s.Payments, userID(r), chi.URLParam(r, "id"))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"checkout_url": url})
}

func (s *Server) verifyBooking(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrderID   string `json:"order_id"`
		PaymentID string `json:"payment_id"`
		Signature string `json:"signature"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	b, err := s.Booking.VerifyReturn(r.Context(), s.Payments, userID(r), chi.URLParam(r, "id"), in.OrderID, in.PaymentID, in.Signature)
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, b)
}

func (s *Server) releaseBooking(w http.ResponseWriter, r *http.Request) {
	if err := s.Booking.Release(r.Context(), userID(r), chi.URLParam(r, "id")); err != nil {
		bookingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bookingTicket(w http.ResponseWriter, r *http.Request) {
	b, err := s.Booking.GetFor(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	tok, err := s.Booking.Ticket(b, s.Payments.Signer)
	if err != nil {
		httpx.Error(w, http.StatusConflict, "not_confirmed", "The ticket is issued once payment is confirmed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"booking": b, "qr": tok})
}

func (s *Server) seatMap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m, err := s.Booking.SeatMap(r.Context(), booking.HoldRequest{
		Train: q.Get("train"), From: q.Get("from"), To: q.Get("to"), Date: q.Get("date"), Class: q.Get("class"), Quota: q.Get("quota"),
	})
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (s *Server) pnrStatus(w http.ResponseWriter, r *http.Request) {
	b, err := s.Booking.PNRStatus(r.Context(), chi.URLParam(r, "pnr"))
	if err != nil {
		bookingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, b)
}

func (s *Server) ticketPublicKey(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"algorithm": "Ed25519", "public_key": s.Payments.Signer.PublicKeyBase64()})
}

func (s *Server) verifyTicket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	p, err := ticket.Verify(s.Payments.Signer.Pub, strings.TrimSpace(in.Token))
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"valid": false})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"valid": true, "ticket": p})
}

// paymentWebhook: POST /v1/payments/webhook, signed by the gateway.
func (s *Server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_body", "Unreadable body")
		return
	}
	if err := s.Booking.HandleWebhook(r.Context(), s.Payments, body, r.Header.Get("X-Gateway-Signature")); err != nil {
		bookingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) devExpireHold(w http.ResponseWriter, r *http.Request) {
	if err := s.Booking.ExpireNow(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Saved passengers ───────────────────────────────────────────────────────

type savedPassenger struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Age       int    `json:"age"`
	Gender    string `json:"gender"`
	BerthPref string `json:"berth_pref"`
}

func (s *Server) listSavedPassengers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id, name, age, gender, berth_pref FROM master_passengers WHERE user_id = $1 ORDER BY created_at`, userID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (savedPassenger, error) {
		var p savedPassenger
		return p, row.Scan(&p.ID, &p.Name, &p.Age, &p.Gender, &p.BerthPref)
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) addSavedPassenger(w http.ResponseWriter, r *http.Request) {
	var in savedPassenger
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.Name, in.Gender, in.BerthPref = strings.TrimSpace(in.Name), strings.ToUpper(in.Gender), strings.ToUpper(in.BerthPref)
	if len(in.Name) < 2 || len(in.Name) > 40 || in.Age < 5 || in.Age > 125 || (in.Gender != "M" && in.Gender != "F" && in.Gender != "O") {
		httpx.Error(w, http.StatusBadRequest, "invalid", "Name 2–40 characters, age 5–125, gender M/F/O")
		return
	}
	var count int
	s.DB.QueryRow(r.Context(), `SELECT count(*) FROM master_passengers WHERE user_id = $1`, userID(r)).Scan(&count)
	if count >= 20 {
		httpx.Error(w, http.StatusBadRequest, "too_many", "You can save up to 20 passengers")
		return
	}
	err := s.DB.QueryRow(r.Context(), `INSERT INTO master_passengers (user_id, name, age, gender, berth_pref) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		userID(r), in.Name, in.Age, in.Gender, in.BerthPref).Scan(&in.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, in)
}

func (s *Server) deleteSavedPassenger(w http.ResponseWriter, r *http.Request) {
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM master_passengers WHERE id = $1 AND user_id = $2`, chi.URLParam(r, "id"), userID(r)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

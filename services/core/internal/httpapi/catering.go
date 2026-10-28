package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/catering"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

func cateringError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, catering.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, catering.ErrNotAllowed):
		httpx.Error(w, http.StatusConflict, "not_allowed", strings.TrimPrefix(err.Error(), catering.ErrNotAllowed.Error()+": "))
	default:
		httpx.Internal(w, r, err)
	}
}

// ── Passengers ─────────────────────────────────────────────────────────────

func (s *Server) foodOptions(w http.ResponseWriter, r *http.Request) {
	out, err := s.Catering.Options(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) vendorMenu(w http.ResponseWriter, r *http.Request) {
	out, err := s.Catering.Menu(r.Context(), chi.URLParam(r, "id"), false)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) placeFoodOrder(w http.ResponseWriter, r *http.Request) {
	var in catering.OrderRequest
	if !httpx.Decode(w, r, &in) {
		return
	}
	in.Station = strings.ToUpper(in.Station)
	o, err := s.Catering.Place(r.Context(), userID(r), in)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, o)
}

func (s *Server) myFoodOrders(w http.ResponseWriter, r *http.Request) {
	out, err := s.Catering.MyOrders(r.Context(), userID(r), r.URL.Query().Get("booking_id"))
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) cancelFoodOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.Catering.Cancel(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func (s *Server) rateFoodOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Stars   int    `json:"stars"`
		Review  string `json:"review"`
		Hygiene bool   `json:"hygiene_complaint"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	o, err := s.Catering.Rate(r.Context(), userID(r), chi.URLParam(r, "id"), in.Stars, in.Review, in.Hygiene)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

// ── Vendors ────────────────────────────────────────────────────────────────

func (s *Server) myVendor(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := s.Catering.VendorFor(r.Context(), userID(r))
	if err != nil {
		cateringError(w, r, err)
		return "", false
	}
	return id, true
}

func (s *Server) vendorOrders(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	out, err := s.Catering.VendorOrders(r.Context(), id, r.URL.Query().Get("closed") != "1")
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) advanceFoodOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	o, err := s.Catering.Advance(r.Context(), id, chi.URLParam(r, "id"), in.Status, in.Note)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func (s *Server) myMenu(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	out, err := s.Catering.Menu(r.Context(), id, true)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) addMenuItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	var in catering.MenuEdit
	if !httpx.Decode(w, r, &in) {
		return
	}
	item, err := s.Catering.AddItem(r.Context(), id, in)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]int64{"id": item})
}

func (s *Server) editMenuItem(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	var in catering.MenuEdit
	if !httpx.Decode(w, r, &in) {
		return
	}
	item, _ := strconv.ParseInt(chi.URLParam(r, "item"), 10, 64)
	if err := s.Catering.EditItem(r.Context(), id, item, in); err != nil {
		cateringError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vendorPayouts(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	p, err := s.Catering.Payouts(r.Context(), id)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (s *Server) requestPayout(w http.ResponseWriter, r *http.Request) {
	id, ok := s.myVendor(w, r)
	if !ok {
		return
	}
	p, err := s.Catering.Payout(r.Context(), id)
	if err != nil {
		cateringError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

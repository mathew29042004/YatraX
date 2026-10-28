package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/layout"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/search"
)

// maxDaysAhead lets search look past the booking window so the UI can say when booking opens.
const maxDaysAhead = 120

// parseTravelDate reads ?date=YYYY-MM-DD (default today) and checks it is not in the past.
func (s *Server) parseTravelDate(w http.ResponseWriter, r *http.Request, key string) (time.Time, bool) {
	today := availability.Midnight(s.Now())
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return today, true
	}
	d, err := time.ParseInLocation(time.DateOnly, raw, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "Dates look like 2026-11-02")
		return time.Time{}, false
	}
	if d.Before(today) {
		httpx.Error(w, http.StatusBadRequest, "past_date", "That date has already gone")
		return time.Time{}, false
	}
	if d.After(today.AddDate(0, 0, maxDaysAhead)) {
		httpx.Error(w, http.StatusBadRequest, "far_date", "Pick a date within the next four months")
		return time.Time{}, false
	}
	return d, true
}

func quotaParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	q := strings.ToUpper(r.URL.Query().Get("quota"))
	if q == "" {
		q = "GN"
	}
	if _, ok := fare.Quotas[q]; !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_quota", "Unknown quota "+q)
		return "", false
	}
	return q, true
}

func truthy(v string) bool { return v == "1" || v == "true" || v == "yes" }

// search: GET /v1/search?from=MAS&to=ERS&date=2026-11-02&quota=GN&class=SL&nearby=1&flex=3
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	date, ok := s.parseTravelDate(w, r, "date")
	if !ok {
		return
	}
	quota, ok := quotaParam(w, r)
	if !ok {
		return
	}
	class := strings.ToUpper(qs.Get("class"))
	if class != "" {
		if cc := s.Net.Classes[class]; cc == nil || !cc.Bookable {
			httpx.Error(w, http.StatusBadRequest, "bad_class", "Unknown class "+class)
			return
		}
	}
	flex, _ := strconv.Atoi(qs.Get("flex"))
	res, err := s.Search.Search(r.Context(), search.Query{
		From: strings.ToUpper(qs.Get("from")), To: strings.ToUpper(qs.Get("to")),
		Date: date, Quota: quota, Class: class, Nearby: truthy(qs.Get("nearby")), FlexDays: flex,
	}, s.Now())
	switch {
	case errors.Is(err, search.ErrUnknownStation):
		httpx.Error(w, http.StatusBadRequest, "bad_station", "Pick a station from the list")
	case errors.Is(err, search.ErrSameStation):
		httpx.Error(w, http.StatusBadRequest, "same_station", "From and To must be different stations")
	case err != nil:
		httpx.Internal(w, r, err)
	default:
		httpx.JSON(w, http.StatusOK, res)
	}
}

// journey resolves ?train=&from=&to= into a train and stop indexes.
func (s *Server) journey(w http.ResponseWriter, r *http.Request) (*network.Train, int, int, bool) {
	qs := r.URL.Query()
	t := s.Net.Trains[qs.Get("train")]
	if t == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such train")
		return nil, 0, 0, false
	}
	from, to := t.StopIndex(strings.ToUpper(qs.Get("from"))), t.StopIndex(strings.ToUpper(qs.Get("to")))
	if from < 0 || to < 0 || from >= to {
		httpx.Error(w, http.StatusBadRequest, "bad_journey", "This train does not run between those stations in that order")
		return nil, 0, 0, false
	}
	return t, from, to, true
}

func (s *Server) classParam(w http.ResponseWriter, r *http.Request, t *network.Train) (string, bool) {
	class := strings.ToUpper(r.URL.Query().Get("class"))
	if t.Capacity(s.Net, class) == 0 || !s.Net.Classes[class].Bookable {
		httpx.Error(w, http.StatusBadRequest, "bad_class", "This train has no "+class+" class")
		return "", false
	}
	return class, true
}

// calendar: GET /v1/availability?train=12623&from=MAS&to=ERS&class=SL&quota=GN&start=2026-11-01&days=30
func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	t, from, to, ok := s.journey(w, r)
	if !ok {
		return
	}
	class, ok := s.classParam(w, r, t)
	if !ok {
		return
	}
	quota, ok := quotaParam(w, r)
	if !ok {
		return
	}
	start, ok := s.parseTravelDate(w, r, "start")
	if !ok {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	days = min(max(days, 1), 62)
	quote, err := fare.Compute(s.Net, t.Type, class, t.Stops[to].DistKm-t.Stops[from].DistKm, quota)
	if err != nil && !errors.Is(err, fare.ErrNoTatkal) {
		httpx.Internal(w, r, err)
		return
	}
	resp := map[string]any{
		"train": t.Number, "from": t.Stops[from].Station, "to": t.Stops[to].Station, "class": class, "quota": quota,
		"days": s.Search.Calendar(r.Context(), t, from, to, class, quota, start, days, s.Now()),
	}
	if err == nil {
		resp["fare"] = quote
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// fareQuote: GET /v1/fare?train=12623&from=MAS&to=ERS&class=3A&quota=GN
func (s *Server) fareQuote(w http.ResponseWriter, r *http.Request) {
	t, from, to, ok := s.journey(w, r)
	if !ok {
		return
	}
	class, ok := s.classParam(w, r, t)
	if !ok {
		return
	}
	quota, ok := quotaParam(w, r)
	if !ok {
		return
	}
	q, err := fare.Compute(s.Net, t.Type, class, t.Stops[to].DistKm-t.Stops[from].DistKm, quota)
	if errors.Is(err, fare.ErrNoTatkal) {
		httpx.Error(w, http.StatusBadRequest, "no_tatkal", err.Error())
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, q)
}

// coach: GET /v1/trains/{number}/coaches/{coach} — one coach with its numbered berths.
func (s *Server) coach(w http.ResponseWriter, r *http.Request) {
	t := s.Net.Trains[chi.URLParam(r, "number")]
	if t == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such train")
		return
	}
	no := strings.ToUpper(chi.URLParam(r, "coach"))
	for _, c := range t.Coaches {
		if c.No != no {
			continue
		}
		cc := s.Net.Classes[c.Class]
		seats, err := layout.Seats(cc.Layout, cc.Seats)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if !cc.Bookable {
			seats = []layout.Seat{}
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"train": t.Number, "coach_no": c.No, "class": c.Class, "class_name": cc.Name, "position": c.Position,
			"bookable": cc.Bookable, "layout": cc.Layout, "seats": seats,
		})
		return
	}
	httpx.Error(w, http.StatusNotFound, "not_found", "No coach "+no+" on this train")
}

func (s *Server) festivals(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, availability.Festivals)
}

func (s *Server) quotas(w http.ResponseWriter, r *http.Request) {
	type quota struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	out := []quota{}
	for _, c := range availability.Quotas {
		out = append(out, quota{c, fare.Quotas[c]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"quotas": out, "advance_days": availability.AdvanceDays, "today": availability.Midnight(s.Now()).Format(time.DateOnly),
	})
}

// alternatives: GET /v1/search/alternatives?from=&to=&date=&class= — split journeys and connections.
func (s *Server) alternatives(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	date, ok := s.parseTravelDate(w, r, "date")
	if !ok {
		return
	}
	class := strings.ToUpper(qs.Get("class"))
	if class != "" {
		if cc := s.Net.Classes[class]; cc == nil || !cc.Bookable {
			httpx.Error(w, http.StatusBadRequest, "bad_class", "Unknown class "+class)
			return
		}
	}
	out, err := s.Search.Alternatives(r.Context(), search.Query{From: strings.ToUpper(qs.Get("from")), To: strings.ToUpper(qs.Get("to")),
		Date: date, Quota: "GN", Class: class}, s.Now())
	if errors.Is(err, search.ErrUnknownStation) {
		httpx.Error(w, http.StatusBadRequest, "bad_station", "Pick a station from the list")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

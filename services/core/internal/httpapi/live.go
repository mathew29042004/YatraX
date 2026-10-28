package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/simclock"
)

type clockView struct {
	SimNow  time.Time `json:"sim_now"`
	RealNow time.Time `json:"real_now"`
	Speed   float64   `json:"speed"`
	Paused  bool      `json:"paused"`
}

func (s *Server) clockView() clockView {
	st := s.Clock.State()
	return clockView{SimNow: s.Clock.Now(), RealNow: time.Now(), Speed: st.Speed, Paused: st.Paused}
}

func (s *Server) getSimClock(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, s.clockView())
}

func (s *Server) setSimClock(w http.ResponseWriter, r *http.Request) {
	var in simclock.Change
	if !httpx.Decode(w, r, &in) {
		return
	}
	if _, err := s.Clock.Set(r.Context(), in, userID(r)); err != nil {
		if errors.Is(err, simclock.ErrSpeed) {
			httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s.clockView())
}

// runFor resolves a train number and optional origin date (default: the run that matters now).
func (s *Server) runFor(w http.ResponseWriter, number, date string) (*network.Train, time.Time, bool) {
	t := s.Net.Trains[number]
	if t == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "No train "+number)
		return nil, time.Time{}, false
	}
	if date == "" {
		return t, s.Live.CurrentRun(t, s.Clock.Now()), true
	}
	d, err := time.ParseInLocation(time.DateOnly, date, network.IST)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_date", "date looks like 2026-11-02 (the day the train leaves its first station)")
		return nil, time.Time{}, false
	}
	return t, d, true
}

func (s *Server) liveTrain(w http.ResponseWriter, r *http.Request) {
	t, origin, ok := s.runFor(w, chi.URLParam(r, "number"), r.URL.Query().Get("date"))
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, s.Live.Status(t, origin, s.Clock.Now()))
}

func filterMap(list []live.Brief, zone string, lateOnly bool) []live.Brief {
	out := list[:0]
	for _, b := range list {
		if (zone == "" || b.Zone == zone) && (!lateOnly || b.DelayMin >= 15) {
			out = append(out, b)
		}
	}
	return out
}

func (s *Server) liveMap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list := filterMap(s.Live.Running(s.Clock.Now()), q.Get("zone"), q.Get("late") == "1")
	httpx.JSON(w, http.StatusOK, map[string]any{"sim_now": s.Clock.Now(), "trains": list})
}

func (s *Server) liveStation(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(chi.URLParam(r, "code"))
	st := s.Net.Stations[code]
	if st == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "No station "+code)
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24 {
		hours = 4
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"station": st, "sim_now": s.Clock.Now(), "trains": s.Live.Board(code, s.Clock.Now(), time.Duration(hours)*time.Hour),
	})
}

func (s *Server) addIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Train      string `json:"train"`
		OriginDate string `json:"origin_date"`
		Station    string `json:"station"`
		DelayMin   int    `json:"delay_min"`
		Reason     string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	t, origin, ok := s.runFor(w, in.Train, in.OriginDate)
	if !ok {
		return
	}
	seq := t.StopIndex(strings.ToUpper(in.Station))
	if seq < 0 || seq == len(t.Stops)-1 {
		httpx.Error(w, http.StatusBadRequest, "bad_station", t.Number+" doesn't depart from "+in.Station)
		return
	}
	if in.DelayMin < 1 || in.DelayMin > 1440 {
		httpx.Error(w, http.StatusBadRequest, "invalid", "delay_min is 1 to 1440")
		return
	}
	if v := s.Live.Status(t, origin, s.Clock.Now()).Stops[seq]; v.ExpDep != nil && v.ExpDep.Before(s.Clock.Now()) {
		httpx.Error(w, http.StatusBadRequest, "already_left", t.Number+" has already left "+v.Name)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = "Held by control"
	}
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO live_incidents (train_number, origin_date, stop_seq, delay_min, reason, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.Number, origin, seq, in.DelayMin, in.Reason, userID(r)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := s.Live.LoadIncidents(r.Context()); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, s.Live.Status(t, origin, s.Clock.Now()))
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT i.id, i.train_number, i.origin_date, i.stop_seq, i.delay_min, i.reason,
		coalesce(u.full_name, ''), i.created_at FROM live_incidents i LEFT JOIN users u ON u.id = i.created_by
		ORDER BY i.id DESC LIMIT 50`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()
	type incident struct {
		ID         int64     `json:"id"`
		Train      string    `json:"train"`
		OriginDate string    `json:"origin_date"`
		Station    string    `json:"station"`
		DelayMin   int       `json:"delay_min"`
		Reason     string    `json:"reason"`
		By         string    `json:"by"`
		At         time.Time `json:"at"`
	}
	out := []incident{}
	for rows.Next() {
		var x incident
		var d time.Time
		var seq int
		if err := rows.Scan(&x.ID, &x.Train, &d, &seq, &x.DelayMin, &x.Reason, &x.By, &x.At); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		x.OriginDate = d.Format(time.DateOnly)
		if t := s.Net.Trains[x.Train]; t != nil && seq < len(t.Stops) {
			x.Station = t.Stops[seq].Station
		}
		out = append(out, x)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// liveSocket streams live status. ?train=12623[&date=…] follows one run (every second,
// plus its events as they happen); ?map=1[&zone=SR][&late=1] streams the live map every
// two seconds. Live status is public, like the railway's own running-status pages.
func (s *Server) liveSocket(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var train *network.Train
	var origin time.Time
	if !(q.Get("map") == "1") {
		var ok bool
		if train, origin, ok = s.runFor(w, q.Get("train"), q.Get("date")); !ok {
			return
		}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.wsOrigins()})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())

	every := time.Second
	if train == nil {
		every = 2 * time.Second
	}
	events := make(chan []byte, 16)
	if train != nil {
		sub := s.Redis.Subscribe(ctx, live.EventsChannel)
		defer sub.Close()
		date := origin.Format(time.DateOnly)
		go func() {
			for m := range sub.Channel() {
				var ev map[string]any
				if json.Unmarshal([]byte(m.Payload), &ev) == nil && ev["train"] == train.Number && ev["origin_date"] == date {
					select {
					case events <- []byte(m.Payload):
					default:
					}
				}
			}
		}()
	}
	send := func(v any) bool {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return wsjson.Write(wctx, c, v) == nil
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var msg any
		if train != nil {
			msg = map[string]any{"kind": "status", "clock": s.clockView(), "status": s.Live.Status(train, origin, s.Clock.Now())}
		} else {
			msg = map[string]any{"kind": "map", "clock": s.clockView(),
				"trains": filterMap(s.Live.Running(s.Clock.Now()), q.Get("zone"), q.Get("late") == "1")}
		}
		if !send(msg) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case b := <-events:
			if !send(map[string]any{"kind": "event", "event": json.RawMessage(b)}) {
				return
			}
		case <-t.C:
		}
	}
}

// stationsGeo is every station as [code, lat, lon, importance]: enough to draw the network
// behind the live map.
func (s *Server) stationsGeo(w http.ResponseWriter, r *http.Request) {
	out := make([][4]any, 0, len(s.Net.Stations))
	for _, st := range s.Net.Stations {
		out = append(out, [4]any{st.Code, st.Lat, st.Lon, st.Importance})
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httpx.JSON(w, http.StatusOK, out)
}

package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/waitroom"
)

func waitroomError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, waitroom.ErrNoRoom):
		httpx.Error(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, waitroom.ErrNotAdmitted), errors.Is(err, waitroom.ErrNotJoined), errors.Is(err, waitroom.ErrNoDraft),
		errors.Is(err, waitroom.ErrNotYet), errors.Is(err, waitroom.ErrClosed), errors.Is(err, waitroom.ErrChallenge),
		errors.Is(err, waitroom.ErrWrongJourney):
		httpx.Error(w, http.StatusConflict, "not_now", err.Error())
	case errors.Is(err, waitroom.ErrBlocked):
		httpx.Error(w, http.StatusForbidden, "blocked", err.Error())
	case errors.Is(err, waitroom.ErrBadSolution):
		httpx.Error(w, http.StatusBadRequest, "bad_solution", err.Error())
	case errors.Is(err, booking.ErrTatkalSoldOut), errors.Is(err, booking.ErrTatkalLimit):
		httpx.Error(w, http.StatusConflict, "tatkal", err.Error())
	default:
		phase3Error(w, r, err)
	}
}

// signals collects the server-side bot signals for a request.
func signals(r *http.Request, sig waitroom.Signals) waitroom.Signals {
	ip := r.RemoteAddr // chi's RealIP middleware has already applied X-Forwarded-For / X-Real-IP
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	sig.IP = ip
	sig.UserAgent = r.UserAgent()
	if sig.Device == "" {
		sig.Device = r.Header.Get("X-Device-Id")
	}
	return sig
}

func (s *Server) listWaitrooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.Rooms.List(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rooms)
}

func (s *Server) joinWaitroom(w http.ResponseWriter, r *http.Request) {
	var in waitroom.Signals
	if r.ContentLength > 0 && !httpx.Decode(w, r, &in) {
		return
	}
	st, err := s.Rooms.Join(r.Context(), chi.URLParam(r, "id"), userID(r), signals(r, in))
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		booking.HoldRequest
		Signals waitroom.Signals `json:"signals"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	st, err := s.Rooms.SaveDraft(r.Context(), chi.URLParam(r, "id"), userID(r), in.HoldRequest, signals(r, in.Signals))
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (s *Server) solveChallenge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Nonce string `json:"nonce"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	room, user := chi.URLParam(r, "id"), userID(r)
	if err := s.Rooms.Detector.Solve(r.Context(), room, user, in.Nonce); err != nil {
		waitroomError(w, r, err)
		return
	}
	st, err := s.Rooms.Status(r.Context(), room, user)
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (s *Server) waitroomStatus(w http.ResponseWriter, r *http.Request) {
	room, user := chi.URLParam(r, "id"), userID(r)
	if n := s.Rooms.Detector.NotePoll(r.Context(), room, user); n > 50 {
		if rm, err := s.Rooms.Get(r.Context(), room); err == nil {
			s.Rooms.Detector.Assess(r.Context(), rm, user, signals(r, waitroom.Signals{PollsPer10s: n}))
		}
	}
	st, err := s.Rooms.Status(r.Context(), room, user)
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

func (s *Server) bookFromWaitroom(w http.ResponseWriter, r *http.Request) {
	b, err := s.Rooms.Book(r.Context(), chi.URLParam(r, "id"), userID(r))
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, b)
}

// waitroomSocket streams the person's position once a second. Browsers can't set
// headers on a WebSocket, so the access token comes in the query string.
func (s *Server) waitroomSocket(w http.ResponseWriter, r *http.Request) {
	claims, err := s.Signer.Parse(r.URL.Query().Get("access_token"))
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "bad_token", "Sign in required")
		return
	}
	room := chi.URLParam(r, "id")
	if _, err := s.Rooms.Get(r.Context(), room); err != nil {
		waitroomError(w, r, err)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.wsOrigins()})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context()) // we only send; this notices the client leaving
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		st, err := s.Rooms.Status(ctx, room, claims.Subject)
		if err != nil {
			c.Close(websocket.StatusInternalError, "status unavailable")
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = wsjson.Write(wctx, c, st)
		cancel()
		if err != nil || st.State == "closed" {
			c.Close(websocket.StatusNormalClosure, "")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// wsOrigins allows the same browser origins as CORS, as host[:port] patterns.
func (s *Server) wsOrigins() []string {
	out := make([]string, 0, len(s.CORSOrigins))
	for _, o := range s.CORSOrigins {
		if u, err := urlHost(o); err == nil {
			out = append(out, u)
		}
	}
	return out
}

func urlHost(origin string) (string, error) {
	for _, p := range []string{"https://", "http://"} {
		if len(origin) > len(p) && origin[:len(p)] == p {
			return origin[len(p):], nil
		}
	}
	return "", errors.New("not a URL")
}

// ── Admin ─────────────────────────────────────────────────────────────────

func (s *Server) createDrill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JourneyDate string `json:"journey_date"`
		AC          bool   `json:"ac"`
		OpensInSec  int    `json:"opens_in_sec"`
		AdmitRate   int    `json:"admit_rate"`
		RushMinutes int    `json:"rush_minutes"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.OpensInSec <= 0 {
		in.OpensInSec = 60
	}
	room, err := s.Rooms.CreateDrill(r.Context(), userID(r), in.JourneyDate, in.AC,
		time.Duration(in.OpensInSec)*time.Second, in.AdmitRate, time.Duration(in.RushMinutes)*time.Minute)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, room)
}

func (s *Server) monitorWaitroom(w http.ResponseWriter, r *http.Request) {
	m, err := s.Rooms.Monitor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		waitroomError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, m)
}

func (s *Server) waitroomBots(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT f.user_id, u.full_name, f.score, f.action, f.reasons, f.ip, f.device, f.created_at
		FROM bot_flags f JOIN users u ON u.id = f.user_id WHERE f.room_id = $1 AND f.action <> 'allow'
		ORDER BY f.score DESC LIMIT $2`, chi.URLParam(r, "id"), limit(r, 50, 500))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()
	type flag struct {
		UserID  string    `json:"user_id"`
		Name    string    `json:"name"`
		Score   int       `json:"score"`
		Action  string    `json:"action"`
		Reasons []string  `json:"reasons"`
		IP      string    `json:"ip"`
		Device  string    `json:"device"`
		At      time.Time `json:"at"`
	}
	out := []flag{}
	for rows.Next() {
		var f flag
		if err := rows.Scan(&f.UserID, &f.Name, &f.Score, &f.Action, &f.Reasons, &f.IP, &f.Device, &f.At); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out = append(out, f)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Package waitroom runs the Tatkal waiting room.
//
// Tatkal opens at 10:00 (AC) and 11:00 (non-AC) for the next day's trains, and far
// more people want berths than exist. The room makes that fair:
//
//   - People join from five minutes before opening and fill in their booking while
//     they wait (the "pre-filled booking"), so booking itself takes a moment.
//   - At the opening second everyone already in the room is shuffled with a
//     cryptographic shuffle and given a random position: arriving early or
//     refreshing gives no advantage. Later arrivals queue behind, in arrival order.
//   - A single leader admits people at a fixed rate the booking core can serve fast.
//   - A bot detector scores each person; suspects are challenged or blocked.
//
// Live state is in Redis (one instance leads admission via a Redis lock); rooms
// and bot flags are recorded in Postgres.
package waitroom

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

const (
	JoinEarly   = 5 * time.Minute  // people may join this long before opening
	RushWindow  = 30 * time.Minute // Tatkal books only through the room for this long after opening
	DefaultRate = 150              // people admitted per second
	tick        = 100 * time.Millisecond
	keyTTL      = 6 * time.Hour
)

var (
	ErrNoRoom       = errors.New("no such waiting room")
	ErrNotYet       = errors.New("the waiting room is not open for joining yet")
	ErrClosed       = errors.New("this waiting room has closed; Tatkal is booking normally now")
	ErrNotJoined    = errors.New("join the waiting room first")
	ErrNotAdmitted  = errors.New("it isn't your turn yet")
	ErrBlocked      = errors.New("this account was blocked by bot protection")
	ErrChallenge    = errors.New("complete the verification first")
	ErrNoDraft      = errors.New("fill in your booking details first")
	ErrWrongJourney = errors.New("this room is for a different journey date or class type")
)

type Room struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	JourneyDate string    `json:"journey_date"`
	AC          bool      `json:"ac"`
	JoinsOpen   time.Time `json:"joins_open"`
	OpensAt     time.Time `json:"opens_at"`
	ClosesAt    time.Time `json:"closes_at"`
	AdmitRate   int       `json:"admit_rate"`
	State       string    `json:"state"` // upcoming, joining, open, closed
	Queue       int64     `json:"queue"`
	Admitted    int64     `json:"admitted"`
}

type Service struct {
	DB       *pgxpool.Pool
	Redis    *redis.Client
	Booking  *booking.Service
	Net      *network.Network
	Now      func() time.Time
	Detector *Detector
	instance string

	cacheMu sync.Mutex
	cache   map[string]cachedRoom // room records change rarely; status is polled constantly
}

type cachedRoom struct {
	room Room
	at   time.Time
}

func New(db *pgxpool.Pool, rdb *redis.Client, b *booking.Service, net *network.Network, now func() time.Time) *Service {
	buf := make([]byte, 6)
	rand.Read(buf)
	s := &Service{DB: db, Redis: rdb, Booking: b, Net: net, Now: now, instance: fmt.Sprintf("%x", buf), cache: map[string]cachedRoom{}}
	s.Detector = &Detector{Redis: rdb, DB: db}
	// Normal booking refuses Tatkal during a room's rush; the room is the way in.
	b.TatkalRush = s.inRush
	return s
}

func key(room, part string) string { return "wr:" + room + ":" + part }

func (s *Service) state(r *Room, now time.Time) string {
	switch {
	case now.Before(r.JoinsOpen):
		return "upcoming"
	case now.Before(r.OpensAt):
		return "joining"
	case now.Before(r.ClosesAt):
		return "open"
	}
	return "closed"
}

const roomCols = `id, kind, journey_date, ac, joins_open, opens_at, closes_at, admit_rate`

func scanRoom(row pgx.Row) (*Room, error) {
	var r Room
	var jd time.Time
	err := row.Scan(&r.ID, &r.Kind, &jd, &r.AC, &r.JoinsOpen, &r.OpensAt, &r.ClosesAt, &r.AdmitRate)
	r.JourneyDate = jd.Format(time.DateOnly)
	return &r, err
}

func (s *Service) Get(ctx context.Context, id string) (*Room, error) {
	s.cacheMu.Lock()
	c, ok := s.cache[id]
	s.cacheMu.Unlock()
	var r *Room
	if ok && time.Since(c.at) < time.Second {
		copy := c.room
		r = &copy
	} else {
		var err error
		r, err = scanRoom(s.DB.QueryRow(ctx, `SELECT `+roomCols+` FROM waitrooms WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoRoom
		} else if err != nil {
			return nil, err
		}
		s.cacheMu.Lock()
		s.cache[id] = cachedRoom{*r, time.Now()}
		s.cacheMu.Unlock()
	}
	s.decorate(ctx, r)
	return r, nil
}

func (s *Service) decorate(ctx context.Context, r *Room) {
	r.State = s.state(r, s.Now())
	r.Queue, _ = s.Redis.ZCard(ctx, key(r.ID, "queue")).Result()
	if r.State == "joining" {
		r.Queue, _ = s.Redis.ZCard(ctx, key(r.ID, "joined")).Result()
	}
	r.Admitted, _ = s.Redis.Get(ctx, key(r.ID, "head")).Int64()
	r.Admitted = min(r.Admitted, r.Queue)
}

// List returns rooms that are upcoming, open, or closed within the last few hours.
func (s *Service) List(ctx context.Context) ([]*Room, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+roomCols+` FROM waitrooms WHERE closes_at > $1 ORDER BY opens_at LIMIT 20`,
		s.Now().Add(-3*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Room{}
	for rows.Next() {
		r, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	for _, r := range out {
		s.decorate(ctx, r)
	}
	return out, rows.Err()
}

func (s *Service) create(ctx context.Context, r Room, createdBy *string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO waitrooms (id, kind, journey_date, ac, joins_open, opens_at, closes_at, admit_rate, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
		r.ID, r.Kind, r.JourneyDate, r.AC, r.JoinsOpen, r.OpensAt, r.ClosesAt, r.AdmitRate, createdBy)
	return err
}

// CreateDrill makes a room that opens in `in` from now, for demos and load tests.
func (s *Service) CreateDrill(ctx context.Context, adminID, journeyDate string, ac bool, in time.Duration, rate int, rush time.Duration) (*Room, error) {
	if _, err := time.ParseInLocation(time.DateOnly, journeyDate, network.IST); err != nil {
		return nil, fmt.Errorf("journey_date looks like 2026-11-02")
	}
	if rate <= 0 {
		rate = DefaultRate
	}
	if rush <= 0 {
		rush = 10 * time.Minute
	}
	now := s.Now()
	opens := now.Add(in)
	kind := "SL"
	if ac {
		kind = "AC"
	}
	r := Room{ID: fmt.Sprintf("drill-%s-%s-%d", journeyDate, kind, now.Unix()), Kind: "drill", JourneyDate: journeyDate, AC: ac,
		JoinsOpen: now, OpensAt: opens, ClosesAt: opens.Add(rush), AdmitRate: min(rate, 5000)}
	if err := s.create(ctx, r, &adminID); err != nil {
		return nil, err
	}
	return s.Get(ctx, r.ID)
}

// ensureDailyRooms creates the real rooms that open today and tomorrow, each for the next
// day's journeys: AC at 10:00, non-AC at 11:00. Creating tomorrow's too means the next
// opening is always listed, even after today's rooms have closed.
func (s *Service) ensureDailyRooms(ctx context.Context) {
	now := s.Now().In(network.IST)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, network.IST)
	for d := 0; d <= 1; d++ {
		day := today.AddDate(0, 0, d)
		journey := day.AddDate(0, 0, 1).Format(time.DateOnly)
		for _, ac := range []bool{true, false} {
			hour, kind := 11, "SL"
			if ac {
				hour, kind = 10, "AC"
			}
			opens := day.Add(time.Duration(hour) * time.Hour)
			r := Room{ID: journey + "-" + kind, Kind: "real", JourneyDate: journey, AC: ac, JoinsOpen: opens.Add(-JoinEarly),
				OpensAt: opens, ClosesAt: opens.Add(RushWindow), AdmitRate: DefaultRate}
			if err := s.create(ctx, r, nil); err != nil {
				slog.Warn("waitroom: create daily room", "err", err)
			}
		}
	}
}

// inRush reports whether Tatkal for this origin date and class type is in a room's
// rush window, during which it books only through the room.
func (s *Service) inRush(originDate time.Time, ac bool) bool {
	var n int
	now := s.Now()
	s.DB.QueryRow(context.Background(), `SELECT count(*) FROM waitrooms WHERE journey_date = $1 AND ac = $2
		AND opens_at <= $3 AND closes_at > $3`, originDate.Format(time.DateOnly), ac, now).Scan(&n)
	return n > 0
}

// ── Joining and the queue ─────────────────────────────────────────────────

type Status struct {
	Room      string     `json:"room"`
	State     string     `json:"state"`
	Joined    bool       `json:"joined"`
	Position  int64      `json:"position"` // people ahead of you (0 = your turn)
	QueueSize int64      `json:"queue_size"`
	Admitted  bool       `json:"admitted"`
	ETA       int64      `json:"eta_seconds"`
	OpensIn   int64      `json:"opens_in_seconds"`
	HasDraft  bool       `json:"has_draft"`
	Action    string     `json:"action"` // allow, challenge, block
	Challenge *Challenge `json:"challenge,omitempty"`
	Booking   string     `json:"booking_id,omitempty"`
	Outcome   string     `json:"outcome,omitempty"`
}

// Join puts a person in the room (before opening) or at the back of the queue (after).
func (s *Service) Join(ctx context.Context, roomID, userID string, sig Signals) (*Status, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	switch r.State {
	case "upcoming":
		return nil, ErrNotYet
	case "closed":
		return nil, ErrClosed
	case "joining":
		if err := s.Redis.ZAddNX(ctx, key(roomID, "joined"), redis.Z{Score: float64(now.UnixMilli()), Member: userID}).Err(); err != nil {
			return nil, err
		}
		s.Redis.Expire(ctx, key(roomID, "joined"), keyTTL)
	case "open":
		if err := s.enqueueLate(ctx, roomID, userID); err != nil {
			return nil, err
		}
	}
	s.Redis.HIncrBy(ctx, key(roomID, "stats"), "joins", 1)
	metrics.WaitroomJoins.Inc()
	sig.JoinedAt = now
	if _, err := s.Detector.Assess(ctx, r, userID, sig); err != nil {
		return nil, err
	}
	return s.Status(ctx, roomID, userID)
}

// enqueueLate gives someone who arrives after opening the next position at the back.
// It runs as one Redis script: two simultaneous joins by the same person must not
// both take a number, or the spare one leaves a gap the admission counter never passes.
var enqueueScript = redis.NewScript(`
if redis.call('ZSCORE', KEYS[1], ARGV[1]) then return 0 end
local rank = redis.call('INCR', KEYS[2])
redis.call('ZADD', KEYS[1], rank - 1, ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return rank`)

func (s *Service) enqueueLate(ctx context.Context, roomID, userID string) error {
	return enqueueScript.Run(ctx, s.Redis, []string{key(roomID, "queue"), key(roomID, "next")}, userID, int(keyTTL.Seconds())).Err()
}

func (s *Service) Status(ctx context.Context, roomID, userID string) (*Status, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	st := &Status{Room: roomID, State: r.State, QueueSize: r.Queue, OpensIn: max(0, int64(r.OpensAt.Sub(now).Seconds()))}
	if r.State == "joining" {
		_, err := s.Redis.ZScore(ctx, key(roomID, "joined"), userID).Result()
		st.Joined = err == nil
	} else {
		rank, err := s.Redis.ZScore(ctx, key(roomID, "queue"), userID).Result()
		if err == nil {
			st.Joined = true
			ahead := int64(rank) - r.Admitted
			st.Position = max(0, ahead)
			st.Admitted = ahead < 0
			if !st.Admitted && r.AdmitRate > 0 {
				st.ETA = (ahead + 1) / int64(r.AdmitRate)
			}
		}
	}
	exists, _ := s.Redis.Exists(ctx, key(roomID, "draft:"+userID)).Result()
	st.HasDraft = exists == 1
	st.Action, st.Challenge = s.Detector.Verdict(ctx, roomID, userID)
	res, _ := s.Redis.HGetAll(ctx, key(roomID, "result:"+userID)).Result()
	st.Booking, st.Outcome = res["booking"], res["outcome"]
	return st, nil
}

// SaveDraft stores the passenger's booking while they wait, after checking it fits the room.
func (s *Service) SaveDraft(ctx context.Context, roomID, userID string, draft booking.HoldRequest, sig Signals) (*Status, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if r.State == "closed" {
		return nil, ErrClosed
	}
	origin, cls, err := s.Booking.JourneyOrigin(draft)
	if err != nil {
		return nil, err
	}
	if origin.Format(time.DateOnly) != r.JourneyDate || s.Net.Classes[cls].AC != r.AC ||
		(strings.ToUpper(draft.Quota) != "TQ" && strings.ToUpper(draft.Quota) != "PT") {
		return nil, ErrWrongJourney
	}
	if len(draft.Passengers) == 0 || len(draft.Passengers) > booking.MaxTatkalPax {
		return nil, fmt.Errorf("a Tatkal booking has 1 to %d passengers", booking.MaxTatkalPax)
	}
	b, _ := json.Marshal(draft)
	if err := s.Redis.Set(ctx, key(roomID, "draft:"+userID), b, keyTTL).Err(); err != nil {
		return nil, err
	}
	s.Booking.WarmTatkal(draft)
	if _, err := s.Detector.Assess(ctx, r, userID, sig); err != nil {
		return nil, err
	}
	return s.Status(ctx, roomID, userID)
}

// Book runs the pre-filled booking for someone whose turn has come.
func (s *Service) Book(ctx context.Context, roomID, userID string) (*booking.Booking, error) {
	st, err := s.Status(ctx, roomID, userID)
	if err != nil {
		return nil, err
	}
	switch {
	case st.Action == "block":
		return nil, ErrBlocked
	case st.Action == "challenge":
		return nil, ErrChallenge
	case !st.Joined:
		return nil, ErrNotJoined
	case !st.Admitted:
		return nil, ErrNotAdmitted
	}
	raw, err := s.Redis.Get(ctx, key(roomID, "draft:"+userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNoDraft
	} else if err != nil {
		return nil, err
	}
	var draft booking.HoldRequest
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, err
	}
	r, _ := s.Get(ctx, roomID)
	b, err := s.Booking.TatkalHold(ctx, userID, "wr:"+roomID+":"+userID, draft, booking.TatkalOptions{RoomOpened: true})
	outcome := "held"
	switch {
	case errors.Is(err, booking.ErrTatkalSoldOut):
		outcome = "sold_out"
		since := strconv.FormatFloat(s.Now().Sub(r.OpensAt).Seconds(), 'f', 1, 64)
		s.Redis.HSetNX(ctx, key(roomID, "soldout"), draft.Train+"/"+strings.ToUpper(draft.Class), since)
	case err != nil:
		outcome = "error"
	}
	s.Redis.HSet(ctx, key(roomID, "result:"+userID), "outcome", outcome)
	s.Redis.Expire(ctx, key(roomID, "result:"+userID), keyTTL)
	s.Redis.HIncrBy(ctx, key(roomID, "stats"), "book_"+outcome, 1)
	metrics.WaitroomBookings.WithLabelValues(outcome).Inc()
	if err != nil {
		return nil, err
	}
	s.Redis.HSet(ctx, key(roomID, "result:"+userID), "booking", b.ID)
	return b, nil
}

// ── Opening and admission ─────────────────────────────────────────────────

// Run leads admission for every open room while this instance holds the lock.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	lastDaily := time.Time{}
	carry := map[string]float64{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Since(lastDaily) > time.Minute {
			s.ensureDailyRooms(ctx)
			lastDaily = time.Now()
		}
		ok, err := s.Redis.SetNX(ctx, "wr:leader", s.instance, 2*time.Second).Result()
		if err != nil {
			continue
		}
		if !ok {
			if v, _ := s.Redis.Get(ctx, "wr:leader").Result(); v != s.instance {
				continue // another instance leads
			}
			s.Redis.Expire(ctx, "wr:leader", 2*time.Second)
		}
		rooms, err := s.List(ctx)
		if err != nil {
			continue
		}
		for _, r := range rooms {
			switch r.State {
			case "open":
				if err := s.openIfNeeded(ctx, r); err != nil {
					slog.Warn("waitroom: open", "room", r.ID, "err", err)
					continue
				}
				carry[r.ID] += float64(r.AdmitRate) * tick.Seconds()
				if n := int64(carry[r.ID]); n > 0 {
					carry[r.ID] -= float64(n)
					s.Redis.IncrBy(ctx, key(r.ID, "head"), n)
					s.Redis.Expire(ctx, key(r.ID, "head"), keyTTL)
				}
				metrics.WaitroomQueue.WithLabelValues(r.ID).Set(float64(max(0, r.Queue-r.Admitted)))
			case "closed":
				metrics.WaitroomQueue.DeleteLabelValues(r.ID)
				delete(carry, r.ID)
			}
		}
	}
}

// openIfNeeded performs the fair shuffle exactly once, at opening.
func (s *Service) openIfNeeded(ctx context.Context, r *Room) error {
	done, err := s.Redis.SetNX(ctx, key(r.ID, "opened"), "1", keyTTL).Result()
	if err != nil || !done {
		return err
	}
	joined, err := s.Redis.ZRange(ctx, key(r.ID, "joined"), 0, -1).Result()
	if err != nil {
		return err
	}
	shuffle(joined)
	pipe := s.Redis.Pipeline()
	for i, u := range joined {
		pipe.ZAdd(ctx, key(r.ID, "queue"), redis.Z{Score: float64(i), Member: u})
	}
	pipe.Set(ctx, key(r.ID, "next"), len(joined), keyTTL)
	pipe.Set(ctx, key(r.ID, "head"), 0, keyTTL)
	pipe.Expire(ctx, key(r.ID, "queue"), keyTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	slog.Info("waitroom opened", "room", r.ID, "shuffled", len(joined))
	return nil
}

// shuffle is Fisher–Yates with crypto/rand, so nobody can predict or game positions.
func shuffle(xs []string) {
	for i := len(xs) - 1; i > 0; i-- {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := int(n.Int64())
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// ── Monitoring ────────────────────────────────────────────────────────────

type Monitor struct {
	Room       *Room              `json:"room"`
	Stats      map[string]int64   `json:"stats"`
	SoldOut    map[string]float64 `json:"sold_out_seconds"` // train/class -> seconds after opening
	Waiting    int64              `json:"waiting"`
	Blocked    int64              `json:"blocked"`
	Challenged int64              `json:"challenged"` // asked to verify and not done yet
	Verified   int64              `json:"verified"`   // challenged, solved it, and let through
	TopReasons map[string]int64   `json:"top_reasons"`
}

func (s *Service) Monitor(ctx context.Context, roomID string) (*Monitor, error) {
	r, err := s.Get(ctx, roomID)
	if err != nil {
		return nil, err
	}
	m := &Monitor{Room: r, Stats: map[string]int64{}, SoldOut: map[string]float64{}, TopReasons: map[string]int64{}}
	raw, _ := s.Redis.HGetAll(ctx, key(roomID, "stats")).Result()
	for k, v := range raw {
		m.Stats[k], _ = strconv.ParseInt(v, 10, 64)
	}
	so, _ := s.Redis.HGetAll(ctx, key(roomID, "soldout")).Result()
	for k, v := range so {
		m.SoldOut[k], _ = strconv.ParseFloat(v, 64)
	}
	m.Waiting = max(0, r.Queue-r.Admitted)
	err = s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action = 'block'), count(*) FILTER (WHERE action = 'challenge'),
		count(*) FILTER (WHERE action = 'allow' AND 'passed verification' = ANY(reasons))
		FROM bot_flags WHERE room_id = $1`, roomID).Scan(&m.Blocked, &m.Challenged, &m.Verified)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT reason, count(*) FROM bot_flags, unnest(reasons) AS reason
		WHERE room_id = $1 AND action <> 'allow' GROUP BY reason ORDER BY 2 DESC LIMIT 8`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		var n int64
		if err := rows.Scan(&reason, &n); err != nil {
			return nil, err
		}
		m.TopReasons[reason] = n
	}
	return m, rows.Err()
}

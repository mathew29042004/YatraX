package waitroom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

// Signals are what the detector learns about a person in the room. The browser
// sends the behavioural ones (how long the form took, keystroke rhythm, pointer use);
// the server adds IP, device fingerprint and request rate.
type Signals struct {
	IP           string    `json:"-"`
	Device       string    `json:"device"`
	UserAgent    string    `json:"-"`
	FormMillis   int       `json:"form_ms"`
	KeyIntervals []int     `json:"key_intervals"`
	PointerMoves int       `json:"pointer_moves"`
	PollsPer10s  int       `json:"-"`
	JoinedAt     time.Time `json:"-"`
}

// Thresholds: score ≥ BlockAt is blocked; ≥ ChallengeAt must solve a proof-of-work.
const (
	ChallengeAt = 40
	BlockAt     = 75
	// PowBits: leading zero bits required in sha256(prefix + nonce). ~65,000
	// hashes: about a second in a browser, a speed bump that makes running many
	// accounts expensive.
	PowBits = 16
)

// Score turns signals into a 0–100 suspicion score with human-readable reasons.
// accountsOnDevice and accountsOnIP count distinct accounts seen in this room.
func Score(sig Signals, accountsOnDevice, accountsOnIP int64) (int, []string) {
	score := 0
	var reasons []string
	add := func(n int, why string) {
		score += n
		reasons = append(reasons, why)
	}
	switch {
	case accountsOnDevice >= 6:
		add(50, "many accounts on one device")
	case accountsOnDevice >= 3:
		add(40, "several accounts on one device") // a challenge: agents with a few accounts can pass it
	}
	switch {
	case accountsOnIP >= 15:
		add(30, "many accounts on one network")
	case accountsOnIP >= 5:
		add(15, "several accounts on one network")
	}
	if sig.Device == "" {
		add(20, "no device fingerprint")
	}
	ua := strings.ToLower(sig.UserAgent)
	for _, bot := range []string{"headless", "python", "go-http-client", "curl", "wget", "selenium", "puppeteer", "playwright"} {
		if strings.Contains(ua, bot) {
			add(40, "automation user agent")
			break
		}
	}
	if sig.FormMillis > 0 {
		switch {
		case sig.FormMillis < 500:
			add(45, "booking form filled in under half a second")
		case sig.FormMillis < 1500:
			add(30, "booking form filled implausibly fast")
		}
	}
	if len(sig.KeyIntervals) >= 5 {
		var sum, sq float64
		for _, v := range sig.KeyIntervals {
			sum += float64(v)
		}
		mean := sum / float64(len(sig.KeyIntervals))
		for _, v := range sig.KeyIntervals {
			sq += (float64(v) - mean) * (float64(v) - mean)
		}
		if std := math.Sqrt(sq / float64(len(sig.KeyIntervals))); std < 8 {
			add(30, "machine-regular typing")
		}
	}
	if sig.FormMillis > 0 && sig.PointerMoves == 0 && len(sig.KeyIntervals) == 0 {
		add(10, "no keyboard or pointer activity")
	}
	if sig.PollsPer10s > 50 {
		add(20, "polling the queue too fast")
	}
	return min(score, 100), reasons
}

func action(score int) string {
	switch {
	case score >= BlockAt:
		return "block"
	case score >= ChallengeAt:
		return "challenge"
	}
	return "allow"
}

type Challenge struct {
	Prefix string `json:"prefix"`
	Bits   int    `json:"bits"`
}

type Detector struct {
	Redis *redis.Client
	DB    *pgxpool.Pool
}

// Assess scores a person and records the verdict. Scores only go up within a room:
// a person who looked like a bot once doesn't get a clean slate by behaving later.
func (d *Detector) Assess(ctx context.Context, r *Room, userID string, sig Signals) (string, error) {
	room := r.ID
	if sig.Device != "" {
		d.Redis.SAdd(ctx, key(room, "dev:"+sig.Device), userID)
		d.Redis.Expire(ctx, key(room, "dev:"+sig.Device), keyTTL)
	}
	if sig.IP != "" {
		d.Redis.SAdd(ctx, key(room, "ip:"+sig.IP), userID)
		d.Redis.Expire(ctx, key(room, "ip:"+sig.IP), keyTTL)
	}
	var onDevice, onIP int64
	if sig.Device != "" {
		onDevice, _ = d.Redis.SCard(ctx, key(room, "dev:"+sig.Device)).Result()
	}
	if sig.IP != "" {
		onIP, _ = d.Redis.SCard(ctx, key(room, "ip:"+sig.IP)).Result()
	}
	score, reasons := Score(sig, onDevice, onIP)

	k := key(room, "bot:"+userID)
	prev, _ := d.Redis.HGet(ctx, k, "score").Int()
	if score < prev {
		score = prev
	}
	act := action(score)
	solved, _ := d.Redis.HGet(ctx, k, "solved").Result()
	if act == "challenge" && solved == "1" {
		act = "allow" // proved they'll spend real work per account
	}
	fields := map[string]any{"score": score, "action": act}
	if act == "challenge" {
		if p, _ := d.Redis.HGet(ctx, k, "prefix").Result(); p == "" {
			buf := make([]byte, 12)
			rand.Read(buf)
			fields["prefix"] = hex.EncodeToString(buf)
		}
	}
	d.Redis.HSet(ctx, k, fields)
	d.Redis.Expire(ctx, k, keyTTL)
	if act != "allow" || len(reasons) > 0 {
		metrics.BotVerdicts.WithLabelValues(act).Inc()
	}
	if reasons == nil {
		reasons = []string{}
	}
	_, err := d.DB.Exec(ctx, `
		INSERT INTO bot_flags (room_id, user_id, score, action, reasons, ip, device) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (room_id, user_id) DO UPDATE SET score = GREATEST(bot_flags.score, EXCLUDED.score),
		  action = EXCLUDED.action,
		  reasons = coalesce((SELECT array_agg(DISTINCT x) FROM unnest(bot_flags.reasons || EXCLUDED.reasons) AS x), '{}'),
		  ip = EXCLUDED.ip, device = EXCLUDED.device`, room, userID, score, act, reasons, sig.IP, sig.Device)
	return act, err
}

// Verdict is the current action for a person, with their challenge if they have one.
func (d *Detector) Verdict(ctx context.Context, room, userID string) (string, *Challenge) {
	v, _ := d.Redis.HGetAll(ctx, key(room, "bot:"+userID)).Result()
	act := v["action"]
	if act == "" {
		act = "allow"
	}
	if act == "challenge" {
		return act, &Challenge{Prefix: v["prefix"], Bits: PowBits}
	}
	return act, nil
}

var ErrBadSolution = errors.New("that verification answer is wrong")

// Solve checks a proof-of-work: sha256(prefix + nonce) must start with PowBits zero bits.
func (d *Detector) Solve(ctx context.Context, room, userID, nonce string) error {
	k := key(room, "bot:"+userID)
	v, _ := d.Redis.HGetAll(ctx, k).Result()
	if v["action"] != "challenge" {
		return nil
	}
	if !ValidPoW(v["prefix"], nonce, PowBits) {
		return ErrBadSolution
	}
	d.Redis.HSet(ctx, k, "solved", "1", "action", "allow")
	_, err := d.DB.Exec(ctx, `UPDATE bot_flags SET action = 'allow', reasons = array_append(reasons, 'passed verification')
		WHERE room_id = $1 AND user_id = $2`, room, userID)
	return err
}

func ValidPoW(prefix, nonce string, need int) bool {
	if prefix == "" || len(nonce) > 64 {
		return false
	}
	h := sha256.Sum256([]byte(prefix + nonce))
	zeros := 0
	for _, b := range h {
		if b == 0 {
			zeros += 8
			continue
		}
		zeros += bits.LeadingZeros8(b)
		break
	}
	return zeros >= need
}

// SolvePoW finds a nonce (used by the bot swarm and tests; browsers do this in JS).
func SolvePoW(prefix string, need int) string {
	for i := 0; ; i++ {
		n := strconv.Itoa(i)
		if ValidPoW(prefix, n, need) {
			return n
		}
	}
}

// NotePoll counts status requests per person per 10 seconds for the polling signal.
func (d *Detector) NotePoll(ctx context.Context, room, userID string) int {
	bucket := time.Now().Unix() / 10
	k := key(room, "rq:"+userID+":"+strconv.FormatInt(bucket, 10))
	n, _ := d.Redis.Incr(ctx, k).Result()
	d.Redis.Expire(ctx, k, 20*time.Second)
	return int(n)
}

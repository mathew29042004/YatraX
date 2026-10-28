package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	otpTTL         = 5 * time.Minute
	otpMaxAttempts = 5
	otpSendWindow  = 10 * time.Minute
	otpSendLimit   = 3
)

var (
	ErrOTPRateLimited = errors.New("too many OTP requests")
	ErrOTPInvalid     = errors.New("invalid or expired OTP")
)

// OTPStore keeps one-time passwords in Redis. SMS is mocked: the code is logged
// (and returned to the client in development) instead of being sent.
type OTPStore struct{ rdb *redis.Client }

func NewOTPStore(rdb *redis.Client) *OTPStore { return &OTPStore{rdb: rdb} }

func (s *OTPStore) Issue(ctx context.Context, phone string) (string, error) {
	sendKey := "otp:sent:" + phone
	n, err := s.rdb.Incr(ctx, sendKey).Result()
	if err != nil {
		return "", err
	}
	if n == 1 {
		s.rdb.Expire(ctx, sendKey, otpSendWindow)
	}
	if n > otpSendLimit {
		return "", ErrOTPRateLimited
	}
	v, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", v.Int64())
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, "otp:tries:"+phone)
	pipe.Set(ctx, "otp:code:"+phone, code, otpTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", err
	}
	return code, nil
}

// Verify checks the code once; a code is burned on success or after too many tries.
func (s *OTPStore) Verify(ctx context.Context, phone, code string) error {
	codeKey, triesKey := "otp:code:"+phone, "otp:tries:"+phone
	want, err := s.rdb.Get(ctx, codeKey).Result()
	if errors.Is(err, redis.Nil) {
		return ErrOTPInvalid
	} else if err != nil {
		return err
	}
	tries, err := s.rdb.Incr(ctx, triesKey).Result()
	if err != nil {
		return err
	}
	s.rdb.Expire(ctx, triesKey, otpTTL)
	if tries > otpMaxAttempts {
		s.rdb.Del(ctx, codeKey)
		return ErrOTPInvalid
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(code)) != 1 {
		return ErrOTPInvalid
	}
	s.rdb.Del(ctx, codeKey, triesKey)
	return nil
}

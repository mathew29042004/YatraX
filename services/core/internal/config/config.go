// Package config reads service configuration from the environment.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string
	HTTPAddr     string
	DatabaseURL  string
	RedisURL     string
	KafkaBrokers []string
	JWTSecret    []byte
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CORSOrigins  []string

	GatewayURL           string // server-to-server
	GatewayPublicURL     string // what browsers open
	GatewayKeyID         string
	GatewayKeySecret     string
	GatewayWebhookSecret string
	PassengerAppURL      string
}

func Load() Config {
	return Config{
		Env:          get("APP_ENV", "development"),
		HTTPAddr:     get("HTTP_ADDR", ":8080"),
		DatabaseURL:  get("DATABASE_URL", "postgres://yatrax:yatrax@localhost:5433/yatrax?sslmode=disable"),
		RedisURL:     get("REDIS_URL", "redis://localhost:6379/0"),
		KafkaBrokers: split(get("KAFKA_BROKERS", "localhost:19092")),
		JWTSecret:    []byte(get("JWT_SECRET", "change-me-dev-only-secret-at-least-32-chars")),
		AccessTTL:    time.Duration(Int("ACCESS_TTL_MIN", 15)) * time.Minute,
		RefreshTTL:   time.Duration(Int("REFRESH_TTL_DAYS", 30)) * 24 * time.Hour,
		CORSOrigins:  split(get("CORS_ORIGINS", "http://localhost:3000")),

		GatewayURL:           get("GATEWAY_URL", "http://localhost:8081"),
		GatewayPublicURL:     get("GATEWAY_PUBLIC_URL", "http://localhost:8081"),
		GatewayKeyID:         get("GATEWAY_KEY_ID", "rzp_test_yatrax"),
		GatewayKeySecret:     get("GATEWAY_KEY_SECRET", "dev-gateway-key-secret"),
		GatewayWebhookSecret: get("GATEWAY_WEBHOOK_SECRET", "dev-gateway-webhook-secret"),
		PassengerAppURL:      get("PASSENGER_APP_URL", "http://localhost:3000"),
	}
}

func (c Config) Dev() bool { return c.Env == "development" }

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Int reads an integer env var, falling back to def when unset or invalid.
func Int(key string, def int) int {
	if n, err := strconv.Atoi(get(key, "")); err == nil {
		return n
	}
	return def
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

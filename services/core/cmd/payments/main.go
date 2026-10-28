// Command payments runs YatraPay, the mock payment gateway (see internal/paygw).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/db"
	"github.com/yatrax/yatrax/services/core/internal/paygw"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, env("DATABASE_URL", "postgres://yatrax:yatrax@localhost:5433/yatrax?sslmode=disable"))
	if err != nil {
		slog.Error("connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	s := &paygw.Server{
		DB:            pool,
		KeyID:         env("GATEWAY_KEY_ID", "rzp_test_yatrax"),
		KeySecret:     env("GATEWAY_KEY_SECRET", "dev-gateway-key-secret"),
		WebhookSecret: env("GATEWAY_WEBHOOK_SECRET", "dev-gateway-webhook-secret"),
		WebhookURL:    env("CORE_WEBHOOK_URL", "http://localhost:8080/v1/payments/webhook"),
	}
	if err := s.Migrate(ctx); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	if m := os.Getenv("GATEWAY_MODE"); m != "" {
		if err := s.SetMode(m); err != nil {
			slog.Error("mode", "err", err)
			os.Exit(1)
		}
	}
	go s.DeliverWebhooks(ctx)

	srv := &http.Server{Addr: env("HTTP_ADDR", ":8081"), Handler: s.Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	slog.Info("payment gateway listening", "addr", srv.Addr, "mode", s.Mode())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}

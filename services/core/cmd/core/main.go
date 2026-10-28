// Command core is the YatraX core API: auth and network data (Phase 0),
// growing into the booking core in later phases.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/analytics"
	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/catering"
	"github.com/yatrax/yatrax/services/core/internal/chaos"
	"github.com/yatrax/yatrax/services/core/internal/complaints"
	"github.com/yatrax/yatrax/services/core/internal/config"
	"github.com/yatrax/yatrax/services/core/internal/control"
	"github.com/yatrax/yatrax/services/core/internal/db"
	"github.com/yatrax/yatrax/services/core/internal/events"
	"github.com/yatrax/yatrax/services/core/internal/httpapi"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/paygw"
	"github.com/yatrax/yatrax/services/core/internal/search"
	"github.com/yatrax/yatrax/services/core/internal/simclock"
	"github.com/yatrax/yatrax/services/core/internal/station"
	"github.com/yatrax/yatrax/services/core/internal/stays"
	"github.com/yatrax/yatrax/services/core/internal/ticket"
	"github.com/yatrax/yatrax/services/core/internal/waitroom"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("core exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(ropts)
	defer rdb.Close()

	producer, err := events.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	signer, err := auth.NewSigner(cfg.JWTSecret, cfg.AccessTTL)
	if err != nil {
		return err
	}
	authSvc := &auth.Service{
		DB: pool, OTP: auth.NewOTPStore(rdb), Signer: signer, Events: producer,
		RefreshTTL: cfg.RefreshTTL, Dev: cfg.Dev(), Now: time.Now,
	}
	if err := control.EnsureSpecialTemplates(ctx, pool); err != nil {
		return err
	}
	net, err := network.Load(ctx, pool)
	if err != nil {
		return err
	}
	slog.Info("network loaded", "stations", len(net.Stations), "trains", len(net.Trains))
	bookings := booking.New(pool, net, time.Now)
	if err := bookings.Start(ctx); err != nil {
		return err
	}
	ticketSigner, err := ticket.Load(ctx, pool)
	if err != nil {
		return err
	}
	go producer.RelayOutbox(ctx, pool)
	rooms := waitroom.New(pool, rdb, bookings, net, time.Now)
	go rooms.Run(ctx)
	go bookings.RunCharts(ctx, time.Minute)
	// Phase 5: the railway's clock, live movement and the watcher that turns it into events.
	clock := simclock.New(pool, rdb)
	if err := clock.Load(ctx); err != nil {
		return err
	}
	go clock.Run(ctx)
	liveEngine := live.NewEngine(net, pool, clock.Now)
	go liveEngine.RunIncidents(ctx, 3*time.Second)
	host, _ := os.Hostname()
	go live.NewWatcher(liveEngine, rdb, fmt.Sprintf("%s-%d", host, os.Getpid())).Run(ctx)
	go liveEngine.RunGauges(ctx, func() float64 {
		if st := clock.State(); !st.Paused {
			return st.Speed
		}
		return 0
	})
	// Event consumers: the promotion engine and in-app notifications.
	go events.Consume(ctx, cfg.KafkaBrokers, "promotion-engine", []string{"booking.events"}, bookings.HandlePromotionEvent)
	go events.Consume(ctx, cfg.KafkaBrokers, "notifications", []string{"booking.events", "payment.events", "train.events"}, bookings.HandleNotificationEvent)
	stationSvc := &station.Service{DB: pool, Net: net, Live: liveEngine, Now: clock.Now}
	cateringSvc := &catering.Service{DB: pool, Redis: rdb, Net: net, Live: liveEngine, Now: clock.Now}
	analyticsSvc := &analytics.Service{DB: pool, Net: net, Live: liveEngine, Now: clock.Now}
	go cateringSvc.Run(ctx, fmt.Sprintf("%s-%d", host, os.Getpid()))
	api := &httpapi.Server{
		DB: pool, Redis: rdb, Auth: authSvc, Signer: signer, CORSOrigins: cfg.CORSOrigins, Dev: cfg.Dev(),
		Net: net, Now: time.Now,
		// Real inventory for runs that exist, the demand model for the rest.
		Search:     &search.Searcher{Net: net, Avail: booking.Provider{S: bookings}},
		Booking:    bookings,
		Rooms:      rooms,
		Clock:      clock,
		Live:       liveEngine,
		Station:    stationSvc,
		Catering:   cateringSvc,
		Analytics:  analyticsSvc,
		Complaints: &complaints.Service{DB: pool, Net: net, Live: liveEngine, Now: clock.Now},
		Stays:      &stays.Service{DB: pool, Net: net, Now: time.Now},
		Control:    &control.Service{DB: pool, Net: net, Live: liveEngine, Station: stationSvc, Booking: bookings, Now: clock.Now},
		Payments: &booking.Payments{
			Gateway: &paygw.Client{BaseURL: cfg.GatewayURL, PublicURL: cfg.GatewayPublicURL,
				KeyID: cfg.GatewayKeyID, KeySecret: cfg.GatewayKeySecret},
			WebhookSecret: cfg.GatewayWebhookSecret,
			AppURL:        cfg.PassengerAppURL,
			Signer:        ticketSigner,
		},
	}

	// Chaos drills pay as a customer would, so their HTTP client must not follow the gateway's redirects.
	api.Chaos = &chaos.Service{DB: pool, Redis: rdb, Booking: bookings, Payments: api.Payments, Live: liveEngine, Net: net, Now: clock.Now,
		GatewayURL: cfg.GatewayURL, HTTP: &http.Client{Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	go bookings.RunReconciler(ctx, api.Payments, time.Duration(config.Int("RECONCILE_EVERY_SEC", 30))*time.Second)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	slog.Info("core listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

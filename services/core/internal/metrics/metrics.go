// Package metrics defines Prometheus collectors for the core service.
package metrics

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	Requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_http_requests_total", Help: "HTTP requests by route and status.",
	}, []string{"method", "route", "status"})

	Latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "yatrax_http_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"method", "route"})

	Logins = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_auth_logins_total", Help: "Sign-in attempts by method and outcome.",
	}, []string{"method", "outcome"})

	Bookings = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_bookings_total", Help: "Booking attempts by outcome (held, conflict, regret, ...).",
	}, []string{"outcome"})

	HoldLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "yatrax_hold_duration_seconds",
		Help:    "Time to allocate and hold berths.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	})

	Payments = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_payments_settled_total", Help: "Payment results by outcome (confirmed, failed, refund_late, ...).",
	}, []string{"outcome"})

	Refunds = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_refunds_total", Help: "Refunds paid out to the original payment method, by outcome.",
	}, []string{"outcome"})

	Promotions = promauto.NewCounter(prometheus.CounterOpts{
		Name: "yatrax_waitlist_promotions_total", Help: "Passengers moved up from RAC or the waitlist.",
	})

	Cancellations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "yatrax_cancellations_total", Help: "Passengers cancelled.",
	})

	TatkalHolds = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_tatkal_holds_total", Help: "Tatkal fast-path booking attempts by outcome.",
	}, []string{"outcome"})

	TatkalHoldLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "yatrax_tatkal_hold_duration_seconds",
		Help:    "Time to book on the Tatkal fast path.",
		Buckets: []float64{.005, .01, .025, .05, .1, .2, .3, .5, 1, 2.5},
	})

	WaitroomJoins = promauto.NewCounter(prometheus.CounterOpts{
		Name: "yatrax_waitroom_joins_total", Help: "People joining Tatkal waiting rooms.",
	})

	WaitroomQueue = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "yatrax_waitroom_waiting", Help: "People still waiting for their turn, per room.",
	}, []string{"room"})

	WaitroomBookings = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_waitroom_bookings_total", Help: "Waiting-room booking attempts by outcome.",
	}, []string{"outcome"})

	BotVerdicts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_bot_verdicts_total", Help: "Bot detector verdicts (allow with signals, challenge, block).",
	}, []string{"action"})

	TrainEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_train_events_total", Help: "Live train events emitted for trains carrying passengers, by type.",
	}, []string{"type"})
	TrainsRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "yatrax_trains_running", Help: "Trains on the move (or halted en route) at simulated time.",
	})
	TrainsLate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "yatrax_trains_late", Help: "Running trains more than 15 minutes late.",
	})
	SimSpeed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "yatrax_sim_speed", Help: "Simulation clock speed (1 = real time; 0 when paused).",
	})
	EventsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "yatrax_events_published_total", Help: "Events sent to the bus by topic and outcome.",
	}, []string{"topic", "outcome"})
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack and Unwrap pass through to the real writer, so WebSocket upgrades work behind this middleware.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("connection cannot be hijacked")
	}
	w.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware records request count and latency, labelled by chi route pattern.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		Requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status)).Inc()
		Latency.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

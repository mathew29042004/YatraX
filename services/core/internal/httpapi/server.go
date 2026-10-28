// Package httpapi wires the HTTP routes of the core service.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/analytics"
	"github.com/yatrax/yatrax/services/core/internal/auth"
	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/catering"
	"github.com/yatrax/yatrax/services/core/internal/chaos"
	"github.com/yatrax/yatrax/services/core/internal/complaints"
	"github.com/yatrax/yatrax/services/core/internal/control"
	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/layout"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/search"
	"github.com/yatrax/yatrax/services/core/internal/simclock"
	stationsvc "github.com/yatrax/yatrax/services/core/internal/station"
	"github.com/yatrax/yatrax/services/core/internal/stays"
	"github.com/yatrax/yatrax/services/core/internal/waitroom"
)

type Server struct {
	DB          *pgxpool.Pool
	Net         *network.Network
	Search      *search.Searcher
	Booking     *booking.Service
	Rooms       *waitroom.Service
	Clock       *simclock.Clock
	Live        *live.Engine
	Station     *stationsvc.Service
	Control     *control.Service
	Catering    *catering.Service
	Analytics   *analytics.Service
	Chaos       *chaos.Service
	Complaints  *complaints.Service
	Stays       *stays.Service
	Payments    *booking.Payments
	Dev         bool
	Now         func() time.Time
	Redis       *redis.Client
	Auth        *auth.Service
	Signer      *auth.Signer
	CORSOrigins []string
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, metrics.Middleware)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "Idempotency-Key"},
		AllowCredentials: false,
		MaxAge:           600,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Get("/readyz", s.ready)
	r.Handle("/metrics", promhttp.Handler())

	r.Route("/v1", func(r chi.Router) {
		r.Route("/auth", s.Auth.Routes)

		// Public network data.
		r.Get("/network/stats", s.stats)
		r.Get("/zones", s.zones)
		r.Get("/stations", s.stations)
		r.Get("/stations/{code}", s.station)
		r.Get("/stations/{code}/trains", s.stationTrains)
		r.Get("/trains", s.trains)
		r.Get("/trains/{number}", s.train)
		r.Get("/coach-classes", s.coachClasses)
		r.Get("/coach-classes/{code}/layout", s.coachLayout)
		r.Get("/trains/{number}/coaches/{coach}", s.coach)

		// Phase 1: search, fares and availability.
		r.Get("/search", s.search)
		r.Get("/search/alternatives", s.alternatives)
		r.Get("/stations/{code}/stays", s.stayOptions)
		r.Get("/availability", s.calendar)
		r.Get("/fare", s.fareQuote)
		r.Get("/festivals", s.festivals)
		r.Get("/quotas", s.quotas)

		// Phase 2: booking core.
		r.Get("/pnr/{pnr}", s.pnrStatus)
		r.Get("/tickets/public-key", s.ticketPublicKey)
		r.Post("/tickets/verify", s.verifyTicket)
		r.Post("/payments/webhook", s.paymentWebhook)

		// Phase 3: cancellation, refunds, charts.
		r.Get("/tdr-reasons", s.tdrReasons)

		// Phase 4: Tatkal waiting room.
		r.Get("/waitrooms", s.listWaitrooms)
		r.Get("/waitrooms/{id}/ws", s.waitroomSocket)

		// Phase 5: live trains and the simulation clock.
		r.Get("/sim/clock", s.getSimClock)
		r.Get("/live/trains/{number}", s.liveTrain)
		r.Get("/live/map", s.liveMap)
		r.Get("/live/stations/{code}", s.liveStation)
		r.Get("/live/ws", s.liveSocket)
		r.Get("/live/stations-geo", s.stationsGeo)

		// Phase 7: station boards, announcements and coach positions are public.
		r.Get("/stations/{code}/announcements", s.stationAnnouncements)
		r.Get("/stations/{code}/coach-position/{train}", s.coachPosition)

		if s.Dev {
			r.Post("/dev/bookings/{id}/expire", s.devExpireHold)
		}

		r.Group(func(r chi.Router) {
			r.Use(s.Signer.Authenticate)
			r.Get("/me", s.Auth.Me)
			r.With(auth.RequireRole(auth.RoleAdmin)).Get("/admin/users", s.adminUsers)
			r.Get("/me/notifications", s.notifications)
			r.Post("/me/notifications/read", s.readNotifications)
			// Charts are for the people running the train.
			r.With(auth.RequireRole(auth.RoleTTE, auth.RoleStation, auth.RoleController)).Get("/charts", s.getChart)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleAdmin))
				r.Get("/admin/finance", s.finance)
				r.Get("/admin/tdrs", s.listTDRs)
				r.Post("/admin/tdrs/{id}/decide", s.decideTDR)
				r.Post("/admin/runs/chart", s.prepareChart)
				r.Post("/admin/runs/simulate-cancellations", s.simulateCancellations)
				r.Post("/admin/reconcile", s.reconcileNow)
				r.Get("/admin/integrity", s.integrity)
				r.Post("/admin/waitrooms/drill", s.createDrill)
				r.Get("/admin/waitrooms/{id}/monitor", s.monitorWaitroom)
				r.Get("/admin/waitrooms/{id}/bots", s.waitroomBots)
			})
			r.With(auth.RequireRole(auth.RoleVendor)).Get("/vendor/me", s.vendorMe)
			// Phase 9: the catering vendor's side.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleVendor))
				r.Get("/vendor/orders", s.vendorOrders)
				r.Post("/vendor/orders/{id}/status", s.advanceFoodOrder)
				r.Get("/vendor/menu", s.myMenu)
				r.Post("/vendor/menu", s.addMenuItem)
				r.Patch("/vendor/menu/{item}", s.editMenuItem)
				r.Get("/vendor/payouts", s.vendorPayouts)
				r.Post("/vendor/payouts", s.requestPayout)
			})
			// Phase 7: the station master's desk (their own station only).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleStation))
				r.Get("/station-master/{code}/platforms", s.stationPlatforms)
				r.Post("/station-master/{code}/platforms", s.assignPlatform)
				r.Get("/station-master/{code}/facilities", s.stationFacilities)
				r.Post("/station-master/{code}/lost-found", s.addLost)
				r.Post("/station-master/{code}/lost-found/{id}/return", s.returnLost)
				r.Post("/station-master/{code}/wheelchair", s.addWheelchair)
				r.Post("/station-master/{code}/wheelchair/{id}", s.updateWheelchair)
				r.Get("/station-master/{code}/stays", s.stayDesk)
				r.Post("/station-master/{code}/stays/{id}", s.advanceStay)
			})
			// Phase 6: the TTE app's offline bundle and sync.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleTTE))
				r.Get("/tte/duties", s.tteDuties)
				r.Get("/tte/runs/{train}/{date}", s.tteBundle)
				r.Post("/tte/runs/{train}/{date}/sync", s.tteSync)
			})
			// Phase 11: zone and division dashboards (station staff and TTEs see their own zone).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleController, auth.RoleStation, auth.RoleTTE))
				r.Get("/analytics/zones", s.analyticsZones)
				r.Get("/analytics/overview", s.analyticsOverview)
				r.Get("/analytics/occupancy", s.analyticsOccupancy)
				r.Get("/analytics/punctuality", s.analyticsPunctuality)
			})
			r.With(auth.RequireRole(auth.RoleAdmin)).Get("/analytics/fraud", s.analyticsFraud)
			// RailMadad complaints desk: station masters (their station), controllers and admins.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleStation, auth.RoleController))
				r.Get("/complaints", s.complaintQueue)
				r.Post("/complaints/{id}", s.actOnComplaint)
				r.Get("/group-desk", s.groupQueue)
				r.Post("/group-desk/{id}", s.decideGroup)
			})
			// Controllers run the railway's clock and hold trains (admins can too).
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RoleController))
				r.Post("/admin/sim/clock", s.setSimClock)
				r.Post("/admin/live/incidents", s.addIncident)
				r.Get("/admin/live/incidents", s.listIncidents)
				// Phase 8: the control room.
				r.Get("/control/section", s.sectionChart)
				r.Post("/control/what-if", s.whatIf)
				r.Get("/control/conflicts", s.networkConflicts)
				r.Get("/control/waitlists", s.longWaitlists)
				r.Get("/control/disruptions", s.listDisruptions)
				r.Get("/control/replay", s.replayDay)
				r.Get("/control/specials", s.listSpecials)
				r.Post("/control/specials", s.runSpecial)
				r.Post("/control/runs/disrupt", s.disruptRun)
				r.Post("/control/runs/extra-coaches", s.addExtraCoaches)
				// Phase 12: chaos mode.
				r.Get("/chaos/drills", s.chaosDrills)
				r.Get("/chaos/runs", s.chaosRuns)
				r.Get("/chaos/runs/{id}", s.chaosRun)
				r.Post("/chaos/runs", s.startChaos)
			})

			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(auth.RolePassenger))
				r.Get("/seatmap", s.seatMap)
				r.Post("/bookings", s.createBooking)
				r.Get("/bookings", s.listBookings)
				r.Get("/bookings/{id}", s.getBooking)
				r.Post("/bookings/{id}/pay", s.payBooking)
				r.Post("/bookings/{id}/verify", s.verifyBooking)
				r.Post("/bookings/{id}/release", s.releaseBooking)
				r.Get("/bookings/{id}/ticket", s.bookingTicket)
				r.Get("/bookings/{id}/cancel-quote", s.cancelQuote)
				r.Post("/bookings/{id}/complaints", s.fileComplaint)
				r.Get("/bookings/{id}/invoice", s.bookingInvoice)
				r.Post("/bookings/guarantee", s.createGuarantee)
				r.Post("/stays", s.bookStay)
				r.Get("/me/stays", s.myStays)
				r.Post("/stays/{id}/cancel", s.cancelStay)
				r.Post("/groups", s.requestGroup)
				r.Get("/me/groups", s.myGroups)
				r.Post("/groups/{id}/cancel", s.cancelGroup)
				r.Get("/me/loyalty", s.myLoyalty)
				r.Post("/me/loyalty/redeem", s.redeemPoints)
				r.Post("/bookings/{id}/sos", s.raiseSOS)
				r.Get("/me/complaints", s.myComplaints)
				r.Post("/bookings/{id}/cancel", s.cancelBooking)
				r.Post("/bookings/{id}/pay-wallet", s.payWithWallet)
				r.Post("/bookings/{id}/boarding", s.changeBoarding)
				r.Post("/bookings/{id}/tdr", s.fileTDR)
				r.Get("/me/wallet", s.wallet)
				r.Post("/waitrooms/{id}/join", s.joinWaitroom)
				r.Put("/waitrooms/{id}/draft", s.saveDraft)
				r.Post("/waitrooms/{id}/challenge", s.solveChallenge)
				r.Get("/waitrooms/{id}/status", s.waitroomStatus)
				r.Post("/waitrooms/{id}/book", s.bookFromWaitroom)
				r.Get("/me/passengers", s.listSavedPassengers)
				// Phase 9: food to the berth.
				r.Get("/bookings/{id}/food", s.foodOptions)
				r.Get("/vendors/{id}/menu", s.vendorMenu)
				r.Post("/food-orders", s.placeFoodOrder)
				r.Get("/food-orders", s.myFoodOrders)
				r.Post("/food-orders/{id}/cancel", s.cancelFoodOrder)
				r.Post("/food-orders/{id}/rate", s.rateFoodOrder)
				r.Post("/me/passengers", s.addSavedPassenger)
				r.Delete("/me/passengers/{id}", s.deleteSavedPassenger)
			})
		})
	})
	return r
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	status := http.StatusOK
	if err := s.DB.Ping(ctx); err != nil {
		checks["postgres"], status = err.Error(), http.StatusServiceUnavailable
	}
	if err := s.Redis.Ping(ctx).Err(); err != nil {
		checks["redis"], status = err.Error(), http.StatusServiceUnavailable
	}
	httpx.JSON(w, status, checks)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Zones      int            `json:"zones"`
		Divisions  int            `json:"divisions"`
		Stations   int            `json:"stations"`
		Trains     int            `json:"trains"`
		Stops      int            `json:"stops"`
		Coaches    int            `json:"coaches"`
		Vendors    int            `json:"vendors"`
		Users      map[string]int `json:"users"`
		SeededAt   string         `json:"seeded_at"`
		SeedRandom string         `json:"seed_random"`
	}
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM zones), (SELECT count(*) FROM divisions), (SELECT count(*) FROM stations),
		(SELECT count(*) FROM trains), (SELECT count(*) FROM train_stops), (SELECT count(*) FROM train_coaches),
		(SELECT count(*) FROM vendors),
		coalesce((SELECT value FROM seed_meta WHERE key='seeded_at'), ''),
		coalesce((SELECT value FROM seed_meta WHERE key='random_seed'), '')`).
		Scan(&out.Zones, &out.Divisions, &out.Stations, &out.Trains, &out.Stops, &out.Coaches, &out.Vendors,
			&out.SeededAt, &out.SeedRandom)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.Users = map[string]int{}
	rows, err := s.DB.Query(r.Context(), `SELECT role::text, count(*) FROM users GROUP BY role`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out.Users[role] = n
	}
	httpx.JSON(w, http.StatusOK, out)
}

type zone struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	HQ        string   `json:"hq"`
	Divisions []string `json:"divisions"`
	Stations  int      `json:"stations"`
}

func (s *Server) zones(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT z.code, z.name, z.hq,
		       array(SELECT name FROM divisions WHERE zone_code=z.code ORDER BY name),
		       (SELECT count(*) FROM stations st JOIN divisions d ON d.id=st.division_id WHERE d.zone_code=z.code)
		  FROM zones z ORDER BY z.code`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (zone, error) {
		var z zone
		err := row.Scan(&z.Code, &z.Name, &z.HQ, &z.Divisions, &z.Stations)
		return z, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type station struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Zone       string  `json:"zone"`
	Division   string  `json:"division"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Importance int16   `json:"importance"`
	Platforms  int16   `json:"platforms"`
	Language   string  `json:"language"`
}

const stationCols = `st.code, st.name, st.state, d.zone_code, d.name, st.lat, st.lon, st.importance, st.platforms, st.language`

func scanStation(row pgx.CollectableRow) (station, error) {
	var s station
	err := row.Scan(&s.Code, &s.Name, &s.State, &s.Zone, &s.Division, &s.Lat, &s.Lon, &s.Importance, &s.Platforms, &s.Language)
	return s, err
}

func limit(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

// stations searches by code prefix or name substring: ?q=chen&limit=20
func (s *Server) stations(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.DB.Query(r.Context(), `
		SELECT `+stationCols+`
		  FROM stations st JOIN divisions d ON d.id = st.division_id
		 WHERE $1 = '' OR st.code ILIKE $1 || '%' OR st.name ILIKE '%' || $1 || '%'
		 ORDER BY (upper(st.code) = upper($1)) DESC, st.importance, st.name
		 LIMIT $2`, q, limit(r, 50, 500))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, scanStation)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) station(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT `+stationCols+` FROM stations st JOIN divisions d ON d.id = st.division_id
		 WHERE st.code = upper($1)`, chi.URLParam(r, "code"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	st, err := pgx.CollectExactlyOneRow(rows, scanStation)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such station")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

type trainSummary struct {
	Number      string   `json:"number"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Origin      string   `json:"origin"`
	Destination string   `json:"destination"`
	Departure   string   `json:"departure"`
	Arrival     string   `json:"arrival"`
	DurationMin int      `json:"duration_min"`
	DistanceKm  int      `json:"distance_km"`
	RunningDays []bool   `json:"running_days"` // Mon..Sun
	Classes     []string `json:"classes"`
	Pantry      bool     `json:"pantry"`
}

func days(mask int16) []bool {
	out := make([]bool, 7)
	for i := range out {
		out[i] = mask&(1<<i) != 0
	}
	return out
}

// clock formats minutes-after-origin-midnight as HH:MM.
func clock(m int) string {
	m %= 1440
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func (s *Server) trains(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	typ := strings.ToUpper(r.URL.Query().Get("type"))
	rows, err := s.DB.Query(r.Context(), `
		SELECT t.number, t.name, t.type_code, t.origin, t.destination,
		       (SELECT departure_min FROM train_stops WHERE train_number=t.number ORDER BY seq LIMIT 1),
		       (SELECT arrival_min FROM train_stops WHERE train_number=t.number ORDER BY seq DESC LIMIT 1),
		       t.distance_km, t.running_days, t.pantry,
		       array(SELECT DISTINCT tc.class_code FROM train_coaches tc JOIN coach_classes cc ON cc.code=tc.class_code
		              WHERE tc.train_number=t.number AND cc.bookable ORDER BY tc.class_code)
		  FROM trains t
		 WHERE ($1 = '' OR t.number LIKE $1 || '%' OR t.name ILIKE '%' || $1 || '%')
		   AND ($2 = '' OR t.type_code = $2)
		 ORDER BY t.number LIMIT $3`, q, typ, limit(r, 50, 500))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (trainSummary, error) {
		var t trainSummary
		var dep, arr int
		var mask int16
		err := row.Scan(&t.Number, &t.Name, &t.Type, &t.Origin, &t.Destination, &dep, &arr,
			&t.DistanceKm, &mask, &t.Pantry, &t.Classes)
		t.Departure, t.Arrival, t.DurationMin, t.RunningDays = clock(dep), clock(arr), arr-dep, days(mask)
		return t, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type stop struct {
	Seq         int16  `json:"seq"`
	Station     string `json:"station"`
	StationName string `json:"station_name"`
	Arrival     string `json:"arrival,omitempty"`
	Departure   string `json:"departure,omitempty"`
	Day         int    `json:"day"`
	HaltMin     int    `json:"halt_min"`
	DistanceKm  int    `json:"distance_km"`
	Platform    int16  `json:"platform"`
}

type coach struct {
	CoachNo  string `json:"coach_no"`
	Class    string `json:"class"`
	Position int16  `json:"position"`
	Seats    int16  `json:"seats"`
	Bookable bool   `json:"bookable"`
}

func (s *Server) train(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	num := chi.URLParam(r, "number")
	var out struct {
		Number       string  `json:"number"`
		Name         string  `json:"name"`
		Type         string  `json:"type"`
		TypeName     string  `json:"type_name"`
		Origin       string  `json:"origin"`
		Destination  string  `json:"destination"`
		DistanceKm   int     `json:"distance_km"`
		RunningDays  []bool  `json:"running_days"`
		Pantry       bool    `json:"pantry"`
		ReturnNumber *string `json:"return_number"`
		Stops        []stop  `json:"stops"`
		Coaches      []coach `json:"coaches"`
	}
	var mask int16
	err := s.DB.QueryRow(ctx, `
		SELECT t.number, t.name, t.type_code, tt.name, t.origin, t.destination, t.distance_km, t.running_days, t.pantry, t.return_number
		  FROM trains t JOIN train_types tt ON tt.code = t.type_code WHERE t.number = $1`, num).
		Scan(&out.Number, &out.Name, &out.Type, &out.TypeName, &out.Origin, &out.Destination, &out.DistanceKm, &mask, &out.Pantry, &out.ReturnNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such train")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.RunningDays = days(mask)

	rows, err := s.DB.Query(ctx, `
		SELECT ts.seq, ts.station_code, st.name, ts.arrival_min, ts.departure_min, ts.distance_km, ts.platform
		  FROM train_stops ts JOIN stations st ON st.code = ts.station_code
		 WHERE ts.train_number = $1 ORDER BY ts.seq`, num)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.Stops, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (stop, error) {
		var st stop
		var arr, dep *int
		err := row.Scan(&st.Seq, &st.Station, &st.StationName, &arr, &dep, &st.DistanceKm, &st.Platform)
		ref := 0
		if arr != nil {
			st.Arrival, ref = clock(*arr), *arr
		}
		if dep != nil {
			st.Departure = clock(*dep)
			if arr == nil {
				ref = *dep
			} else {
				st.HaltMin = *dep - *arr
			}
		}
		st.Day = ref/1440 + 1
		return st, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}

	rows, err = s.DB.Query(ctx, `
		SELECT tc.coach_no, tc.class_code, tc.position, cc.seats_per_coach, cc.bookable
		  FROM train_coaches tc JOIN coach_classes cc ON cc.code = tc.class_code
		 WHERE tc.train_number = $1 ORDER BY tc.position`, num)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.Coaches, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (coach, error) {
		var c coach
		err := row.Scan(&c.CoachNo, &c.Class, &c.Position, &c.Seats, &c.Bookable)
		return c, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type coachClass struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	Bookable          bool     `json:"bookable"`
	SeatsPerCoach     int16    `json:"seats_per_coach"`
	Layout            string   `json:"layout"`
	AC                bool     `json:"ac"`
	FarePerKm         float64  `json:"fare_per_km"`
	ReservationCharge int      `json:"reservation_charge"`
	SuperfastCharge   int      `json:"superfast_charge"`
	GSTPct            float64  `json:"gst_pct"`
	TatkalPct         *float64 `json:"tatkal_pct"`
}

func (s *Server) coachClasses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT code, name, bookable, seats_per_coach, layout, ac, fare_per_km::float8, reservation_charge,
		       superfast_charge, gst_pct::float8, tatkal_pct::float8
		  FROM coach_classes ORDER BY sort_order`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (coachClass, error) {
		var c coachClass
		err := row.Scan(&c.Code, &c.Name, &c.Bookable, &c.SeatsPerCoach, &c.Layout, &c.AC, &c.FarePerKm,
			&c.ReservationCharge, &c.SuperfastCharge, &c.GSTPct, &c.TatkalPct)
		return c, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) coachLayout(w http.ResponseWriter, r *http.Request) {
	var name string
	var count int
	err := s.DB.QueryRow(r.Context(), `SELECT layout, seats_per_coach FROM coach_classes WHERE code = upper($1)`,
		chi.URLParam(r, "code")).Scan(&name, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "not_found", "No such class")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	seats, err := layout.Seats(name, count)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"layout": name, "seats": seats})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.DB.Query(r.Context(), `
		SELECT u.id, u.role::text, u.full_name, coalesce(u.phone,''), coalesce(u.employee_id,''), u.language,
		       coalesce(u.home_station,''), coalesce(p.designation,''), coalesce(p.zone_code,''),
		       coalesce(d.name,''), coalesce(p.station_code,'')
		  FROM users u
		  LEFT JOIN staff_postings p ON p.user_id = u.id
		  LEFT JOIN divisions d ON d.id = p.division_id
		 WHERE ($1 = '' OR u.role::text = $1)
		   AND ($2 = '' OR u.full_name ILIKE '%' || $2 || '%' OR u.phone = $2 OR u.employee_id ILIKE $2)
		 ORDER BY u.is_demo DESC, u.full_name LIMIT $3`, role, q, limit(r, 50, 200))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (auth.User, error) {
		var u auth.User
		err := row.Scan(&u.ID, &u.Role, &u.FullName, &u.Phone, &u.EmployeeID, &u.Language, &u.HomeStation,
			&u.Designation, &u.Zone, &u.Division, &u.Station)
		return u, err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

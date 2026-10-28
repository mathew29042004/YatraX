package catering

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Rules.
const (
	CommissionPct = 12               // YatraX's share of each delivered order
	MinHalt       = 2                // minutes: shorter halts leave no time to hand food over
	OrderBuffer   = 15 * time.Minute // on top of the longest item's prep time
	StartBuffer   = 10 * time.Minute // vendors start cooking this long before prep time runs out
	RetimeAfter   = 10               // minutes the ETA must move before the passenger is told again
)

type Service struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
	Net   *network.Network
	Live  *live.Engine
	Now   func() time.Time // railway (simulated) time: ETAs and cut-offs follow it
}

var (
	ErrNotFound   = errors.New("not found")
	ErrNotAllowed = errors.New("not allowed")
)

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotAllowed, fmt.Sprintf(format, a...))
}

func clock(t time.Time) string { return t.In(network.IST).Format("15:04") }

// ── Ordering: where can this booking get food? ─────────────────────────────

type VendorBrief struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Cuisines []string `json:"cuisines"`
	VegOnly  bool     `json:"veg_only"`
	Rating   float64  `json:"rating"`
}

type StopOption struct {
	Seq     int           `json:"seq"`
	Station string        `json:"station"`
	Name    string        `json:"name"`
	ETA     time.Time     `json:"eta"`      // live expected arrival
	HaltMin int           `json:"halt_min"` // timetabled
	Open    bool          `json:"open"`
	Reason  string        `json:"reason,omitempty"` // why not
	Vendors []VendorBrief `json:"vendors"`
}

type bookingRow struct {
	id, user, train, coach, passenger, status string
	runID                                     int64
	origin                                    time.Time
	from, to, berth                           int
}

func (s *Service) booking(ctx context.Context, userID, bookingID string) (*bookingRow, error) {
	var b bookingRow
	err := s.DB.QueryRow(ctx, `
		SELECT b.id, b.user_id, b.train_number, b.status::text, b.run_id, r.origin_date, b.from_idx, b.to_idx,
		       coalesce((SELECT p.coach_no FROM booking_passengers p WHERE p.booking_id = b.id AND p.status IN ('CNF', 'RAC') ORDER BY p.seq LIMIT 1), ''),
		       coalesce((SELECT p.berth_no FROM booking_passengers p WHERE p.booking_id = b.id AND p.status IN ('CNF', 'RAC') ORDER BY p.seq LIMIT 1), 0),
		       coalesce((SELECT p.name FROM booking_passengers p WHERE p.booking_id = b.id AND p.status IN ('CNF', 'RAC') ORDER BY p.seq LIMIT 1), '')
		  FROM bookings b JOIN train_runs r ON r.id = b.run_id WHERE b.id = $1 AND b.user_id = $2`, bookingID, userID).
		Scan(&b.id, &b.user, &b.train, &b.status, &b.runID, &b.origin, &b.from, &b.to, &b.coach, &b.berth, &b.passenger)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	b.origin = time.Date(b.origin.Year(), b.origin.Month(), b.origin.Day(), 0, 0, 0, 0, network.IST)
	return &b, err
}

// stopCheck says whether food can still be ordered for a stop, given the longest prep time.
func (s *Service) stopCheck(t *network.Train, st live.Status, seq, prep int) (time.Time, string) {
	v := st.Stops[seq]
	if v.State == "cancelled" || v.State == "skipped" || st.Disruption == "cancelled" {
		return time.Time{}, "the train no longer calls here"
	}
	if v.ExpArr == nil {
		return time.Time{}, "no arrival here"
	}
	eta := *v.ExpArr
	stop := t.Stops[seq]
	if stop.Dep == nil || *stop.Dep-*stop.Arr < MinHalt {
		return eta, fmt.Sprintf("the halt is too short (%d min)", max(0, func() int {
			if stop.Dep == nil {
				return 0
			}
			return *stop.Dep - *stop.Arr
		}()))
	}
	if v.State != "upcoming" {
		return eta, "the train has already reached here"
	}
	if cut := eta.Add(-time.Duration(prep)*time.Minute - OrderBuffer); s.Now().After(cut) {
		return eta, fmt.Sprintf("too close: orders here close %d min before arrival", prep+int(OrderBuffer.Minutes()))
	}
	return eta, ""
}

// Options lists the stops on a booking's journey where food can be delivered, with live
// ETAs and the vendors there.
func (s *Service) Options(ctx context.Context, userID, bookingID string) ([]StopOption, error) {
	b, err := s.booking(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	t := s.Net.Trains[b.train]
	if t == nil || b.status != "CONFIRMED" {
		return nil, reject("only a confirmed booking can order food")
	}
	st := s.Live.Status(t, b.origin, s.Now())
	out := []StopOption{}
	for seq := b.from + 1; seq < b.to; seq++ {
		code := t.Stops[seq].Station
		rows, err := s.DB.Query(ctx, `SELECT id, name, cuisines, veg_only, rating::float8 FROM vendors WHERE station_code = $1 ORDER BY rating DESC`, code)
		if err != nil {
			return nil, err
		}
		vs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (VendorBrief, error) {
			var v VendorBrief
			return v, r.Scan(&v.ID, &v.Name, &v.Cuisines, &v.VegOnly, &v.Rating)
		})
		if err != nil {
			return nil, err
		}
		if len(vs) == 0 {
			continue
		}
		o := StopOption{Seq: seq, Station: code, Name: st.Stops[seq].Name, Vendors: vs}
		if stop := t.Stops[seq]; stop.Dep != nil && stop.Arr != nil {
			o.HaltMin = *stop.Dep - *stop.Arr
		}
		var reason string
		o.ETA, reason = s.stopCheck(t, st, seq, 20)
		o.Open, o.Reason = reason == "", reason
		out = append(out, o)
	}
	return out, nil
}

type MenuItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Diet        string `json:"diet"`
	Price       int    `json:"price"`
	PrepMin     int    `json:"prep_min"`
	Available   bool   `json:"available"`
}

func (s *Service) Menu(ctx context.Context, vendorID string, all bool) ([]MenuItem, error) {
	if err := s.EnsureMenu(ctx, vendorID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id, name, description, category, diet, price, prep_min, available FROM menu_items
		WHERE vendor_id = $1 AND ($2 OR available) ORDER BY CASE category WHEN 'meal' THEN 0 WHEN 'snack' THEN 1 WHEN 'sweet' THEN 2 ELSE 3 END, price DESC`, vendorID, all)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MenuItem, error) {
		var m MenuItem
		return m, r.Scan(&m.ID, &m.Name, &m.Description, &m.Category, &m.Diet, &m.Price, &m.PrepMin, &m.Available)
	})
}

// ── Orders ─────────────────────────────────────────────────────────────────

type Line struct {
	ItemID int64  `json:"item_id"`
	Name   string `json:"name,omitempty"`
	Diet   string `json:"diet,omitempty"`
	Qty    int    `json:"qty"`
	Price  int    `json:"price,omitempty"`
}

type OrderRequest struct {
	BookingID string `json:"booking_id"`
	VendorID  string `json:"vendor_id"`
	Station   string `json:"station"`
	Items     []Line `json:"items"`
	Payment   string `json:"payment"` // wallet or cod
}

type Order struct {
	ID          string     `json:"id"`
	Code        string     `json:"code"`
	BookingID   string     `json:"booking_id"`
	VendorID    string     `json:"vendor_id"`
	VendorName  string     `json:"vendor_name"`
	Train       string     `json:"train"`
	TrainName   string     `json:"train_name"`
	OriginDate  string     `json:"origin_date"`
	Station     string     `json:"station"`
	StationName string     `json:"station_name"`
	Coach       string     `json:"coach"`
	Berth       int        `json:"berth"`
	Passenger   string     `json:"passenger"`
	Items       []Line     `json:"items"`
	Total       int        `json:"total"`
	Commission  int        `json:"commission"`
	Payment     string     `json:"payment"`
	Status      string     `json:"status"`
	Note        string     `json:"note,omitempty"`
	PrepMin     int        `json:"prep_min"`
	ETA         time.Time  `json:"eta"`      // live expected arrival at the station
	StartBy     time.Time  `json:"start_by"` // vendors: start cooking by
	DelayMin    int        `json:"delay_min"`
	Platform    int        `json:"platform"`
	TrainState  string     `json:"train_state"`
	Rating      *int       `json:"rating,omitempty"`
	Review      string     `json:"review,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

const orderCols = `o.id, o.code, o.booking_id, o.vendor_id, v.name, o.train_number, o.origin_date, o.station_code, o.stop_seq, o.coach_no,
	o.berth_no, o.passenger, o.total, o.commission, o.payment, o.status::text, o.note, o.prep_min, o.eta_at, o.rating, coalesce(o.review, ''),
	o.created_at, o.delivered_at`

func (s *Service) scan(row pgx.Row) (*Order, error) {
	var o Order
	var od time.Time
	var seq int
	err := row.Scan(&o.ID, &o.Code, &o.BookingID, &o.VendorID, &o.VendorName, &o.Train, &od, &o.Station, &seq, &o.Coach, &o.Berth,
		&o.Passenger, &o.Total, &o.Commission, &o.Payment, &o.Status, &o.Note, &o.PrepMin, &o.ETA, &o.Rating, &o.Review, &o.CreatedAt, &o.DeliveredAt)
	if err != nil {
		return nil, err
	}
	o.OriginDate = od.Format(time.DateOnly)
	o.Items = []Line{}
	if t := s.Net.Trains[o.Train]; t != nil {
		o.TrainName = t.Name
		origin := time.Date(od.Year(), od.Month(), od.Day(), 0, 0, 0, 0, network.IST)
		st := s.Live.Status(t, origin, s.Now())
		if seq < len(st.Stops) {
			v := st.Stops[seq]
			o.StationName, o.Platform, o.DelayMin, o.TrainState = v.Name, v.Platform, v.ArrDelay, st.Message
			if v.ExpArr != nil && (o.Status == "placed" || o.Status == "accepted" || o.Status == "preparing" || o.Status == "handed_over") {
				o.ETA = *v.ExpArr // always the live figure while the order is open
			}
		}
	}
	o.StartBy = o.ETA.Add(-time.Duration(o.PrepMin)*time.Minute - StartBuffer)
	return &o, nil
}

func (s *Service) loadItems(ctx context.Context, orders []*Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]string, len(orders))
	byID := map[string]*Order{}
	for i, o := range orders {
		ids[i], byID[o.ID] = o.ID, o
	}
	rows, err := s.DB.Query(ctx, `SELECT order_id, item_id, name, diet, qty, price FROM food_order_items WHERE order_id = ANY($1::uuid[]) ORDER BY name`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var l Line
		if err := rows.Scan(&id, &l.ItemID, &l.Name, &l.Diet, &l.Qty, &l.Price); err != nil {
			return err
		}
		byID[id].Items = append(byID[id].Items, l)
	}
	return rows.Err()
}

func (s *Service) get(ctx context.Context, where string, args ...any) (*Order, error) {
	o, err := s.scan(s.DB.QueryRow(ctx, `SELECT `+orderCols+` FROM food_orders o JOIN vendors v ON v.id = o.vendor_id WHERE `+where, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return o, s.loadItems(ctx, []*Order{o})
}

func (s *Service) list(ctx context.Context, where string, args ...any) ([]*Order, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+orderCols+` FROM food_orders o JOIN vendors v ON v.id = o.vendor_id WHERE `+where+` ORDER BY o.eta_at, o.created_at LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	var out []*Order
	for rows.Next() {
		o, err := s.scan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, o)
	}
	rows.Close()
	if out == nil {
		out = []*Order{}
	}
	return out, s.loadItems(ctx, out)
}

func event(ctx context.Context, tx pgx.Tx, o map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ('booking.events', $1, $2)`, o["order_id"], o)
	return err
}

// Place orders food for delivery at a stop of a booking's journey.
func (s *Service) Place(ctx context.Context, userID string, req OrderRequest) (*Order, error) {
	b, err := s.booking(ctx, userID, req.BookingID)
	if err != nil {
		return nil, err
	}
	t := s.Net.Trains[b.train]
	if t == nil || b.status != "CONFIRMED" {
		return nil, reject("only a confirmed booking can order food")
	}
	if b.coach == "" {
		return nil, reject("waitlisted passengers have no berth to deliver to")
	}
	seq := t.StopIndex(req.Station)
	if seq <= b.from || seq >= b.to {
		return nil, reject("%s isn't on your journey after you board", req.Station)
	}
	var vendorStation string
	if err := s.DB.QueryRow(ctx, `SELECT station_code FROM vendors WHERE id = $1`, req.VendorID).Scan(&vendorStation); err != nil || vendorStation != req.Station {
		return nil, reject("that vendor isn't at %s", req.Station)
	}
	if req.Payment != "wallet" && req.Payment != "cod" {
		return nil, reject("pay from your YatraX Pay wallet or cash on delivery")
	}
	if len(req.Items) == 0 || len(req.Items) > 15 {
		return nil, reject("order 1 to 15 dishes")
	}
	menu, err := s.Menu(ctx, req.VendorID, false)
	if err != nil {
		return nil, err
	}
	byID := map[int64]MenuItem{}
	for _, m := range menu {
		byID[m.ID] = m
	}
	total, prep := 0, 0
	for i, l := range req.Items {
		m, ok := byID[l.ItemID]
		if !ok || l.Qty < 1 || l.Qty > 20 {
			return nil, reject("item %d isn't on today's menu", l.ItemID)
		}
		req.Items[i].Name, req.Items[i].Diet, req.Items[i].Price = m.Name, m.Diet, m.Price
		total += m.Price * l.Qty
		prep = max(prep, m.PrepMin)
	}
	st := s.Live.Status(t, b.origin, s.Now())
	eta, why := s.stopCheck(t, st, seq, prep)
	if why != "" {
		return nil, reject("can't order for %s: %s", req.Station, why)
	}
	commission := (total*CommissionPct + 50) / 100

	var id string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var n int64
		if err := tx.QueryRow(ctx, `SELECT nextval('food_order_seq')`).Scan(&n); err != nil {
			return err
		}
		if req.Payment == "wallet" {
			// One spender at a time per wallet, as for ticket payments.
			if _, err := tx.Exec(ctx, `INSERT INTO ledger_accounts (code, user_id) VALUES ($1, $2) ON CONFLICT (code) DO NOTHING`, ledger.Wallet(userID), userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT id FROM ledger_accounts WHERE code = $1 FOR UPDATE`, ledger.Wallet(userID)); err != nil {
				return err
			}
			owed, err := ledger.Owed(ctx, tx, ledger.Wallet(userID))
			if err != nil {
				return err
			}
			if owed < ledger.Paise(total) {
				return reject("your YatraX Pay wallet has ₹%d; this order is ₹%d (choose cash on delivery)", owed/100, total)
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO food_orders (code, booking_id, user_id, vendor_id, run_id, train_number, origin_date, station_code, stop_seq,
			coach_no, berth_no, passenger, total, commission, payment, prep_min, eta_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`,
			fmt.Sprintf("FD%06d", n), b.id, userID, req.VendorID, b.runID, b.train, b.origin, req.Station, seq,
			b.coach, b.berth, b.passenger, total, commission, req.Payment, prep, eta).Scan(&id); err != nil {
			return err
		}
		for _, l := range req.Items {
			if _, err := tx.Exec(ctx, `INSERT INTO food_order_items (order_id, item_id, name, diet, qty, price) VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (order_id, item_id) DO UPDATE SET qty = food_order_items.qty + EXCLUDED.qty`, id, l.ItemID, l.Name, l.Diet, l.Qty, l.Price); err != nil {
				return err
			}
		}
		if req.Payment == "wallet" {
			// Held in escrow until it's delivered: then it's the vendor's (less commission).
			if err := ledger.Post(ctx, tx, "food_order", id, "Food order "+fmt.Sprintf("FD%06d", n),
				ledger.Line{Account: ledger.Wallet(userID), Amount: ledger.Paise(total)},
				ledger.Line{Account: ledger.CateringEscrow, Amount: -ledger.Paise(total)}); err != nil {
				return err
			}
		}
		return event(ctx, tx, map[string]any{"type": "food.placed", "order_id": id, "booking_id": b.id})
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, "o.id = $1", id)
}

// MyOrders are a passenger's orders (optionally for one booking).
func (s *Service) MyOrders(ctx context.Context, userID, bookingID string) ([]*Order, error) {
	if bookingID != "" {
		return s.list(ctx, "o.user_id = $1 AND o.booking_id = $2", userID, bookingID)
	}
	return s.list(ctx, "o.user_id = $1", userID)
}

// settle closes an order: deliver pays the vendor; anything else refunds a prepaid order.
func (s *Service) settle(ctx context.Context, tx pgx.Tx, o *Order, status, note string) error {
	tag, err := tx.Exec(ctx, `UPDATE food_orders SET status = $2::food_status, note = $3, updated_at = now(),
		delivered_at = CASE WHEN $2::food_status = 'delivered' THEN now() END
		WHERE id = $1 AND status IN ('placed', 'accepted', 'preparing', 'handed_over')`, o.ID, status, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return reject("this order is already %s", o.Status)
	}
	var userID string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM food_orders WHERE id = $1`, o.ID).Scan(&userID); err != nil {
		return err
	}
	vendor := ledger.Vendor(o.VendorID)
	switch {
	case status == "delivered" && o.Payment == "wallet":
		err = ledger.Post(ctx, tx, "food_delivered", o.ID, "Delivered "+o.Code,
			ledger.Line{Account: ledger.CateringEscrow, Amount: ledger.Paise(o.Total)},
			ledger.Line{Account: vendor, Amount: -ledger.Paise(o.Total - o.Commission)},
			ledger.Line{Account: ledger.CateringCommission, Amount: -ledger.Paise(o.Commission)})
	case status == "delivered":
		// Cash on delivery: the vendor has the money and owes YatraX the commission.
		err = ledger.Post(ctx, tx, "food_delivered_cod", o.ID, "Delivered "+o.Code+" (cash)",
			ledger.Line{Account: vendor, Amount: ledger.Paise(o.Commission)},
			ledger.Line{Account: ledger.CateringCommission, Amount: -ledger.Paise(o.Commission)})
	case o.Payment == "wallet":
		err = ledger.Post(ctx, tx, "food_refund", o.ID, "Refund for "+o.Code,
			ledger.Line{Account: ledger.CateringEscrow, Amount: ledger.Paise(o.Total)},
			ledger.Line{Account: ledger.Wallet(userID), Amount: -ledger.Paise(o.Total)})
	}
	if err != nil {
		return err
	}
	return event(ctx, tx, map[string]any{"type": "food." + status, "order_id": o.ID, "booking_id": o.BookingID, "note": note})
}

// Cancel: a passenger may cancel until the kitchen starts.
func (s *Service) Cancel(ctx context.Context, userID, id string) (*Order, error) {
	o, err := s.get(ctx, "o.id = $1 AND o.user_id = $2", id, userID)
	if err != nil {
		return nil, err
	}
	if o.Status != "placed" && o.Status != "accepted" {
		return nil, reject("the vendor has started on it; it can't be cancelled now")
	}
	if err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error { return s.settle(ctx, tx, o, "cancelled", "Cancelled by the passenger") }); err != nil {
		return nil, err
	}
	return s.get(ctx, "o.id = $1", id)
}

// Rate a delivered order; a hygiene complaint goes to the complaints desk against the vendor.
func (s *Service) Rate(ctx context.Context, userID, id string, stars int, review string, hygiene bool) (*Order, error) {
	o, err := s.get(ctx, "o.id = $1 AND o.user_id = $2", id, userID)
	if err != nil {
		return nil, err
	}
	if o.Status != "delivered" {
		return nil, reject("rate an order once it's delivered")
	}
	if stars < 1 || stars > 5 {
		return nil, reject("1 to 5 stars")
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE food_orders SET rating = $2, review = nullif($3, '') WHERE id = $1`, id, stars, review); err != nil {
			return err
		}
		// The vendor's rating becomes the average of real ratings once there are a few.
		if _, err := tx.Exec(ctx, `UPDATE vendors v SET rating = round(x.avg, 1) FROM (
			SELECT avg(rating)::numeric AS avg, count(*) AS n FROM food_orders WHERE vendor_id = $1 AND rating IS NOT NULL) x
			WHERE v.id = $1 AND x.n >= 3`, o.VendorID); err != nil {
			return err
		}
		if hygiene {
			_, err := tx.Exec(ctx, `INSERT INTO complaints (run_id, coach_no, berth_no, category, description, reported_by, source, vendor_id)
				SELECT run_id, coach_no, berth_no, 'food_hygiene', $2, $3, 'passenger', vendor_id FROM food_orders WHERE id = $1`,
				id, fmt.Sprintf("Order %s from %s: %s", o.Code, o.VendorName, review), userID)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, "o.id = $1", id)
}

// ── Vendor side ────────────────────────────────────────────────────────────

func (s *Service) VendorFor(ctx context.Context, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM vendors WHERE owner_user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// VendorOrders lists a vendor's open orders (soonest train first) and recent closed ones.
func (s *Service) VendorOrders(ctx context.Context, vendorID string, open bool) ([]*Order, error) {
	if open {
		return s.list(ctx, "o.vendor_id = $1 AND o.status IN ('placed', 'accepted', 'preparing', 'handed_over')", vendorID)
	}
	return s.list(ctx, "o.vendor_id = $1 AND o.status NOT IN ('placed', 'accepted', 'preparing', 'handed_over') AND o.updated_at > now() - interval '3 days'", vendorID)
}

var next = map[string][]string{
	"placed":      {"accepted", "rejected"},
	"accepted":    {"preparing"},
	"preparing":   {"handed_over"},
	"handed_over": {"delivered"},
}

// Advance moves an order along: accept (or reject) → preparing → handed over → delivered.
func (s *Service) Advance(ctx context.Context, vendorID, id, to, note string) (*Order, error) {
	o, err := s.get(ctx, "o.id = $1 AND o.vendor_id = $2", id, vendorID)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, n := range next[o.Status] {
		allowed = allowed || n == to
	}
	if !allowed {
		return nil, reject("an order that's %s can't become %s", o.Status, to)
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if to == "delivered" || to == "rejected" {
			if to == "rejected" && note == "" {
				note = "The vendor couldn't take this order"
			}
			return s.settle(ctx, tx, o, to, note)
		}
		if _, err := tx.Exec(ctx, `UPDATE food_orders SET status = $2::food_status, updated_at = now() WHERE id = $1`, id, to); err != nil {
			return err
		}
		return event(ctx, tx, map[string]any{"type": "food." + to, "order_id": id, "booking_id": o.BookingID})
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, "o.id = $1", id)
}

type MenuEdit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Diet        string `json:"diet"`
	Price       int    `json:"price"`
	PrepMin     int    `json:"prep_min"`
	Available   *bool  `json:"available"`
}

func (s *Service) AddItem(ctx context.Context, vendorID string, m MenuEdit) (int64, error) {
	var vegOnly bool
	if err := s.DB.QueryRow(ctx, `SELECT veg_only FROM vendors WHERE id = $1`, vendorID).Scan(&vegOnly); err != nil {
		return 0, err
	}
	if vegOnly && (m.Diet == "nonveg" || m.Diet == "egg") {
		return 0, reject("a pure-veg outlet can't list %s dishes", m.Diet)
	}
	if m.Name == "" || m.Price <= 0 || m.PrepMin < 1 || m.PrepMin > 120 {
		return 0, reject("a dish needs a name, a price and a prep time of 1–120 min")
	}
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO menu_items (vendor_id, name, description, category, diet, price, prep_min) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		vendorID, m.Name, m.Description, m.Category, m.Diet, m.Price, m.PrepMin).Scan(&id)
	return id, err
}

func (s *Service) EditItem(ctx context.Context, vendorID string, id int64, m MenuEdit) error {
	tag, err := s.DB.Exec(ctx, `UPDATE menu_items SET price = CASE WHEN $3 > 0 THEN $3 ELSE price END,
		prep_min = CASE WHEN $4 > 0 THEN $4 ELSE prep_min END, available = coalesce($5, available)
		WHERE id = $1 AND vendor_id = $2`, id, vendorID, m.Price, m.PrepMin, m.Available)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

type Payouts struct {
	Balance    int          `json:"balance"`   // rupees YatraX owes the vendor now (negative: the vendor owes commission)
	Delivered  int          `json:"delivered"` // orders delivered, all time
	Sales      int          `json:"sales"`     // value of delivered orders
	Commission int          `json:"commission"`
	Payouts    []PayoutLine `json:"payouts"`
	Recent     []LedgerLine `json:"recent"`
}

type PayoutLine struct {
	Amount    int       `json:"amount"`
	Reference string    `json:"reference"`
	At        time.Time `json:"at"`
}

type LedgerLine struct {
	Kind   string    `json:"kind"`
	Ref    string    `json:"ref"`
	Memo   string    `json:"memo"`
	Amount int       `json:"amount"` // rupees: + earned, − commission owed or paid out
	At     time.Time `json:"at"`
}

func (s *Service) Payouts(ctx context.Context, vendorID string) (*Payouts, error) {
	owed, err := ledger.Owed(ctx, s.DB, ledger.Vendor(vendorID))
	if err != nil {
		return nil, err
	}
	p := &Payouts{Balance: int(owed / 100), Payouts: []PayoutLine{}, Recent: []LedgerLine{}}
	if err := s.DB.QueryRow(ctx, `SELECT count(*), coalesce(sum(total), 0), coalesce(sum(commission), 0) FROM food_orders
		WHERE vendor_id = $1 AND status = 'delivered'`, vendorID).Scan(&p.Delivered, &p.Sales, &p.Commission); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT amount, reference, created_at FROM vendor_payouts WHERE vendor_id = $1 ORDER BY id DESC LIMIT 20`, vendorID)
	if err != nil {
		return nil, err
	}
	if p.Payouts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayoutLine, error) {
		var x PayoutLine
		return x, r.Scan(&x.Amount, &x.Reference, &x.At)
	}); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT t.kind, t.ref, t.memo, -e.amount / 100, t.created_at FROM ledger_entries e
		JOIN ledger_txns t ON t.id = e.txn_id JOIN ledger_accounts a ON a.id = e.account_id
		WHERE a.code = $1 ORDER BY t.id DESC LIMIT 30`, ledger.Vendor(vendorID))
	if err != nil {
		return nil, err
	}
	p.Recent, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (LedgerLine, error) {
		var x LedgerLine
		return x, r.Scan(&x.Kind, &x.Ref, &x.Memo, &x.Amount, &x.At)
	})
	return p, err
}

// Payout settles the vendor's balance to their bank account (simulated transfer).
func (s *Service) Payout(ctx context.Context, vendorID string) (*PayoutLine, error) {
	var line *PayoutLine
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_accounts (code) VALUES ($1) ON CONFLICT (code) DO NOTHING`, ledger.Vendor(vendorID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM ledger_accounts WHERE code = $1 FOR UPDATE`, ledger.Vendor(vendorID)); err != nil {
			return err
		}
		owed, err := ledger.Owed(ctx, tx, ledger.Vendor(vendorID))
		if err != nil {
			return err
		}
		amount := int(owed / 100)
		if amount < 1 {
			return reject("nothing to pay out: balance ₹%d", amount)
		}
		buf := make([]byte, 6)
		rand.Read(buf)
		ref := "UTR" + strings.ToUpper(hex.EncodeToString(buf))
		if err := ledger.Post(ctx, tx, "vendor_payout", vendorID, "Payout "+ref,
			ledger.Line{Account: ledger.Vendor(vendorID), Amount: ledger.Paise(amount)},
			ledger.Line{Account: ledger.Gateway, Amount: -ledger.Paise(amount)}); err != nil {
			return err
		}
		line = &PayoutLine{Amount: amount, Reference: ref, At: time.Now()}
		_, err = tx.Exec(ctx, `INSERT INTO vendor_payouts (vendor_id, amount, reference) VALUES ($1,$2,$3)`, vendorID, amount, ref)
		return err
	})
	return line, err
}

// ── The loop: live ETAs, missed trains, disruptions ────────────────────────

// Run watches open orders every 20 seconds (one instance leads): when the train's ETA
// moves the passenger is told and the vendor's "start by" moves with it; if the train
// leaves the station before delivery, or no longer calls there, the order closes and a
// prepaid one is refunded.
func (s *Service) Run(ctx context.Context, instance string) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if ok, _ := s.Redis.SetNX(ctx, "catering:leader", instance, 30*time.Second).Result(); !ok {
			if v, _ := s.Redis.Get(ctx, "catering:leader").Result(); v != instance {
				continue
			}
			s.Redis.Expire(ctx, "catering:leader", 30*time.Second)
		}
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("catering: tick", "err", err)
		}
	}
}

func (s *Service) Tick(ctx context.Context) error {
	orders, err := s.list(ctx, "o.status IN ('placed', 'accepted', 'preparing', 'handed_over')")
	if err != nil {
		return err
	}
	for _, o := range orders {
		t := s.Net.Trains[o.Train]
		if t == nil {
			continue
		}
		od, _ := time.ParseInLocation(time.DateOnly, o.OriginDate, network.IST)
		st := s.Live.Status(t, od, s.Now())
		seq := t.StopIndex(o.Station)
		v := st.Stops[seq]
		var close, note string
		switch {
		case st.Disruption == "cancelled" || v.State == "cancelled" || v.State == "skipped":
			close, note = "cancelled", "The train no longer calls at "+v.Name
		case v.State == "departed":
			close, note = "missed", "The train left "+v.Name+" before the food was delivered"
		}
		if close != "" {
			if err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error { return s.settle(ctx, tx, o, close, note) }); err != nil {
				slog.Warn("catering: close", "order", o.Code, "err", err)
			}
			continue
		}
		var stored time.Time
		if err := s.DB.QueryRow(ctx, `SELECT eta_at FROM food_orders WHERE id = $1`, o.ID).Scan(&stored); err != nil {
			return err
		}
		if v.ExpArr == nil {
			continue
		}
		if shift := v.ExpArr.Sub(stored); shift >= RetimeAfter*time.Minute || shift <= -RetimeAfter*time.Minute {
			err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE food_orders SET eta_at = $2, updated_at = now() WHERE id = $1`, o.ID, *v.ExpArr); err != nil {
					return err
				}
				return event(ctx, tx, map[string]any{"type": "food.retimed", "order_id": o.ID, "booking_id": o.BookingID,
					"eta": v.ExpArr.Format(time.RFC3339), "shift_min": int(shift.Minutes()), "station": v.Name,
					"start_by": clock(v.ExpArr.Add(-time.Duration(o.PrepMin)*time.Minute - StartBuffer))})
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

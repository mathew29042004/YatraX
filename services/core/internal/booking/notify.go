package booking

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/events"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// ── Promotion engine (event-driven) ────────────────────────────────────────

// HandlePromotionEvent re-runs promotion when berths are freed outside a
// cancellation (expired or released holds, boarding changes). Cancellations
// already promote inside their own transaction; running again is harmless.
func (s *Service) HandlePromotionEvent(ctx context.Context, _ string, ev events.Event) error {
	switch ev.Type() {
	case "booking.expired", "booking.released", "booking.cancelled", "boarding.changed":
	default:
		return nil
	}
	var runID int64
	var class string
	err := s.DB.QueryRow(ctx, `SELECT run_id, class_code FROM bookings WHERE id = $1`, ev.String("booking_id")).Scan(&runID, &class)
	if err != nil {
		return err
	}
	_, err = s.Promote(ctx, runID, class)
	return err
}

// ── Notifications ──────────────────────────────────────────────────────────

type Notification struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type bookingBrief struct {
	user, pnr, train, trainName, date, source string
}

func (s *Service) brief(ctx context.Context, bookingID string) (*bookingBrief, error) {
	var b bookingBrief
	var jd time.Time
	err := s.DB.QueryRow(ctx, `SELECT user_id, coalesce(pnr, ''), train_number, journey_date, source FROM bookings WHERE id = $1`, bookingID).
		Scan(&b.user, &b.pnr, &b.train, &jd, &b.source)
	if err != nil {
		return nil, err
	}
	b.date = jd.Format("2 Jan")
	if t := s.Net.Trains[b.train]; t != nil {
		b.trainName = t.Name
	}
	return &b, nil
}

func (s *Service) notify(ctx context.Context, user, kind, title, body, link, dedupe string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO notifications (user_id, kind, title, body, link, dedupe_key) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (dedupe_key) DO NOTHING`, user, kind, title, body, link, dedupe)
	return err
}

// HandleNotificationEvent turns booking and payment events into in-app notifications.
// Dedupe keys make replays harmless.
func (s *Service) HandleNotificationEvent(ctx context.Context, _ string, ev events.Event) error {
	id := ev.String("booking_id")
	switch ev.Type() {
	case "chart.prepared":
		return s.notifyChart(ctx, ev)
	case "train.departed", "train.delayed", "train.approaching", "platform.changed":
		return s.notifyTrain(ctx, ev)
	case "disruption.notice":
		return s.notifyDisruption(ctx, ev)
	case "food.placed", "food.accepted", "food.rejected", "food.handed_over", "food.delivered", "food.cancelled", "food.missed", "food.retimed":
		return s.notifyFood(ctx, ev)
	case "booking.confirmed", "passenger.promoted", "booking.cancelled", "refund.processed", "payment.refund_required",
		"payment.failed", "tdr.decided", "boarding.changed", "booking.upgraded", "guarantee.settled":
	default:
		return nil
	}
	if t := ev.Type(); t == "booking.confirmed" || t == "passenger.promoted" {
		// A confirmation guarantee settles the moment one of its two tickets is fully confirmed.
		if err := s.settleGuarantee(ctx, id); err != nil {
			return err
		}
	}
	b, err := s.brief(ctx, id)
	if err != nil || b.source != "user" {
		return err
	}
	link := "/booking/" + id
	trip := fmt.Sprintf("%s %s · %s", b.train, b.trainName, b.date)
	rupees := func(k string) string { return fmt.Sprintf("₹%d", ev.Int(k)) }
	switch ev.Type() {
	case "booking.confirmed":
		return s.notify(ctx, b.user, "confirmed", "Ticket booked · PNR "+ev.String("pnr"), trip, link, "confirmed:"+id)
	case "passenger.promoted":
		return s.notify(ctx, b.user, "promoted", fmt.Sprintf("Passenger %d moved up: %s → %s", ev.Int("seq"), ev.String("from"), ev.String("to")),
			trip+" · PNR "+b.pnr, link, fmt.Sprintf("promoted:%s:%d:%s", id, ev.Int("seq"), ev.String("to")))
	case "booking.cancelled":
		body := trip
		if ev.Int("refund") > 0 {
			body += " · refund " + rupees("refund")
		}
		title := "Passengers cancelled"
		if full, _ := ev["full"].(bool); full {
			title = "Booking cancelled · PNR " + b.pnr
		}
		return s.notify(ctx, b.user, "cancelled", title, body, link, fmt.Sprintf("cancelled:%s:%v", id, ev["passengers"]))
	case "refund.processed":
		where := "your original payment method"
		if ev.String("destination") == "wallet" {
			where = "your YatraX Pay wallet"
		}
		return s.notify(ctx, b.user, "refund", "Refund of "+rupees("amount")+" sent", "To "+where+" · "+trip, link, "refund:"+ev.String("refund_id"))
	case "payment.refund_required":
		return s.notify(ctx, b.user, "refund", "Refund of "+rupees("amount")+" on its way", ev.String("reason")+" · "+trip, link, "refund_due:"+ev.String("refund_id"))
	case "payment.failed":
		return s.notify(ctx, b.user, "payment", "Payment declined", "Your berths are still held for a few minutes · "+trip, link, "payfail:"+ev.String("order_id"))
	case "tdr.decided":
		body := "Your TDR was rejected"
		if ev.String("status") == "APPROVED" {
			body = "Your TDR was approved; refund " + rupees("refund")
		}
		return s.notify(ctx, b.user, "tdr", "TDR "+ev.String("status"), body+" · "+trip, link, "tdr:"+ev.String("tdr_id"))
	case "boarding.changed":
		return s.notify(ctx, b.user, "boarding", "Boarding point changed to "+ev.String("to"), trip, link, "boarding:"+id)
	case "booking.upgraded":
		return s.notify(ctx, b.user, "upgraded", fmt.Sprintf("Free upgrade: %s → %s", ev.String("from"), ev.String("to")),
			trip+" · PNR "+b.pnr+" · see your new berths", link, "upgraded:"+id)
	case "guarantee.settled":
		return s.notify(ctx, b.user, "guarantee", "Confirmed · PNR "+ev.String("pnr"), ev.String("note"), link, "guarantee:"+id)
	}
	return nil
}

// notifyChart tells every passenger on a run their final status.
func (s *Service) notifyChart(ctx context.Context, ev events.Event) error {
	rows, err := s.DB.Query(ctx, `SELECT b.id, b.user_id, coalesce(b.pnr, ''), b.status::text,
		(SELECT string_agg(CASE p.status::text WHEN 'CNF' THEN p.coach_no || '/' || p.berth_no WHEN 'RAC' THEN 'RAC ' || p.coach_no || '/' || p.berth_no
		        ELSE p.status::text END, ', ' ORDER BY p.seq) FROM booking_passengers p WHERE p.booking_id = b.id AND p.status <> 'CAN')
		FROM bookings b WHERE b.run_id = $1 AND b.source = 'user' AND b.status IN ('CONFIRMED', 'CANCELLED')`, int64(ev.Int("run_id")))
	if err != nil {
		return err
	}
	type row struct{ id, user, pnr, status, places string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var places *string
		err := r.Scan(&x.id, &x.user, &x.pnr, &x.status, &places)
		if places != nil {
			x.places = *places
		}
		return x, err
	})
	if err != nil {
		return err
	}
	version := ev.Int("version")
	for _, x := range list {
		title := fmt.Sprintf("Chart prepared for %s · PNR %s", ev.String("train"), x.pnr)
		body := "Final status: " + x.places
		if x.status == "CANCELLED" {
			body = "Still waitlisted at chart time, so the ticket was cancelled and refunded"
		}
		if err := s.notify(ctx, x.user, "chart", title, body, "/booking/"+x.id, fmt.Sprintf("chart:%d:%s", version, x.id)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Notifications(ctx context.Context, userID string, limit int) ([]Notification, int, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, kind, title, body, link, read_at, created_at FROM notifications
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, 0, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Notification, error) {
		var n Notification
		return n, r.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.ReadAt, &n.CreatedAt)
	})
	if err != nil {
		return nil, 0, err
	}
	var unread int
	err = s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&unread)
	if list == nil {
		list = []Notification{}
	}
	return list, unread, err
}

func (s *Service) MarkNotificationsRead(ctx context.Context, userID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	return err
}

// notifyTrain tells the passengers of a run about its live progress. Each hears only
// what concerns them: approach and platform alerts for their own boarding or alighting
// station, delays while the train is still ahead of their destination.
func (s *Service) notifyTrain(ctx context.Context, ev events.Event) error {
	t := s.Net.Trains[ev.String("train")]
	if t == nil {
		return nil
	}
	rows, err := s.DB.Query(ctx, `SELECT b.id, b.user_id, coalesce(b.pnr, ''), b.from_idx, b.to_idx, u.phone,
		coalesce((SELECT p.coach_no || '/' || p.berth_no FROM booking_passengers p
		           WHERE p.booking_id = b.id AND p.status = 'CNF' ORDER BY p.seq LIMIT 1), '')
		FROM bookings b JOIN train_runs r ON r.id = b.run_id JOIN users u ON u.id = b.user_id
		WHERE r.train_number = $1 AND r.origin_date = $2 AND b.status = 'CONFIRMED' AND b.source = 'user'`,
		t.Number, ev.String("origin_date"))
	if err != nil {
		return err
	}
	type row struct {
		id, user, pnr, phone, seat string
		from, to                   int
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var phone *string
		err := r.Scan(&x.id, &x.user, &x.pnr, &x.from, &x.to, &phone, &x.seat)
		if phone != nil {
			x.phone = *phone
		}
		return x, err
	})
	if err != nil {
		return err
	}
	name := func(seq int) string {
		if seq < 0 || seq >= len(t.Stops) {
			return ""
		}
		if st := s.Net.Stations[t.Stops[seq].Station]; st != nil {
			return st.Name
		}
		return t.Stops[seq].Station
	}
	clockAt := func(rfc string) string {
		tm, err := time.Parse(time.RFC3339, rfc)
		if err != nil {
			return ""
		}
		return tm.In(network.IST).Format("15:04")
	}
	run := t.Number + ":" + ev.String("origin_date")
	seq, delay := ev.Int("seq"), ev.Int("delay")
	for _, x := range list {
		link := "/booking/" + x.id
		trip := fmt.Sprintf("%s %s · PNR %s", t.Number, t.Name, x.pnr)
		var kind, title, body, key string
		switch ev.Type() {
		case "train.departed":
			if x.from == 0 {
				continue // they're on it; they know
			}
			kind, key = "live", "departed:"+run+":"+x.id
			title = fmt.Sprintf("%s has left %s", t.Number, name(0))
			if delay > 0 {
				title += fmt.Sprintf(", %d min late", delay)
			} else {
				title += " on time"
			}
			body = trip
		case "train.delayed":
			atSeq := ev.Int("at_seq")
			if atSeq >= x.to {
				continue // already past their destination
			}
			kind, key = "delay", fmt.Sprintf("delay:%s:%d:%s", run, ev.Int("bucket"), x.id)
			title = fmt.Sprintf("%s is running %d min late", t.Number, delay)
			stop := x.from
			if atSeq >= x.from {
				stop = x.to // they're on board: what matters is when they get there
			}
			exp, _ := ev["expected"].(map[string]any)
			if e, ok := exp[fmt.Sprint(stop)].(string); ok {
				body = fmt.Sprintf("Now expected at %s at %s", name(stop), clockAt(e))
				if stop == 0 {
					body = fmt.Sprintf("Now leaves %s at %s", name(0), clockAt(e))
				}
			}
			if r := ev.String("reason"); r != "" {
				body += " · " + r
			}
			body += " · " + trip
		case "platform.changed":
			if seq != x.from {
				continue
			}
			kind, key = "platform", fmt.Sprintf("platform:%s:%d:%d:%s", run, seq, ev.Int("platform"), x.id)
			title = fmt.Sprintf("Platform changed to %d at %s", ev.Int("platform"), name(seq))
			body = fmt.Sprintf("%s now arrives on platform %d (was %d) · %s", t.Number, ev.Int("platform"), ev.Int("old_platform"), trip)
		case "train.approaching":
			switch {
			case seq == 0 && x.from == 0:
				kind, key = "live", fmt.Sprintf("approach:%s:%d:%s", run, seq, x.id)
				title = fmt.Sprintf("%s leaves %s at %s · platform %d", t.Number, name(0), clockAt(ev.String("eta")), ev.Int("platform"))
				body = trip
				if x.seat != "" {
					body = "Your berth: " + x.seat + " · " + trip
				}
			case seq == x.from:
				kind, key = "live", fmt.Sprintf("approach:%s:%d:%s", run, seq, x.id)
				title = fmt.Sprintf("%s reaches %s at %s · platform %d", t.Number, name(seq), clockAt(ev.String("eta")), ev.Int("platform"))
				body = trip
				if x.seat != "" {
					body = "Your berth: " + x.seat + " · " + trip
				}
			case seq == x.to:
				kind, key = "live", fmt.Sprintf("approach:%s:%d:%s", run, seq, x.id)
				title = fmt.Sprintf("Arriving at %s around %s", name(seq), clockAt(ev.String("eta")))
				body = "Your destination is about 30 minutes away · " + trip
			default:
				continue
			}
		}
		if err := s.notify(ctx, x.user, kind, title, body, link, key); err != nil {
			return err
		}
		if (kind == "delay" || kind == "platform") && x.phone != "" {
			// Mock SMS gateway, as for OTPs: the text goes to the log.
			slog.Info("mock sms", "to", "+91"+x.phone, "text", "YatraX: "+title+". "+body)
		}
	}
	return nil
}

// notifyDisruption tells a passenger their train was cancelled, cut short or diverted:
// what they get back, and the seats held for them on another train.
func (s *Service) notifyDisruption(ctx context.Context, ev events.Event) error {
	id := ev.String("booking_id")
	b, err := s.brief(ctx, id)
	if err != nil || b.source != "user" {
		return err
	}
	what := map[string]string{"cancelled": "is cancelled", "short_terminated": "will end at " + ev.String("station"), "diverted": "is diverted"}[ev.String("kind")]
	title := fmt.Sprintf("%s %s %s", ev.String("train"), ev.String("train_name"), what)
	body := ev.String("reason") + " · PNR " + b.pnr
	if r := ev.Int("refund"); r > 0 {
		if partial, _ := ev["partial"].(bool); partial {
			body += fmt.Sprintf(" · ₹%d refunded for the distance you won't travel", r)
		} else {
			body += fmt.Sprintf(" · full refund of ₹%d on its way", r)
		}
	}
	link := "/booking/" + id
	if alt := ev.String("alt_booking_id"); alt != "" {
		until := ""
		if t, err := time.Parse(time.RFC3339, ev.String("hold_until")); err == nil {
			until = t.In(network.IST).Format("15:04")
		}
		body += fmt.Sprintf(" · we've held %s on %s %s for you until %s: pay to confirm", ev.String("alt_status"), ev.String("alt_train"), ev.String("alt_name"), until)
		link = "/booking/" + alt
	}
	if err := s.notify(ctx, b.user, "disruption", title, body, link, "disruption:"+id); err != nil {
		return err
	}
	slog.Info("mock sms", "booking", id, "text", "YatraX: "+title+". "+body)
	return nil
}

// notifyFood keeps a passenger posted on a food order, including when the train's delay
// moves its delivery time (the vendor's timing follows the live ETA too).
func (s *Service) notifyFood(ctx context.Context, ev events.Event) error {
	id := ev.String("order_id")
	var user, code, vendor, station, coach, payment string
	var berth, total int
	var bookingID string
	err := s.DB.QueryRow(ctx, `SELECT o.user_id, o.code, v.name, o.station_code, o.coach_no, o.berth_no, o.total, o.payment, o.booking_id
		FROM food_orders o JOIN vendors v ON v.id = o.vendor_id WHERE o.id = $1`, id).
		Scan(&user, &code, &vendor, &station, &coach, &berth, &total, &payment, &bookingID)
	if err != nil {
		return err
	}
	stName := station
	if st := s.Net.Stations[station]; st != nil {
		stName = st.Name
	}
	refund := ""
	if payment == "wallet" {
		refund = fmt.Sprintf(" · ₹%d back in your YatraX Pay wallet", total)
	}
	var title, body string
	switch ev.Type() {
	case "food.placed":
		title, body = "Food ordered · "+code, fmt.Sprintf("%s will deliver to %s/%d at %s", vendor, coach, berth, stName)
	case "food.accepted":
		title, body = vendor+" accepted your order", fmt.Sprintf("%s · delivery to %s/%d at %s", code, coach, berth, stName)
	case "food.rejected":
		title, body = vendor+" couldn't take your order", ev.String("note")+refund
	case "food.handed_over":
		title, body = "Your food is on its way to "+coach+"/"+fmt.Sprint(berth), fmt.Sprintf("%s · the delivery person will meet the train at %s", code, stName)
	case "food.delivered":
		title, body = "Delivered · "+code, "Enjoy your meal. Rate "+vendor+" from your booking page."
	case "food.cancelled", "food.missed":
		title, body = "Food order "+code+" closed", ev.String("note")+refund
	case "food.retimed":
		eta := ""
		if t, err := time.Parse(time.RFC3339, ev.String("eta")); err == nil {
			eta = t.In(network.IST).Format("15:04")
		}
		later := "later"
		if ev.Int("shift_min") < 0 {
			later = "earlier"
		}
		title = fmt.Sprintf("Your food now arrives around %s", eta)
		body = fmt.Sprintf("The train reaches %s %d min %s than planned; %s has been told and will have it ready · %s", stName, abs(ev.Int("shift_min")), later, vendor, code)
	}
	key := "food:" + ev.Type() + ":" + id
	if ev.Type() == "food.retimed" {
		key += ":" + ev.String("eta")
	}
	return s.notify(ctx, user, "food", title, body, "/booking/"+bookingID, key)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

package analytics

import "context"

// The fraud report gathers the signals the system already records — the Tatkal bot
// detector, booking patterns, the TTEs' fines, and food complaints — into one place for
// the vigilance team. Each list is a lead to look into, not proof.

type SharedOrigin struct {
	Key      string `json:"key"` // an IP address or a device fingerprint
	Accounts int    `json:"accounts"`
	Blocked  int    `json:"blocked"`
}

// Tout: the same traveller (name and age) booked on Tatkal from many accounts — the
// pattern of agents running several IDs.
type Tout struct {
	Name     string `json:"name"`
	Age      int    `json:"age"`
	Accounts int    `json:"accounts"`
	Bookings int    `json:"bookings"`
}

type HeavyUser struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Count    int    `json:"count"`
	Amount   int    `json:"amount"` // rupees: fares, or refunds for cancellers
	BotFlags int    `json:"bot_flags"`
}

type FineSpot struct {
	Train      string `json:"train"`
	Name       string `json:"name"`
	Ticketless int    `json:"ticketless"`
	WrongClass int    `json:"wrong_class"`
	Collected  int    `json:"collected"`
}

type TTECash struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Fines      int    `json:"fines"`
	Collected  int    `json:"collected"`
}

type VendorFlag struct {
	Name       string  `json:"name"`
	Station    string  `json:"station"`
	Hygiene    int     `json:"hygiene_complaints"`
	Rating     float64 `json:"rating"`
	Rejected   int     `json:"rejected"`
	Delivered  int     `json:"delivered"`
	MissedRate float64 `json:"missed_rate"`
}

type Fraud struct {
	Bots struct {
		Checked       int            `json:"checked"`
		Blocked       int            `json:"blocked"`
		Challenged    int            `json:"challenged"`
		TopReasons    []Count        `json:"top_reasons"`
		SharedIPs     []SharedOrigin `json:"shared_ips"`
		SharedDevices []SharedOrigin `json:"shared_devices"`
	} `json:"bots"`
	Touts       []Tout       `json:"touts"`
	TatkalHeavy []HeavyUser  `json:"tatkal_heavy"`
	Cancellers  []HeavyUser  `json:"cancellers"`
	FineSpots   []FineSpot   `json:"fine_spots"`
	TTECash     []TTECash    `json:"tte_cash"`
	Vendors     []VendorFlag `json:"vendors"`
	Payments    struct {
		Unresolved    int `json:"unresolved"`     // money taken, booking not settled
		RefundsFailed int `json:"refunds_failed"` // refunds the gateway keeps refusing
		LateRefunds   int `json:"late_refunds"`   // payments that arrived after the hold expired
	} `json:"payments"`
}

// Fraud is all of India; vigilance is a headquarters job.
func (s *Service) Fraud(ctx context.Context) (*Fraud, error) {
	f := &Fraud{}
	b := &f.Bots
	err := s.DB.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE action = 'block'), count(*) FILTER (WHERE action = 'challenge') FROM bot_flags`).
		Scan(&b.Checked, &b.Blocked, &b.Challenged)
	if err != nil {
		return nil, err
	}
	if b.TopReasons, err = collect[Count](ctx, s.DB, `SELECT r, count(*)::int FROM bot_flags, unnest(reasons) r
		WHERE action <> 'allow' GROUP BY 1 ORDER BY 2 DESC LIMIT 8`); err != nil {
		return nil, err
	}
	shared := func(col string) ([]SharedOrigin, error) {
		return collect[SharedOrigin](ctx, s.DB, `SELECT `+col+`, count(DISTINCT user_id)::int, count(*) FILTER (WHERE action = 'block')::int
			FROM bot_flags WHERE `+col+` <> '' GROUP BY 1 HAVING count(DISTINCT user_id) >= 3 ORDER BY 3 DESC, 2 DESC LIMIT 10`)
	}
	if b.SharedIPs, err = shared("ip"); err != nil {
		return nil, err
	}
	if b.SharedDevices, err = shared("device"); err != nil {
		return nil, err
	}

	if f.Touts, err = collect[Tout](ctx, s.DB, `SELECT initcap(lower(p.name)), p.age::int, count(DISTINCT b.user_id)::int, count(DISTINCT b.id)::int
		FROM bookings b JOIN booking_passengers p ON p.booking_id = b.id
		WHERE b.quota IN ('TQ', 'PT') AND b.confirmed_at IS NOT NULL
		GROUP BY lower(p.name), p.age HAVING count(DISTINCT b.user_id) >= 3 ORDER BY 3 DESC, 4 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if f.TatkalHeavy, err = collect[HeavyUser](ctx, s.DB, `SELECT u.id::text, u.full_name, coalesce(u.phone, ''), count(*)::int, sum(b.total_fare)::int,
			(SELECT count(*) FROM bot_flags f WHERE f.user_id = u.id AND f.action <> 'allow')::int
		FROM bookings b JOIN users u ON u.id = b.user_id
		WHERE b.quota IN ('TQ', 'PT') AND b.confirmed_at IS NOT NULL AND b.source = 'user'
		GROUP BY u.id HAVING count(*) >= 3 ORDER BY 4 DESC, 5 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if f.Cancellers, err = collect[HeavyUser](ctx, s.DB, `SELECT u.id::text, u.full_name, coalesce(u.phone, ''), count(DISTINCT b.id)::int,
			coalesce(sum(r.amount), 0)::int, 0
		FROM bookings b JOIN users u ON u.id = b.user_id LEFT JOIN refunds r ON r.booking_id = b.id
		WHERE b.status = 'CANCELLED' AND b.source = 'user'
		GROUP BY u.id HAVING count(DISTINCT b.id) >= 3 ORDER BY 4 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if f.FineSpots, err = collect[FineSpot](ctx, s.DB, `SELECT r.train_number, t.name,
			count(*) FILTER (WHERE f.reason = 'ticketless')::int, count(*) FILTER (WHERE f.reason = 'wrong_class')::int, sum(f.total)::int
		FROM tte_fines f JOIN train_runs r ON r.id = f.run_id JOIN trains t ON t.number = r.train_number
		GROUP BY 1, 2 ORDER BY 5 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if f.TTECash, err = collect[TTECash](ctx, s.DB, `SELECT coalesce(u.employee_id, ''), u.full_name, count(*)::int, sum(f.total)::int
		FROM tte_fines f JOIN users u ON u.id = f.tte_id GROUP BY u.id ORDER BY 4 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	if f.Vendors, err = collect[VendorFlag](ctx, s.DB, `SELECT v.name, v.station_code,
			(SELECT count(*) FROM complaints c WHERE c.vendor_id = v.id)::int, v.rating::float8,
			count(o.id) FILTER (WHERE o.status = 'rejected')::int, count(o.id) FILTER (WHERE o.status = 'delivered')::int,
			coalesce(count(o.id) FILTER (WHERE o.status = 'missed')::float8 / nullif(count(o.id), 0), 0)
		FROM vendors v LEFT JOIN food_orders o ON o.vendor_id = v.id
		GROUP BY v.id HAVING (SELECT count(*) FROM complaints c WHERE c.vendor_id = v.id) > 0
		    OR count(o.id) FILTER (WHERE o.status IN ('rejected', 'missed')) >= 2
		ORDER BY 3 DESC, 5 DESC LIMIT 10`); err != nil {
		return nil, err
	}
	p := &f.Payments
	err = s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM payments p JOIN bookings b ON b.id = p.booking_id WHERE p.status = 'CAPTURED' AND b.status NOT IN ('CONFIRMED', 'CANCELLED', 'FAILED')),
		(SELECT count(*) FROM refunds WHERE status = 'FAILED'),
		(SELECT count(*) FROM bookings WHERE status = 'FAILED')`).Scan(&p.Unresolved, &p.RefundsFailed, &p.LateRefunds)
	return f, err
}
